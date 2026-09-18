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
	PrivateNetwork         string `json:"privateNetwork"`
	TunnelProtocol         string `json:"tunnelProtocol"`
	EdgePort               int    `json:"edgePort"`
	Name                   string `json:"name"`
	TargetHost             string `json:"targetHost"`
	TargetPort             int    `json:"targetPort"`
	Protocol               string `json:"protocol"`
	BindPort               int    `json:"bindPort"`
	GatewayMode            string `json:"gatewayMode"`
	GatewayAddress         string `json:"gatewayAddress"`
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
	s.serviceMu.Lock()
	defer s.serviceMu.Unlock()
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
	s.serviceMu.Lock()
	defer s.serviceMu.Unlock()
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
	resources, resourceErr := s.store.CloudflareResources(r.Context(), existing.CloudflareConnectionID)
	if resourceErr != nil {
		mapStoreError(w, resourceErr)
		return
	}
	for _, resource := range resources {
		if resource.OwnerID == existing.ID && (existing.CloudflareConnectionID != service.CloudflareConnectionID || existing.EntryHostname != service.EntryHostname || existing.OriginHostname != service.OriginHostname || existing.PublishMode != service.PublishMode || existing.ManageDNS != service.ManageDNS || existing.PrivateNetwork != service.PrivateNetwork) {
			writeError(w, 409, "cleanup_required", "更换发布方式、连接或域名前，请先清理此服务的云端发布")
			return
		}
	}
	service.ID = existing.ID
	service.CreatedAt = existing.CreatedAt
	service.UpdatedAt = time.Now()
	service.Status = existing.Status
	service.LastError = existing.LastError
	service.PublicIP = existing.PublicIP
	service.PublicPort = existing.PublicPort
	service.MappingChangedAt = existing.MappingChangedAt
	service.RuntimeURL = existing.RuntimeURL
	if err := s.store.UpdateService(r.Context(), service); err != nil {
		mapStoreError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"service": service})
}

