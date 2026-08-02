package httpapi

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/Nciae-Zyh/stundeck/internal/engine"
	"github.com/Nciae-Zyh/stundeck/internal/security"
	"github.com/Nciae-Zyh/stundeck/internal/store"
	"github.com/Nciae-Zyh/stundeck/internal/version"
	"github.com/Nciae-Zyh/stundeck/internal/webhook"
	"github.com/Nciae-Zyh/stundeck/web"
)

type Config struct {
	Store         *store.Store
	Cipher        *security.Cipher
	Engine        *engine.Manager
	Webhooks      *webhook.Dispatcher
	Logger        *slog.Logger
	SecureCookies bool
	SessionTTL    time.Duration
	InternalToken string
	StartedAt     time.Time
}

type Server struct {
	store         *store.Store
	cipher        *security.Cipher
	engine        *engine.Manager
	webhooks      *webhook.Dispatcher
	logger        *slog.Logger
	secureCookies bool
	sessionTTL    time.Duration
	internalToken string
	startedAt     time.Time
	loginLimiter  *loginLimiter
}

func New(config Config) *Server {
	return &Server{
		store:         config.Store,
		cipher:        config.Cipher,
		engine:        config.Engine,
		webhooks:      config.Webhooks,
		logger:        config.Logger,
		secureCookies: config.SecureCookies,
		sessionTTL:    config.SessionTTL,
		internalToken: config.InternalToken,
		startedAt:     config.StartedAt,
		loginLimiter:  newLoginLimiter(),
	}
}

func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/v1/health", s.health)
	mux.HandleFunc("GET /api/v1/auth/state", s.authState)
	mux.HandleFunc("POST /api/v1/auth/setup", s.setup)
	mux.HandleFunc("POST /api/v1/auth/login", s.login)
	mux.HandleFunc("POST /internal/v1/natmap-event", s.natmapEvent)

	mux.Handle("GET /api/v1/status", s.protected(http.HandlerFunc(s.status)))
	mux.Handle("GET /api/v1/access-policy", s.protected(http.HandlerFunc(s.getAccessPolicy)))
	mux.Handle("PUT /api/v1/access-policy", s.protected(http.HandlerFunc(s.updateAccessPolicy)))
	mux.Handle("GET /api/v1/auth/me", s.protected(http.HandlerFunc(s.me)))
	mux.Handle("POST /api/v1/auth/logout", s.protected(http.HandlerFunc(s.logout)))
	mux.Handle("GET /api/v1/security", s.protected(http.HandlerFunc(s.securityState)))
	mux.Handle("POST /api/v1/security/totp/begin", s.protected(http.HandlerFunc(s.beginTOTP)))
	mux.Handle("POST /api/v1/security/totp/confirm", s.protected(http.HandlerFunc(s.confirmTOTP)))
	mux.Handle("DELETE /api/v1/security/totp", s.protected(http.HandlerFunc(s.disableTOTP)))
	mux.Handle("GET /api/v1/cloudflare/connections", s.protected(http.HandlerFunc(s.cloudflareConnections)))
	mux.Handle("POST /api/v1/cloudflare/validate", s.protected(http.HandlerFunc(s.validateCloudflare)))
	mux.Handle("POST /api/v1/cloudflare/connections", s.protected(http.HandlerFunc(s.saveCloudflareConnection)))
	mux.Handle("DELETE /api/v1/cloudflare/connections/{id}", s.protected(http.HandlerFunc(s.deleteCloudflareConnection)))
	mux.Handle("GET /api/v1/services", s.protected(http.HandlerFunc(s.services)))
	mux.Handle("POST /api/v1/services", s.protected(http.HandlerFunc(s.createService)))
	mux.Handle("PUT /api/v1/services/{id}", s.protected(http.HandlerFunc(s.updateService)))
	mux.Handle("DELETE /api/v1/services/{id}", s.protected(http.HandlerFunc(s.deleteService)))
	mux.Handle("POST /api/v1/services/{id}/start", s.protected(http.HandlerFunc(s.startService)))
	mux.Handle("POST /api/v1/services/{id}/stop", s.protected(http.HandlerFunc(s.stopService)))
	mux.Handle("POST /api/v1/services/{id}/sync", s.protected(http.HandlerFunc(s.syncService)))
	mux.Handle("POST /api/v1/services/{id}/diagnose", s.protected(http.HandlerFunc(s.diagnoseService)))
	mux.Handle("GET /api/v1/events", s.protected(http.HandlerFunc(s.events)))
	mux.Handle("GET /api/v1/webhooks", s.protected(http.HandlerFunc(s.listWebhooks)))
	mux.Handle("POST /api/v1/webhooks", s.protected(http.HandlerFunc(s.createWebhook)))
	mux.Handle("DELETE /api/v1/webhooks/{id}", s.protected(http.HandlerFunc(s.deleteWebhook)))
	mux.Handle("POST /api/v1/webhooks/{id}/test", s.protected(http.HandlerFunc(s.testWebhook)))
	mux.Handle("/", web.Handler())
	return s.securityHeaders(s.accessPolicy(s.requestLog(mux)))
}

