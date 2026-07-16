package proaccountgateway

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
)

const maxAPICallBody = 8 * 1024 * 1024

func (c *Client) Capabilities(ctx context.Context, baseURL string, managementKey string) (Capabilities, error) {
	_, headers, err := c.get(ctx, baseURL, managementKey, "/v0/management/auth-files")
	if err != nil {
		return Capabilities{}, err
	}
	return capabilitiesFromHeaders(headers), nil
}

func (c *Client) APICall(ctx context.Context, baseURL string, managementKey string, input APICallRequest) (APICallResult, error) {
	input.AuthIndex = strings.TrimSpace(input.AuthIndex)
	input.Method = strings.ToUpper(strings.TrimSpace(input.Method))
	input.URL = strings.TrimSpace(input.URL)
	if input.AuthIndex == "" || input.Method == "" || input.URL == "" {
		return APICallResult{}, errors.New("gateway api-call requires auth index, method and url")
	}
	parsed, err := url.Parse(input.URL)
	if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Host == "" || parsed.User != nil {
		return APICallResult{}, errors.New("gateway api-call url is invalid")
	}
	data := ""
	if input.Body != nil {
		switch value := input.Body.(type) {
		case string:
			data = value
		case []byte:
			data = string(value)
		default:
			raw, marshalErr := json.Marshal(value)
			if marshalErr != nil {
				return APICallResult{}, fmt.Errorf("encode gateway api-call body: %w", marshalErr)
			}
			data = string(raw)
		}
	}
	raw, _, err := c.requestJSON(ctx, baseURL, managementKey, http.MethodPost, "/v0/management/api-call", map[string]any{
		"authIndex": input.AuthIndex,
		"method":    input.Method,
		"url":       input.URL,
		"header":    cloneHeaders(input.Headers),
		"data":      data,
	})
	if err != nil {
		return APICallResult{}, err
	}
	if len(raw) > maxAPICallBody {
		return APICallResult{}, errors.New("gateway api-call response is too large")
	}
	var payload struct {
		StatusCode int                 `json:"status_code"`
		Headers    map[string][]string `json:"header"`
		Body       string              `json:"body"`
	}
	if err := json.Unmarshal(raw, &payload); err != nil || payload.StatusCode < 100 || payload.StatusCode > 599 {
		return APICallResult{}, errors.New("gateway api-call returned an invalid response")
	}
	return APICallResult{StatusCode: payload.StatusCode, Headers: payload.Headers, Body: payload.Body}, nil
}

func cloneHeaders(value map[string]string) map[string]string {
	result := make(map[string]string, len(value))
	for key, item := range value {
		key = strings.TrimSpace(key)
		if key != "" {
			result[key] = item
		}
	}
	return result
}
