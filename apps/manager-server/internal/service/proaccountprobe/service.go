package proaccountprobe

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"time"

	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/service/proaccountgateway"
)

const (
	CapabilitySupported   = "supported"
	CapabilityUnsupported = "unsupported"
	CapabilityUnknown     = "unknown"

	maxProbeResponse = 4 * 1024 * 1024
)

var ErrInvalidProbeRequest = errors.New("invalid account probe request")

type Input struct {
	Platform      string
	AuthType      string
	BaseURL       string
	APIKey        string
	ProtocolMode  string
	Model         string
	AllowedModels []string
	ModelMapping  map[string]string
	Headers       map[string]string
}

type ProtocolResult struct {
	Status     string `json:"status"`
	StatusCode int    `json:"statusCode,omitempty"`
	ErrorCode  string `json:"errorCode,omitempty"`
	Retryable  bool   `json:"retryable"`
}

type Result struct {
	Platform          string         `json:"platform"`
	SelectedProtocol  string         `json:"selectedProtocol,omitempty"`
	SourceType        string         `json:"sourceType,omitempty"`
	TestModel         string         `json:"testModel,omitempty"`
	Models            []string       `json:"models"`
	ModelsStatus      string         `json:"modelsStatus"`
	Responses         ProtocolResult `json:"responses"`
	ChatCompletions   ProtocolResult `json:"chatCompletions"`
	BasicConnectivity ProtocolResult `json:"basicConnectivity"`
	ErrorCode         string         `json:"errorCode,omitempty"`
	Retryable         bool           `json:"retryable"`
}

type Service struct {
	httpClient *http.Client
	timeout    time.Duration
}

func New(client *http.Client) *Service {
	if client == nil {
		client = http.DefaultClient
	}
	return &Service{httpClient: client, timeout: 20 * time.Second}
}

func (s *Service) ProbeCandidate(ctx context.Context, input Input) (Result, error) {
	input.Platform = strings.ToLower(strings.TrimSpace(input.Platform))
	input.AuthType = strings.ToLower(strings.TrimSpace(input.AuthType))
	input.ProtocolMode = strings.ToLower(strings.TrimSpace(input.ProtocolMode))
	input.BaseURL = strings.TrimRight(strings.TrimSpace(input.BaseURL), "/")
	input.APIKey = strings.TrimSpace(input.APIKey)
	if input.ProtocolMode == "" {
		input.ProtocolMode = "auto"
	}
	if input.APIKey == "" || (input.AuthType != "" && input.AuthType != "api") {
		return Result{}, ErrInvalidProbeRequest
	}
	if input.ProtocolMode != "auto" && input.ProtocolMode != "responses" && input.ProtocolMode != "chat_completions" {
		return Result{}, ErrInvalidProbeRequest
	}
	base, err := validateBaseURL(input.BaseURL)
	if err != nil {
		return Result{}, ErrInvalidProbeRequest
	}
	rules, err := proaccountgateway.NormalizeModelRules(proaccountgateway.ModelRules{AllowedModels: input.AllowedModels, ModelMapping: input.ModelMapping})
	if err != nil {
		return Result{}, err
	}
	requestCtx, cancel := context.WithTimeout(ctx, s.timeout)
	defer cancel()
	client := s.sameOriginClient(base)
	result := Result{Platform: input.Platform, Models: []string{}, ModelsStatus: CapabilityUnknown}

	models, modelErr := s.syncModels(requestCtx, client, base, input)
	if modelErr == nil {
		result.Models = models
		result.ModelsStatus = CapabilitySupported
	} else if errors.Is(modelErr, errModelsUnsupported) {
		result.ModelsStatus = CapabilityUnsupported
	}
	testModel := chooseTestModel(input.Model, models, input.Platform)
	if !proaccountgateway.ModelAllowed(testModel, rules) {
		return Result{}, fmt.Errorf("%w: test model is outside allowed models", proaccountgateway.ErrInvalidModelRule)
	}
	testModel = proaccountgateway.ResolveMappedModel(testModel, rules)
	result.TestModel = testModel

	switch input.Platform {
	case "openai":
		return s.probeOpenAI(requestCtx, client, base, input, result)
	case "anthropic", "gemini":
		probe := s.probeBasic(requestCtx, client, base, input, testModel)
		result.BasicConnectivity = probe
		result.ErrorCode, result.Retryable = resultError(probe)
		if probe.Status == CapabilitySupported {
			if input.Platform == "anthropic" {
				result.SourceType = proaccountgateway.SourceClaudeAPIKey
			} else {
				result.SourceType = proaccountgateway.SourceGeminiAPIKey
			}
		}
		return result, nil
	default:
		return Result{}, ErrInvalidProbeRequest
	}
}

