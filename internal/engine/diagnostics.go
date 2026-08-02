package engine

import (
	"context"
	"crypto/rand"
	"crypto/tls"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/Nciae-Zyh/stundeck/internal/store"
)

const diagnosticTimeout = 4 * time.Second

type DiagnosticCheck struct {
	Key        string `json:"key"`
	Label      string `json:"label"`
	Category   string `json:"category"`
	Status     string `json:"status"`
	Message    string `json:"message"`
	DurationMS int64  `json:"durationMs"`
}

type DiagnosticReport struct {
	ServiceID               string            `json:"serviceId"`
	Outcome                 string            `json:"outcome"`
	STUNFeasible            bool              `json:"stunFeasible"`
	TargetReady             bool              `json:"targetReady"`
	GatewayReady            bool              `json:"gatewayReady"`
	MappingActive           bool              `json:"mappingActive"`
	ExternalInboundVerified bool              `json:"externalInboundVerified"`
	CheckedAt               time.Time         `json:"checkedAt"`
	Checks                  []DiagnosticCheck `json:"checks"`
}

type diagnosticTask struct {
	key      string
	label    string
	category string
	run      func(context.Context) (string, string)
}

func (m *Manager) Diagnose(ctx context.Context, service store.Service) DiagnosticReport {
	ctx, cancel := context.WithTimeout(ctx, 12*time.Second)
	defer cancel()

	tasks := []diagnosticTask{
		{key: "proxy_environment", label: "代理环境", category: "environment", run: checkProxyEnvironment},
		{key: "natmap_binary", label: "NATMap 程序", category: "environment", run: func(context.Context) (string, string) {
			if !m.Available() {
				return "fail", "找不到 NATMap 程序，无法建立映射"
			}
			return "pass", "NATMap 程序可执行"
		}},
		{key: "target_tcp", label: "目标端口", category: "target", run: func(checkCtx context.Context) (string, string) {
			return checkTargetTCP(checkCtx, service)
		}},
		{key: "target_protocol", label: "目标协议", category: "target", run: func(checkCtx context.Context) (string, string) {
			return checkTargetProtocol(checkCtx, service)
		}},
		{key: "stun_udp", label: "UDP STUN", category: "stun", run: func(checkCtx context.Context) (string, string) {
			family, err := stunBinding(checkCtx, "udp", m.config.STUNServer)
			if err != nil {
				status := "warn"
				if service.Protocol == "udp" {
					status = "fail"
				}
				return status, "UDP STUN Binding 失败：" + conciseNetworkError(err)
			}
			return "pass", "UDP STUN Binding 成功（" + family + "）"
		}},
		{key: "stun_tcp", label: "TCP STUN", category: "stun", run: func(checkCtx context.Context) (string, string) {
			family, err := stunBinding(checkCtx, "tcp", m.config.STUNServer)
			if err != nil {
				status := "warn"
				if service.Protocol == "tcp" {
					status = "fail"
				}
				return status, "TCP STUN Binding 失败：" + conciseNetworkError(err)
			}
			return "pass", "TCP STUN Binding 成功（" + family + "）"
		}},
		{key: "keepalive", label: "TCP 保活出口", category: "stun", run: func(checkCtx context.Context) (string, string) {
			if service.Protocol != "tcp" {
				return "info", "UDP 服务不使用 HTTP TCP 保活"
			}
			if err := dialTCP(checkCtx, m.config.KeepAliveServer); err != nil {
				return "fail", "无法连接保活服务器：" + conciseNetworkError(err)
			}
			return "pass", "保活服务器可直连"
		}},
		{key: "gateway_discovery", label: "路由器能力", category: "gateway", run: func(checkCtx context.Context) (string, string) {
			return m.checkGatewayDiscovery(checkCtx, service)
		}},
		{key: "gateway_mapping", label: "路由器端口映射", category: "gateway", run: func(context.Context) (string, string) {
			if service.GatewayMode == "" || service.GatewayMode == "none" {
				return "warn", "未启用 UPnP/NAT-PMP；局域网运行时可能只能取得映射，无法接受公网连接"
			}
			if m.GatewayMappingActive(service.ID) {
				return "pass", strings.ToUpper(service.GatewayMode) + " 端口映射已下发"
			}
			if m.Running(service.ID) && net.ParseIP(service.PublicIP) != nil && service.PublicPort > 0 {
				return "fail", strings.ToUpper(service.GatewayMode) + " 已配置，但尚未成功下发端口映射"
			}
			return "info", "取得公网映射后才会向路由器下发端口映射"
		}},
		{key: "process", label: "映射进程", category: "runtime", run: func(context.Context) (string, string) {
			if m.Running(service.ID) {
				return "pass", "NATMap 子进程正在运行"
			}
			if service.Enabled {
				return "fail", "服务已启用，但 NATMap 子进程不在运行"
			}
			return "info", "服务当前未启动"
		}},
		{key: "mapping", label: "公网映射", category: "runtime", run: func(context.Context) (string, string) {
			if net.ParseIP(service.PublicIP) == nil || service.PublicPort < 1 {
				if service.Enabled {
					return "fail", "尚未收到 NATMap 公网映射"
				}
				return "info", "服务启动后才会生成公网映射"
			}
			if service.MappingChangedAt.IsZero() {
				return "pass", "已取得公网映射"
			}
			return "pass", "已取得公网映射，最近更新于 " + humanDuration(time.Since(service.MappingChangedAt)) + "前"
		}},
		{key: "external_inbound", label: "公网回连", category: "external", run: func(context.Context) (string, string) {
			if m.GatewayMappingActive(service.ID) {
				return "info", "路由器映射已下发；最终公网回连仍需使用蜂窝网络或外部探针验证"
			}
			return "info", "本机只能确认映射已建立；最终公网回连需使用蜂窝网络或外部探针验证"
		}},
	}

	checks := make([]DiagnosticCheck, len(tasks))
	var wait sync.WaitGroup
	for index, task := range tasks {
		wait.Add(1)
		go func() {
			defer wait.Done()
			started := time.Now()
			checkCtx, checkCancel := context.WithTimeout(ctx, diagnosticTimeout)
			defer checkCancel()
			status, message := task.run(checkCtx)
			checks[index] = DiagnosticCheck{
				Key: task.key, Label: task.label, Category: task.category,
				Status: status, Message: message, DurationMS: time.Since(started).Milliseconds(),
			}
		}()
	}
	wait.Wait()

	report := DiagnosticReport{
		ServiceID: service.ID, CheckedAt: time.Now(), Checks: checks,
		MappingActive:           net.ParseIP(service.PublicIP) != nil && service.PublicPort > 0,
		ExternalInboundVerified: false,
	}
	report.STUNFeasible = checksPass(checks, requiredSTUNKeys(service.Protocol)...)
	report.TargetReady = checksPass(checks, "target_tcp", "target_protocol")
	report.GatewayReady = service.GatewayMode != "" && service.GatewayMode != "none" && checksPass(checks, "gateway_discovery", "gateway_mapping")
	report.Outcome = diagnosticOutcome(checks)
	return report
}

