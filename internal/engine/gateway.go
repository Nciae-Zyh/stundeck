package engine

import (
	"bytes"
	"context"
	"encoding/binary"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/Nciae-Zyh/stundeck/internal/store"
)

const (
	gatewayTimeout     = 4 * time.Second
	natPMPLeaseSeconds = 7200
)

type GatewayMapping struct {
	Mode         string
	Gateway      string
	ControlURL   string
	ServiceType  string
	InternalIP   string
	InternalPort int
	ExternalPort int
	Protocol     string
}

type upnpRoot struct {
	Device upnpDevice `xml:"device"`
}

type upnpDevice struct {
	Services []upnpService `xml:"serviceList>service"`
	Devices  []upnpDevice  `xml:"deviceList>device"`
}

type upnpService struct {
	ServiceType string `xml:"serviceType"`
	ControlURL  string `xml:"controlURL"`
}

func (m *Manager) ApplyGatewayMapping(ctx context.Context, service store.Service, mapping Mapping) error {
	if service.GatewayMode == "" || service.GatewayMode == "none" {
		return nil
	}
	if mapping.PrivatePort < 1 || mapping.PrivatePort > 65535 {
		return errors.New("NATMap returned an invalid private bind port")
	}
	internalIP, err := mappingInternalIP(mapping.PrivateIP, service.GatewayAddress)
	if err != nil {
		return err
	}
	state := GatewayMapping{
		Mode:         service.GatewayMode,
		Gateway:      service.GatewayAddress,
		InternalIP:   internalIP,
		InternalPort: mapping.PrivatePort,
		ExternalPort: mapping.PublicPort,
		Protocol:     strings.ToUpper(mapping.Protocol),
	}

	switch service.GatewayMode {
	case "upnp":
		gatewayService, location, err := discoverUPnP(ctx, service.GatewayAddress)
		if err != nil {
			return fmt.Errorf("discover UPnP gateway: %w", err)
		}
		state.Gateway = location.Hostname()
		state.ControlURL = gatewayService.ControlURL
		state.ServiceType = gatewayService.ServiceType
		if err := addUPnPMapping(ctx, state, "StunDeck "+shortServiceID(service.ID)); err != nil {
			return fmt.Errorf("add UPnP mapping: %w", err)
		}
	case "natpmp":
		gateway := net.ParseIP(service.GatewayAddress)
		if gateway == nil {
			gateway, err = defaultGatewayIPv4()
			if err != nil {
				return fmt.Errorf("find NAT-PMP gateway: %w", err)
			}
		}
		state.Gateway = gateway.String()
		if err := setNATPMPMapping(ctx, state, natPMPLeaseSeconds); err != nil {
			return fmt.Errorf("add NAT-PMP mapping: %w", err)
		}
	default:
		return fmt.Errorf("unsupported gateway mode %q", service.GatewayMode)
	}

	m.mu.Lock()
	running := m.processes[service.ID]
	if running == nil {
		m.mu.Unlock()
		_ = removeGatewayMapping(context.Background(), state)
		return errors.New("NATMap process stopped before gateway mapping completed")
	}
	previous := running.gateway
	running.gateway = &state
	running.gatewayGeneration++
	generation := running.gatewayGeneration
	processContext := running.ctx
	m.mu.Unlock()
	if previous != nil && !sameGatewayMapping(*previous, state) && separateGatewayEntry(*previous, state) {
		cleanupCtx, cancel := context.WithTimeout(context.Background(), gatewayTimeout)
		defer cancel()
		_ = removeGatewayMapping(cleanupCtx, *previous)
	}
	if state.Mode == "natpmp" {
		go m.renewNATPMP(processContext, service.ID, state, generation)
	}
	return nil
}

func (m *Manager) GatewayMappingActive(serviceID string) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	running := m.processes[serviceID]
	return running != nil && running.gateway != nil
}

func sameGatewayMapping(left, right GatewayMapping) bool {
	return left.Mode == right.Mode && left.Gateway == right.Gateway &&
		left.InternalIP == right.InternalIP && left.InternalPort == right.InternalPort &&
		left.ExternalPort == right.ExternalPort && left.Protocol == right.Protocol
}

func separateGatewayEntry(left, right GatewayMapping) bool {
	return left.Mode != right.Mode || left.Gateway != right.Gateway || left.ControlURL != right.ControlURL ||
		left.ExternalPort != right.ExternalPort || left.Protocol != right.Protocol
}