func (s *Server) deleteService(w http.ResponseWriter, r *http.Request) {
	s.serviceMu.Lock()
	defer s.serviceMu.Unlock()
	id, ok := requireID(w, r)
	if !ok {
		return
	}
	if s.engine.Running(id) {
		writeError(w, http.StatusConflict, "service_running", "Stop the service before deleting it")
		return
	}
	existing, err := s.store.Service(r.Context(), id)
	if err != nil {
		mapStoreError(w, err)
		return
	}
	resources, err := s.store.CloudflareResources(r.Context(), existing.CloudflareConnectionID)
	if err != nil {
		mapStoreError(w, err)
		return
	}
	for _, resource := range resources {
		if resource.OwnerID == id {
			writeError(w, 409, "cleanup_required", "请先清理此服务的云端发布，再删除服务；Access 单独管理")
			return
		}
	}
	if err := s.store.DeleteService(r.Context(), id); err != nil {
		mapStoreError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) startService(w http.ResponseWriter, r *http.Request) {
	s.serviceMu.Lock()
	defer s.serviceMu.Unlock()
	id, ok := requireID(w, r)
	if !ok {
		return
	}
	service, err := s.store.Service(r.Context(), id)
	if err != nil {
		mapStoreError(w, err)
		return
	}
	if err := s.engine.Start(r.Context(), service); err != nil {
		_ = s.store.SetServiceRuntime(r.Context(), service.ID, "error", err.Error(), false)
		s.addEvent(r.Context(), store.Event{ServiceID: service.ID, Type: "engine.start_failed", Level: "error", Message: err.Error()})
		writeError(w, http.StatusBadRequest, "engine_start_failed", err.Error())
		return
	}
	s.addEvent(r.Context(), store.Event{ServiceID: service.ID, Type: "engine.started", Level: "info", Message: "Service connector started"})
	updated, _ := s.store.Service(r.Context(), id)
	writeJSON(w, http.StatusOK, map[string]any{"service": updated})
}

func (s *Server) stopService(w http.ResponseWriter, r *http.Request) {
	s.serviceMu.Lock()
	defer s.serviceMu.Unlock()
	id, ok := requireID(w, r)
	if !ok {
		return
	}
	if err := s.engine.Stop(id); err != nil {
		mapStoreError(w, err)
		return
	}
	s.addEvent(r.Context(), store.Event{ServiceID: id, Type: "engine.stopped", Level: "info", Message: "Service connector stopped"})
	updated, _ := s.store.Service(r.Context(), id)
	writeJSON(w, http.StatusOK, map[string]any{"service": updated})
}

func (s *Server) syncService(w http.ResponseWriter, r *http.Request) {
	s.serviceMu.Lock()
	defer s.serviceMu.Unlock()
	id, ok := requireID(w, r)
	if !ok {
		return
	}
	service, err := s.store.Service(r.Context(), id)
	if err != nil {
		mapStoreError(w, err)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 50*time.Second)
	defer cancel()
	result, err := s.syncCloudflare(ctx, service)
	if err != nil {
		writeError(w, http.StatusBadRequest, "cloudflare_sync_failed", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"sync": result})
}

func (s *Server) diagnoseService(w http.ResponseWriter, r *http.Request) {
	id, ok := requireID(w, r)
	if !ok {
		return
	}
	service, err := s.store.Service(r.Context(), id)
	if err != nil {
		mapStoreError(w, err)
		return
	}
	if service.UsesConnector() {
		writeError(w, 400, "not_stun_service", "Tunnel 不依赖 STUN；请在 Cloudflare 页面查看权限和连接状态")
		return
	}
	report := s.engine.Diagnose(r.Context(), service)
	writeJSON(w, http.StatusOK, map[string]any{"diagnostic": report})
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
	s.serviceMu.Lock()
	defer s.serviceMu.Unlock()
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	current, err := s.store.Service(ctx, mapping.ServiceID)
	if err != nil || !current.Enabled || current.UsesConnector() || !s.engine.Running(current.ID) {
		return
	}
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
	if err != nil {
		return
	}
	if service.GatewayMode != "none" {
		if err := s.engine.ApplyGatewayMapping(ctx, service, mapping); err != nil {
			_ = s.store.SetServiceRuntime(ctx, service.ID, "gateway_error", err.Error(), true)
			s.addEvent(ctx, store.Event{ServiceID: service.ID, Type: "gateway.mapping_failed", Level: "error", Message: err.Error()})
			return
		}
		s.addEvent(ctx, store.Event{
			ServiceID: service.ID,
			Type:      "gateway.mapping_ready",
			Level:     "info",
			Message:   service.GatewayMode + " gateway mapping is active",
		})
		_ = s.store.SetServiceRuntime(ctx, service.ID, "gateway_mapped", "", true)
	}
	if service.PublishMode == "direct" || service.UsesConnector() {
		return
	}
	if _, err := s.syncCloudflare(ctx, service); err != nil {
		_ = s.store.SetServiceRuntime(ctx, service.ID, "sync_error", err.Error(), true)
		s.addEvent(ctx, store.Event{ServiceID: service.ID, Type: "cloudflare.sync_failed", Level: "error", Message: err.Error()})
	}
}

func (s *Server) syncCloudflare(ctx context.Context, service store.Service) (cf.SyncResult, error) {
	if !service.UsesCloudflareAccount() {
		return cf.SyncResult{}, errors.New("此模式无需同步 Cloudflare 账户")
	}
	if !service.UsesConnector() && !service.Enabled {
		return cf.SyncResult{}, errors.New("请先启动服务并取得公网映射，再同步 Cloudflare")
	}
	result, err := s.publisher.Sync(ctx, service)
	if err != nil {
		return result, err
	}
	status := service.Status
	if !service.UsesConnector() && service.Enabled {
		status = "healthy"
	}
	if err = s.store.SetServiceRuntime(ctx, service.ID, status, "", service.Enabled); err != nil {
		return result, err
	}
	s.addEvent(ctx, store.Event{ServiceID: service.ID, Type: "cloudflare.synced", Level: "info", Message: "Cloudflare " + service.PublishMode + " synchronized", Payload: map[string]any{"targetUrl": result.TargetURL}})
	return result, nil
}

func (s *Server) serviceFromRequest(ctx context.Context, input serviceRequest) (store.Service, error) {
	service := store.Service{
		PrivateNetwork:         strings.TrimSpace(input.PrivateNetwork),
		TunnelProtocol:         strings.ToLower(strings.TrimSpace(input.TunnelProtocol)),
		EdgePort:               input.EdgePort,
		Name:                   strings.TrimSpace(input.Name),
		TargetHost:             strings.TrimSpace(input.TargetHost),
		TargetPort:             input.TargetPort,
		Protocol:               strings.ToLower(strings.TrimSpace(input.Protocol)),
		BindPort:               input.BindPort,
		GatewayMode:            strings.ToLower(strings.TrimSpace(input.GatewayMode)),
		GatewayAddress:         strings.TrimSpace(input.GatewayAddress),
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
	if service.GatewayMode == "" {
		service.GatewayMode = "none"
	}
	if service.GatewayMode != "none" && service.GatewayMode != "upnp" && service.GatewayMode != "natpmp" {
		return store.Service{}, errors.New("gateway mode must be none, upnp or natpmp")
	}
	if service.GatewayAddress != "" && net.ParseIP(service.GatewayAddress) == nil {
		return store.Service{}, errors.New("gateway address must be an IP address")
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
	switch service.PublishMode {
	case "direct", "redirect", "dns", "proxy", "tunnel", "spectrum", "quick", "warp", "workers":
	default:
		return store.Service{}, errors.New("unsupported Cloudflare publishing mode")
	}
	if service.TunnelProtocol == "" {
		service.TunnelProtocol = "http"
	}
	if service.PublishMode == "tunnel" || service.PublishMode == "quick" {
		if service.Protocol != "tcp" || !cf.TunnelProtocolAllowed(service.TunnelProtocol) {
			return store.Service{}, errors.New("Tunnel supports HTTP, HTTPS, TCP, SSH and RDP, not public UDP")
		}
		service.BindPort, service.GatewayMode, service.GatewayAddress = 0, "none", ""
	}
	if service.PublishMode == "quick" && service.TunnelProtocol != "http" && service.TunnelProtocol != "https" {
		return store.Service{}, errors.New("Quick Tunnel only supports HTTP / HTTPS")
	}
	if service.PublishMode == "warp" {
		network, err := cf.PrivateNetwork(service.TargetHost, service.PrivateNetwork)
		if err != nil {
			return store.Service{}, err
		}
		service.PrivateNetwork = network.String()
		service.BindPort, service.GatewayMode, service.GatewayAddress = 0, "none", ""
	} else {
		service.PrivateNetwork = ""
	}
	if !service.UsesCloudflareAccount() || service.PublishMode == "warp" {
		service.EntryHostname, service.OriginHostname, service.ManageDNS = "", "", false
		if !service.UsesCloudflareAccount() {
			service.CloudflareConnectionID = ""
		}
	}
	if service.PublishMode == "spectrum" && (service.EdgePort < 1 || service.EdgePort > 65535) {
		return store.Service{}, errors.New("Spectrum requires an edge port between 1 and 65535")
	}
	if (service.PublishMode == "proxy" || service.PublishMode == "workers") && service.Protocol != "tcp" {
		return store.Service{}, errors.New("Cloudflare HTTP proxy requires a TCP web service")
	}
	if service.PublishMode == "workers" {
		if !validHostname(service.OriginHostname) || net.ParseIP(service.OriginHostname) != nil {
			return store.Service{}, errors.New("Workers requires a separate origin hostname")
		}
		service.ManageDNS = true
	}
	if service.UsesCloudflareAccount() {
		if service.PublishMode != "warp" && (!validHostname(service.EntryHostname) || net.ParseIP(service.EntryHostname) != nil) {
			return store.Service{}, errors.New("entry hostname is invalid")
		}
		connection, err := s.store.CloudflareConnection(ctx, service.CloudflareConnectionID)
		if err != nil {
			return store.Service{}, errors.New("Cloudflare connection does not exist")
		}
		if service.PublishMode != "warp" && !cf.HostInZone(service.EntryHostname, connection.ZoneName) {
			return store.Service{}, errors.New("entry hostname must belong to the selected Cloudflare zone")
		}
		if service.OriginHostname != "" && service.ManageDNS && !cf.HostInZone(service.OriginHostname, connection.ZoneName) {
			return store.Service{}, errors.New("managed origin hostname must belong to the selected Cloudflare zone")
		}
		if service.EntryHostname != "" && service.EntryHostname == service.OriginHostname {
			return store.Service{}, errors.New("entry and origin hostnames must differ")
		}
	}
	if service.PublishMode == "dns" || service.PublishMode == "proxy" || service.PublishMode == "tunnel" {
		service.ManageDNS = true
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