func (s *Server) health(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{
		"status":  "ok",
		"version": version.Version,
	})
}

func (s *Server) status(w http.ResponseWriter, r *http.Request) {
	initialized, err := s.store.HasAdmin(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, "status_unavailable", "Unable to load system status")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"version":         version.Version,
		"commit":          version.Commit,
		"uptimeSeconds":   int(time.Since(s.startedAt).Seconds()),
		"initialized":     initialized,
		"engineAvailable": s.engine.Available(),
	})
}

func (s *Server) requestLog(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		started := time.Now()
		next.ServeHTTP(w, r)
		if !strings.HasPrefix(r.URL.Path, "/api/") && !strings.HasPrefix(r.URL.Path, "/internal/") {
			return
		}
		s.logger.Debug("http request", "method", r.Method, "path", r.URL.Path, "duration", time.Since(started))
	})
}

func (s *Server) securityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("X-Frame-Options", "DENY")
		w.Header().Set("Referrer-Policy", "no-referrer")
		w.Header().Set("Permissions-Policy", "camera=(), microphone=(), geolocation=()")
		w.Header().Set("Content-Security-Policy", "default-src 'self'; style-src 'self' 'unsafe-inline'; img-src 'self' data:; connect-src 'self'; frame-ancestors 'none'; base-uri 'none'; form-action 'self'")
		next.ServeHTTP(w, r)
	})
}

func decodeJSON(w http.ResponseWriter, r *http.Request, target any) bool {
	r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_json", "Request body is invalid")
		return false
	}
	if decoder.More() {
		writeError(w, http.StatusBadRequest, "invalid_json", "Request body must contain one JSON object")
		return false
	}
	return true
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}

func writeError(w http.ResponseWriter, status int, code, message string) {
	writeJSON(w, status, map[string]any{
		"error": map[string]string{
			"code":    code,
			"message": message,
		},
	})
}

func (s *Server) addEvent(ctx context.Context, event store.Event) {
	if event.ID == "" {
		event.ID, _ = security.RandomToken(18)
	}
	if event.CreatedAt.IsZero() {
		event.CreatedAt = time.Now()
	}
	if err := s.store.AddEvent(ctx, event); err != nil {
		s.logger.Error("save event", "type", event.Type, "error", err)
		return
	}
	if err := s.webhooks.Enqueue(ctx, event); err != nil {
		s.logger.Error("queue webhook event", "event_id", event.ID, "error", err)
	}
}

func bearerMatches(header, expected string) bool {
	provided := strings.TrimPrefix(header, "Bearer ")
	if len(provided) != len(expected) || expected == "" {
		return false
	}
	return subtle.ConstantTimeCompare([]byte(provided), []byte(expected)) == 1
}

func mapStoreError(w http.ResponseWriter, err error) {
	if errors.Is(err, store.ErrNotFound) {
		writeError(w, http.StatusNotFound, "not_found", "Resource was not found")
		return
	}
	writeError(w, http.StatusInternalServerError, "internal_error", "The operation could not be completed")
}

func requireID(w http.ResponseWriter, r *http.Request) (string, bool) {
	id := strings.TrimSpace(r.PathValue("id"))
	if id == "" {
		writeError(w, http.StatusBadRequest, "missing_id", "Resource ID is required")
		return "", false
	}
	return id, true
}

func formatPublicEndpoint(ip string, port int) string {
	if strings.Contains(ip, ":") {
		ip = "[" + ip + "]"
	}
	return fmt.Sprintf("%s:%d", ip, port)
}