func (m *Manager) renewNATPMP(ctx context.Context, serviceID string, mapping GatewayMapping, generation uint64) {
	ticker := time.NewTicker(time.Duration(natPMPLeaseSeconds/2) * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			m.mu.Lock()
			running := m.processes[serviceID]
			current := running != nil && running.gatewayGeneration == generation && running.gateway != nil && sameGatewayMapping(*running.gateway, mapping)
			m.mu.Unlock()
			if !current {
				return
			}
			renewCtx, cancel := context.WithTimeout(ctx, gatewayTimeout)
			err := setNATPMPMapping(renewCtx, mapping, natPMPLeaseSeconds)
			cancel()
			if err != nil {
				m.logger.Warn("renew NAT-PMP mapping", "service_id", serviceID, "error", err)
				_ = m.store.SetServiceRuntime(context.Background(), serviceID, "gateway_error", "NAT-PMP renewal failed: "+err.Error(), true)
			}
		}
	}
}

func removeGatewayMapping(ctx context.Context, mapping GatewayMapping) error {
	switch mapping.Mode {
	case "upnp":
		return deleteUPnPMapping(ctx, mapping)
	case "natpmp":
		return setNATPMPMapping(ctx, mapping, 0)
	default:
		return nil
	}
}

func discoverUPnP(ctx context.Context, gateway string) (upnpService, *url.URL, error) {
	connection, err := net.ListenUDP("udp4", &net.UDPAddr{IP: net.IPv4zero})
	if err != nil {
		return upnpService{}, nil, err
	}
	defer connection.Close()
	deadline := time.Now().Add(gatewayTimeout)
	if ctxDeadline, ok := ctx.Deadline(); ok && ctxDeadline.Before(deadline) {
		deadline = ctxDeadline
	}
	_ = connection.SetDeadline(deadline)
	request := strings.Join([]string{
		"M-SEARCH * HTTP/1.1",
		"HOST: 239.255.255.250:1900",
		`MAN: "ssdp:discover"`,
		"MX: 2",
		"ST: urn:schemas-upnp-org:device:InternetGatewayDevice:1",
		"", "",
	}, "\r\n")
	if _, err := connection.WriteToUDP([]byte(request), &net.UDPAddr{IP: net.ParseIP("239.255.255.250"), Port: 1900}); err != nil {
		return upnpService{}, nil, err
	}

	buffer := make([]byte, 64*1024)
	for {
		count, source, err := connection.ReadFromUDP(buffer)
		if err != nil {
			if timeout, ok := err.(net.Error); ok && timeout.Timeout() {
				return upnpService{}, nil, errors.New("no UPnP IGD response")
			}
			return upnpService{}, nil, err
		}
		locationText := ssdpHeader(string(buffer[:count]), "location")
		location, err := url.Parse(locationText)
		if err != nil || location.Scheme != "http" || location.Hostname() == "" {
			continue
		}
		if gateway != "" && source.IP.String() != gateway && location.Hostname() != gateway {
			continue
		}
		service, err := loadUPnPService(ctx, location)
		if err == nil {
			return service, location, nil
		}
	}
}

func ssdpHeader(message, name string) string {
	wanted := strings.ToLower(name) + ":"
	for _, line := range strings.Split(message, "\r\n") {
		if strings.HasPrefix(strings.ToLower(line), wanted) {
			return strings.TrimSpace(line[len(wanted):])
		}
	}
	return ""
}

func loadUPnPService(ctx context.Context, location *url.URL) (upnpService, error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, location.String(), nil)
	if err != nil {
		return upnpService{}, err
	}
	client := directHTTPClient()
	response, err := client.Do(request)
	if err != nil {
		return upnpService{}, err
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return upnpService{}, fmt.Errorf("description returned HTTP %d", response.StatusCode)
	}
	var root upnpRoot
	if err := xml.NewDecoder(io.LimitReader(response.Body, 1024*1024)).Decode(&root); err != nil {
		return upnpService{}, err
	}
	service, ok := findWANService(root.Device)
	if !ok {
		return upnpService{}, errors.New("WANIPConnection service not found")
	}
	control, err := location.Parse(service.ControlURL)
	if err != nil {
		return upnpService{}, err
	}
	if control.Scheme != "http" || control.Hostname() != location.Hostname() {
		return upnpService{}, errors.New("UPnP control URL leaves the discovered gateway")
	}
	service.ControlURL = control.String()
	return service, nil
}

