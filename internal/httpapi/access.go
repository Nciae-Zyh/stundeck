package httpapi

import (
	"errors"
	"net"
	"net/http"
	"strings"

	"github.com/Nciae-Zyh/stundeck/internal/store"
)

func (s *Server) accessPolicy(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		remoteIP := net.ParseIP(remoteHost(r.RemoteAddr))
		if remoteIP == nil {
			writeError(w, http.StatusForbidden, "access_denied", "Unable to identify the client network")
			return
		}
		initialized, err := s.store.HasAdmin(r.Context())
		if err != nil {
			writeError(w, http.StatusServiceUnavailable, "access_policy_unavailable", "Unable to load access policy")
			return
		}
		if !initialized {
			if !isLocalNetwork(remoteIP) {
				writeError(w, http.StatusForbidden, "setup_local_only", "First-run setup is only available from this device or the local network")
				return
			}
			next.ServeHTTP(w, r)
			return
		}
		policy, err := s.store.AccessPolicy(r.Context())
		if err != nil {
			writeError(w, http.StatusServiceUnavailable, "access_policy_unavailable", "Unable to load access policy")
			return
		}
		if !sourceAllowed(policy.Mode, remoteIP) {
			writeError(w, http.StatusForbidden, "access_denied", "This network is not allowed by the StunDeck access policy")
			return
		}
		hostBypass := remoteIP.IsLoopback() && (r.URL.Path == "/api/v1/health" || strings.HasPrefix(r.URL.Path, "/internal/"))
		if !hostBypass && len(policy.AllowedHosts) > 0 && !hostAllowed(requestHost(r.Host), policy.AllowedHosts) {
			writeError(w, http.StatusMisdirectedRequest, "host_not_allowed", "Use an allowed StunDeck hostname or IP address")
			return
		}
		next.ServeHTTP(w, r)
	})
}

func (s *Server) getAccessPolicy(w http.ResponseWriter, r *http.Request) {
	policy, err := s.store.AccessPolicy(r.Context())
	if err != nil {
		mapStoreError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"policy": policy})
}

func (s *Server) updateAccessPolicy(w http.ResponseWriter, r *http.Request) {
	var input store.AccessPolicy
	if !decodeJSON(w, r, &input) {
		return
	}
	policy, err := normalizeAccessPolicy(input.Mode, input.AllowedHosts)
	if err != nil {
		writeError(w, http.StatusBadRequest, "access_policy_invalid", err.Error())
		return
	}
	if err := s.store.SetAccessPolicy(r.Context(), policy); err != nil {
		mapStoreError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"policy": policy})
}

func normalizeAccessPolicy(mode string, hosts []string) (store.AccessPolicy, error) {
	mode = strings.ToLower(strings.TrimSpace(mode))
	if mode == "" {
		mode = "lan"
	}
	if mode != "local" && mode != "lan" && mode != "public" {
		return store.AccessPolicy{}, errors.New("access mode must be local, lan or public")
	}
	if len(hosts) > 32 {
		return store.AccessPolicy{}, errors.New("at most 32 allowed hosts can be configured")
	}
	normalized := make([]string, 0, len(hosts))
	seen := map[string]bool{}
	for _, raw := range hosts {
		host := strings.TrimSuffix(strings.ToLower(strings.TrimSpace(raw)), ".")
		if host == "" {
			continue
		}
		parsedIP := net.ParseIP(strings.Trim(host, "[]"))
		valid := parsedIP != nil
		if parsedIP != nil {
			host = parsedIP.String()
		}
		if strings.HasPrefix(host, "*.") {
			valid = validHostname(strings.TrimPrefix(host, "*."))
		} else if !valid {
			valid = validHostname(host)
		}
		if !valid {
			return store.AccessPolicy{}, errors.New("allowed hosts must be exact hostnames, IP addresses, or wildcard subdomains")
		}
		if !seen[host] {
			normalized = append(normalized, host)
			seen[host] = true
		}
	}
	if mode == "public" && len(normalized) == 0 {
		return store.AccessPolicy{}, errors.New("public access mode requires at least one allowed hostname or IP address")
	}
	return store.AccessPolicy{Mode: mode, AllowedHosts: normalized}, nil
}

func sourceAllowed(mode string, ip net.IP) bool {
	switch mode {
	case "local":
		return ip.IsLoopback()
	case "lan", "":
		return isLocalNetwork(ip)
	case "public":
		return true
	default:
		return false
	}
}

func isLocalNetwork(ip net.IP) bool {
	return ip.IsLoopback() || ip.IsPrivate() || ip.IsLinkLocalUnicast()
}

func requestHost(value string) string {
	var host string
	if host, _, err := net.SplitHostPort(value); err == nil {
		value = host
	}
	host = strings.ToLower(strings.Trim(strings.TrimSuffix(value, "."), "[]"))
	if parsed := net.ParseIP(host); parsed != nil {
		return parsed.String()
	}
	return host
}

func hostAllowed(host string, allowed []string) bool {
	for _, candidate := range allowed {
		if host == candidate {
			return true
		}
		if strings.HasPrefix(candidate, "*.") {
			suffix := strings.TrimPrefix(candidate, "*")
			if strings.HasSuffix(host, suffix) && host != strings.TrimPrefix(suffix, ".") {
				return true
			}
		}
	}
	return false
}
