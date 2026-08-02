package config

import (
	"os"
	"path/filepath"
	"strconv"
	"time"
)

type Config struct {
	Listen          string
	DataDir         string
	DatabasePath    string
	MasterKeyFile   string
	NatmapBinary    string
	NotifyBinary    string
	STUNServer      string
	KeepAliveServer string
	KeepAlive       time.Duration
	SecureCookies   bool
	SessionTTL      time.Duration
	ShutdownTimeout time.Duration
}

func Load() Config {
	dataDir := env("STUNDECK_DATA_DIR", "./data")
	return Config{
		Listen:          env("STUNDECK_LISTEN", "127.0.0.1:8080"),
		DataDir:         dataDir,
		DatabasePath:    env("STUNDECK_DATABASE", filepath.Join(dataDir, "stundeck.db")),
		MasterKeyFile:   env("STUNDECK_MASTER_KEY_FILE", filepath.Join(dataDir, "master.key")),
		NatmapBinary:    env("STUNDECK_NATMAP_BINARY", "natmap"),
		NotifyBinary:    env("STUNDECK_NOTIFY_BINARY", "stundeck-notify"),
		STUNServer:      env("STUNDECK_STUN_SERVER", "turn.cloudflare.com:3478"),
		KeepAliveServer: env("STUNDECK_KEEPALIVE_SERVER", "www.cloudflare.com:80"),
		KeepAlive:       30 * time.Second,
		SecureCookies:   envBool("STUNDECK_SECURE_COOKIES", false),
		SessionTTL:      24 * time.Hour,
		ShutdownTimeout: 10 * time.Second,
	}
}

func env(name, fallback string) string {
	if value := os.Getenv(name); value != "" {
		return value
	}
	return fallback
}

func envBool(name string, fallback bool) bool {
	value := os.Getenv(name)
	if value == "" {
		return fallback
	}
	parsed, err := strconv.ParseBool(value)
	if err != nil {
		return fallback
	}
	return parsed
}