func requiredSTUNKeys(protocol string) []string {
	keys := []string{"proxy_environment", "natmap_binary", "process", "mapping"}
	if protocol == "udp" {
		return append(keys, "stun_udp")
	}
	return append(keys, "stun_tcp", "keepalive")
}

func (m *Manager) checkGatewayDiscovery(ctx context.Context, service store.Service) (string, string) {
	mode := service.GatewayMode
	if mode == "" || mode == "none" {
		if gateway, err := defaultGatewayIPv4(); err == nil {
			return "info", "检测到默认网关 " + gateway.String() + "；可在服务中启用 UPnP 或 NAT-PMP"
		}
		return "info", "未配置路由器端口映射"
	}
	if mode == "upnp" {
		gatewayService, location, err := discoverUPnP(ctx, service.GatewayAddress)
		if err != nil {
			return "fail", "UPnP IGD 发现失败：" + conciseNetworkError(err)
		}
		gatewayWAN, wanErr := getUPnPExternalIP(ctx, GatewayMapping{ControlURL: gatewayService.ControlURL, ServiceType: gatewayService.ServiceType})
		publicIP := net.ParseIP(service.PublicIP)
		if wanErr == nil && publicIP != nil && !gatewayWAN.Equal(publicIP) {
			return "pass", "已发现 UPnP IGD 网关 " + location.Hostname() + "；检测到多层 NAT，将放行穿透监听端口"
		}
		return "pass", "已发现 UPnP IGD 网关 " + location.Hostname()
	}
	gateway := net.ParseIP(service.GatewayAddress)
	var err error
	if gateway == nil {
		gateway, err = defaultGatewayIPv4()
		if err != nil {
			return "fail", "默认网关发现失败：" + conciseNetworkError(err)
		}
	}
	if err := probeNATPMP(ctx, gateway); err != nil {
		return "fail", "NAT-PMP 探测失败：" + conciseNetworkError(err)
	}
	return "pass", "网关 " + gateway.String() + " 支持 NAT-PMP"
}

