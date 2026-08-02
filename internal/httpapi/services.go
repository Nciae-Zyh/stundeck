package httpapi

import (
	"context"
	"errors"
	"net"
	"net/http"
	"regexp"
	"strings"
	"time"

	cf "github.com/Nciae-Zyh/stundeck/internal/cloudflare"
	"github.com/Nciae-Zyh/stundeck/internal/engine"
	"github.com/Nciae-Zyh/stundeck/internal/security"
	"github.com/Nciae-Zyh/stundeck/internal/store"
)

var hostnamePattern = regexp.MustCompile(`(?i)^(?:[a-z0-9](?:[a-z0-9-]{0,61}[a-z0-9])?\.)*[a-z0-9](?:[a-z0-9-]{0,61}[a-z0-9])?$`)

type serviceRequest struct {
	Name                   string `json:"name"`
	TargetHost             string `json:"targetHost"`
	TargetPort             int    `json:"targetPort"`
	Protocol               string `json:"protocol"`
	BindPort               int    `json:"bindPort"`
	Scheme                 string `json:"scheme"`
	PublishMode            string `json:"publishMode"`
	CloudflareConnectionID string `json:"cloudflareConnectionId"`
	EntryHostname          string `json:"entryHostname"`
	OriginHostname         string `json:"originHostname"`
	RedirectStatus         int    `json:"redirectStatus"`
	PreservePath           bool   `json:"preservePath"`
	PreserveQuery          bool   `json:"preserveQuery"`
	ManageDNS              bool   `json:"manageDns"`
}

func (s *Server) services(w http.ResponseWriter, r *http.Request) {
	services, err := s.store.Services(r.Context())
	if err != nil {
		mapStoreError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"services": services})
}

func (s *Server) createService(w http.ResponseWriter, r *http.Request) {
	var input serviceRequest
	if !decodeJSON(w, r, &input) {
		return
	}
	service, err := s.serviceFromRequest(r.Context(), input)
	if err != nil {
		writeError(w, http.StatusBadRequest, "service_invalid", err.Error())
		return
	}
	service.ID, err = security.RandomToken(18)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "random_failed", "Unable to create service")
		return
	}
	now := time.Now()
	service.CreatedAt = now
	service.UpdatedAt = now
	service.Status = "stopped"
	if err := s.store.CreateService(r.Context(), service); err != nil {
		mapStoreError(w, err)
		return
	}
	s.addEvent(r.Context(), store.Event{ServiceID: service.ID, Type: "service.created", Level: "info", Message: "Service configuration created"})
	writeJSON(w, http.StatusCreated, map[string]any{"service": service})
}

func (s *Server) updateService(w http.ResponseWriter, r *http.Request) {
	id, ok := requireID(w, r)
	if !ok {
		return
	}
	existing, err := s.store.Service(r.Context(), id)
	if err != nil {
		mapStoreError(w, err)
		return
	}
	if s.engine.Running(id) {
		writeError(w, http.StatusConflict, "service_running", "Stop the service before changing its configuration")
		return
	}
	var input serviceRequest
	if !decodeJSON(w, r, &input) {
		return
	}
	service, err := s.serviceFromRequest(r.Context(), input)
	if err != nil {
		writeError(w, http.StatusBadRequest, "service_invalid", err.Error())
		return
	}
	service.ID = existing.ID
	service.CreatedAt = existing.CreatedAt
	service.UpdatedAt = time.Now()
	service.Status = existing.Status
	service.LastError = existing.LastError
	service.PublicIP = existing.PublicIP
	service.PublicPort = existing.PublicPort
	service.MappingChangedAt = existing.MappingChangedAt
	if err := s.store.UpdateService(r.Context(), service); err != nil {
		mapStoreError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"service": service})
}