func (s *Service) probeOpenAI(ctx context.Context, client *http.Client, base *url.URL, input Input, result Result) (Result, error) {
	if input.ProtocolMode != "chat_completions" {
		result.Responses = s.probeResponses(ctx, client, base, input, result.TestModel)
		if result.Responses.Status == CapabilitySupported {
			result.SelectedProtocol = "responses"
			result.SourceType = proaccountgateway.SourceCodexAPIKey
			return result, nil
		}
		if input.ProtocolMode == "responses" || result.Responses.Status == CapabilityUnknown {
			result.ErrorCode, result.Retryable = resultError(result.Responses)
			return result, nil
		}
	}
	result.ChatCompletions = s.probeChatCompletions(ctx, client, base, input, result.TestModel)
	if result.ChatCompletions.Status == CapabilitySupported {
		result.SelectedProtocol = "chat_completions"
		result.SourceType = proaccountgateway.SourceOpenAICompatibility
		return result, nil
	}
	result.ErrorCode, result.Retryable = resultError(result.ChatCompletions)
	return result, nil
}

func (s *Service) probeResponses(ctx context.Context, client *http.Client, base *url.URL, input Input, modelName string) ProtocolResult {
	payload := map[string]any{
		"model": modelName, "input": "Call the probe_account tool.", "stream": false,
		"tools":       []map[string]any{{"type": "function", "name": "probe_account", "description": "Verify tool calling support.", "parameters": map[string]any{"type": "object", "properties": map[string]any{}, "additionalProperties": false}}},
		"tool_choice": map[string]string{"type": "function", "name": "probe_account"},
	}
	status, body, err := s.doJSON(ctx, client, endpoint(base, "/v1/responses"), bearerHeaders(input), payload)
	if err != nil {
		return ProtocolResult{Status: CapabilityUnknown, ErrorCode: "network_error", Retryable: true}
	}
	if status == http.StatusNotFound || status == http.StatusMethodNotAllowed {
		return ProtocolResult{Status: CapabilityUnsupported, StatusCode: status, ErrorCode: "protocol_not_supported"}
	}
	if status >= 200 && status < 300 {
		var value any
		if json.Unmarshal(body, &value) == nil && containsFunctionCall(value) {
			return ProtocolResult{Status: CapabilitySupported, StatusCode: status}
		}
		return ProtocolResult{Status: CapabilityUnknown, StatusCode: status, ErrorCode: "responses_tool_call_missing", Retryable: true}
	}
	code, retryable := classifyStatus(status)
	return ProtocolResult{Status: CapabilityUnknown, StatusCode: status, ErrorCode: code, Retryable: retryable}
}

func (s *Service) probeChatCompletions(ctx context.Context, client *http.Client, base *url.URL, input Input, modelName string) ProtocolResult {
	payload := map[string]any{
		"model": modelName, "messages": []map[string]string{{"role": "user", "content": "Call the probe_account tool."}}, "stream": false,
		"tools": []map[string]any{{
			"type": "function",
			"function": map[string]any{
				"name": "probe_account", "description": "Verify tool calling support.",
				"parameters": map[string]any{"type": "object", "properties": map[string]any{}},
			},
		}},
		"tool_choice": map[string]any{"type": "function", "function": map[string]string{"name": "probe_account"}},
	}
	status, _, err := s.doJSON(ctx, client, endpoint(base, "/v1/chat/completions"), bearerHeaders(input), payload)
	if err != nil {
		return ProtocolResult{Status: CapabilityUnknown, ErrorCode: "network_error", Retryable: true}
	}
	if status >= 200 && status < 300 {
		return ProtocolResult{Status: CapabilitySupported, StatusCode: status}
	}
	if status == http.StatusNotFound || status == http.StatusMethodNotAllowed {
		return ProtocolResult{Status: CapabilityUnsupported, StatusCode: status, ErrorCode: "protocol_not_supported"}
	}
	code, retryable := classifyStatus(status)
	return ProtocolResult{Status: CapabilityUnknown, StatusCode: status, ErrorCode: code, Retryable: retryable}
}

