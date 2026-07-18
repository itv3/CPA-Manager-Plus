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
	ProxyURL      string
	ProtocolMode  string
	Model         string
	AllowedModels []string
	ModelMapping  map[string]string
	Headers       map[string]string
}

type ProtocolResult struct {
	Status       string `json:"status"`
	StatusCode   int    `json:"statusCode,omitempty"`
	ErrorCode    string `json:"errorCode,omitempty"`
	ErrorMessage string `json:"errorMessage,omitempty"`
	Retryable    bool   `json:"retryable"`
}

type Result struct {
	Platform          string         `json:"platform"`
	SelectedProtocol  string         `json:"selectedProtocol,omitempty"`
	SourceType        string         `json:"sourceType,omitempty"`
	TestModel         string         `json:"testModel,omitempty"`
	Models            []string       `json:"models"`
	UpstreamModels    []string       `json:"upstreamModels"`
	BuiltInModels     []string       `json:"builtInModels"`
	ManualModels      []string       `json:"manualModels"`
	ModelsStatus      string         `json:"modelsStatus"`
	Warnings          []string       `json:"warnings"`
	Responses         ProtocolResult `json:"responses"`
	ChatCompletions   ProtocolResult `json:"chatCompletions"`
	BasicConnectivity ProtocolResult `json:"basicConnectivity"`
	ErrorCode         string         `json:"errorCode,omitempty"`
	ErrorMessage      string         `json:"errorMessage,omitempty"`
	Retryable         bool           `json:"retryable"`
}

type Service struct {
	httpClient *http.Client
	timeout    time.Duration
	builtIn    BuiltInProvider
}

type BuiltInProvider interface {
	BuiltIn(ctx context.Context, platform string, authType string) ([]string, error)
}

func New(client *http.Client, providers ...BuiltInProvider) *Service {
	if client == nil {
		client = http.DefaultClient
	}
	var builtIn BuiltInProvider
	if len(providers) > 0 {
		builtIn = providers[0]
	}
	return &Service{httpClient: client, timeout: 20 * time.Second, builtIn: builtIn}
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
	client, err := s.probeClient(base, input.ProxyURL)
	if err != nil {
		return Result{}, ErrInvalidProbeRequest
	}
	result := Result{
		Platform: input.Platform, Models: []string{}, UpstreamModels: []string{}, BuiltInModels: []string{},
		ManualModels: []string{}, ModelsStatus: CapabilityUnknown, Warnings: []string{},
	}

	models, modelErr := s.syncModels(requestCtx, client, base, input)
	if modelErr == nil {
		result.UpstreamModels = models
		result.ModelsStatus = CapabilitySupported
	} else if errors.Is(modelErr, errModelsUnsupported) {
		result.ModelsStatus = CapabilityUnsupported
		result.Warnings = append(result.Warnings, "上游未提供模型列表，可使用内置目录或手工添加模型")
	} else {
		result.Warnings = append(result.Warnings, "上游模型列表同步失败，可使用内置目录或手工添加模型")
	}
	if s.builtIn != nil {
		builtIn, builtInErr := s.builtIn.BuiltIn(requestCtx, input.Platform, input.AuthType)
		if builtInErr == nil {
			result.BuiltInModels = normalizeModels(builtIn)
		} else {
			result.Warnings = append(result.Warnings, "当前 Gateway 的内置模型目录暂不可用")
		}
	}
	result.ManualModels = manualModels(input.AllowedModels, input.ModelMapping)
	result.Models = mergeModels(result.UpstreamModels, result.BuiltInModels, result.ManualModels)
	// 预探测只验证协议能力:请求的模型不在白名单内时忽略请求值并自动重选,不因此中断探测
	testModel := chooseRuleAwareTestModel(input.Model, result.Models, input.Platform, rules)
	if !proaccountgateway.ModelAllowed(testModel, rules) {
		testModel = chooseRuleAwareTestModel("", result.Models, input.Platform, rules)
	}
	testModel = proaccountgateway.ResolveMappedModel(testModel, rules)
	result.TestModel = testModel

	switch input.Platform {
	case "openai":
		return s.probeOpenAI(requestCtx, client, base, input, result)
	case "anthropic", "gemini":
		if input.Platform == "anthropic" {
			result.SourceType = proaccountgateway.SourceClaudeAPIKey
		} else {
			result.SourceType = proaccountgateway.SourceGeminiAPIKey
		}
		probe := s.probeBasic(requestCtx, client, base, input, testModel)
		result.BasicConnectivity = probe
		if probe.Status != CapabilitySupported {
			applyResultDiagnostic(&result, probe)
			result.Warnings = append(result.Warnings, protocolWarning("基础连通性", probe, "基础能力暂时无法确认，账号仍可保存为停用状态"))
		}
		return result, nil
	default:
		return Result{}, ErrInvalidProbeRequest
	}
}

