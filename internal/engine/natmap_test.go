package engine

import (
	"reflect"
	"testing"
	"time"

	"github.com/Nciae-Zyh/stundeck/internal/store"
)

func TestBuildArgsTCP(t *testing.T) {
	service := store.Service{
		TargetHost: "192.168.1.20",
		TargetPort: 8080,
		Protocol:   "tcp",
		BindPort:   12000,
	}
	config := Config{
		STUNServer:      "turn.cloudflare.com:3478",
		KeepAliveServer: "www.cloudflare.com:80",
		KeepAlive:       30 * time.Second,
	}
	want := []string{
		"-4", "-s", "turn.cloudflare.com:3478", "-b", "12000",
		"-t", "192.168.1.20", "-p", "8080", "-e", "/bin/notify",
		"-k", "30", "-h", "www.cloudflare.com:80",
	}
	if got := BuildArgs(service, config, "/bin/notify"); !reflect.DeepEqual(got, want) {
		t.Fatalf("BuildArgs() = %#v, want %#v", got, want)
	}
}

func TestValidateMapping(t *testing.T) {
	valid := Mapping{ServiceID: "service", PublicIP: "203.0.113.10", PublicPort: 12345, Protocol: "tcp"}
	if err := ValidateMapping(valid); err != nil {
		t.Fatalf("valid mapping rejected: %v", err)
	}
	invalid := valid
	invalid.PublicIP = "not-an-ip"
	if err := ValidateMapping(invalid); err == nil {
		t.Fatal("invalid mapping accepted")
	}
}
