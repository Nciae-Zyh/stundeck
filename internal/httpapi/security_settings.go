package httpapi

import (
	"net/http"
	"strings"
	"time"

	"github.com/Nciae-Zyh/stundeck/internal/security"
)

type totpConfirmRequest struct {
	Secret string `json:"secret"`
	Code   string `json:"code"`
}

type totpDisableRequest struct {
	Password string `json:"password"`
	Code     string `json:"code"`
}

func (s *Server) securityState(w http.ResponseWriter, r *http.Request) {
	user, ok := s.authenticatedUser(w, r)
	if !ok {
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"username":    user.Username,
		"totpEnabled": user.TOTPEnabled,
	})
}

func (s *Server) beginTOTP(w http.ResponseWriter, r *http.Request) {
	user, ok := s.authenticatedUser(w, r)
	if !ok {
		return
	}
	if user.TOTPEnabled {
		writeError(w, http.StatusConflict, "totp_already_enabled", "Two-factor authentication is already enabled")
		return
	}
	secret, err := security.GenerateTOTPSecret()
	if err != nil {
		writeError(w, http.StatusInternalServerError, "totp_setup_failed", "Unable to create an authenticator secret")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"secret": secret,
		"uri":    security.TOTPURI("StunDeck", user.Username, secret),
	})
}

func (s *Server) confirmTOTP(w http.ResponseWriter, r *http.Request) {
	user, ok := s.authenticatedUser(w, r)
	if !ok {
		return
	}
	if user.TOTPEnabled {
		writeError(w, http.StatusConflict, "totp_already_enabled", "Two-factor authentication is already enabled")
		return
	}
	var input totpConfirmRequest
	if !decodeJSON(w, r, &input) {
		return
	}
	input.Secret = strings.TrimSpace(input.Secret)
	if !security.ValidateTOTP(input.Secret, input.Code, time.Now()) {
		writeError(w, http.StatusBadRequest, "totp_invalid", "Authenticator code is invalid")
		return
	}
	ciphertext, err := s.cipher.Encrypt(input.Secret)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "totp_setup_failed", "Unable to encrypt the authenticator secret")
		return
	}
	if err := s.store.SetUserTOTP(r.Context(), user.ID, ciphertext, true); err != nil {
		mapStoreError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"totpEnabled": true})
}

func (s *Server) disableTOTP(w http.ResponseWriter, r *http.Request) {
	user, ok := s.authenticatedUser(w, r)
	if !ok {
		return
	}
	if !user.TOTPEnabled {
		writeError(w, http.StatusConflict, "totp_not_enabled", "Two-factor authentication is not enabled")
		return
	}
	var input totpDisableRequest
	if !decodeJSON(w, r, &input) {
		return
	}
	secret, err := s.cipher.Decrypt(user.TOTPSecretCiphertext)
	if !security.VerifyPassword(user.PasswordHash, input.Password) || err != nil || !security.ValidateTOTP(secret, input.Code, time.Now()) {
		writeError(w, http.StatusUnauthorized, "totp_disable_denied", "Password or authenticator code is invalid")
		return
	}
	if err := s.store.SetUserTOTP(r.Context(), user.ID, "", false); err != nil {
		mapStoreError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"totpEnabled": false})
}

func (s *Server) authenticatedUser(w http.ResponseWriter, r *http.Request) (userResult, bool) {
	auth, ok := r.Context().Value(authContextKey{}).(authData)
	if !ok {
		writeError(w, http.StatusUnauthorized, "authentication_required", "Authentication is required")
		return userResult{}, false
	}
	user, err := s.store.User(r.Context(), auth.UserID)
	if err != nil {
		mapStoreError(w, err)
		return userResult{}, false
	}
	return userResult{
		ID:                   user.ID,
		Username:             user.Username,
		PasswordHash:         user.PasswordHash,
		TOTPSecretCiphertext: user.TOTPSecretCiphertext,
		TOTPEnabled:          user.TOTPEnabled,
	}, true
}

type userResult struct {
	ID                   string
	Username             string
	PasswordHash         string
	TOTPSecretCiphertext string
	TOTPEnabled          bool
}
