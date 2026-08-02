package engine

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestSSDPHeaderIsCaseInsensitive(t *testing.T) {
	message := "HTTP/1.1 200 OK\r\nLoCaTiOn: http://192.0.2.1/root.xml\r\n\r\n"
	if got := ssdpHeader(message, "location"); got != "http://192.0.2.1/root.xml" {
		t.Fatalf("ssdpHeader() = %q", got)
	}
}

func TestFindWANServiceInNestedDevice(t *testing.T) {
	want := upnpService{ServiceType: "urn:schemas-upnp-org:service:WANIPConnection:1", ControlURL: "/ctl/IPConn"}
	root := upnpDevice{Devices: []upnpDevice{{Services: []upnpService{want}}}}
	got, ok := findWANService(root)
	if !ok || got != want {
		t.Fatalf("findWANService() = %#v, %v", got, ok)
	}
}

func TestAddUPnPMappingUsesDiscoveredPorts(t *testing.T) {
	var receivedBody string
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		if got := request.Header.Get("SOAPAction"); !strings.Contains(got, "#AddPortMapping") {
			t.Errorf("unexpected SOAPAction: %q", got)
		}
		payload, _ := io.ReadAll(request.Body)
		receivedBody = string(payload)
		response.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	mapping := GatewayMapping{
		Mode: "upnp", ControlURL: server.URL, ServiceType: "urn:schemas-upnp-org:service:WANIPConnection:1",
		InternalIP: "10.1.1.227", InternalPort: 40123, ExternalPort: 51000, Protocol: "TCP",
	}
	if err := addUPnPMapping(context.Background(), mapping, "StunDeck test"); err != nil {
		t.Fatal(err)
	}
	for _, expected := range []string{"<NewExternalPort>51000</NewExternalPort>", "<NewInternalPort>40123</NewInternalPort>", "<NewInternalClient>10.1.1.227</NewInternalClient>", "<NewProtocol>TCP</NewProtocol>"} {
		if !strings.Contains(receivedBody, expected) {
			t.Fatalf("SOAP body does not contain %q: %s", expected, receivedBody)
		}
	}
}
