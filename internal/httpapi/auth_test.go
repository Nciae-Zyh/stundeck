package httpapi

import (
	"bytes"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Nciae-Zyh/stundeck/internal/engine"
	"github.com/Nciae-Zyh/stundeck/internal/security"
	"github.com/Nciae-Zyh/stundeck/internal/store"
	"github.com/Nciae-Zyh/stundeck/internal/webhook"
)

func TestSetupCreatesLocalSessionWithoutEchoingPassword(t *testing.T) {
	tempDir := t.TempDir()
	database, err := store.Open(filepath.Join(tempDir, "stundeck.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	cipher, err := security.LoadOrCreateCipher(filepath.Join(tempDir, "master.key"))
	if err != nil {
		t.Fatal(err)
	}
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	dispatcher := webhook.NewDispatcher(database, cipher, logger)
	manager := engine.NewManager(engine.Config{Binary: "missing-natmap"}, database, logger)
	server := New(Config{
		Store: database, Cipher: cipher, Engine: manager, Webhooks: dispatcher,
		Logger: logger, SessionTTL: time.Hour, InternalToken: "internal-test-token", StartedAt: time.Now(),
	})
	payload := []byte(`{"username":"admin","password":"a secure local password"}`)
	request := httptest.NewRequest(http.MethodPost, "/api/v1/auth/setup", bytes.NewReader(payload))
	response := httptest.NewRecorder()
	server.Handler().ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", response.Code, response.Body.String())
	}
	if strings.Contains(response.Body.String(), "a secure local password") {
		t.Fatal("response echoed password")
	}
	if len(response.Result().Cookies()) != 1 || !response.Result().Cookies()[0].HttpOnly {
		t.Fatal("secure session cookie was not created")
	}
	var body map[string]any
	if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body["csrfToken"] == "" {
		t.Fatal("csrf token missing")
	}
}

func TestRemoteHostIgnoresEphemeralPort(t *testing.T) {
	if got := remoteHost("192.0.2.10:49152"); got != "192.0.2.10" {
		t.Fatalf("remoteHost() = %q", got)
	}
	if got := remoteHost("[2001:db8::10]:49152"); got != "2001:db8::10" {
		t.Fatalf("remoteHost() = %q", got)
	}
}
