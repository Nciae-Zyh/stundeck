package httpapi

import (
	"net/http"
	"strings"
	"time"

	"github.com/Nciae-Zyh/stundeck/internal/security"
	"github.com/Nciae-Zyh/stundeck/internal/store"
	webhookpkg "github.com/Nciae-Zyh/stundeck/internal/webhook"
)

type webhookRequest struct {
	Name         string `json:"name"`
	URL          string `json:"url"`
	AllowPrivate bool   `json:"allowPrivate"`
}

func (s *Server) listWebhooks(w http.ResponseWriter, r *http.Request) {
	webhooks, err := s.store.Webhooks(r.Context())
	if err != nil {
		mapStoreError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"webhooks": webhooks})
}

func (s *Server) createWebhook(w http.ResponseWriter, r *http.Request) {
	var input webhookRequest
	if !decodeJSON(w, r, &input) {
		return
	}
	input.Name = strings.TrimSpace(input.Name)
	input.URL = strings.TrimSpace(input.URL)
	if input.Name == "" || len(input.Name) > 100 {
		writeError(w, http.StatusBadRequest, "webhook_invalid", "Webhook name is required")
		return
	}
	if err := webhookpkg.ValidateURL(input.URL, input.AllowPrivate); err != nil {
		writeError(w, http.StatusBadRequest, "webhook_invalid", err.Error())
		return
	}
	secret, err := security.RandomToken(32)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "random_failed", "Unable to create webhook")
		return
	}
	ciphertext, err := s.cipher.Encrypt(secret)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "webhook_storage_failed", "Unable to encrypt webhook secret")
		return
	}
	id, err := security.RandomToken(18)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "random_failed", "Unable to create webhook")
		return
	}
	now := time.Now()
	webhook := store.Webhook{
		ID:               id,
		Name:             input.Name,
		URL:              input.URL,
		SecretCiphertext: ciphertext,
		AllowPrivate:     input.AllowPrivate,
		Enabled:          true,
		CreatedAt:        now,
		UpdatedAt:        now,
	}
	if err := s.store.CreateWebhook(r.Context(), webhook); err != nil {
		mapStoreError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{
		"webhook": webhook,
		"secret":  secret,
		"warning": "This secret is shown only once.",
	})
}

func (s *Server) deleteWebhook(w http.ResponseWriter, r *http.Request) {
	id, ok := requireID(w, r)
	if !ok {
		return
	}
	if err := s.store.DeleteWebhook(r.Context(), id); err != nil {
		mapStoreError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) testWebhook(w http.ResponseWriter, r *http.Request) {
	id, ok := requireID(w, r)
	if !ok {
		return
	}
	if _, err := s.store.Webhook(r.Context(), id); err != nil {
		mapStoreError(w, err)
		return
	}
	eventID, _ := security.RandomToken(18)
	event := store.Event{
		ID:        eventID,
		Type:      "webhook.test",
		Level:     "info",
		Message:   "StunDeck webhook test",
		Payload:   map[string]any{"requestedWebhookId": id},
		CreatedAt: time.Now(),
	}
	if err := s.store.AddEvent(r.Context(), event); err != nil {
		mapStoreError(w, err)
		return
	}
	if err := s.webhooks.EnqueueTo(r.Context(), id, event); err != nil {
		mapStoreError(w, err)
		return
	}
	writeJSON(w, http.StatusAccepted, map[string]bool{"queued": true})
}
