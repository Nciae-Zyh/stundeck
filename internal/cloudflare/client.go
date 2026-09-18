package cloudflare

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

const defaultBaseURL = "https://api.cloudflare.com/client/v4"

type Client struct {
	baseURL    string
	token      string
	httpClient *http.Client
}

type APIError struct {
	StatusCode int
	Messages   []string
}

func (e *APIError) Error() string {
	if len(e.Messages) == 0 {
		return fmt.Sprintf("cloudflare api returned status %d", e.StatusCode)
	}
	return fmt.Sprintf("cloudflare api returned status %d: %s", e.StatusCode, strings.Join(e.Messages, "; "))
}

type apiMessage struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

type envelope[T any] struct {
	Success    bool         `json:"success"`
	Errors     []apiMessage `json:"errors"`
	Result     T            `json:"result"`
	ResultInfo struct {
		TotalPages int `json:"total_pages"`
	} `json:"result_info"`
}

type TokenStatus struct {
	ID        string `json:"id"`
	Status    string `json:"status"`
	ExpiresOn string `json:"expires_on"`
	NotBefore string `json:"not_before"`
}

type Zone struct {
	ID      string `json:"id"`
	Name    string `json:"name"`
	Status  string `json:"status"`
	Account struct {
		ID   string `json:"id"`
		Name string `json:"name"`
	} `json:"account"`
	Permissions []string `json:"permissions,omitempty"`
}

func New(token string) *Client {
	return &Client{
		baseURL: defaultBaseURL,
		token:   token,
		httpClient: &http.Client{
			Timeout: 15 * time.Second,
		},
	}
}

func NewForTest(baseURL, token string, client *http.Client) *Client {
	return &Client{baseURL: strings.TrimRight(baseURL, "/"), token: token, httpClient: client}
}

func (c *Client) VerifyToken(ctx context.Context) (TokenStatus, error) {
	var result TokenStatus
	if err := c.do(ctx, http.MethodGet, "/user/tokens/verify", nil, &result); err != nil {
		return TokenStatus{}, err
	}
	if result.Status != "active" {
		return result, fmt.Errorf("cloudflare token is %s", result.Status)
	}
	return result, nil
}

func (c *Client) Zones(ctx context.Context) ([]Zone, error) {
	return listAll[Zone](ctx, c, "/zones", 50)
}

func (c *Client) do(ctx context.Context, method, path string, body any, result any) error {
	_, err := c.request(ctx, method, path, body, result)
	return err
}

func (c *Client) request(ctx context.Context, method, path string, body any, result any) (int, error) {
	var reader io.Reader
	if body != nil {
		encoded, err := json.Marshal(body)
		if err != nil {
			return 0, fmt.Errorf("encode cloudflare request: %w", err)
		}
		reader = bytes.NewReader(encoded)
	}
	return c.requestBody(ctx, method, path, reader, "application/json", result)
}

func (c *Client) requestBody(ctx context.Context, method, path string, reader io.Reader, contentType string, result any) (int, error) {
	request, err := http.NewRequestWithContext(ctx, method, c.baseURL+path, reader)
	if err != nil {
		return 0, fmt.Errorf("create cloudflare request: %w", err)
	}
	request.Header.Set("Authorization", "Bearer "+c.token)
	request.Header.Set("Accept", "application/json")
	if reader != nil {
		request.Header.Set("Content-Type", contentType)
	}
	response, err := c.httpClient.Do(request)
	if err != nil {
		return 0, fmt.Errorf("call cloudflare api: %w", err)
	}
	defer response.Body.Close()
	responseBody, err := io.ReadAll(io.LimitReader(response.Body, 2<<20))
	if err != nil {
		return 0, fmt.Errorf("read cloudflare response: %w", err)
	}
	var raw envelope[json.RawMessage]
	if err := json.Unmarshal(responseBody, &raw); err != nil {
		if response.StatusCode < 200 || response.StatusCode >= 300 {
			return 0, &APIError{StatusCode: response.StatusCode}
		}
		return 0, fmt.Errorf("decode cloudflare response: %w", err)
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 || !raw.Success {
		messages := make([]string, 0, len(raw.Errors))
		for _, apiError := range raw.Errors {
			messages = append(messages, strings.ReplaceAll(apiError.Message, c.token, "[redacted]"))
		}
		return 0, &APIError{StatusCode: response.StatusCode, Messages: messages}
	}
	if result == nil || len(raw.Result) == 0 || string(raw.Result) == "null" {
		return raw.ResultInfo.TotalPages, nil
	}
	if err := json.Unmarshal(raw.Result, result); err != nil {
		return 0, fmt.Errorf("decode cloudflare result: %w", err)
	}
	return raw.ResultInfo.TotalPages, nil
}

// Never silently truncate the zone/domain inventory. A bounded failure is safer
// than treating an incomplete list as proof that a hostname is unused.
func listAll[T any](ctx context.Context, c *Client, path string, perPage int) ([]T, error) {
	result := []T{}
	separator := "?"
	if strings.Contains(path, "?") {
		separator = "&"
	}
	for page := 1; page <= 1000; page++ {
		var batch []T
		total, err := c.request(ctx, http.MethodGet, fmt.Sprintf("%s%sper_page=%d&page=%d", path, separator, perPage, page), nil, &batch)
		if err != nil {
			return nil, err
		}
		result = append(result, batch...)
		if (total > 0 && page >= total) || (total == 0 && len(batch) < perPage) {
			return result, nil
		}
	}
	return nil, errors.New("Cloudflare inventory exceeds pagination limit; narrow the token resource scope")
}

func (c *Client) Zone(ctx context.Context, id string) (Zone, error) {
	var zone Zone
	err := c.do(ctx, http.MethodGet, "/zones/"+escaped(id), nil, &zone)
	return zone, err
}

func HostInZone(host, zone string) bool {
	host, zone = strings.ToLower(host), strings.ToLower(zone)
	return host == zone || strings.HasSuffix(host, "."+zone)
}

func escaped(value string) string {
	return url.QueryEscape(value)
}

func isNotFound(err error) bool {
	var apiError *APIError
	return errors.As(err, &apiError) && apiError.StatusCode == http.StatusNotFound
}

// Account-owned tokens use the account verification endpoint. Account IDs come
// from Cloudflare's zone response, never from a user-entered account identifier.
func (c *Client) VerifyForZones(ctx context.Context, zones []Zone) (TokenStatus, error) {
	status, err := c.VerifyToken(ctx)
	if err == nil {
		return status, nil
	}
	if !permissionDenied(err) {
		return status, err
	}
	seen := map[string]bool{}
	for _, zone := range zones {
		id := zone.Account.ID
		if id == "" || seen[id] {
			continue
		}
		seen[id] = true
		var result TokenStatus
		if accountErr := c.do(ctx, http.MethodGet, "/accounts/"+escaped(id)+"/tokens/verify", nil, &result); accountErr == nil && result.Status == "active" {
			return result, nil
		}
	}
	return status, err
}
