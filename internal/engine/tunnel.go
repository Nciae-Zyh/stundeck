package engine

import (
	"context"
	"errors"
	"fmt"
	"net"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"time"

	"github.com/Nciae-Zyh/stundeck/internal/store"
)

func (m *Manager) SetTunnelProvider(provider func(context.Context, store.Service) (string, error)) {
	m.config.TunnelToken = provider
}

func (m *Manager) TunnelAvailable() bool {
	_, err := exec.LookPath(m.tunnelBinary())
	return err == nil
}

func (m *Manager) tunnelBinary() string {
	if m.config.CloudflaredBinary != "" {
		return m.config.CloudflaredBinary
	}
	return "cloudflared"
}

func (m *Manager) startTunnel(ctx context.Context, service store.Service) error {
	if service.TunnelProtocol == "" {
		service.TunnelProtocol = "http"
	}
	if service.PublishMode != "warp" && (service.Protocol != "tcp" || !validTunnelProtocol(service.TunnelProtocol)) {
		return errors.New("Tunnel 支持 HTTP、HTTPS、TCP、SSH、RDP；UDP 请使用 WARP 私网或 Spectrum")
	}
	if m.config.TunnelToken == nil {
		return errors.New("Tunnel provider 未配置")
	}
	return m.startConnector(ctx, service, false)
}

func validTunnelProtocol(protocol string) bool {
	return protocol == "http" || protocol == "https" || protocol == "tcp" || protocol == "ssh" || protocol == "rdp"
}

const (
	connectorRetryInitial = time.Second
	connectorRetryMaximum = 30 * time.Second
)

// startConnector starts cloudflared without probing the local origin. A slow
// origin is a valid state: cloudflared can keep its edge connection alive and
// retry the origin when requests arrive. The supervisor below also retries a
// connector that exits before the origin is ready or while Cloudflare is
// temporarily unavailable.
func (m *Manager) startConnector(ctx context.Context, service store.Service, quick bool) error {
	_, err := exec.LookPath(m.tunnelBinary())
	if err != nil {
		return errors.New("cloudflared 不可用：请安装 cloudflared 或设置 STUNDECK_CLOUDFLARED_BINARY")
	}
	m.mu.Lock()
	if _, exists := m.processes[service.ID]; exists {
		m.mu.Unlock()
		return nil
	}
	if _, exists := m.connectors[service.ID]; exists {
		m.mu.Unlock()
		return nil
	}
	runContext, cancelRun := context.WithCancel(context.Background())
	m.connectors[service.ID] = &connectorRun{ctx: runContext, cancel: cancelRun, service: service}
	m.mu.Unlock()

	token := ""
	if !quick {
		prepareContext, cancelPrepare := context.WithTimeout(ctx, 45*time.Second)
		token, err = m.config.TunnelToken(prepareContext, service)
		cancelPrepare()
		if err != nil {
			m.removeConnector(service.ID, runContext)
			return err
		}
	}
	if err = runContext.Err(); err != nil {
		m.removeConnector(service.ID, runContext)
		return err
	}
	if _, err = m.launchConnector(runContext, service, token, quick); err != nil {
		m.removeConnector(service.ID, runContext)
		return err
	}
	go m.superviseConnector(runContext, service, quick)
	return nil
}

func (m *Manager) launchConnector(runContext context.Context, service store.Service, token string, quick bool) (*process, error) {
	binary, err := exec.LookPath(m.tunnelBinary())
	if err != nil {
		return nil, errors.New("cloudflared 不可用：请安装 cloudflared 或设置 STUNDECK_CLOUDFLARED_BINARY")
	}
	processContext, cancel := context.WithCancel(runContext)
	args := []string{"--no-autoupdate", "tunnel", "--config", os.DevNull}
	if quick {
		target := service.TunnelProtocol + "://" + net.JoinHostPort(service.TargetHost, strconv.Itoa(service.TargetPort))
		args = append(args, "--url", target)
	} else {
		args = append(args, "run")
	}
	cmd := exec.CommandContext(processContext, binary, args...)
	cmd.Env = tunnelEnvironment(os.Environ(), token)
	if quick {
		output := &quickTunnelOutput{manager: m, serviceID: service.ID, cmd: cmd}
		cmd.Stdout, cmd.Stderr = output, output
	}
	// cloudflared startup output can contain settings. Named Tunnel output is
	// intentionally discarded; Quick Tunnel output is parsed only for its URL.
	if err = cmd.Start(); err != nil {
		cancel()
		return nil, fmt.Errorf("启动 cloudflared 失败：%w", err)
	}
	running := &process{cancel: cancel, ctx: processContext, cmd: cmd}
	m.mu.Lock()
	connector, connectorExists := m.connectors[service.ID]
	if !connectorExists || connector.ctx != runContext || runContext.Err() != nil {
		m.mu.Unlock()
		cancel()
		go func() { _ = cmd.Wait() }()
		return nil, context.Canceled
	}
	m.processes[service.ID] = running
	m.mu.Unlock()
	status := "tunnel_running"
	if quick {
		status = "tunnel_starting"
	}
	if !m.setConnectorRuntime(runContext, service.ID, status, "", true) {
		m.mu.Lock()
		if current := m.processes[service.ID]; current == running {
			delete(m.processes, service.ID)
		}
		m.mu.Unlock()
		cancel()
		go func() { _ = cmd.Wait() }()
		return nil, context.Canceled
	}
	return running, nil
}

