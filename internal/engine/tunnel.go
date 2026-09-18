package engine

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
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
	if err := validateTarget(ctx, service); err != nil {
		return err
	}
	binary, err := exec.LookPath(m.tunnelBinary())
	if err != nil {
		return errors.New("cloudflared 不可用：请安装 cloudflared 或设置 STUNDECK_CLOUDFLARED_BINARY")
	}
	if m.config.TunnelToken == nil {
		return errors.New("Tunnel provider 未配置")
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, exists := m.processes[service.ID]; exists {
		return nil
	}
	prepareCtx, cancelPrepare := context.WithTimeout(ctx, 45*time.Second)
	defer cancelPrepare()
	token, err := m.config.TunnelToken(prepareCtx, service)
	if err != nil {
		return err
	}
	processContext, cancel := context.WithCancel(context.Background())
	cmd := exec.CommandContext(processContext, binary, "--no-autoupdate", "tunnel", "--config", os.DevNull, "run")
	cmd.Env = tunnelEnvironment(os.Environ(), token)
	// cloudflared startup output can contain settings. Do not pipe it into the
	// event feed or application logs; report process failure without its secrets.
	if err = cmd.Start(); err != nil {
		cancel()
		return fmt.Errorf("启动 cloudflared 失败：%w", err)
	}
	m.processes[service.ID] = &process{cancel: cancel, ctx: processContext, cmd: cmd}
	if err = m.store.SetServiceRuntime(context.Background(), service.ID, "tunnel_running", "", true); err != nil {
		delete(m.processes, service.ID)
		cancel()
		_ = cmd.Wait()
		return err
	}
	go m.wait(service.ID, processContext, cmd)
	return nil
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
