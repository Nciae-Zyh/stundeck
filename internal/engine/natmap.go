package engine

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"os"
	"os/exec"
	"strconv"
	"sync"
	"time"

	"github.com/Nciae-Zyh/stundeck/internal/store"
)

type Mapping struct {
	ServiceID   string `json:"serviceId"`
	PublicIP    string `json:"publicIp"`
	PublicPort  int    `json:"publicPort"`
	IP4P        string `json:"ip4p,omitempty"`
	PrivatePort int    `json:"privatePort"`
	Protocol    string `json:"protocol"`
	PrivateIP   string `json:"privateIp"`
}

type Config struct {
	Binary          string
	NotifyBinary    string
	CallbackURL     string
	CallbackToken   string
	STUNServer      string
	KeepAliveServer string
	KeepAlive       time.Duration
}

type process struct {
	cancel            context.CancelFunc
	ctx               context.Context
	cmd               *exec.Cmd
	gateway           *GatewayMapping
	gatewayGeneration uint64
}

type Manager struct {
	config    Config
	store     *store.Store
	logger    *slog.Logger
	mu        sync.Mutex
	processes map[string]*process
}

func NewManager(config Config, database *store.Store, logger *slog.Logger) *Manager {
	return &Manager{
		config:    config,
		store:     database,
		logger:    logger,
		processes: make(map[string]*process),
	}
}

func (m *Manager) Available() bool {
	_, err := exec.LookPath(m.config.Binary)
	return err == nil
}

func (m *Manager) Start(ctx context.Context, service store.Service) error {
	if err := validateTarget(ctx, service); err != nil {
		return err
	}
	binary, err := exec.LookPath(m.config.Binary)
	if err != nil {
		return fmt.Errorf("natmap binary is unavailable: %w", err)
	}
	notify, err := exec.LookPath(m.config.NotifyBinary)
	if err != nil {
		return fmt.Errorf("stundeck-notify binary is unavailable: %w", err)
	}

	m.mu.Lock()
	if _, exists := m.processes[service.ID]; exists {
		m.mu.Unlock()
		return nil
	}
	// The NATMap process belongs to the manager lifecycle, not to the HTTP
	// request that happened to start it. Stop and StopAll own cancellation.
	processContext, cancel := context.WithCancel(context.Background())
	args := BuildArgs(service, m.config, notify)
	cmd := exec.CommandContext(processContext, binary, args...)
	cmd.Env = append(os.Environ(),
		"STUNDECK_CALLBACK_URL="+m.config.CallbackURL,
		"STUNDECK_CALLBACK_TOKEN="+m.config.CallbackToken,
		"STUNDECK_SERVICE_ID="+service.ID,
	)
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		cancel()
		m.mu.Unlock()
		return fmt.Errorf("open natmap stdout: %w", err)
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		cancel()
		m.mu.Unlock()
		return fmt.Errorf("open natmap stderr: %w", err)
	}
	if err := cmd.Start(); err != nil {
		cancel()
		m.mu.Unlock()
		return fmt.Errorf("start natmap: %w", err)
	}
	m.processes[service.ID] = &process{cancel: cancel, ctx: processContext, cmd: cmd}
	m.mu.Unlock()

	_ = m.store.SetServiceRuntime(context.Background(), service.ID, "discovering", "", true)
	go m.logPipe(service.ID, "stdout", stdout)
	go m.logPipe(service.ID, "stderr", stderr)
	go m.wait(service.ID, processContext, cmd)
	return nil
}

func (m *Manager) Stop(serviceID string) error {
	m.mu.Lock()
	running, exists := m.processes[serviceID]
	if exists {
		delete(m.processes, serviceID)
	}
	m.mu.Unlock()
	if !exists {
		return m.store.SetServiceRuntime(context.Background(), serviceID, "stopped", "", false)
	}
	running.cancel()
	_ = running.cmd.Process.Signal(os.Interrupt)
	if running.gateway != nil {
		cleanupCtx, cancel := context.WithTimeout(context.Background(), gatewayTimeout)
		defer cancel()
		if err := removeGatewayMapping(cleanupCtx, *running.gateway); err != nil {
			m.logger.Warn("remove gateway mapping", "service_id", serviceID, "error", err)
		}
	}
	return m.store.SetServiceRuntime(context.Background(), serviceID, "stopped", "", false)
}

func (m *Manager) StopAll() {
	m.mu.Lock()
	processes := make([]*process, 0, len(m.processes))
	for _, running := range m.processes {
		processes = append(processes, running)
	}
	m.processes = make(map[string]*process)
	m.mu.Unlock()
	for _, running := range processes {
		running.cancel()
		if running.gateway != nil {
			cleanupCtx, cancel := context.WithTimeout(context.Background(), gatewayTimeout)
			_ = removeGatewayMapping(cleanupCtx, *running.gateway)
			cancel()
		}
	}
}

func (m *Manager) Running(serviceID string) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	_, exists := m.processes[serviceID]
	return exists
}

func (m *Manager) wait(serviceID string, ctx context.Context, cmd *exec.Cmd) {
	err := cmd.Wait()
	m.mu.Lock()
	running, exists := m.processes[serviceID]
	if exists && running.cmd == cmd {
		delete(m.processes, serviceID)
	}
	m.mu.Unlock()
	if exists && running.gateway != nil {
		cleanupCtx, cancel := context.WithTimeout(context.Background(), gatewayTimeout)
		_ = removeGatewayMapping(cleanupCtx, *running.gateway)
		cancel()
	}
	if ctx.Err() != nil {
		return
	}
	message := "natmap exited"
	if err != nil {
		message = err.Error()
	}
	m.logger.Error("natmap process exited", "service_id", serviceID, "error", message)
	_ = m.store.SetServiceRuntime(context.Background(), serviceID, "error", message, true)
}

func (m *Manager) logPipe(serviceID, stream string, reader io.Reader) {
	scanner := bufio.NewScanner(reader)
	for scanner.Scan() {
		m.logger.Debug("natmap", "service_id", serviceID, "stream", stream, "message", scanner.Text())
	}
}

func BuildArgs(service store.Service, config Config, notifyBinary string) []string {
	args := []string{"-4"}
	if service.Protocol == "udp" {
		args = append(args, "-u")
	}
	args = append(args,
		"-s", config.STUNServer,
		"-b", strconv.Itoa(service.BindPort),
		"-t", service.TargetHost,
		"-p", strconv.Itoa(service.TargetPort),
		"-e", notifyBinary,
		"-k", strconv.Itoa(max(10, int(config.KeepAlive.Seconds()))),
	)
	if service.Protocol == "tcp" {
		args = append(args, "-h", config.KeepAliveServer)
	}
	return args
}

func validateTarget(ctx context.Context, service store.Service) error {
	if service.Protocol == "udp" {
		return nil
	}
	dialer := net.Dialer{Timeout: 3 * time.Second}
	connection, err := dialer.DialContext(ctx, "tcp", net.JoinHostPort(service.TargetHost, strconv.Itoa(service.TargetPort)))
	if err != nil {
		return fmt.Errorf("local target is unreachable: %w", err)
	}
	return connection.Close()
}

func ValidateMapping(mapping Mapping) error {
	if mapping.ServiceID == "" {
		return errors.New("service id is required")
	}
	if net.ParseIP(mapping.PublicIP) == nil {
		return errors.New("public ip is invalid")
	}
	if mapping.PublicPort < 1 || mapping.PublicPort > 65535 {
		return errors.New("public port is invalid")
	}
	if mapping.Protocol != "tcp" && mapping.Protocol != "udp" {
		return errors.New("protocol is invalid")
	}
	return nil
}
