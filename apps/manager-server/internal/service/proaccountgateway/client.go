package proaccountgateway

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/service/cpa"
)

const (
	defaultTimeout        = 30 * time.Second
	maxManagementResponse = 16 * 1024 * 1024
)

type GatewayError struct {
	StatusCode int
	Code       string
	Message    string
	Retryable  bool
}

func (e *GatewayError) Error() string {
	if e == nil {
		return "gateway management request failed"
	}
	if e.Code != "" {
		return fmt.Sprintf("gateway management request failed: %s", e.Code)
	}
	return fmt.Sprintf("gateway management request failed: HTTP %d", e.StatusCode)
}

type Client struct {
	httpClient *http.Client
	timeout    time.Duration
}

func New(client *http.Client, timeout ...time.Duration) *Client {
	if client == nil {
		client = http.DefaultClient
	}
	requestTimeout := defaultTimeout
	if len(timeout) > 0 && timeout[0] > 0 {
		requestTimeout = timeout[0]
	}
	return &Client{httpClient: client, timeout: requestTimeout}
}

func (c *Client) get(ctx context.Context, baseURL string, managementKey string, path string) ([]byte, http.Header, error) {
	return c.request(ctx, baseURL, managementKey, http.MethodGet, path, nil)
}

func (c *Client) requestJSON(ctx context.Context, baseURL string, managementKey string, method string, path string, payload any) ([]byte, http.Header, error) {
	var body io.Reader
	if payload != nil {
		raw, errMarshal := json.Marshal(payload)
		if errMarshal != nil {
			return nil, nil, fmt.Errorf("encode gateway request: %w", errMarshal)
		}
		body = bytes.NewReader(raw)
	}
	return c.request(ctx, baseURL, managementKey, method, path, body)
}

func (c *Client) request(ctx context.Context, baseURL string, managementKey string, method string, path string, body io.Reader) ([]byte, http.Header, error) {
	baseURL = cpa.NormalizeBaseURL(baseURL)
	managementKey = strings.TrimSpace(managementKey)
	if baseURL == "" || managementKey == "" {
		return nil, nil, errors.New("gateway connection is not configured")
	}
	requestCtx, cancel := context.WithTimeout(ctx, c.timeout)
	defer cancel()
	req, errNew := http.NewRequestWithContext(requestCtx, method, baseURL+path, body)
	if errNew != nil {
		return nil, nil, fmt.Errorf("create gateway request: %w", errNew)
	}
	req.Header.Set("Authorization", "Bearer "+managementKey)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	response, errDo := c.httpClient.Do(req)
	if errDo != nil {
		return nil, nil, fmt.Errorf("gateway management request failed: %w", errDo)
	}
	defer response.Body.Close()
	limited := io.LimitReader(response.Body, maxManagementResponse+1)
	raw, errRead := io.ReadAll(limited)
	if errRead != nil {
		return nil, response.Header.Clone(), fmt.Errorf("read gateway response: %w", errRead)
	}
	if len(raw) > maxManagementResponse {
		return nil, response.Header.Clone(), errors.New("gateway management response is too large")
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return nil, response.Header.Clone(), decodeGatewayError(response.StatusCode, raw)
	}
	return raw, response.Header.Clone(), nil
}

func decodeGatewayError(statusCode int, raw []byte) error {
	payload := map[string]any{}
	_ = json.Unmarshal(raw, &payload)
	code := safeErrorText(payload["code"], 128)
	message := safeErrorText(payload["message"], 512)
	if message == "" {
		message = safeErrorText(payload["error"], 512)
	}
	retryable, _ := payload["retryable"].(bool)
	if statusCode == http.StatusTooManyRequests || statusCode >= http.StatusInternalServerError {
		retryable = true
	}
	return &GatewayError{StatusCode: statusCode, Code: code, Message: message, Retryable: retryable}
}

func safeErrorText(value any, limit int) string {
	text, ok := value.(string)
	if !ok {
		return ""
	}
	text = strings.TrimSpace(text)
	if len(text) > limit {
		text = text[:limit]
	}
	return text
}