func (s *Service) probeBasic(ctx context.Context, client *http.Client, base *url.URL, input Input, modelName string) ProtocolResult {
	var target string
	var headers map[string]string
	var payload any
	if input.Platform == "anthropic" {
		target = endpoint(base, "/v1/messages")
		headers = copyHeaders(input.Headers)
		setHeader(headers, "x-api-key", input.APIKey)
		setHeader(headers, "anthropic-version", "2023-06-01")
		payload = map[string]any{"model": modelName, "max_tokens": 8, "messages": []map[string]string{{"role": "user", "content": "Reply with OK."}}}
	} else {
		target = endpoint(base, "/v1beta/models/"+url.PathEscape(modelName)+":generateContent")
		headers = copyHeaders(input.Headers)
		setHeader(headers, "x-goog-api-key", input.APIKey)
		payload = map[string]any{"contents": []map[string]any{{"parts": []map[string]string{{"text": "Reply with OK."}}}}}
	}
	status, _, err := s.doJSON(ctx, client, target, headers, payload)
	if err != nil {
		return ProtocolResult{Status: CapabilityUnknown, ErrorCode: "network_error", Retryable: true}
	}
	if status >= 200 && status < 300 {
		return ProtocolResult{Status: CapabilitySupported, StatusCode: status}
	}
	code, retryable := classifyStatus(status)
	return ProtocolResult{Status: CapabilityUnknown, StatusCode: status, ErrorCode: code, Retryable: retryable}
}

var errModelsUnsupported = errors.New("model listing is unsupported")

func (s *Service) syncModels(ctx context.Context, client *http.Client, base *url.URL, input Input) ([]string, error) {
	target := endpoint(base, "/v1/models")
	headers := bearerHeaders(input)
	if input.Platform == "anthropic" {
		headers = copyHeaders(input.Headers)
		setHeader(headers, "x-api-key", input.APIKey)
		setHeader(headers, "anthropic-version", "2023-06-01")
	}
	if input.Platform == "gemini" {
		target = endpoint(base, "/v1beta/models")
		headers = copyHeaders(input.Headers)
		setHeader(headers, "x-goog-api-key", input.APIKey)
	}
	status, body, err := s.doJSON(ctx, client, target, headers, nil)
	if err != nil {
		return nil, err
	}
	if status == http.StatusNotFound || status == http.StatusMethodNotAllowed {
		return nil, errModelsUnsupported
	}
	if status < 200 || status >= 300 {
		return nil, errors.New("model listing failed")
	}
	var payload map[string]any
	if json.Unmarshal(body, &payload) != nil {
		return nil, errors.New("model listing returned invalid json")
	}
	items, _ := payload["data"].([]any)
	if input.Platform == "gemini" {
		items, _ = payload["models"].([]any)
	}
	models := make([]string, 0, len(items))
	seen := map[string]struct{}{}
	for _, raw := range items {
		item, _ := raw.(map[string]any)
		name, _ := item["id"].(string)
		if input.Platform == "gemini" {
			name, _ = item["name"].(string)
			name = strings.TrimPrefix(name, "models/")
		}
		name = strings.TrimSpace(name)
		if name != "" {
			if _, exists := seen[name]; !exists {
				seen[name] = struct{}{}
				models = append(models, name)
			}
		}
	}
	sort.Strings(models)
	return models, nil
}