func findWANService(device upnpDevice) (upnpService, bool) {
	for _, service := range device.Services {
		if strings.Contains(service.ServiceType, ":WANIPConnection:") || strings.Contains(service.ServiceType, ":WANPPPConnection:") {
			return service, true
		}
	}
	for _, child := range device.Devices {
		if service, ok := findWANService(child); ok {
			return service, true
		}
	}
	return upnpService{}, false
}

func addUPnPMapping(ctx context.Context, mapping GatewayMapping, description string) error {
	body := "<NewRemoteHost></NewRemoteHost>" +
		soapValue("NewExternalPort", strconv.Itoa(mapping.ExternalPort)) +
		soapValue("NewProtocol", mapping.Protocol) +
		soapValue("NewInternalPort", strconv.Itoa(mapping.InternalPort)) +
		soapValue("NewInternalClient", mapping.InternalIP) +
		"<NewEnabled>1</NewEnabled>" + soapValue("NewPortMappingDescription", description) +
		"<NewLeaseDuration>0</NewLeaseDuration>"
	return upnpSOAP(ctx, mapping, "AddPortMapping", body)
}

func deleteUPnPMapping(ctx context.Context, mapping GatewayMapping) error {
	body := "<NewRemoteHost></NewRemoteHost>" +
		soapValue("NewExternalPort", strconv.Itoa(mapping.ExternalPort)) +
		soapValue("NewProtocol", mapping.Protocol)
	return upnpSOAP(ctx, mapping, "DeletePortMapping", body)
}

func upnpSOAP(ctx context.Context, mapping GatewayMapping, action, inner string) error {
	envelope := `<?xml version="1.0"?>` +
		`<s:Envelope xmlns:s="http://schemas.xmlsoap.org/soap/envelope/" s:encodingStyle="http://schemas.xmlsoap.org/soap/encoding/"><s:Body>` +
		`<u:` + action + ` xmlns:u="` + mapping.ServiceType + `">` + inner + `</u:` + action + `>` +
		`</s:Body></s:Envelope>`
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, mapping.ControlURL, strings.NewReader(envelope))
	if err != nil {
		return err
	}
	request.Header.Set("Content-Type", `text/xml; charset="utf-8"`)
	request.Header.Set("SOAPAction", `"`+mapping.ServiceType+`#`+action+`"`)
	response, err := directHTTPClient().Do(request)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	if response.StatusCode >= 200 && response.StatusCode < 300 {
		_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, 4096))
		return nil
	}
	payload, _ := io.ReadAll(io.LimitReader(response.Body, 4096))
	return fmt.Errorf("gateway returned HTTP %d: %s", response.StatusCode, conciseSOAPError(payload))
}

func soapValue(name, value string) string {
	var escaped bytes.Buffer
	_ = xml.EscapeText(&escaped, []byte(value))
	return "<" + name + ">" + escaped.String() + "</" + name + ">"
}

func conciseSOAPError(payload []byte) string {
	type fault struct {
		Code        string `xml:"Body>Fault>detail>UPnPError>errorCode"`
		Description string `xml:"Body>Fault>detail>UPnPError>errorDescription"`
	}
	var response fault
	if xml.Unmarshal(payload, &response) == nil && (response.Code != "" || response.Description != "") {
		return strings.TrimSpace(response.Code + " " + response.Description)
	}
	return strings.TrimSpace(string(payload))
}