func checksPass(checks []DiagnosticCheck, keys ...string) bool {
	wanted := make(map[string]bool, len(keys))
	for _, key := range keys {
		wanted[key] = true
	}
	for _, check := range checks {
		if wanted[check.Key] && check.Status == "fail" {
			return false
		}
	}
	return true
}

func diagnosticOutcome(checks []DiagnosticCheck) string {
	outcome := "pass"
	for _, check := range checks {
		if check.Status == "fail" {
			return "fail"
		}
		if check.Status == "warn" {
			outcome = "warning"
		}
	}
	return outcome
}

func checkProxyEnvironment(context.Context) (string, string) {
	keys := []string{"HTTP_PROXY", "HTTPS_PROXY", "ALL_PROXY", "http_proxy", "https_proxy", "all_proxy"}
	detected := make([]string, 0)
	for _, key := range keys {
		if strings.TrimSpace(os.Getenv(key)) != "" {
			detected = append(detected, key)
		}
	}
	if len(detected) == 0 {
		return "pass", "未设置 HTTP、HTTPS 或 ALL_PROXY，网络保持直连"
	}
	sort.Strings(detected)
	return "fail", "检测到代理环境变量：" + strings.Join(detected, ", ") + "；STUN 网关应保持直连"
}

func checkTargetTCP(ctx context.Context, service store.Service) (string, string) {
	if service.Protocol == "udp" {
		return "info", "UDP 目标无法通过 TCP 握手检测"
	}
	address := net.JoinHostPort(service.TargetHost, fmt.Sprintf("%d", service.TargetPort))
	if err := dialTCP(ctx, address); err != nil {
		return "fail", "目标端口不可达：" + conciseNetworkError(err)
	}
	return "pass", "局域网目标端口可直连"
}

func checkTargetProtocol(ctx context.Context, service store.Service) (string, string) {
	if service.Protocol != "tcp" || service.PublishMode != "redirect" {
		return "info", "当前发布方式不需要 HTTP 协议探测"
	}
	statusCode, err := requestTarget(ctx, service)
	if err == nil {
		if statusCode == http.StatusBadRequest && service.Scheme == "http" && acceptsTLS(ctx, service) {
			return "fail", "目标端口实际接受 HTTPS，但服务配置为 HTTP"
		}
		if statusCode >= 500 {
			return "warn", fmt.Sprintf("目标服务响应 HTTP %d", statusCode)
		}
		return "pass", fmt.Sprintf("目标服务按 %s 响应（HTTP %d）", strings.ToUpper(service.Scheme), statusCode)
	}
	if service.Scheme == "http" && acceptsTLS(ctx, service) {
		return "fail", "目标端口实际接受 HTTPS，但服务配置为 HTTP"
	}
	return "fail", "目标协议探测失败：" + conciseNetworkError(err)
}

func requestTarget(ctx context.Context, service store.Service) (int, error) {
	host := net.JoinHostPort(service.TargetHost, fmt.Sprintf("%d", service.TargetPort))
	targetURL := &url.URL{Scheme: service.Scheme, Host: host, Path: "/"}
	tlsConfig := &tls.Config{MinVersion: tls.VersionTLS12}
	if service.OriginHostname != "" {
		tlsConfig.ServerName = service.OriginHostname
	}
	transport := &http.Transport{
		Proxy:             nil,
		DialContext:       (&net.Dialer{Timeout: diagnosticTimeout}).DialContext,
		TLSClientConfig:   tlsConfig,
		DisableKeepAlives: true,
	}
	defer transport.CloseIdleConnections()
	client := &http.Client{Transport: transport}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, targetURL.String(), nil)
	if err != nil {
		return 0, err
	}
	request.Header.Set("Connection", "close")
	if service.OriginHostname != "" {
		request.Host = service.OriginHostname
	}
	response, err := client.Do(request)
	if err != nil {
		return 0, err
	}
	defer response.Body.Close()
	_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, 1024))
	return response.StatusCode, nil
}

func acceptsTLS(ctx context.Context, service store.Service) bool {
	address := net.JoinHostPort(service.TargetHost, fmt.Sprintf("%d", service.TargetPort))
	dialer := &tls.Dialer{
		NetDialer: &net.Dialer{Timeout: diagnosticTimeout},
		Config: &tls.Config{
			InsecureSkipVerify: true, // Detection only; never used for traffic forwarding.
			MinVersion:         tls.VersionTLS12,
		},
	}
	connection, err := dialer.DialContext(ctx, "tcp", address)
	if err != nil {
		return false
	}
	return connection.Close() == nil
}

