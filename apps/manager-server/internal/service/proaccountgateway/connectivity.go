package proaccountgateway

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"strings"
)

func (c *Client) TestAccount(ctx context.Context, gatewayBaseURL string, managementKey string, account AccountReference, modelName string) (ConnectivityResult, error) {
	runtime, err := c.ResolveAccountRuntime(ctx, gatewayBaseURL, managementKey, account.SourceType, account.SourceLocator)
	if err != nil {
		return ConnectivityResult{}, err
	}
	if strings.TrimSpace(runtime.Platform) == "" {
		runtime.Platform = account.Platform
	}
	request, protocol, err := connectivityRequest(runtime, account, modelName)
	if err != nil {
		return ConnectivityResult{}, err
	}
	result, err := c.APICall(ctx, gatewayBaseURL, managementKey, request)
	if err != nil {
		return ConnectivityResult{}, err
	}
	output := ConnectivityResult{
		Success:    result.StatusCode >= 200 && result.StatusCode < 300,
		StatusCode: result.StatusCode, Protocol: protocol, Model: modelName,
	}
	if !output.Success {
		output.ErrorCode, output.Retryable = classifyHTTPStatus(result.StatusCode)
	}
	return output, nil
}

func connectivityRequest(runtime AccountRuntime, account AccountReference, modelName string) (APICallRequest, string, error) {
	modelName = strings.TrimSpace(modelName)
	if modelName == "" {
		return APICallRequest{}, "", ErrInvalidModelRule
	}
	headers := cloneHeaders(runtime.Headers)
	setHeader(headers, "Content-Type", "application/json")
	platform := strings.ToLower(strings.TrimSpace(runtime.Platform))
	switch platform {
	case "openai":
		setHeader(headers, "Authorization", "Bearer $TOKEN$")
		if account.SourceType == SourceOpenAICompatibility {
			target, err := joinAPIPath(runtime.BaseURL, "chat/completions")
			return APICallRequest{AuthIndex: account.AuthIndex, Method: http.MethodPost, URL: target, Headers: headers, Body: map[string]any{
				"model": modelName, "messages": []map[string]string{{"role": "user", "content": "Reply with OK."}}, "max_tokens": 8, "stream": false,
			}}, "chat_completions", err
		}
		target, err := joinAPIPath(runtime.BaseURL, "responses")
		return APICallRequest{AuthIndex: account.AuthIndex, Method: http.MethodPost, URL: target, Headers: headers, Body: map[string]any{
			"model": modelName, "input": "Reply with OK.", "max_output_tokens": 8, "stream": false,
		}}, "responses", err
	case "anthropic":
		if strings.EqualFold(account.AuthType, "api") {
			setHeader(headers, "x-api-key", "$TOKEN$")
		} else {
			setHeader(headers, "Authorization", "Bearer $TOKEN$")
		}
		setHeader(headers, "anthropic-version", "2023-06-01")
		target, err := joinAPIPath(runtime.BaseURL, "messages")
		return APICallRequest{AuthIndex: account.AuthIndex, Method: http.MethodPost, URL: target, Headers: headers, Body: map[string]any{
			"model": modelName, "messages": []map[string]string{{"role": "user", "content": "Reply with OK."}}, "max_tokens": 8,
		}}, "messages", err
	case "gemini":
		if strings.EqualFold(account.AuthType, "api") {
			setHeader(headers, "x-goog-api-key", "$TOKEN$")
		} else {
			setHeader(headers, "Authorization", "Bearer $TOKEN$")
		}
		target, err := joinAPIPath(runtime.BaseURL, "models/"+url.PathEscape(modelName)+":generateContent")
		return APICallRequest{AuthIndex: account.AuthIndex, Method: http.MethodPost, URL: target, Headers: headers, Body: map[string]any{
			"contents": []map[string]any{{"role": "user", "parts": []map[string]string{{"text": "Reply with OK."}}}},
		}}, "generate_content", err
	case "xai":
		setHeader(headers, "Authorization", "Bearer $TOKEN$")
		target, err := joinAPIPath(runtime.BaseURL, "chat/completions")
		return APICallRequest{AuthIndex: account.AuthIndex, Method: http.MethodPost, URL: target, Headers: headers, Body: map[string]any{
			"model": modelName, "messages": []map[string]string{{"role": "user", "content": "Reply with OK."}}, "max_tokens": 8, "stream": false,
		}}, "chat_completions", err
	default:
		return APICallRequest{}, "", fmt.Errorf("%w: connectivity test for %s", ErrUnsupportedSource, platform)
	}
}

func setHeader(headers map[string]string, name string, value string) {
	for key := range headers {
		if strings.EqualFold(key, name) && key != name {
			delete(headers, key)
		}
	}
	headers[name] = value
}

func classifyHTTPStatus(statusCode int) (string, bool) {
	switch {
	case statusCode == http.StatusUnauthorized || statusCode == http.StatusForbidden:
		return "authentication_failed", false
	case statusCode == http.StatusNotFound || statusCode == http.StatusMethodNotAllowed:
		return "protocol_not_supported", false
	case statusCode == http.StatusTooManyRequests:
		return "rate_limited", true
	case statusCode >= 500:
		return "upstream_unavailable", true
	default:
		return "connectivity_test_failed", false
	}
}