func setNATPMPMapping(ctx context.Context, mapping GatewayMapping, lifetime int) error {
	gateway := net.ParseIP(mapping.Gateway)
	if gateway == nil {
		return errors.New("gateway address is invalid")
	}
	connection, err := net.DialUDP("udp4", nil, &net.UDPAddr{IP: gateway, Port: 5351})
	if err != nil {
		return err
	}
	defer connection.Close()
	deadline := time.Now().Add(gatewayTimeout)
	if ctxDeadline, ok := ctx.Deadline(); ok && ctxDeadline.Before(deadline) {
		deadline = ctxDeadline
	}
	_ = connection.SetDeadline(deadline)
	opcode := byte(2)
	if mapping.Protocol == "UDP" {
		opcode = 1
	}
	request := make([]byte, 12)
	request[1] = opcode
	binary.BigEndian.PutUint16(request[4:6], uint16(mapping.InternalPort))
	binary.BigEndian.PutUint16(request[6:8], uint16(mapping.ExternalPort))
	binary.BigEndian.PutUint32(request[8:12], uint32(lifetime))
	if _, err := connection.Write(request); err != nil {
		return err
	}
	response := make([]byte, 32)
	count, err := connection.Read(response)
	if err != nil {
		return err
	}
	if count < 16 || response[0] != 0 || response[1] != opcode+128 {
		return errors.New("invalid NAT-PMP response")
	}
	if result := binary.BigEndian.Uint16(response[2:4]); result != 0 {
		return fmt.Errorf("gateway result code %d", result)
	}
	assigned := int(binary.BigEndian.Uint16(response[10:12]))
	if lifetime > 0 && assigned != mapping.ExternalPort {
		return fmt.Errorf("gateway assigned external port %d instead of discovered port", assigned)
	}
	return nil
}

func probeNATPMP(ctx context.Context, gateway net.IP) error {
	connection, err := net.DialUDP("udp4", nil, &net.UDPAddr{IP: gateway, Port: 5351})
	if err != nil {
		return err
	}
	defer connection.Close()
	deadline := time.Now().Add(gatewayTimeout)
	if ctxDeadline, ok := ctx.Deadline(); ok && ctxDeadline.Before(deadline) {
		deadline = ctxDeadline
	}
	_ = connection.SetDeadline(deadline)
	if _, err := connection.Write([]byte{0, 0}); err != nil {
		return err
	}
	response := make([]byte, 16)
	count, err := connection.Read(response)
	if err != nil {
		return err
	}
	if count < 12 || response[0] != 0 || response[1] != 128 {
		return errors.New("invalid NAT-PMP public address response")
	}
	if result := binary.BigEndian.Uint16(response[2:4]); result != 0 {
		return fmt.Errorf("gateway result code %d", result)
	}
	return nil
}

func defaultGatewayIPv4() (net.IP, error) {
	payload, err := os.ReadFile("/proc/net/route")
	if err != nil {
		return nil, err
	}
	for _, line := range strings.Split(string(payload), "\n")[1:] {
		fields := strings.Fields(line)
		if len(fields) < 4 || fields[1] != "00000000" {
			continue
		}
		flags, err := strconv.ParseUint(fields[3], 16, 32)
		if err != nil || flags&0x2 == 0 {
			continue
		}
		encoded, err := strconv.ParseUint(fields[2], 16, 32)
		if err != nil {
			continue
		}
		value := make(net.IP, net.IPv4len)
		binary.LittleEndian.PutUint32(value, uint32(encoded))
		return value, nil
	}
	return nil, errors.New("default IPv4 gateway not found")
}

func mappingInternalIP(candidate, gateway string) (string, error) {
	ip := net.ParseIP(candidate)
	if ip != nil && ip.To4() != nil && !ip.IsUnspecified() && !ip.IsLoopback() {
		return ip.String(), nil
	}
	if gateway == "" {
		resolved, err := defaultGatewayIPv4()
		if err != nil {
			return "", errors.New("cannot determine internal IP without a gateway")
		}
		gateway = resolved.String()
	}
	connection, err := net.DialUDP("udp4", nil, &net.UDPAddr{IP: net.ParseIP(gateway), Port: 9})
	if err != nil {
		return "", fmt.Errorf("determine internal IP: %w", err)
	}
	defer connection.Close()
	local, ok := connection.LocalAddr().(*net.UDPAddr)
	if !ok || local.IP.To4() == nil {
		return "", errors.New("determine internal IP: no IPv4 source address")
	}
	return local.IP.String(), nil
}

func directHTTPClient() *http.Client {
	return &http.Client{
		Timeout: gatewayTimeout,
		CheckRedirect: func(*http.Request, []*http.Request) error {
			return http.ErrUseLastResponse
		},
		Transport: &http.Transport{
			Proxy:             nil,
			DialContext:       (&net.Dialer{Timeout: gatewayTimeout}).DialContext,
			DisableKeepAlives: true,
		},
	}
}

func shortServiceID(serviceID string) string {
	if len(serviceID) > 12 {
		return serviceID[:12]
	}
	return serviceID
}
