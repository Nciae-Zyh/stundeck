package httpapi

import (
	"context"
	"crypto/subtle"
	"errors"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/Nciae-Zyh/stundeck/internal/security"
	"github.com/Nciae-Zyh/stundeck/internal/store"
)

const sessionCookie = "stundeck_session"

type authContextKey struct{}

type authData struct {
	Session store.Session
	UserID  string
}

type authRequest struct {
	Username string `json:"username"`
	Password string `json:"password"`
}

type loginAttempt struct {
	Count        int
	BlockedUntil time.Time
}

type loginLimiter struct {
	mu       sync.Mutex
	attempts map[string]loginAttempt
}

func newLoginLimiter() *loginLimiter {
	return &loginLimiter{attempts: make(map[string]loginAttempt)}
}

func (l *loginLimiter) allowed(key string) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	attempt := l.attempts[key]
	return time.Now().After(attempt.BlockedUntil)
}

func (l *loginLimiter) failed(key string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	attempt := l.attempts[key]
	attempt.Count++
	if attempt.Count >= 5 {
		attempt.BlockedUntil = time.Now().Add(5 * time.Minute)
		attempt.Count = 0
	}
	l.attempts[key] = attempt
}

func (l *loginLimiter) clear(key string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	delete(l.attempts, key)
}

func (s *Server) authState(w http.ResponseWriter, r *http.Request) {
	initialized, err := s.store.HasAdmin(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, "auth_state_unavailable", "Unable to load authentication state")
		return
	}
	authenticated := false
	if cookie, err := r.Cookie(sessionCookie); err == nil {
		_, authenticated = s.loadSession(r.Context(), cookie.Value)
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"setupRequired": !initialized,
		"authenticated": authenticated,
	})
}

func (s *Server) setup(w http.ResponseWriter, r *http.Request) {
	initialized, err := s.store.HasAdmin(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, "setup_unavailable", "Unable to initialize StunDeck")
		return
	}
	if initialized {
		writeError(w, http.StatusConflict, "already_initialized", "StunDeck is already initialized")
		return
	}
	var input authRequest
	if !decodeJSON(w, r, &input) {
		return
	}
	input.Username = strings.TrimSpace(input.Username)
	if input.Username == "" || len(input.Username) > 64 {
		writeError(w, http.StatusBadRequest, "invalid_username", "Username is required")
		return
	}
	passwordHash, err := security.HashPassword(input.Password)
	if err != nil {
		writeError(w, http.StatusBadRequest, "weak_password", err.Error())
		return
	}
	userID, err := security.RandomToken(18)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "random_failed", "Unable to initialize StunDeck")
		return
	}
	user := store.User{ID: userID, Username: input.Username, PasswordHash: passwordHash, CreatedAt: time.Now()}
	if err := s.store.CreateAdmin(r.Context(), user); err != nil {
		writeError(w, http.StatusConflict, "setup_conflict", "StunDeck was initialized by another request")
		return
	}
	s.createAuthenticatedSession(w, r, user)
}

func (s *Server) login(w http.ResponseWriter, r *http.Request) {
	key := remoteHost(r.RemoteAddr)
	if !s.loginLimiter.allowed(key) {
		writeError(w, http.StatusTooManyRequests, "login_rate_limited", "Too many failed attempts; try again later")
		return
	}
	var input authRequest
	if !decodeJSON(w, r, &input) {
		return
	}
	user, err := s.store.UserByUsername(r.Context(), strings.TrimSpace(input.Username))
	if err != nil || !security.VerifyPassword(user.PasswordHash, input.Password) {
		s.loginLimiter.failed(key)
		time.Sleep(250 * time.Millisecond)
		writeError(w, http.StatusUnauthorized, "invalid_credentials", "Username or password is incorrect")
		return
	}
	s.loginLimiter.clear(key)
	s.createAuthenticatedSession(w, r, user)
}

func (s *Server) createAuthenticatedSession(w http.ResponseWriter, r *http.Request, user store.User) {
	token, err := security.RandomToken(32)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "session_failed", "Unable to create session")
		return
	}
	csrf, err := security.RandomToken(24)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "session_failed", "Unable to create session")
		return
	}
	session := store.Session{Token: token, UserID: user.ID, CSRFToken: csrf, ExpiresAt: time.Now().Add(s.sessionTTL)}
	if err := s.store.CreateSession(r.Context(), session); err != nil {
		writeError(w, http.StatusInternalServerError, "session_failed", "Unable to create session")
		return
	}
	http.SetCookie(w, &http.Cookie{
		Name:     sessionCookie,
		Value:    token,
		Path:     "/",
		HttpOnly: true,
		Secure:   s.secureCookies,
		SameSite: http.SameSiteStrictMode,
		Expires:  session.ExpiresAt,
	})
	writeJSON(w, http.StatusOK, map[string]any{
		"user":      map[string]string{"id": user.ID, "username": user.Username},
		"csrfToken": csrf,
	})
}

func (s *Server) protected(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		cookie, err := r.Cookie(sessionCookie)
		if err != nil {
			writeError(w, http.StatusUnauthorized, "authentication_required", "Authentication is required")
			return
		}
		session, ok := s.loadSession(r.Context(), cookie.Value)
		if !ok {
			writeError(w, http.StatusUnauthorized, "session_expired", "Session has expired")
			return
		}
		if r.Method != http.MethodGet && r.Method != http.MethodHead && r.Method != http.MethodOptions {
			provided := r.Header.Get("X-CSRF-Token")
			if provided == "" || len(provided) != len(session.CSRFToken) || subtle.ConstantTimeCompare([]byte(provided), []byte(session.CSRFToken)) != 1 {
				writeError(w, http.StatusForbidden, "invalid_csrf", "CSRF token is missing or invalid")
				return
			}
		}
		ctx := context.WithValue(r.Context(), authContextKey{}, authData{Session: session, UserID: session.UserID})
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

func remoteHost(remoteAddress string) string {
	host, _, err := net.SplitHostPort(remoteAddress)
	if err == nil {
		return host
	}
	return remoteAddress
}

func (s *Server) loadSession(ctx context.Context, token string) (store.Session, bool) {
	session, err := s.store.Session(ctx, token)
	return session, err == nil
}

func (s *Server) me(w http.ResponseWriter, r *http.Request) {
	auth, ok := r.Context().Value(authContextKey{}).(authData)
	if !ok {
		writeError(w, http.StatusUnauthorized, "authentication_required", "Authentication is required")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"userId":    auth.UserID,
		"csrfToken": auth.Session.CSRFToken,
	})
}

func (s *Server) logout(w http.ResponseWriter, r *http.Request) {
	cookie, err := r.Cookie(sessionCookie)
	if err == nil {
		_ = s.store.DeleteSession(r.Context(), cookie.Value)
	}
	http.SetCookie(w, &http.Cookie{
		Name:     sessionCookie,
		Value:    "",
		Path:     "/",
		HttpOnly: true,
		Secure:   s.secureCookies,
		SameSite: http.SameSiteStrictMode,
		MaxAge:   -1,
	})
	w.WriteHeader(http.StatusNoContent)
}

func isNotFound(err error) bool {
	return errors.Is(err, store.ErrNotFound)
}
