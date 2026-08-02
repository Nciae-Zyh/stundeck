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
	Success bool         `json:"success"`
	Errors  []apiMessage `json:"errors"`
	Result  T            `json:"result"`
}

type TokenStatus struct {
	ID        string `json:"id"`
	Status    string `json:"status"`
	ExpiresOn string `json:"expires_on"`
	NotBefore string `json:"not_before"`
}

type Zone struct {
	ID     string `json:"id"`
	Name   string `json:"name"`
	Status string `json:"status"`
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
	var result []Zone
	if err := c.do(ctx, http.MethodGet, "/zones?per_page=50", nil, &result); err != nil {
		return nil, err
	}
	return result, nil
}

func (c *Client) do(ctx context.Context, method, path string, body any, result any) error {
	var reader io.Reader
	if body != nil {
		encoded, err := json.Marshal(body)
		if err != nil {
			return fmt.Errorf("encode cloudflare request: %w", err)
		}
		reader = bytes.NewReader(encoded)
	}
	request, err := http.NewRequestWithContext(ctx, method, c.baseURL+path, reader)
	if err != nil {
		return fmt.Errorf("create cloudflare request: %w", err)
	}
	request.Header.Set("Authorization", "Bearer "+c.token)
	request.Header.Set("Accept", "application/json")
	if body != nil {
		request.Header.Set("Content-Type", "application/json")
	}
	response, err := c.httpClient.Do(request)
	if err != nil {
		return fmt.Errorf("call cloudflare api: %w", err)
	}
	defer response.Body.Close()
	responseBody, err := io.ReadAll(io.LimitReader(response.Body, 2<<20))
	if err != nil {
		return fmt.Errorf("read cloudflare response: %w", err)
	}
	var raw envelope[json.RawMessage]
	if err := json.Unmarshal(responseBody, &raw); err != nil {
		return fmt.Errorf("decode cloudflare response: %w", err)
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 || !raw.Success {
		messages := make([]string, 0, len(raw.Errors))
		for _, apiError := range raw.Errors {
			messages = append(messages, apiError.Message)
		}
		return &APIError{StatusCode: response.StatusCode, Messages: messages}
	}
	if result == nil || len(raw.Result) == 0 || string(raw.Result) == "null" {
		return nil
	}
	if err := json.Unmarshal(raw.Result, result); err != nil {
		return fmt.Errorf("decode cloudflare result: %w", err)
	}
	return nil
}

func escaped(value string) string {
	return url.QueryEscape(value)
}

func isNotFound(err error) bool {
	var apiError *APIError
	return errors.As(err, &apiError) && apiError.StatusCode == http.StatusNotFound
}
