package engine

import (
	"context"
	"errors"
	"net"
	"os"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
	"sync"

	"github.com/Nciae-Zyh/stundeck/internal/store"
)

var quickURLPattern = regexp.MustCompile(`https://[a-z0-9]+(?:-[a-z0-9]+)*\.trycloudflare\.com(?:[\s"/]|$)`)

func (m *Manager) startQuickTunnel(ctx context.Context, service store.Service) error {
	if service.Protocol != "tcp" || (service.TunnelProtocol != "http" && service.TunnelProtocol != "https") {
		return errors.New("Quick Tunnel 仅支持 HTTP / HTTPS 服务")
	}
	if err := validateTarget(ctx, service); err != nil {
		return err
	}
	binary, err := exec.LookPath(m.tunnelBinary())
	if err != nil {
		return errors.New("cloudflared 不可用，请安装或使用 StunDeck 镜像")
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, exists := m.processes[service.ID]; exists {
		return nil
	}
	processContext, cancel := context.WithCancel(context.Background())
	target := service.TunnelProtocol + "://" + net.JoinHostPort(service.TargetHost, strconv.Itoa(service.TargetPort))
	cmd := exec.CommandContext(processContext, binary, "--no-autoupdate", "tunnel", "--config", os.DevNull, "--url", target)
	cmd.Env = tunnelEnvironment(os.Environ(), "")
	output := &quickTunnelOutput{manager: m, serviceID: service.ID, cmd: cmd}
	cmd.Stdout, cmd.Stderr = output, output
	if err = cmd.Start(); err != nil {
		cancel()
		return err
	}
	m.processes[service.ID] = &process{cancel: cancel, ctx: processContext, cmd: cmd}
	if err = m.store.SetServiceRuntime(context.Background(), service.ID, "tunnel_starting", "", true); err != nil {
		delete(m.processes, service.ID)
		cancel()
		// Wait outside the manager lock: the output copier also takes this lock.
		go func() { _ = cmd.Wait() }()
		return err
	}
	go m.wait(service.ID, processContext, cmd)
	return nil
}

// Keep only the generated hostname; never forward raw connector logs, settings,
// credentials, or arbitrary log URLs to the API. Bound partial-line memory.
type quickTunnelOutput struct {
	mu        sync.Mutex
	manager   *Manager
	serviceID string
	cmd       *exec.Cmd
	buffer    string
	found     bool
}

func (w *quickTunnelOutput) Write(data []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.found {
		return len(data), nil
	}
	w.buffer += string(data)
	// Wait for a complete line; a write boundary is not a URL boundary.
	end := strings.LastIndexByte(w.buffer, '\n')
	if end >= 0 {
		match := quickURLPattern.FindString(w.buffer[:end+1])
		w.buffer = w.buffer[end+1:]
		if match != "" {
			match = strings.TrimRight(match, " \r\n\t\"/")
			w.manager.mu.Lock()
			defer w.manager.mu.Unlock()
			running := w.manager.processes[w.serviceID]
			if running != nil && running.cmd == w.cmd && running.ctx.Err() == nil {
				if err := w.manager.store.SetServiceRuntimeURL(context.Background(), w.serviceID, match); err == nil {
					_ = w.manager.store.SetServiceRuntime(context.Background(), w.serviceID, "tunnel_running", "", true)
					w.found = true
				}
			}
		}
	}
	if len(w.buffer) > 8192 {
		w.buffer = w.buffer[len(w.buffer)-8192:]
	}
	return len(data), nil
}
