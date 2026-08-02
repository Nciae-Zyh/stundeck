package engine

import (
	"context"
	"encoding/binary"
	"net"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"

	"github.com/Nciae-Zyh/stundeck/internal/store"
)

func TestParseSTUNBindingResponseIPv4(t *testing.T) {
	transactionID := []byte("abcdefghijkl")
	response := make([]byte, 32)
	binary.BigEndian.PutUint16(response[0:2], 0x0101)
	binary.BigEndian.PutUint16(response[2:4], 12)
	binary.BigEndian.PutUint32(response[4:8], 0x2112A442)
	copy(response[8:20], transactionID)
	binary.BigEndian.PutUint16(response[20:22], 0x0020)
	binary.BigEndian.PutUint16(response[22:24], 8)
	response[25] = 0x01

	family, err := parseSTUNBindingResponse(response, transactionID)
	if err != nil {
		t.Fatal(err)
	}
	if family != "IPv4" {
		t.Fatalf("family = %q", family)
	}
}

func TestParseSTUNBindingResponseRejectsTransactionMismatch(t *testing.T) {
	response := make([]byte, 20)
	binary.BigEndian.PutUint16(response[0:2], 0x0101)
	binary.BigEndian.PutUint32(response[4:8], 0x2112A442)
	copy(response[8:20], []byte("abcdefghijkl"))

	if _, err := parseSTUNBindingResponse(response, []byte("mnopqrstuvwx")); err == nil {
		t.Fatal("transaction mismatch was accepted")
	}
}

func TestDiagnosticOutcome(t *testing.T) {
	if got := diagnosticOutcome([]DiagnosticCheck{{Status: "pass"}, {Status: "info"}}); got != "pass" {
		t.Fatalf("outcome = %q", got)
	}
	if got := diagnosticOutcome([]DiagnosticCheck{{Status: "warn"}}); got != "warning" {
		t.Fatalf("outcome = %q", got)
	}
	if got := diagnosticOutcome([]DiagnosticCheck{{Status: "warn"}, {Status: "fail"}}); got != "fail" {
		t.Fatalf("outcome = %q", got)
	}
}

func TestCheckTargetProtocolDetectsHTTPSOnHTTPService(t *testing.T) {
	tlsServer := httptest.NewTLSServer(nil)
	defer tlsServer.Close()
	host, portText, err := net.SplitHostPort(strings.TrimPrefix(tlsServer.URL, "https://"))
	if err != nil {
		t.Fatal(err)
	}
	port, err := strconv.Atoi(portText)
	if err != nil {
		t.Fatal(err)
	}

	status, message := checkTargetProtocol(context.Background(), store.Service{
		TargetHost:  host,
		TargetPort:  port,
		Protocol:    "tcp",
		PublishMode: "redirect",
		Scheme:      "http",
	})
	if status != "fail" || !strings.Contains(message, "实际接受 HTTPS") {
		t.Fatalf("status = %q, message = %q", status, message)
	}
}

func TestCheckProxyEnvironmentReportsConfiguredProxy(t *testing.T) {
	t.Setenv("HTTP_PROXY", "http://proxy.invalid:7890")
	status, message := checkProxyEnvironment(context.Background())
	if status != "fail" || !strings.Contains(message, "HTTP_PROXY") {
		t.Fatalf("status = %q, message = %q", status, message)
	}
}