func (s *Service) probeOpenAI(ctx context.Context, client *http.Client, base *url.URL, input Input, result Result) (Result, error) {
	switch input.ProtocolMode {
	case "responses":
		result.SelectedProtocol = "responses"
		result.SourceType = proaccountgateway.SourceCodexAPIKey
		result.Responses, result.TestModel = s.probeResponsesCandidates(ctx, client, base, input, result)
		if result.Responses.Status != CapabilitySupported {
			applyResultDiagnostic(&result, result.Responses)
			result.Warnings = append(result.Warnings, protocolWarning("Responses", result.Responses, "已按高级选项强制选择 Responses，建议保存为停用账号后排查"))
		}
		return result, nil
	case "chat_completions":
		result.SelectedProtocol = "chat_completions"
		result.SourceType = proaccountgateway.SourceOpenAICompatibility
		result.ChatCompletions = s.probeChatCompletions(ctx, client, base, input, result.TestModel)
		if result.ChatCompletions.Status != CapabilitySupported {
			applyResultDiagnostic(&result, result.ChatCompletions)
			result.Warnings = append(result.Warnings, protocolWarning("Chat Completions", result.ChatCompletions, "已按高级选项强制选择 Chat Completions，建议保存为停用账号后排查"))
		}
		return result, nil
	}

	result.Responses, result.TestModel = s.probeResponsesCandidates(ctx, client, base, input, result)
	switch result.Responses.Status {
	case CapabilitySupported:
		result.SelectedProtocol = "responses"
		result.SourceType = proaccountgateway.SourceCodexAPIKey
	case CapabilityUnsupported:
		result.SelectedProtocol = "chat_completions"
		result.SourceType = proaccountgateway.SourceOpenAICompatibility
		result.ChatCompletions = s.probeChatCompletions(ctx, client, base, input, result.TestModel)
		if result.ChatCompletions.Status != CapabilitySupported {
			applyResultDiagnostic(&result, result.ChatCompletions)
			result.Warnings = append(result.Warnings, protocolWarning("Chat Completions", result.ChatCompletions, "Responses 明确不支持，已自动选择 Chat Completions；建议先保存为停用账号并检查上游配置"))
		}
	default:
		result.SelectedProtocol = "responses"
		result.SourceType = proaccountgateway.SourceCodexAPIKey
		applyResultDiagnostic(&result, result.Responses)
		result.Warnings = append(result.Warnings, protocolWarning("Responses", result.Responses, "Responses 能力暂时无法确认，已按默认策略选择 Responses；保存后请执行连通性测试"))
	}
	return result, nil
}

// probeResponsesCandidates 依次使用明确配置的具体模型探测 Responses。
// 模型级 404 只能说明当前模型不支持该协议，不能据此把整个端点误判为不存在。
func (s *Service) probeResponsesCandidates(ctx context.Context, client *http.Client, base *url.URL, input Input, result Result) (ProtocolResult, string) {
	candidates := responsesProbeCandidates(input, result)
	lastResult := ProtocolResult{Status: CapabilityUnknown, ErrorCode: "probe_failed", ErrorMessage: "没有可用的 Responses 探测模型"}
	lastModel := result.TestModel
	for _, modelName := range candidates {
		lastModel = modelName
		lastResult = s.probeResponses(ctx, client, base, input, modelName)
		if lastResult.ErrorCode != "model_unavailable" {
			return lastResult, lastModel
		}
	}
	return lastResult, lastModel
}

func responsesProbeCandidates(input Input, result Result) []string {
	rules, err := proaccountgateway.NormalizeModelRules(proaccountgateway.ModelRules{
		AllowedModels: input.AllowedModels,
		ModelMapping:  input.ModelMapping,
	})
	if err != nil {
		return normalizeModels([]string{result.TestModel})
	}

	candidates := make([]string, 0, len(rules.AllowedModels)+len(rules.ModelMapping)+1)
	requested := strings.TrimSpace(input.Model)
	if requested != "" && proaccountgateway.ModelAllowed(requested, rules) {
		candidates = append(candidates, proaccountgateway.ResolveMappedModel(requested, rules))
	}

	mappingTargets := make([]string, 0, len(rules.ModelMapping))
	for _, target := range rules.ModelMapping {
		if target = strings.TrimSpace(target); target != "" {
			mappingTargets = append(mappingTargets, target)
		}
	}
	sort.Strings(mappingTargets)
	candidates = append(candidates, mappingTargets...)
	for _, modelName := range rules.AllowedModels {
		if modelName = strings.TrimSpace(modelName); modelName != "" && !strings.Contains(modelName, "*") {
			candidates = append(candidates, proaccountgateway.ResolveMappedModel(modelName, rules))
		}
	}

	// 没有明确模型规则时优先尝试常见 OpenAI/Responses 模型，避免按字典序先选到其他协议模型。
	if len(candidates) == 0 {
		catalog := result.UpstreamModels
		if len(catalog) == 0 {
			catalog = result.Models
		}
		candidates = append(candidates, prioritizeResponsesModels(catalog)...)
	}
	candidates = append(candidates, result.TestModel)
	return normalizeModels(candidates)
}

