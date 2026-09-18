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
