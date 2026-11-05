package engine

import (
	"context"
	"errors"
	"os/exec"
	"regexp"
	"strings"
	"sync"

	"github.com/Nciae-Zyh/stundeck/internal/store"
)

var quickURLPattern = regexp.MustCompile(`https://[a-z0-9]+(?:-[a-z0-9]+)*\.trycloudflare\.com(?:[\s"/]|$)`)

func (m *Manager) startQuickTunnel(ctx context.Context, service store.Service) error {
	if service.Protocol != "tcp" || (service.TunnelProtocol != "http" && service.TunnelProtocol != "https") {
		return errors.New("Quick Tunnel 仅支持 HTTP / HTTPS 服务")
	}
	return m.startConnector(ctx, service, true)
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
