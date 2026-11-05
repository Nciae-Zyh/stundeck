package engine

import (
	"context"
	"io"
	"log/slog"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/Nciae-Zyh/stundeck/internal/store"
)

func TestTunnelProcessSurvivesRequestAndKeepsTokenOutOfArguments(t *testing.T) {
	dir := t.TempDir()
	db, err := store.Open(filepath.Join(dir, "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	go func() {
		for {
			conn, err := listener.Accept()
			if err != nil {
				return
			}
			conn.Close()
		}
	}()
	_, port, _ := net.SplitHostPort(listener.Addr().String())
	number, _ := strconv.Atoi(port)
	svc := store.Service{ID: "tunnel", Name: "Test", PublishMode: "tunnel", TargetHost: "127.0.0.1", TargetPort: number, Protocol: "tcp", CreatedAt: time.Now(), UpdatedAt: time.Now()}
	if err = db.CreateService(context.Background(), svc); err != nil {
		t.Fatal(err)
	}
	binary := filepath.Join(dir, "cloudflared")
	if err = os.WriteFile(binary, []byte("#!/bin/sh\nexec sleep 30\n"), 0700); err != nil {
		t.Fatal(err)
	}
	manager := NewManager(Config{Binary: "missing-natmap", CloudflaredBinary: binary, TunnelToken: func(context.Context, store.Service) (string, error) { return "connector-test-secret", nil }}, db, slog.New(slog.NewTextHandler(io.Discard, nil)))
	defer manager.StopAll()
	ctx, cancel := context.WithCancel(context.Background())
	if err = manager.Start(ctx, svc); err != nil {
		t.Fatal(err)
	}
	cancel()
	manager.mu.Lock()
	process := manager.processes[svc.ID]
	manager.mu.Unlock()
	if process == nil {
		t.Fatal("process missing")
	}
	if process.ctx.Err() != nil {
		t.Fatal("request cancellation stopped connector")
	}
	if strings.Contains(strings.Join(process.cmd.Args, " "), "connector-test-secret") {
		t.Fatal("token exposed in argv")
	}
	if !strings.Contains(strings.Join(process.cmd.Env, "\n"), "TUNNEL_TOKEN=connector-test-secret") {
		t.Fatal("connector token not provided")
	}
	if err = manager.Stop(svc.ID); err != nil {
		t.Fatal(err)
	}
	saved, err := db.Service(context.Background(), svc.ID)
	if err != nil || saved.Enabled || saved.Status != "stopped" {
		t.Fatalf("stop failed: %+v %v", saved, err)
	}
}

func TestTunnelStartsBeforeSlowOriginIsListening(t *testing.T) {
	dir := t.TempDir()
	db, err := store.Open(filepath.Join(dir, "slow.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	// Nothing listens on this port. Starting a connector must not wait for or
	// reject the local service; the service may come up after the connector.
	svc := store.Service{
		ID:             "slow",
		Name:           "Slow",
		PublishMode:    "tunnel",
		TunnelProtocol: "http",
		TargetHost:     "127.0.0.1",
		TargetPort:     45991,
		Protocol:       "tcp",
		CreatedAt:      time.Now(),
		UpdatedAt:      time.Now(),
	}
	if err = db.CreateService(context.Background(), svc); err != nil {
		t.Fatal(err)
	}
	binary := filepath.Join(dir, "cloudflared")
	if err = os.WriteFile(binary, []byte("#!/bin/sh\nexec sleep 30\n"), 0700); err != nil {
		t.Fatal(err)
	}
	manager := NewManager(Config{
		CloudflaredBinary: binary,
		TunnelToken:       func(context.Context, store.Service) (string, error) { return "slow-origin-token", nil },
	}, db, slog.New(slog.NewTextHandler(io.Discard, nil)))
	defer manager.StopAll()
	started := time.Now()
	if err = manager.Start(context.Background(), svc); err != nil {
		t.Fatal(err)
	}
	if elapsed := time.Since(started); elapsed > time.Second {
		t.Fatalf("connector start waited for slow origin: %s", elapsed)
	}
	saved, err := db.Service(context.Background(), svc.ID)
	if err != nil || saved.Status != "tunnel_running" || !saved.Enabled {
		t.Fatalf("connector was not enabled: %+v %v", saved, err)
	}
	if err = manager.Stop(svc.ID); err != nil {
		t.Fatal(err)
	}
}

func TestTunnelAutomaticallyRetriesConnectorExit(t *testing.T) {
	dir := t.TempDir()
	db, err := store.Open(filepath.Join(dir, "retry.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	svc := store.Service{
		ID:             "retry",
		Name:           "Retry",
		PublishMode:    "tunnel",
		TunnelProtocol: "http",
		TargetHost:     "slow.service",
		TargetPort:     8080,
		Protocol:       "tcp",
		CreatedAt:      time.Now(),
		UpdatedAt:      time.Now(),
	}
	if err = db.CreateService(context.Background(), svc); err != nil {
		t.Fatal(err)
	}
	countFile := filepath.Join(dir, "starts")
	binary := filepath.Join(dir, "cloudflared")
	script := "#!/bin/sh\n" +
		"count=$(cat '" + countFile + "' 2>/dev/null || echo 0)\n" +
		"count=$((count+1))\n" +
		"printf '%s' \"$count\" > '" + countFile + "'\n" +
		"if [ \"$count\" -eq 1 ]; then exit 17; fi\n" +
		"exec sleep 30\n"
	if err = os.WriteFile(binary, []byte(script), 0700); err != nil {
		t.Fatal(err)
	}
	manager := NewManager(Config{
		CloudflaredBinary: binary,
		TunnelToken:       func(context.Context, store.Service) (string, error) { return "retry-token", nil },
	}, db, slog.New(slog.NewTextHandler(io.Discard, nil)))
	defer manager.StopAll()
	if err = manager.Start(context.Background(), svc); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(4 * time.Second)
	for {
		count, readErr := os.ReadFile(countFile)
		loaded, loadErr := db.Service(context.Background(), svc.ID)
		if readErr == nil && string(count) == "2" && loadErr == nil && loaded.Status == "tunnel_running" && manager.Running(svc.ID) {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("connector was not restarted: starts=%q service=%+v readErr=%v loadErr=%v", count, loaded, readErr, loadErr)
		}
		time.Sleep(10 * time.Millisecond)
	}
	if err = manager.Stop(svc.ID); err != nil {
		t.Fatal(err)
	}
	loaded, err := db.Service(context.Background(), svc.ID)
	if err != nil || loaded.Status != "stopped" || loaded.Enabled {
		t.Fatalf("stop after retry failed: %+v %v", loaded, err)
	}
}

func TestTunnelStopCancelsPendingRetry(t *testing.T) {
	dir := t.TempDir()
	db, err := store.Open(filepath.Join(dir, "cancel-retry.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	svc := store.Service{
		ID:             "cancel-retry",
		Name:           "Cancel retry",
		PublishMode:    "tunnel",
		TunnelProtocol: "http",
		TargetHost:     "slow.service",
		TargetPort:     8080,
		Protocol:       "tcp",
		CreatedAt:      time.Now(),
		UpdatedAt:      time.Now(),
	}
	if err = db.CreateService(context.Background(), svc); err != nil {
		t.Fatal(err)
	}
	countFile := filepath.Join(dir, "starts")
	binary := filepath.Join(dir, "cloudflared")
	script := "#!/bin/sh\n" +
		"count=$(cat '" + countFile + "' 2>/dev/null || echo 0)\n" +
		"count=$((count+1))\n" +
		"printf '%s' \"$count\" > '" + countFile + "'\n" +
		"exit 17\n"
	if err = os.WriteFile(binary, []byte(script), 0700); err != nil {
		t.Fatal(err)
	}
	manager := NewManager(Config{
		CloudflaredBinary: binary,
		TunnelToken:       func(context.Context, store.Service) (string, error) { return "cancel-retry-token", nil },
	}, db, slog.New(slog.NewTextHandler(io.Discard, nil)))
	defer manager.StopAll()
	if err = manager.Start(context.Background(), svc); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(2 * time.Second)
	for {
		loaded, loadErr := db.Service(context.Background(), svc.ID)
		if loadErr == nil && loaded.Status == "tunnel_retrying" {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("connector did not enter retry state: %+v %v", loaded, loadErr)
		}
		time.Sleep(10 * time.Millisecond)
	}
	if err = manager.Stop(svc.ID); err != nil {
		t.Fatal(err)
	}
	time.Sleep(1200 * time.Millisecond)
	count, readErr := os.ReadFile(countFile)
	loaded, loadErr := db.Service(context.Background(), svc.ID)
	if readErr != nil || string(count) != "1" || loadErr != nil || loaded.Status != "stopped" || loaded.Enabled {
		t.Fatalf("pending retry was not cancelled: starts=%q service=%+v readErr=%v loadErr=%v", count, loaded, readErr, loadErr)
	}
}

func TestTunnelEnvironmentRemovesStaleToken(t *testing.T) {
	env := tunnelEnvironment([]string{"PATH=/bin", "TUNNEL_TOKEN=old", "OTHER=value"}, "new")
	joined := strings.Join(env, "\n")
	if strings.Count(joined, "TUNNEL_TOKEN=") != 1 || strings.Contains(joined, "=old") {
		t.Fatal(env)
	}
}

func TestQuickTunnelURLLifecycleAndIsolatedEnvironment(t *testing.T) {
	dir := t.TempDir()
	db, err := store.Open(filepath.Join(dir, "quick.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	_, port, _ := net.SplitHostPort(listener.Addr().String())
	number, _ := strconv.Atoi(port)
	svc := store.Service{ID: "quick", Name: "Quick", PublishMode: "quick", TunnelProtocol: "http", Protocol: "tcp", TargetHost: "127.0.0.1", TargetPort: number, CreatedAt: time.Now(), UpdatedAt: time.Now()}
	if err = db.CreateService(context.Background(), svc); err != nil {
		t.Fatal(err)
	}
	binary := filepath.Join(dir, "cloudflared")
	script := "#!/bin/sh\nprintf 'settings with sensitive data\\nhttps://bad.trycloudflare.com.evil.test\\nhttps://valid-host.trycloudflare.com\\n' >&2\nexec sleep 30\n"
	if err = os.WriteFile(binary, []byte(script), 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("TUNNEL_TOKEN", "inherited-secret")
	t.Setenv("TUNNEL_TOKEN_FILE", "/must-not-read")
	manager := NewManager(Config{CloudflaredBinary: binary}, db, slog.New(slog.NewTextHandler(io.Discard, nil)))
	defer manager.StopAll()
	ctx, cancel := context.WithCancel(context.Background())
	if err = manager.Start(ctx, svc); err != nil {
		t.Fatal(err)
	}
	cancel()
	deadline := time.Now().Add(3 * time.Second)
	for {
		loaded, err := db.Service(context.Background(), svc.ID)
		if err != nil {
			t.Fatal(err)
		}
		if loaded.RuntimeURL != "" {
			if loaded.RuntimeURL != "https://valid-host.trycloudflare.com" || loaded.Status != "tunnel_running" {
				t.Fatalf("invalid URL/status: %+v", loaded)
			}
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("URL not captured")
		}
		time.Sleep(10 * time.Millisecond)
	}
	manager.mu.Lock()
	running := manager.processes[svc.ID]
	env, args := strings.Join(running.cmd.Env, "\n"), strings.Join(running.cmd.Args, " ")
	manager.mu.Unlock()
	if strings.Contains(env, "TUNNEL_TOKEN") || !strings.Contains(args, "--config "+os.DevNull) || !strings.Contains(args, "--url http://127.0.0.1:") {
		t.Fatal("Quick Tunnel inherited account config")
	}
	if err = manager.Stop(svc.ID); err != nil {
		t.Fatal(err)
	}
	loaded, _ := db.Service(context.Background(), svc.ID)
	if loaded.RuntimeURL != "" || loaded.Enabled {
		t.Fatal("stale public URL after stop")
	}
}

func TestQuickTunnelOutputRejectsPartialAndForeignURLs(t *testing.T) {
	for _, value := range []string{"https://ok.trycloudflare.com.evil.test\n", "https://trycloudflare.com\n", "http://ok.trycloudflare.com\n"} {
		if quickURLPattern.MatchString(value) {
			t.Errorf("unsafe URL matched: %s", value)
		}
	}
	output := &quickTunnelOutput{}
	if _, err := output.Write([]byte("https://ok.trycloudflare.com")); err != nil || output.found {
		t.Fatal("partial line treated as full URL")
	}
}