func (s *Server) deleteService(w http.ResponseWriter, r *http.Request) {
	id, ok := requireID(w, r)
	if !ok {
		return
	}
	if s.engine.Running(id) {
		writeError(w, http.StatusConflict, "service_running", "Stop the service before deleting it")
		return
	}
	if err := s.store.DeleteService(r.Context(), id); err != nil {
		mapStoreError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) startService(w http.ResponseWriter, r *http.Request) {
	id, ok := requireID(w, r)
	if !ok {
		return
	}
	service, err := s.store.Service(r.Context(), id)
	if err != nil {
		mapStoreError(w, err)
		return
	}
	if err := s.engine.Start(context.Background(), service); err != nil {
		_ = s.store.SetServiceRuntime(r.Context(), service.ID, "error", err.Error(), false)
		s.addEvent(r.Context(), store.Event{ServiceID: service.ID, Type: "engine.start_failed", Level: "error", Message: err.Error()})
		writeError(w, http.StatusBadRequest, "engine_start_failed", err.Error())
		return
	}
	s.addEvent(r.Context(), store.Event{ServiceID: service.ID, Type: "engine.started", Level: "info", Message: "NAT mapping discovery started"})
	updated, _ := s.store.Service(r.Context(), id)
	writeJSON(w, http.StatusOK, map[string]any{"service": updated})
}

func (s *Server) stopService(w http.ResponseWriter, r *http.Request) {
	id, ok := requireID(w, r)
	if !ok {
		return
	}
	if err := s.engine.Stop(id); err != nil {
		mapStoreError(w, err)
		return
	}
	s.addEvent(r.Context(), store.Event{ServiceID: id, Type: "engine.stopped", Level: "info", Message: "NAT mapping discovery stopped"})
	updated, _ := s.store.Service(r.Context(), id)
	writeJSON(w, http.StatusOK, map[string]any{"service": updated})
}

func (s *Server) syncService(w http.ResponseWriter, r *http.Request) {
	id, ok := requireID(w, r)
	if !ok {
		return
	}
	service, err := s.store.Service(r.Context(), id)
	if err != nil {
		mapStoreError(w, err)
		return
	}
	result, err := s.syncCloudflare(r.Context(), service)
	if err != nil {
		writeError(w, http.StatusBadRequest, "cloudflare_sync_failed", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"sync": result})
}

func (s *Server) natmapEvent(w http.ResponseWriter, r *http.Request) {
	if !bearerMatches(r.Header.Get("Authorization"), s.internalToken) {
		writeError(w, http.StatusUnauthorized, "invalid_callback_token", "Callback token is invalid")
		return
	}
	var mapping engine.Mapping
	if !decodeJSON(w, r, &mapping) {
		return
	}
	if err := engine.ValidateMapping(mapping); err != nil {
		writeError(w, http.StatusBadRequest, "mapping_invalid", err.Error())
		return
	}
	if _, err := s.store.Service(r.Context(), mapping.ServiceID); err != nil {
		mapStoreError(w, err)
		return
	}
	go s.handleMapping(mapping)
	writeJSON(w, http.StatusAccepted, map[string]bool{"accepted": true})
}

func (s *Server) handleMapping(mapping engine.Mapping) {
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	changed, err := s.store.SetServiceMapping(ctx, mapping.ServiceID, mapping.PublicIP, mapping.PublicPort)
	if err != nil {
		s.logger.Error("save nat mapping", "service_id", mapping.ServiceID, "error", err)
		return
	}
	if changed {
		s.addEvent(ctx, store.Event{
			ServiceID: mapping.ServiceID,
			Type:      "mapping.changed",
			Level:     "info",
			Message:   "Public mapping changed to " + formatPublicEndpoint(mapping.PublicIP, mapping.PublicPort),
			Payload: map[string]any{
				"publicIp": mapping.PublicIP, "publicPort": mapping.PublicPort, "protocol": mapping.Protocol,
			},
		})
	}
	service, err := s.store.Service(ctx, mapping.ServiceID)
	if err != nil || service.PublishMode != "redirect" {
		return
	}
	if _, err := s.syncCloudflare(ctx, service); err != nil {
		_ = s.store.SetServiceRuntime(ctx, service.ID, "sync_error", err.Error(), true)
		s.addEvent(ctx, store.Event{ServiceID: service.ID, Type: "cloudflare.sync_failed", Level: "error", Message: err.Error()})
	}
}

func (s *Server) syncCloudflare(ctx context.Context, service store.Service) (cf.SyncResult, error) {
	if service.PublishMode != "redirect" {
		return cf.SyncResult{}, errors.New("service is not configured for Cloudflare redirect publishing")
	}
	connection, err := s.store.CloudflareConnection(ctx, service.CloudflareConnectionID)
	if err != nil {
		return cf.SyncResult{}, errors.New("Cloudflare connection is missing")
	}
	token, err := s.cipher.Decrypt(connection.TokenCiphertext)
	if err != nil {
		return cf.SyncResult{}, errors.New("Cloudflare token could not be decrypted")
	}
	result, err := cf.New(token).ReconcileService(ctx, connection.ZoneID, service)
	if err != nil {
		return cf.SyncResult{}, err
	}
	_ = s.store.SetServiceRuntime(ctx, service.ID, "healthy", "", true)
	s.addEvent(ctx, store.Event{
		ServiceID: service.ID,
		Type:      "cloudflare.synced",
		Level:     "info",
		Message:   "Cloudflare redirect synchronized",
		Payload:   map[string]any{"targetUrl": result.TargetURL, "ruleId": result.RuleID},
	})
	return result, nil
}

func (s *Server) serviceFromRequest(ctx context.Context, input serviceRequest) (store.Service, error) {
	service := store.Service{
		Name:                   strings.TrimSpace(input.Name),
		TargetHost:             strings.TrimSpace(input.TargetHost),
		TargetPort:             input.TargetPort,
		Protocol:               strings.ToLower(strings.TrimSpace(input.Protocol)),
		BindPort:               input.BindPort,
		Scheme:                 strings.ToLower(strings.TrimSpace(input.Scheme)),
		PublishMode:            strings.ToLower(strings.TrimSpace(input.PublishMode)),
		CloudflareConnectionID: strings.TrimSpace(input.CloudflareConnectionID),
		EntryHostname:          strings.ToLower(strings.TrimSpace(input.EntryHostname)),
		OriginHostname:         strings.ToLower(strings.TrimSpace(input.OriginHostname)),
		RedirectStatus:         input.RedirectStatus,
		PreservePath:           input.PreservePath,
		PreserveQuery:          input.PreserveQuery,
		ManageDNS:              input.ManageDNS,
	}
	if service.Name == "" || len(service.Name) > 100 {
		return store.Service{}, errors.New("service name is required")
	}
	if !validHost(service.TargetHost) {
		return store.Service{}, errors.New("target host is invalid")
	}
	if service.TargetPort < 1 || service.TargetPort > 65535 {
		return store.Service{}, errors.New("target port must be between 1 and 65535")
	}
	if service.Protocol != "tcp" && service.Protocol != "udp" {
		return store.Service{}, errors.New("protocol must be tcp or udp")
	}
	if service.BindPort != 0 && (service.BindPort < 1024 || service.BindPort > 65535) {
		return store.Service{}, errors.New("bind port must be 0 or between 1024 and 65535")
	}
	if service.Scheme == "" {
		service.Scheme = "http"
	}
	if service.Scheme != "http" && service.Scheme != "https" {
		return store.Service{}, errors.New("scheme must be http or https")
	}
	if service.PublishMode == "" {
		service.PublishMode = "direct"
	}
	if service.PublishMode != "direct" && service.PublishMode != "redirect" {
		return store.Service{}, errors.New("publish mode must be direct or redirect")
	}
	if service.RedirectStatus == 0 {
		service.RedirectStatus = 302
	}
	if service.PublishMode == "redirect" {
		if service.Protocol != "tcp" {
			return store.Service{}, errors.New("Cloudflare HTTP redirects can only publish TCP web services")
		}
		if service.RedirectStatus != 302 && service.RedirectStatus != 307 {
			return store.Service{}, errors.New("redirect status must be 302 or 307")
		}
		if !validHostname(service.EntryHostname) {
			return store.Service{}, errors.New("entry hostname is invalid")
		}
		if service.Scheme == "https" && !validHostname(service.OriginHostname) {
			return store.Service{}, errors.New("HTTPS redirects require an origin hostname with a valid certificate")
		}
		if service.OriginHostname != "" && !validHostname(service.OriginHostname) {
			return store.Service{}, errors.New("origin hostname is invalid")
		}
		if service.CloudflareConnectionID == "" {
			return store.Service{}, errors.New("Cloudflare connection is required")
		}
		if _, err := s.store.CloudflareConnection(ctx, service.CloudflareConnectionID); err != nil {
			return store.Service{}, errors.New("Cloudflare connection does not exist")
		}
	}
	return service, nil
}

func validHost(value string) bool {
	return net.ParseIP(value) != nil || validHostname(value)
}

func validHostname(value string) bool {
	return len(value) > 0 && len(value) <= 253 && hostnamePattern.MatchString(value)
}
