package webhook

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/Nciae-Zyh/stundeck/internal/security"
	"github.com/Nciae-Zyh/stundeck/internal/store"
)

type Dispatcher struct {
	store  *store.Store
	cipher *security.Cipher
	logger *slog.Logger
}

type payload struct {
	ID         string         `json:"id"`
	Event      string         `json:"event"`
	ServiceID  string         `json:"serviceId,omitempty"`
	Level      string         `json:"level"`
	Message    string         `json:"message"`
	Data       map[string]any `json:"data,omitempty"`
	OccurredAt time.Time      `json:"occurredAt"`
}

func NewDispatcher(database *store.Store, cipher *security.Cipher, logger *slog.Logger) *Dispatcher {
	return &Dispatcher{store: database, cipher: cipher, logger: logger}
}

func (d *Dispatcher) Enqueue(ctx context.Context, event store.Event) error {
	webhooks, err := d.store.Webhooks(ctx)
	if err != nil {
		return err
	}
	body, err := encodeEvent(event)
	if err != nil {
		return err
	}
	for _, endpoint := range webhooks {
		if !endpoint.Enabled {
			continue
		}
		if err := d.queue(ctx, endpoint.ID, event.ID, body); err != nil {
			return err
		}
	}
	return nil
}

func (d *Dispatcher) EnqueueTo(ctx context.Context, webhookID string, event store.Event) error {
	endpoint, err := d.store.Webhook(ctx, webhookID)
	if err != nil {
		return err
	}
	if !endpoint.Enabled {
		return errors.New("webhook is disabled")
	}
	body, err := encodeEvent(event)
	if err != nil {
		return err
	}
	return d.queue(ctx, endpoint.ID, event.ID, body)
}

func encodeEvent(event store.Event) ([]byte, error) {
	body, err := json.Marshal(payload{
		ID:         event.ID,
		Event:      event.Type,
		ServiceID:  event.ServiceID,
		Level:      event.Level,
		Message:    event.Message,
		Data:       event.Payload,
		OccurredAt: event.CreatedAt,
	})
	if err != nil {
		return nil, fmt.Errorf("encode webhook payload: %w", err)
	}
	return body, nil
}

func (d *Dispatcher) queue(ctx context.Context, webhookID, eventID string, body []byte) error {
	id, err := security.RandomToken(18)
	if err != nil {
		return err
	}
	delivery := store.WebhookDelivery{
		ID:            id,
		WebhookID:     webhookID,
		EventID:       eventID,
		Payload:       string(body),
		NextAttemptAt: time.Now(),
		CreatedAt:     time.Now(),
	}
	return d.store.QueueWebhookDelivery(ctx, delivery)
}

func (d *Dispatcher) Run(ctx context.Context) {
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			d.deliverPending(ctx)
		}
	}
}

func (d *Dispatcher) deliverPending(ctx context.Context) {
	deliveries, err := d.store.PendingWebhookDeliveries(ctx, 20)
	if err != nil {
		d.logger.Error("load webhook deliveries", "error", err)
		return
	}
	for _, delivery := range deliveries {
		if err := d.deliver(ctx, delivery); err != nil {
			attempts := delivery.Attempts + 1
			delay := time.Duration(1<<min(attempts, 8)) * time.Second
			if attempts >= 10 {
				delay = 24 * time.Hour
			}
			_ = d.store.RetryWebhookDelivery(ctx, delivery.ID, err.Error(), attempts, time.Now().Add(delay))
			d.logger.Warn("webhook delivery failed", "delivery_id", delivery.ID, "attempts", attempts, "error", err)
			continue
		}
		_ = d.store.MarkWebhookDelivered(ctx, delivery.ID)
	}
}