func (m *Manager) setConnectorRuntime(runContext context.Context, serviceID, status, lastError string, enabled bool) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	connector, exists := m.connectors[serviceID]
	if !exists || connector.ctx != runContext || runContext.Err() != nil {
		return false
	}
	return m.store.SetServiceRuntime(context.Background(), serviceID, status, lastError, enabled) == nil
}

func (m *Manager) clearConnectorRuntimeURL(runContext context.Context, serviceID string) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	connector, exists := m.connectors[serviceID]
	if !exists || connector.ctx != runContext || runContext.Err() != nil {
		return false
	}
	return m.store.SetServiceRuntimeURL(context.Background(), serviceID, "") == nil
}

func (m *Manager) removeConnector(serviceID string, runContext context.Context) {
	m.mu.Lock()
	if connector, exists := m.connectors[serviceID]; exists {
		if runContext == nil || connector.ctx == runContext {
			delete(m.connectors, serviceID)
			connector.cancel()
		}
	}
	m.mu.Unlock()
}

func (m *Manager) superviseConnector(runContext context.Context, service store.Service, quick bool) {
	backoff := connectorRetryInitial
	for {
		m.mu.Lock()
		running := m.processes[service.ID]
		m.mu.Unlock()
		if running == nil {
			m.removeConnector(service.ID, runContext)
			return
		}
		err := running.cmd.Wait()
		m.mu.Lock()
		current, exists := m.processes[service.ID]
		if !exists || current != running {
			m.mu.Unlock()
			return
		}
		delete(m.processes, service.ID)
		intentional := runContext.Err() != nil
		m.mu.Unlock()
		if running.gateway != nil {
			cleanupContext, cancel := context.WithTimeout(context.Background(), gatewayTimeout)
			_ = removeGatewayMapping(cleanupContext, *running.gateway)
			cancel()
		}
		if intentional {
			m.removeConnector(service.ID, runContext)
			return
		}
		// Clear a previous Quick Tunnel URL immediately; it is not valid while
		// a replacement process is being prepared. Both this update and the
		// retrying status are serialized with Stop so a manual stop cannot be
		// overwritten by a late supervisor update.
		if !m.clearConnectorRuntimeURL(runContext, service.ID) {
			m.removeConnector(service.ID, runContext)
			return
		}
		message := "Tunnel connector exited; will retry automatically"
		if err != nil {
			m.logger.Warn("cloudflared connector exited", "service_id", service.ID, "retrying", true)
		}
		if !m.setConnectorRuntime(runContext, service.ID, "tunnel_retrying", message, true) {
			m.removeConnector(service.ID, runContext)
			return
		}
		// Keep trying until a replacement process is actually running. Provider
		// refreshes and process starts can fail independently of a connector
		// exit, so those failures must stay inside the retry loop instead of
		// falling through to the no-process exit path above.
		for {
			if !m.connectorStillEnabled(service.ID) {
				m.removeConnector(service.ID, runContext)
				return
			}
			if !waitConnectorRetry(runContext, backoff) {
				m.removeConnector(service.ID, runContext)
				return
			}
			currentService, serviceErr := m.store.Service(context.Background(), service.ID)
			if serviceErr != nil || !currentService.Enabled || !currentService.UsesConnector() {
				m.removeConnector(service.ID, runContext)
				return
			}
			token := ""
			if !quick {
				prepareContext, cancelPrepare := context.WithTimeout(runContext, 45*time.Second)
				token, serviceErr = m.config.TunnelToken(prepareContext, currentService)
				cancelPrepare()
				if serviceErr != nil {
					if !m.setConnectorRuntime(runContext, service.ID, "tunnel_retrying", "Tunnel 配置暂不可用，将自动重试", true) {
						m.removeConnector(service.ID, runContext)
						return
					}
					backoff = nextConnectorBackoff(backoff)
					continue
				}
			}
			if _, serviceErr = m.launchConnector(runContext, currentService, token, quick); serviceErr != nil {
				if !m.setConnectorRuntime(runContext, service.ID, "tunnel_retrying", "cloudflared 暂不可用，将自动重试", true) {
					m.removeConnector(service.ID, runContext)
					return
				}
				backoff = nextConnectorBackoff(backoff)
				continue
			}
			backoff = connectorRetryInitial
			service = currentService
			break
		}
	}
}

func (m *Manager) connectorStillEnabled(serviceID string) bool {
	service, err := m.store.Service(context.Background(), serviceID)
	return err == nil && service.Enabled && service.UsesConnector()
}

func waitConnectorRetry(ctx context.Context, delay time.Duration) bool {
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-timer.C:
		return true
	case <-ctx.Done():
		return false
	}
}

func nextConnectorBackoff(current time.Duration) time.Duration {
	if current >= connectorRetryMaximum/2 {
		return connectorRetryMaximum
	}
	return current * 2
}

func tunnelEnvironment(environment []string, token string) []string {
	result := []string{}
	for _, entry := range environment {
		if !strings.HasPrefix(entry, "TUNNEL_") && !strings.HasPrefix(entry, "CLOUDFLARE_API_TOKEN=") && !strings.HasPrefix(entry, "CF_API_TOKEN=") {
			result = append(result, entry)
		}
	}
	if token != "" {
		result = append(result, "TUNNEL_TOKEN="+token)
	}
	return result
}