func dialTCP(ctx context.Context, address string) error {
	connection, err := (&net.Dialer{Timeout: diagnosticTimeout}).DialContext(ctx, "tcp", address)
	if err != nil {
		return err
	}
	return connection.Close()
}

func stunBinding(ctx context.Context, network, address string) (string, error) {
	request := make([]byte, 20)
	binary.BigEndian.PutUint16(request[0:2], 0x0001)
	binary.BigEndian.PutUint32(request[4:8], 0x2112A442)
	if _, err := rand.Read(request[8:20]); err != nil {
		return "", err
	}

	connection, err := (&net.Dialer{Timeout: diagnosticTimeout}).DialContext(ctx, network, address)
	if err != nil {
		return "", err
	}
	defer connection.Close()
	deadline := time.Now().Add(diagnosticTimeout)
	if contextDeadline, ok := ctx.Deadline(); ok && contextDeadline.Before(deadline) {
		deadline = contextDeadline
	}
	if err := connection.SetDeadline(deadline); err != nil {
		return "", err
	}
	if _, err := connection.Write(request); err != nil {
		return "", err
	}

	var response []byte
	if network == "tcp" {
		header := make([]byte, 20)
		if _, err := io.ReadFull(connection, header); err != nil {
			return "", err
		}
		body := make([]byte, int(binary.BigEndian.Uint16(header[2:4])))
		if _, err := io.ReadFull(connection, body); err != nil {
			return "", err
		}
		response = append(header, body...)
	} else {
		buffer := make([]byte, 2048)
		length, err := connection.Read(buffer)
		if err != nil {
			return "", err
		}
		response = buffer[:length]
	}
	return parseSTUNBindingResponse(response, request[8:20])
}

func parseSTUNBindingResponse(response, transactionID []byte) (string, error) {
	if len(response) < 20 || binary.BigEndian.Uint32(response[4:8]) != 0x2112A442 {
		return "", errors.New("invalid STUN response")
	}
	if !equalBytes(response[8:20], transactionID) {
		return "", errors.New("STUN transaction mismatch")
	}
	messageType := binary.BigEndian.Uint16(response[0:2])
	if messageType != 0x0101 {
		return "", fmt.Errorf("STUN server returned message 0x%04x", messageType)
	}
	bodyLength := int(binary.BigEndian.Uint16(response[2:4]))
	if 20+bodyLength > len(response) {
		return "", errors.New("truncated STUN response")
	}
	for offset := 20; offset+4 <= 20+bodyLength; {
		attributeType := binary.BigEndian.Uint16(response[offset : offset+2])
		attributeLength := int(binary.BigEndian.Uint16(response[offset+2 : offset+4]))
		valueStart := offset + 4
		valueEnd := valueStart + attributeLength
		if valueEnd > len(response) {
			return "", errors.New("truncated STUN attribute")
		}
		if attributeType == 0x0020 || attributeType == 0x0001 {
			if attributeLength < 8 {
				return "", errors.New("invalid mapped address")
			}
			switch response[valueStart+1] {
			case 0x01:
				return "IPv4", nil
			case 0x02:
				return "IPv6", nil
			}
		}
		offset = valueEnd + ((4 - attributeLength%4) % 4)
	}
	return "", errors.New("STUN response has no mapped address")
}

func equalBytes(left, right []byte) bool {
	if len(left) != len(right) {
		return false
	}
	var different byte
	for index := range left {
		different |= left[index] ^ right[index]
	}
	return different == 0
}

func conciseNetworkError(err error) string {
	if err == nil {
		return "未知错误"
	}
	if errors.Is(err, context.DeadlineExceeded) || errors.Is(err, os.ErrDeadlineExceeded) {
		return "连接超时"
	}
	var netError net.Error
	if errors.As(err, &netError) && netError.Timeout() {
		return "连接超时"
	}
	message := strings.ToLower(err.Error())
	for _, marker := range []string{"connection refused", "no route to host", "network is unreachable", "certificate"} {
		if strings.Contains(message, marker) {
			return marker
		}
	}
	return "连接失败"
}

func humanDuration(duration time.Duration) string {
	if duration < time.Minute {
		return fmt.Sprintf("%d 秒", max(0, int(duration.Seconds())))
	}
	if duration < time.Hour {
		return fmt.Sprintf("%d 分钟", int(duration.Minutes()))
	}
	return fmt.Sprintf("%d 小时", int(duration.Hours()))
}
