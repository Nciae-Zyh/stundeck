package store

import "time"

type User struct {
	ID                   string    `json:"id"`
	Username             string    `json:"username"`
	PasswordHash         string    `json:"-"`
	TOTPSecretCiphertext string    `json:"-"`
	TOTPEnabled          bool      `json:"totpEnabled"`
	CreatedAt            time.Time `json:"createdAt"`
}

type AccessPolicy struct {
	Mode         string   `json:"mode"`
	AllowedHosts []string `json:"allowedHosts"`
}

type Session struct {
	Token     string
	UserID    string
	CSRFToken string
	ExpiresAt time.Time
}

type CloudflareConnection struct {
	ID              string    `json:"id"`
	Name            string    `json:"name"`
	TokenCiphertext string    `json:"-"`
	ZoneID          string    `json:"zoneId"`
	ZoneName        string    `json:"zoneName"`
	CreatedAt       time.Time `json:"createdAt"`
	UpdatedAt       time.Time `json:"updatedAt"`
}

type Service struct {
	ID                     string    `json:"id"`
	Name                   string    `json:"name"`
	TargetHost             string    `json:"targetHost"`
	TargetPort             int       `json:"targetPort"`
	Protocol               string    `json:"protocol"`
	BindPort               int       `json:"bindPort"`
	GatewayMode            string    `json:"gatewayMode"`
	GatewayAddress         string    `json:"gatewayAddress"`
	Scheme                 string    `json:"scheme"`
	PublishMode            string    `json:"publishMode"`
	CloudflareConnectionID string    `json:"cloudflareConnectionId"`
	EntryHostname          string    `json:"entryHostname"`
	OriginHostname         string    `json:"originHostname"`
	RedirectStatus         int       `json:"redirectStatus"`
	PreservePath           bool      `json:"preservePath"`
	PreserveQuery          bool      `json:"preserveQuery"`
	ManageDNS              bool      `json:"manageDns"`
	Enabled                bool      `json:"enabled"`
	Status                 string    `json:"status"`
	LastError              string    `json:"lastError,omitempty"`
	PublicIP               string    `json:"publicIp,omitempty"`
	PublicPort             int       `json:"publicPort,omitempty"`
	MappingChangedAt       time.Time `json:"mappingChangedAt,omitempty"`
	CreatedAt              time.Time `json:"createdAt"`
	UpdatedAt              time.Time `json:"updatedAt"`
}

type Event struct {
	ID        string         `json:"id"`
	ServiceID string         `json:"serviceId,omitempty"`
	Type      string         `json:"type"`
	Level     string         `json:"level"`
	Message   string         `json:"message"`
	Payload   map[string]any `json:"payload,omitempty"`
	CreatedAt time.Time      `json:"createdAt"`
}

type Webhook struct {
	ID               string    `json:"id"`
	Name             string    `json:"name"`
	URL              string    `json:"url"`
	SecretCiphertext string    `json:"-"`
	AllowPrivate     bool      `json:"allowPrivate"`
	Enabled          bool      `json:"enabled"`
	CreatedAt        time.Time `json:"createdAt"`
	UpdatedAt        time.Time `json:"updatedAt"`
}

type WebhookDelivery struct {
	ID            string
	WebhookID     string
	EventID       string
	Payload       string
	Attempts      int
	NextAttemptAt time.Time
	DeliveredAt   time.Time
	LastError     string
	CreatedAt     time.Time
}
