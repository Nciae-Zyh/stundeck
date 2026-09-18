package httpapi

import (
	"context"
	"net/http"
	"strings"
	"time"

	cf "github.com/Nciae-Zyh/stundeck/internal/cloudflare"
	"github.com/Nciae-Zyh/stundeck/internal/store"
)

func (s *Server) inspectCloudflareConnection(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 45*time.Second)
	defer cancel()
	report, err := s.publisher.Inspect(ctx, r.PathValue("id"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "cloudflare_inspection_failed", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"inspection": report, "tunnelAvailable": s.engine.TunnelAvailable()})
}

func (s *Server) inspectCloudflareToken(w http.ResponseWriter, r *http.Request) {
	var input struct {
		Token  string `json:"token"`
		ZoneID string `json:"zoneId"`
	}
	if !decodeJSON(w, r, &input) {
		return
	}
	if strings.TrimSpace(input.Token) == "" || strings.TrimSpace(input.ZoneID) == "" {
		writeError(w, 400, "input_required", "Token 与 Zone 必填")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 45*time.Second)
	defer cancel()
	report, err := s.publisher.NewClient(strings.TrimSpace(input.Token)).Inspect(ctx, input.ZoneID)
	if err != nil {
		writeError(w, 400, "cloudflare_inspection_failed", err.Error())
		return
	}
	writeJSON(w, 200, map[string]any{"inspection": report, "tunnelAvailable": s.engine.TunnelAvailable()})
}

func (s *Server) saveCloudflareAccess(w http.ResponseWriter, r *http.Request) {
	var input cf.AccessRequest
	if !decodeJSON(w, r, &input) {
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 50*time.Second)
	defer cancel()
	app, err := s.publisher.SaveAccess(ctx, r.PathValue("id"), input)
	if err != nil {
		writeError(w, 400, "cloudflare_access_failed", err.Error())
		return
	}
	s.addEvent(ctx, store.Event{Type: "cloudflare.access_configured", Level: "info", Message: "Cloudflare Access 已配置并回读确认", Payload: map[string]any{"hostname": app.Domain}})
	writeJSON(w, 200, map[string]any{"application": app})
}

func (s *Server) deleteCloudflareAccess(w http.ResponseWriter, r *http.Request) {
	var input struct {
		Hostname string `json:"hostname"`
	}
	if !decodeJSON(w, r, &input) {
		return
	}
	if err := s.publisher.DeleteAccess(r.Context(), r.PathValue("id"), strings.ToLower(strings.TrimSpace(input.Hostname))); err != nil {
		writeError(w, 400, "cloudflare_access_failed", err.Error())
		return
	}
	s.addEvent(r.Context(), store.Event{Type: "cloudflare.access_removed", Level: "info", Message: "Cloudflare Access 已移除", Payload: map[string]any{"hostname": input.Hostname}})
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) cleanupCloudflareService(w http.ResponseWriter, r *http.Request) {
	s.serviceMu.Lock()
	defer s.serviceMu.Unlock()
	service, err := s.store.Service(r.Context(), r.PathValue("id"))
	if err != nil {
		mapStoreError(w, err)
		return
	}
	if s.engine.Running(service.ID) || service.Enabled {
		writeError(w, 409, "service_running", "请先停止服务，再清理云端发布")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 50*time.Second)
	defer cancel()
	if err = s.publisher.Cleanup(ctx, service); err != nil {
		writeError(w, 400, "cloudflare_cleanup_failed", err.Error())
		return
	}
	s.addEvent(ctx, store.Event{ServiceID: service.ID, Type: "cloudflare.cleaned", Level: "info", Message: "已清理此服务拥有的云端发布资源；Access 策略保留"})
	w.WriteHeader(http.StatusNoContent)
}