func (d *Dispatcher) deliver(ctx context.Context, delivery store.WebhookDelivery) error {
	endpoint, err := d.store.Webhook(ctx, delivery.WebhookID)
	if err != nil {
		return err
	}
	secret, err := d.cipher.Decrypt(endpoint.SecretCiphertext)
	if err != nil {
		return err
	}
	timestamp := strconv.FormatInt(time.Now().Unix(), 10)
	signature := sign(secret, timestamp, []byte(delivery.Payload))
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint.URL, bytes.NewBufferString(delivery.Payload))
	if err != nil {
		return fmt.Errorf("create webhook request: %w", err)
	}
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("User-Agent", "StunDeck/1")
	request.Header.Set("X-StunDeck-Event-ID", delivery.EventID)
	request.Header.Set("X-StunDeck-Timestamp", timestamp)
	request.Header.Set("X-StunDeck-Signature", "v1="+signature)
	client, err := safeHTTPClient(endpoint.AllowPrivate)
	if err != nil {
		return err
	}
	response, err := client.Do(request)
	if err != nil {
		return fmt.Errorf("send webhook: %w", err)
	}
	defer response.Body.Close()
	_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, 64<<10))
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return fmt.Errorf("webhook returned %s", response.Status)
	}
	return nil
}

func ValidateURL(value string, allowPrivate bool) error {
	parsed, err := url.Parse(value)
	if err != nil || parsed.Hostname() == "" {
		return errors.New("webhook URL is invalid")
	}
	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		return errors.New("webhook URL must use http or https")
	}
	if parsed.User != nil {
		return errors.New("webhook URL must not contain credentials")
	}
	if !allowPrivate {
		addresses, err := net.LookupIP(parsed.Hostname())
		if err != nil {
			return fmt.Errorf("resolve webhook host: %w", err)
		}
		for _, address := range addresses {
			if isPrivateAddress(address) {
				return errors.New("webhook resolves to a private or local address")
			}
		}
	}
	return nil
}

func safeHTTPClient(allowPrivate bool) (*http.Client, error) {
	dialer := &net.Dialer{Timeout: 5 * time.Second}
	transport := &http.Transport{
		// Connecting directly makes the DNS/IP policy below authoritative. A
		// process-wide HTTP proxy could otherwise become an SSRF bypass.
		Proxy: nil,
		DialContext: func(ctx context.Context, network, address string) (net.Conn, error) {
			host, port, err := net.SplitHostPort(address)
			if err != nil {
				return nil, err
			}
			addresses, err := net.DefaultResolver.LookupIP(ctx, "ip", host)
			if err != nil {
				return nil, err
			}
			for _, candidate := range addresses {
				if !allowPrivate && isPrivateAddress(candidate) {
					continue
				}
				return dialer.DialContext(ctx, network, net.JoinHostPort(candidate.String(), port))
			}
			return nil, errors.New("webhook host has no allowed address")
		},
		TLSHandshakeTimeout: 5 * time.Second,
	}
	return &http.Client{
		Timeout:   10 * time.Second,
		Transport: transport,
		CheckRedirect: func(_ *http.Request, _ []*http.Request) error {
			return errors.New("webhook redirects are disabled")
		},
	}, nil
}

func isPrivateAddress(ip net.IP) bool {
	if ip.IsLoopback() || ip.IsPrivate() || ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() || ip.IsUnspecified() || ip.IsMulticast() {
		return true
	}
	address, ok := netip.AddrFromSlice(ip)
	if !ok {
		return true
	}
	address = address.Unmap()
	for _, prefix := range blockedPrefixes {
		if prefix.Contains(address) {
			return true
		}
	}
	return false
}

var blockedPrefixes = []netip.Prefix{
	netip.MustParsePrefix("0.0.0.0/8"),
	netip.MustParsePrefix("100.64.0.0/10"),
	netip.MustParsePrefix("192.0.0.0/24"),
	netip.MustParsePrefix("192.0.2.0/24"),
	netip.MustParsePrefix("198.18.0.0/15"),
	netip.MustParsePrefix("198.51.100.0/24"),
	netip.MustParsePrefix("203.0.113.0/24"),
	netip.MustParsePrefix("240.0.0.0/4"),
	netip.MustParsePrefix("2001:db8::/32"),
}

func sign(secret, timestamp string, body []byte) string {
	mac := hmac.New(sha256.New, []byte(secret))
	_, _ = mac.Write([]byte(strings.Join([]string{timestamp, string(body)}, ".")))
	return hex.EncodeToString(mac.Sum(nil))
}
