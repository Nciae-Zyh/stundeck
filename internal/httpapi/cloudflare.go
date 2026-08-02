package httpapi

import (
	"net/http"
	"strings"
	"time"

	cf "github.com/Nciae-Zyh/stundeck/internal/cloudflare"
	"github.com/Nciae-Zyh/stundeck/internal/security"
	"github.com/Nciae-Zyh/stundeck/internal/store"
)

type cloudflareTokenRequest struct {
	Token string `json:"token"`
}

type cloudflareConnectionRequest struct {
	ID       string `json:"id"`
	Name     string `json:"name"`
	Token    string `json:"token"`
	ZoneID   string `json:"zoneId"`
	ZoneName string `json:"zoneName"`
}

func (s *Server) validateCloudflare(w http.ResponseWriter, r *http.Request) {
	var input cloudflareTokenRequest
	if !decodeJSON(w, r, &input) {
		return
	}
	input.Token = strings.TrimSpace(input.Token)
	if input.Token == "" {
		writeError(w, http.StatusBadRequest, "token_required", "Cloudflare API Token is required")
		return
	}
	client := cf.New(input.Token)
	status, err := client.VerifyToken(r.Context())
	if err != nil {
		writeError(w, http.StatusBadRequest, "token_invalid", err.Error())
		return
	}
	zones, err := client.Zones(r.Context())
	if err != nil {
		writeError(w, http.StatusBadRequest, "zone_access_failed", "Token is active but cannot list zones; add Zone Read permission")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"token": status, "zones": zones})
}

func (s *Server) cloudflareConnections(w http.ResponseWriter, r *http.Request) {
	connections, err := s.store.CloudflareConnections(r.Context())
	if err != nil {
		mapStoreError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"connections": connections})
}

func (s *Server) saveCloudflareConnection(w http.ResponseWriter, r *http.Request) {
	var input cloudflareConnectionRequest
	if !decodeJSON(w, r, &input) {
		return
	}
	input.Name = strings.TrimSpace(input.Name)
	input.Token = strings.TrimSpace(input.Token)
	input.ZoneID = strings.TrimSpace(input.ZoneID)
	input.ZoneName = strings.TrimSpace(strings.ToLower(input.ZoneName))
	if input.Name == "" || input.Token == "" || input.ZoneID == "" || input.ZoneName == "" {
		writeError(w, http.StatusBadRequest, "connection_invalid", "Name, token, zone ID and zone name are required")
		return
	}
	client := cf.New(input.Token)
	if _, err := client.VerifyToken(r.Context()); err != nil {
		writeError(w, http.StatusBadRequest, "token_invalid", err.Error())
		return
	}
	zones, err := client.Zones(r.Context())
	if err != nil {
		writeError(w, http.StatusBadRequest, "zone_access_failed", "Token cannot list the selected zone")
		return
	}
	matched := false
	for _, zone := range zones {
		if zone.ID == input.ZoneID && strings.EqualFold(zone.Name, input.ZoneName) {
			matched = true
			break
		}
	}
	if !matched {
		writeError(w, http.StatusBadRequest, "zone_mismatch", "Selected zone is not accessible with this token")
		return
	}
	ciphertext, err := s.cipher.Encrypt(input.Token)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "token_storage_failed", "Unable to encrypt Cloudflare token")
		return
	}
	now := time.Now()
	connection := store.CloudflareConnection{
		ID:              input.ID,
		Name:            input.Name,
		TokenCiphertext: ciphertext,
		ZoneID:          input.ZoneID,
		ZoneName:        input.ZoneName,
		CreatedAt:       now,
		UpdatedAt:       now,
	}
	if connection.ID == "" {
		connection.ID, err = security.RandomToken(18)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "random_failed", "Unable to create connection")
			return
		}
	} else if existing, err := s.store.CloudflareConnection(r.Context(), connection.ID); err == nil {
		connection.CreatedAt = existing.CreatedAt
	}
	if err := s.store.UpsertCloudflareConnection(r.Context(), connection); err != nil {
		mapStoreError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{"connection": connection})
}

func (s *Server) deleteCloudflareConnection(w http.ResponseWriter, r *http.Request) {
	id, ok := requireID(w, r)
	if !ok {
		return
	}
	if err := s.store.DeleteCloudflareConnection(r.Context(), id); err != nil {
		if strings.Contains(err.Error(), "still used") {
			writeError(w, http.StatusConflict, "connection_in_use", err.Error())
			return
		}
		mapStoreError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