func (s *Service) doJSON(ctx context.Context, client *http.Client, target string, headers map[string]string, payload any) (int, []byte, error) {
	var body io.Reader
	if payload != nil {
		raw, err := json.Marshal(payload)
		if err != nil {
			return 0, nil, err
		}
		body = bytes.NewReader(raw)
	}
	method := http.MethodGet
	if payload != nil {
		method = http.MethodPost
	}
	req, err := http.NewRequestWithContext(ctx, method, target, body)
	if err != nil {
		return 0, nil, errors.New("create probe request failed")
	}
	for key, value := range headers {
		req.Header.Set(key, value)
	}
	if payload != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	response, err := client.Do(req)
	if err != nil {
		return 0, nil, errors.New("probe request failed")
	}
	defer response.Body.Close()
	limited := io.LimitReader(response.Body, maxProbeResponse+1)
	raw, err := io.ReadAll(limited)
	if err != nil || len(raw) > maxProbeResponse {
		return 0, nil, errors.New("probe response failed")
	}
	return response.StatusCode, raw, nil
}

func (s *Service) sameOriginClient(base *url.URL) *http.Client {
	clone := *s.httpClient
	originScheme := strings.ToLower(base.Scheme)
	originHost := strings.ToLower(base.Host)
	clone.CheckRedirect = func(req *http.Request, _ []*http.Request) error {
		if strings.ToLower(req.URL.Scheme) != originScheme || strings.ToLower(req.URL.Host) != originHost {
			return errors.New("cross-origin redirect is not allowed")
		}
		return nil
	}
	return &clone
}

func validateBaseURL(raw string) (*url.URL, error) {
	parsed, err := url.Parse(raw)
	if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Host == "" || parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" {
		return nil, ErrInvalidProbeRequest
	}
	return parsed, nil
}

func endpoint(base *url.URL, suffix string) string {
	copyURL := *base
	basePath := strings.TrimRight(copyURL.Path, "/")
	if strings.HasSuffix(basePath, "/v1") && strings.HasPrefix(suffix, "/v1/") {
		suffix = strings.TrimPrefix(suffix, "/v1")
	}
	if strings.HasSuffix(basePath, "/v1beta") && strings.HasPrefix(suffix, "/v1beta/") {
		suffix = strings.TrimPrefix(suffix, "/v1beta")
	}
	copyURL.Path = basePath + "/" + strings.TrimLeft(suffix, "/")
	return copyURL.String()
}

func bearerHeaders(input Input) map[string]string {
	headers := copyHeaders(input.Headers)
	setHeader(headers, "Authorization", "Bearer "+input.APIKey)
	return headers
}

func copyHeaders(value map[string]string) map[string]string {
	result := make(map[string]string, len(value)+2)
	for key, item := range value {
		if key = strings.TrimSpace(key); key != "" {
			result[key] = item
		}
	}
	return result
}

func setHeader(headers map[string]string, name string, value string) {
	for key := range headers {
		if strings.EqualFold(key, name) && key != name {
			delete(headers, key)
		}
	}
	headers[name] = value
}

func chooseTestModel(requested string, models []string, platform string) string {
	if requested = strings.TrimSpace(requested); requested != "" {
		return requested
	}
	if len(models) > 0 {
		return models[0]
	}
	switch platform {
	case "anthropic":
		return "claude-3-5-haiku-latest"
	case "gemini":
		return "gemini-2.0-flash"
	default:
		return "gpt-4.1-mini"
	}
}

func containsFunctionCall(value any) bool {
	switch typed := value.(type) {
	case map[string]any:
		if valueType, _ := typed["type"].(string); valueType == "function_call" {
			return true
		}
		for _, child := range typed {
			if containsFunctionCall(child) {
				return true
			}
		}
	case []any:
		for _, child := range typed {
			if containsFunctionCall(child) {
				return true
			}
		}
	}
	return false
}

func classifyStatus(status int) (string, bool) {
	switch {
	case status == http.StatusUnauthorized || status == http.StatusForbidden:
		return "authentication_failed", false
	case status == http.StatusNotFound || status == http.StatusMethodNotAllowed:
		return "protocol_not_supported", false
	case status == http.StatusTooManyRequests:
		return "rate_limited", true
	case status >= 500:
		return "upstream_unavailable", true
	default:
		return "probe_failed", false
	}
}

func resultError(result ProtocolResult) (string, bool) {
	if result.Status == CapabilitySupported {
		return "", false
	}
	return result.ErrorCode, result.Retryable
}