func prioritizeResponsesModels(models []string) []string {
	preferred := make([]string, 0, len(models))
	remaining := make([]string, 0, len(models))
	for _, modelName := range models {
		normalized := strings.ToLower(strings.TrimSpace(modelName))
		if strings.HasPrefix(normalized, "gpt-") || strings.HasPrefix(normalized, "o1") || strings.HasPrefix(normalized, "o3") || strings.HasPrefix(normalized, "o4") || strings.Contains(normalized, "codex") {
			preferred = append(preferred, modelName)
		} else {
			remaining = append(remaining, modelName)
		}
	}
	return append(preferred, remaining...)
}

func (s *Service) probeResponses(ctx context.Context, client *http.Client, base *url.URL, input Input, modelName string) ProtocolResult {
	payload := map[string]any{
		"model": modelName, "input": "Call the probe_account tool.", "stream": false,
		"tools":       []map[string]any{{"type": "function", "name": "probe_account", "description": "Verify tool calling support.", "parameters": map[string]any{"type": "object", "properties": map[string]any{}, "additionalProperties": false}}},
		"tool_choice": map[string]string{"type": "function", "name": "probe_account"},
	}
	status, body, err := s.doJSON(ctx, client, endpoint(base, "/v1/responses"), bearerHeaders(input), payload)
	if err != nil {
		return ProtocolResult{Status: CapabilityUnknown, ErrorCode: "network_error", ErrorMessage: "网络连接失败或请求超时", Retryable: true}
	}
	if status == http.StatusNotFound && isModelSpecificProbeFailure(body, modelName) {
		return ProtocolResult{
			Status: CapabilityUnknown, StatusCode: status, ErrorCode: "model_unavailable",
			ErrorMessage: fmt.Sprintf("探测模型 %s 不支持 Responses（HTTP %d）", modelName, status), Retryable: true,
		}
	}
	if status == http.StatusNotFound || status == http.StatusMethodNotAllowed {
		return ProtocolResult{Status: CapabilityUnsupported, StatusCode: status, ErrorCode: "protocol_not_supported", ErrorMessage: "上游未提供 Responses 协议端点"}
	}
	if status >= 200 && status < 300 {
		var value any
		if json.Unmarshal(body, &value) == nil && containsFunctionCall(value) {
			return ProtocolResult{Status: CapabilitySupported, StatusCode: status}
		}
		return ProtocolResult{Status: CapabilityUnknown, StatusCode: status, ErrorCode: "responses_tool_call_missing", ErrorMessage: "Responses 响应未包含必需的工具调用", Retryable: true}
	}
	code, retryable := classifyStatus(status)
	return ProtocolResult{Status: CapabilityUnknown, StatusCode: status, ErrorCode: code, ErrorMessage: statusMessage(status, code), Retryable: retryable}
}

func isModelSpecificProbeFailure(body []byte, modelName string) bool {
	message := strings.ToLower(strings.TrimSpace(string(body)))
	if message == "" {
		return false
	}
	modelName = strings.ToLower(strings.TrimSpace(modelName))
	mentionsModel := strings.Contains(message, "model") || strings.Contains(message, "模型")
	if modelName != "" && strings.Contains(message, modelName) {
		mentionsModel = true
	}
	if !mentionsModel {
		return false
	}
	for _, marker := range []string{
		"not found", "not support", "unsupported", "unavailable", "does not exist",
		"不支持", "不可用", "不存在", "无权", "无法访问",
	} {
		if strings.Contains(message, marker) {
			return true
		}
	}
	return false
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
		return ProtocolResult{Status: CapabilityUnknown, ErrorCode: "network_error", ErrorMessage: "网络连接失败或请求超时", Retryable: true}
	}
	if status >= 200 && status < 300 {
		return ProtocolResult{Status: CapabilitySupported, StatusCode: status}
	}
	if status == http.StatusNotFound || status == http.StatusMethodNotAllowed {
		return ProtocolResult{Status: CapabilityUnsupported, StatusCode: status, ErrorCode: "protocol_not_supported", ErrorMessage: "上游未提供 Chat Completions 协议端点"}
	}
	code, retryable := classifyStatus(status)
	return ProtocolResult{Status: CapabilityUnknown, StatusCode: status, ErrorCode: code, ErrorMessage: statusMessage(status, code), Retryable: retryable}
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
		return ProtocolResult{Status: CapabilityUnknown, ErrorCode: "network_error", ErrorMessage: "网络连接失败或请求超时", Retryable: true}
	}
	if status >= 200 && status < 300 {
		return ProtocolResult{Status: CapabilitySupported, StatusCode: status}
	}
	code, retryable := classifyStatus(status)
	return ProtocolResult{Status: CapabilityUnknown, StatusCode: status, ErrorCode: code, ErrorMessage: statusMessage(status, code), Retryable: retryable}
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

// probeClient 在同源客户端基础上按账号级代理路由探测请求,与 CLIProxyAPI 的 proxy-url 语义一致。
func (s *Service) probeClient(base *url.URL, proxyURL string) (*http.Client, error) {
	client := s.sameOriginClient(base)
	proxyURL = strings.TrimSpace(proxyURL)
	if proxyURL == "" {
		return client, nil
	}
	parsed, err := url.Parse(proxyURL)
	if err != nil || parsed.Scheme == "" || parsed.Host == "" {
		return nil, errors.New("invalid proxy url")
	}
	switch strings.ToLower(parsed.Scheme) {
	case "http", "https", "socks5", "socks5h":
	default:
		return nil, errors.New("unsupported proxy scheme")
	}
	var transport *http.Transport
	if existing, ok := client.Transport.(*http.Transport); ok && existing != nil {
		transport = existing.Clone()
	} else {
		transport = http.DefaultTransport.(*http.Transport).Clone()
	}
	transport.Proxy = http.ProxyURL(parsed)
	client.Transport = transport
	return client, nil
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

func chooseRuleAwareTestModel(requested string, models []string, platform string, rules proaccountgateway.ModelRules) string {
	if requested = strings.TrimSpace(requested); requested != "" {
		return requested
	}
	for _, modelName := range rules.AllowedModels {
		if value := strings.TrimSpace(modelName); value != "" && !strings.Contains(value, "*") {
			return value
		}
	}
	aliases := make([]string, 0, len(rules.ModelMapping))
	for alias := range rules.ModelMapping {
		if value := strings.TrimSpace(alias); value != "" && !strings.Contains(value, "*") {
			aliases = append(aliases, value)
		}
	}
	sort.Strings(aliases)
	if len(aliases) > 0 {
		return aliases[0]
	}
	return chooseTestModel("", models, platform)
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

func statusMessage(status int, code string) string {
	switch code {
	case "authentication_failed":
		return fmt.Sprintf("上游拒绝了凭证（HTTP %d）", status)
	case "rate_limited":
		return "上游正在限流，请稍后重试"
	case "upstream_unavailable":
		return fmt.Sprintf("上游服务暂时不可用（HTTP %d）", status)
	case "protocol_not_supported":
		return fmt.Sprintf("上游未提供所选协议端点（HTTP %d）", status)
	default:
		return fmt.Sprintf("上游拒绝了能力探测请求（HTTP %d）", status)
	}
}

func protocolWarning(protocol string, result ProtocolResult, guidance string) string {
	diagnostic := strings.TrimSpace(result.ErrorMessage)
	if diagnostic == "" {
		diagnostic = "能力探测未得到确定结果"
	}
	return protocol + "：" + diagnostic + "。" + guidance
}

func applyResultDiagnostic(result *Result, diagnostic ProtocolResult) {
	result.ErrorCode = diagnostic.ErrorCode
	result.ErrorMessage = diagnostic.ErrorMessage
	result.Retryable = diagnostic.Retryable
}

func manualModels(allowed []string, mapping map[string]string) []string {
	items := make([]string, 0, len(allowed)+len(mapping)*2)
	for _, item := range allowed {
		if !strings.Contains(item, "*") {
			items = append(items, item)
		}
	}
	aliases := make([]string, 0, len(mapping))
	for alias := range mapping {
		aliases = append(aliases, alias)
	}
	sort.Strings(aliases)
	for _, alias := range aliases {
		items = append(items, alias, mapping[alias])
	}
	return normalizeModels(items)
}

func normalizeModels(items []string) []string {
	return mergeModels(items)
}

func mergeModels(groups ...[]string) []string {
	result := make([]string, 0)
	seen := map[string]struct{}{}
	for _, group := range groups {
		for _, raw := range group {
			value := strings.TrimSpace(raw)
			if value == "" {
				continue
			}
			key := strings.ToLower(value)
			if _, exists := seen[key]; exists {
				continue
			}
			seen[key] = struct{}{}
			result = append(result, value)
		}
	}
	return result
}
