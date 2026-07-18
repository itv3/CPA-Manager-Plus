package proaccountusage

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode"

	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/model"
	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/service/proaccountgateway"
	usageheaders "github.com/seakee/cpa-manager-plus/apps/manager-server/internal/usage"
)

const (
	codexUsageURL                = "https://chatgpt.com/backend-api/wham/usage"
	codexResponsesURL            = "https://chatgpt.com/backend-api/codex/responses"
	codexUsageProbeVersion       = "0.144.1"
	codexUsageProbeUserAgent     = "codex_cli_rs/0.144.1 (Ubuntu 22.4.0; x86_64) xterm-256color"
	codexUsageProbeDefaultModel  = "gpt-5.4"
	codexUsageProbeInstructions  = "Reply briefly to confirm the connection is available."
	codexUsageProbeTimeout       = 15 * time.Second
	anthropicUsageURL            = "https://api.anthropic.com/api/oauth/usage"
	anthropicProfileURL          = "https://api.anthropic.com/api/oauth/profile"
	antigravityLoadCodeAssistURL = "https://cloudcode-pa.googleapis.com/v1internal:loadCodeAssist"
	antigravityUsageUserAgent    = "antigravity/hub/2.2.1 darwin/arm64"
)

var antigravityQuotaURLs = []string{
	"https://daily-cloudcode-pa.googleapis.com/v1internal:retrieveUserQuotaSummary",
	"https://daily-cloudcode-pa.sandbox.googleapis.com/v1internal:retrieveUserQuotaSummary",
	"https://cloudcode-pa.googleapis.com/v1internal:retrieveUserQuotaSummary",
	"https://daily-cloudcode-pa.googleapis.com/v1internal:fetchAvailableModels",
	"https://daily-cloudcode-pa.sandbox.googleapis.com/v1internal:fetchAvailableModels",
	"https://cloudcode-pa.googleapis.com/v1internal:fetchAvailableModels",
}

type officialUsageAdapter interface {
	Query(ctx context.Context, account model.ProAccount) (officialUsageResult, error)
}

type officialUsageResult struct {
	Windows  []model.ProAccountUsageWindow
	PlanType string
}

type officialUsageAdapterFunc func(ctx context.Context, account model.ProAccount) (officialUsageResult, error)

func (f officialUsageAdapterFunc) Query(ctx context.Context, account model.ProAccount) (officialUsageResult, error) {
	return f(ctx, account)
}

func windowsOnlyAdapter(query func(context.Context, model.ProAccount) ([]model.ProAccountUsageWindow, error)) officialUsageAdapterFunc {
	return func(ctx context.Context, account model.ProAccount) (officialUsageResult, error) {
		windows, err := query(ctx, account)
		return officialUsageResult{Windows: windows}, err
	}
}

type usageQueryError struct {
	message   string
	retryable bool
}

func (e *usageQueryError) Error() string {
	if e == nil || strings.TrimSpace(e.message) == "" {
		return "官方用量查询失败"
	}
	return e.message
}

func (e *usageQueryError) UsageRetryable() bool {
	return e != nil && e.retryable
}

func newOfficialUsageAdapters(service *Service) map[string]officialUsageAdapter {
	return map[string]officialUsageAdapter{
		"openai:oauth":      officialUsageAdapterFunc(service.queryOpenAIUsage),
		"anthropic:oauth":   officialUsageAdapterFunc(service.queryAnthropicUsage),
		"antigravity:oauth": windowsOnlyAdapter(service.queryAntigravityUsage),
		"xai:oauth":         windowsOnlyAdapter(service.queryXAIUsage),
	}
}

func officialUsageAdapterKey(account model.ProAccount) string {
	return strings.ToLower(strings.TrimSpace(account.Platform)) + ":" + strings.ToLower(strings.TrimSpace(account.AuthType))
}

func (s *Service) queryOpenAIUsage(ctx context.Context, account model.ProAccount) (officialUsageResult, error) {
	setup, err := s.resolveGatewaySetup(ctx)
	if err != nil {
		return officialUsageResult{}, err
	}
	return s.queryOpenAIUsageWithSetup(ctx, account, setup)
}

func (s *Service) queryOpenAIUsageWithSetup(ctx context.Context, account model.ProAccount, setup gatewaySetup) (officialUsageResult, error) {
	wham, whamErr := s.queryOpenAIWhamUsage(ctx, account, setup)
	probe, probeErr := s.queryOpenAIResponsesProbe(ctx, account, setup)
	result := officialUsageResult{
		Windows:  mergeOpenAIUsageWindows(probe.Windows, wham.Windows),
		PlanType: firstNonEmptyPlanType(probe.PlanType, wham.PlanType),
	}
	if len(result.Windows) > 0 || result.PlanType != "" {
		return result, nil
	}
	if probeErr != nil {
		return officialUsageResult{}, probeErr
	}
	if whamErr != nil {
		return officialUsageResult{}, whamErr
	}
	return officialUsageResult{}, &usageQueryError{message: "OpenAI 官方用量响应没有可识别的配额窗口", retryable: true}
}

func (s *Service) queryOpenAIWhamUsage(ctx context.Context, account model.ProAccount, setup gatewaySetup) (officialUsageResult, error) {
	result, err := s.gatewayUsageCallAllowStatusWithSetup(ctx, account, setup, proaccountgateway.APICallRequest{
		Method: http.MethodGet,
		URL:    codexUsageURL,
		Headers: map[string]string{
			"Authorization": "Bearer $TOKEN$",
			"Content-Type":  "application/json",
			"User-Agent":    "CLIProxyAPI-Pro/1.0",
		},
	})
	if err != nil {
		return officialUsageResult{}, err
	}
	if result.StatusCode < http.StatusOK || result.StatusCode >= http.StatusMultipleChoices {
		return officialUsageResult{}, statusUsageError(account.Platform, result.StatusCode)
	}
	payload, err := decodeUsageObject(result.Body)
	if err != nil {
		return officialUsageResult{}, err
	}
	planType := stringValue(payload, "plan_type", "planType")
	return officialUsageResult{Windows: parseCodexWindows(payload, time.Now(), planType), PlanType: planType}, nil
}

func (s *Service) queryOpenAIResponsesProbe(ctx context.Context, account model.ProAccount, setup gatewaySetup) (officialUsageResult, error) {
	probeCtx, cancel := context.WithTimeout(ctx, codexUsageProbeTimeout)
	defer cancel()
	headers := map[string]string{
		"Authorization": "Bearer $TOKEN$",
		"Content-Type":  "application/json",
		"Accept":        "text/event-stream",
		"Host":          "chatgpt.com",
		"OpenAI-Beta":   "responses=experimental",
		"Originator":    "codex_cli_rs",
		"Version":       codexUsageProbeVersion,
		"User-Agent":    codexUsageProbeUserAgent,
	}
	if account.Binding != nil {
		if snapshot, snapshotErr := s.gateway.FindAccountByAuthIndex(probeCtx, setup.baseURL, setup.managementKey, account.Binding.AuthIndex); snapshotErr == nil {
			if upstreamAccountID := strings.TrimSpace(snapshot.UpstreamAccountID); upstreamAccountID != "" {
				headers["Chatgpt-Account-Id"] = upstreamAccountID
			}
		}
	}
	request := proaccountgateway.APICallRequest{
		Method:  http.MethodPost,
		URL:     codexResponsesURL,
		Headers: headers,
		Body: map[string]any{
			"model":        openAIUsageProbeModel(account),
			"input":        []map[string]any{{"role": "user", "content": []map[string]any{{"type": "input_text", "text": "hi"}}}},
			"stream":       true,
			"store":        false,
			"instructions": codexUsageProbeInstructions,
		},
	}
	result, err := s.gatewayUsageCallAllowStatusWithSetup(probeCtx, account, setup, request)
	if err != nil {
		return officialUsageResult{}, err
	}
	windows, planType := parseCodexProbeWindows(result.Headers, time.Now())
	// sub2api 会优先采用响应头中的限额，即使上游同时返回 429 等非 2xx 状态。
	if len(windows) > 0 {
		return officialUsageResult{Windows: windows, PlanType: planType}, nil
	}
	if result.StatusCode < http.StatusOK || result.StatusCode >= http.StatusMultipleChoices {
		return officialUsageResult{PlanType: planType}, statusUsageError(account.Platform, result.StatusCode)
	}
	return officialUsageResult{PlanType: planType}, &usageQueryError{message: "OpenAI Codex 探针响应没有可识别的配额窗口", retryable: true}
}

func (s *Service) queryAnthropicUsage(ctx context.Context, account model.ProAccount) (officialUsageResult, error) {
	result, err := s.gatewayUsageCall(ctx, account, proaccountgateway.APICallRequest{
		Method: http.MethodGet,
		URL:    anthropicUsageURL,
		Headers: map[string]string{
			"Authorization":  "Bearer $TOKEN$",
			"Content-Type":   "application/json",
			"anthropic-beta": "oauth-2025-04-20",
		},
	})
	if err != nil {
		return officialUsageResult{}, err
	}
	payload, err := decodeUsageObject(result.Body)
	if err != nil {
		return officialUsageResult{}, err
	}
	windows := parseAnthropicWindows(payload)
	if len(windows) == 0 {
		return officialUsageResult{}, &usageQueryError{message: "Anthropic 官方用量响应没有可识别的配额窗口", retryable: true}
	}
	planType := ""
	profile, profileErr := s.gatewayUsageCallAllowStatus(ctx, account, proaccountgateway.APICallRequest{
		Method: http.MethodGet,
		URL:    anthropicProfileURL,
		Headers: map[string]string{
			"Authorization":  "Bearer $TOKEN$",
			"Content-Type":   "application/json",
			"anthropic-beta": "oauth-2025-04-20",
		},
	})
	if profileErr == nil && profile.StatusCode >= http.StatusOK && profile.StatusCode < http.StatusMultipleChoices {
		if profilePayload, decodeErr := decodeUsageObject(profile.Body); decodeErr == nil {
			planType = resolveAnthropicPlanType(profilePayload)
		}
	}
	return officialUsageResult{Windows: windows, PlanType: planType}, nil
}

func resolveAnthropicPlanType(payload map[string]any) string {
	account := mapValue(payload, "account")
	if value, ok := normalizedBool(account, "has_claude_max", "hasClaudeMax"); ok && value {
		return "max"
	}
	if value, ok := normalizedBool(account, "has_claude_pro", "hasClaudePro"); ok && value {
		return "pro"
	}
	organization := mapValue(payload, "organization")
	if strings.EqualFold(stringValue(organization, "organization_type", "organizationType"), "claude_team") &&
		strings.EqualFold(stringValue(organization, "subscription_status", "subscriptionStatus"), "active") {
		return "team"
	}
	maxValue, hasMax := normalizedBool(account, "has_claude_max", "hasClaudeMax")
	proValue, hasPro := normalizedBool(account, "has_claude_pro", "hasClaudePro")
	if hasMax && hasPro && !maxValue && !proValue {
		return "free"
	}
	return ""
}

func (s *Service) queryAntigravityUsage(ctx context.Context, account model.ProAccount) ([]model.ProAccountUsageWindow, error) {
	setup, err := s.resolveGatewaySetup(ctx)
	if err != nil {
		return nil, err
	}
	runtime, err := s.gateway.ResolveAccountRuntime(ctx, setup.baseURL, setup.managementKey, account.Binding.SourceType, account.Binding.SourceLocator)
	if err != nil {
		return nil, err
	}
	userAgent := strings.TrimSpace(runtime.UserAgent)
	if userAgent == "" {
		userAgent = antigravityUsageUserAgent
	}
	projectID := strings.TrimSpace(runtime.ProjectID)
	if projectID == "" {
		projectID, err = s.resolveAntigravityProject(ctx, account, userAgent)
		if err != nil {
			return nil, err
		}
	}
	request := proaccountgateway.APICallRequest{
		Method: http.MethodPost,
		Headers: map[string]string{
			"Authorization": "Bearer $TOKEN$",
			"Content-Type":  "application/json",
			"User-Agent":    userAgent,
		},
		Body: map[string]string{"project": projectID},
	}
	var lastStatus int
	var hadSuccess bool
	for _, targetURL := range antigravityQuotaURLs {
		request.URL = targetURL
		result, callErr := s.gatewayUsageCallAllowStatus(ctx, account, request)
		if callErr != nil {
			continue
		}
		lastStatus = result.StatusCode
		if result.StatusCode < http.StatusOK || result.StatusCode >= http.StatusMultipleChoices {
			continue
		}
		hadSuccess = true
		payload, decodeErr := decodeUsageObject(result.Body)
		if decodeErr != nil {
			continue
		}
		if windows := parseAntigravityWindows(payload); len(windows) > 0 {
			return windows, nil
		}
	}
	if hadSuccess {
		return nil, &usageQueryError{message: "Antigravity 官方用量响应没有可识别的模型配额", retryable: true}
	}
	if lastStatus > 0 {
		return nil, statusUsageError("Antigravity", lastStatus)
	}
	return nil, &usageQueryError{message: "Antigravity 官方用量请求失败", retryable: true}
}

func (s *Service) resolveAntigravityProject(ctx context.Context, account model.ProAccount, userAgent string) (string, error) {
	result, err := s.gatewayUsageCall(ctx, account, proaccountgateway.APICallRequest{
		Method: http.MethodPost,
		URL:    antigravityLoadCodeAssistURL,
		Headers: map[string]string{
			"Authorization": "Bearer $TOKEN$",
			"Accept":        "*/*",
			"Content-Type":  "application/json",
			"User-Agent":    userAgent,
		},
		Body: map[string]any{"metadata": map[string]string{"ideType": "ANTIGRAVITY"}},
	})
	if err != nil {
		return "", err
	}
	payload, err := decodeUsageObject(result.Body)
	if err != nil {
		return "", err
	}
	for _, key := range []string{"cloudaicompanionProject", "projectId", "project"} {
		switch value := payload[key].(type) {
		case string:
			if value = strings.TrimSpace(value); value != "" {
				return value, nil
			}
		case map[string]any:
			if id := stringValue(value, "id"); id != "" {
				return id, nil
			}
		}
	}
	return "", &usageQueryError{message: "Antigravity 账号缺少可用的项目标识", retryable: false}
}

func (s *Service) queryXAIUsage(ctx context.Context, account model.ProAccount) ([]model.ProAccountUsageWindow, error) {
	if s.xaiUsage == nil {
		return nil, &usageQueryError{message: "xAI 官方用量 Adapter 未初始化", retryable: false}
	}
	windows, _, err := s.xaiUsage.QueryXAIUsage(ctx, account.Binding.AuthIndex)
	if err != nil {
		return nil, err
	}
	result := make([]model.ProAccountUsageWindow, 0, len(windows))
	for _, window := range windows {
		label := xaiWindowLabel(window)
		item := model.ProAccountUsageWindow{
			ID:        strings.TrimSpace(window.ID),
			Label:     label,
			ResetAtMS: parseUsageTimeMS(window.ResetLabel),
			Source:    "official",
		}
		if window.UsedPercent != nil {
			used := clampPercent(*window.UsedPercent)
			remaining := clampPercent(100 - used)
			item.UsedPercent = &used
			item.RemainingPercent = &remaining
		}
		result = append(result, item)
	}
	if len(result) == 0 {
		return nil, &usageQueryError{message: "xAI 官方用量响应没有可识别的配额窗口", retryable: true}
	}
	return result, nil
}

type gatewaySetup struct {
	baseURL       string
	managementKey string
}

func (s *Service) resolveGatewaySetup(ctx context.Context) (gatewaySetup, error) {
	setup, _, ok, err := s.managerConfig.ResolveSetupWithSource(ctx)
	if err != nil {
		return gatewaySetup{}, err
	}
	if !ok || strings.TrimSpace(setup.CPAUpstreamURL) == "" || strings.TrimSpace(setup.ManagementKey) == "" {
		return gatewaySetup{}, &usageQueryError{message: "官方用量服务尚未配置 Gateway 连接", retryable: false}
	}
	return gatewaySetup{baseURL: setup.CPAUpstreamURL, managementKey: setup.ManagementKey}, nil
}

func (s *Service) gatewayUsageCall(ctx context.Context, account model.ProAccount, request proaccountgateway.APICallRequest) (proaccountgateway.APICallResult, error) {
	result, err := s.gatewayUsageCallAllowStatus(ctx, account, request)
	if err != nil {
		return proaccountgateway.APICallResult{}, err
	}
	if result.StatusCode < http.StatusOK || result.StatusCode >= http.StatusMultipleChoices {
		return proaccountgateway.APICallResult{}, statusUsageError(account.Platform, result.StatusCode)
	}
	return result, nil
}

func (s *Service) gatewayUsageCallAllowStatus(ctx context.Context, account model.ProAccount, request proaccountgateway.APICallRequest) (proaccountgateway.APICallResult, error) {
	setup, err := s.resolveGatewaySetup(ctx)
	if err != nil {
		return proaccountgateway.APICallResult{}, err
	}
	return s.gatewayUsageCallAllowStatusWithSetup(ctx, account, setup, request)
}

func (s *Service) gatewayUsageCallAllowStatusWithSetup(ctx context.Context, account model.ProAccount, setup gatewaySetup, request proaccountgateway.APICallRequest) (proaccountgateway.APICallResult, error) {
	if account.Binding == nil || strings.TrimSpace(account.Binding.AuthIndex) == "" {
		return proaccountgateway.APICallResult{}, &usageQueryError{message: "账号缺少可用的 Gateway 运行时绑定", retryable: false}
	}
	request.AuthIndex = account.Binding.AuthIndex
	return s.gateway.APICall(ctx, setup.baseURL, setup.managementKey, request)
}

func statusUsageError(platform string, statusCode int) error {
	retryable := statusCode == http.StatusTooManyRequests || statusCode >= http.StatusInternalServerError
	return &usageQueryError{
		message:   fmt.Sprintf("%s 官方用量接口返回 HTTP %d", strings.TrimSpace(platform), statusCode),
		retryable: retryable,
	}
}

func safeUsageError(err error) string {
	message := strings.TrimSpace(err.Error())
	message = strings.Join(strings.Fields(message), " ")
	if len(message) > 300 {
		message = message[:300]
	}
	if message == "" {
		return "官方用量查询失败"
	}
	return message
}

func usageErrorRetryable(err error) bool {
	if err == nil || errors.Is(err, context.Canceled) {
		return false
	}
	var classified interface{ UsageRetryable() bool }
	if errors.As(err, &classified) {
		return classified.UsageRetryable()
	}
	var gatewayErr *proaccountgateway.GatewayError
	if errors.As(err, &gatewayErr) {
		return gatewayErr.Retryable
	}
	return true
}

func decodeUsageObject(raw string) (map[string]any, error) {
	decoder := json.NewDecoder(strings.NewReader(raw))
	decoder.UseNumber()
	payload := map[string]any{}
	if err := decoder.Decode(&payload); err != nil {
		return nil, &usageQueryError{message: "官方用量响应不是有效的 JSON 对象", retryable: true}
	}
	return payload, nil
}

func openAIUsageProbeModel(account model.ProAccount) string {
	// sub2api 默认使用 gpt-5.4；但 Gateway 当前账号可能只开放更新的模型。
	// 线上 Free 账号已验证：不在 allowedModels 中的 gpt-5.4 会返回 400 且没有配额头，
	// 因此优先使用账号已确认可用的首个非通配模型，仅在目录为空时回退 sub2api 默认值。
	for _, raw := range account.AllowedModels {
		candidate := strings.TrimSpace(raw)
		if candidate == "" || strings.Contains(candidate, "*") {
			continue
		}
		if mapped := strings.TrimSpace(account.ModelMapping[candidate]); mapped != "" && !strings.Contains(mapped, "*") {
			return mapped
		}
		return candidate
	}
	return codexUsageProbeDefaultModel
}

func mergeOpenAIUsageWindows(probeWindows []model.ProAccountUsageWindow, whamWindows []model.ProAccountUsageWindow) []model.ProAccountUsageWindow {
	result := make([]model.ProAccountUsageWindow, 0, len(probeWindows)+len(whamWindows))
	seen := make(map[string]struct{}, len(probeWindows)+len(whamWindows))
	appendUnique := func(windows []model.ProAccountUsageWindow) {
		for _, window := range windows {
			id := strings.ToLower(strings.TrimSpace(window.ID))
			if id == "" {
				id = strings.ToLower(strings.TrimSpace(window.Label))
			}
			if _, exists := seen[id]; exists {
				continue
			}
			seen[id] = struct{}{}
			result = append(result, window)
		}
	}
	// 响应头是 sub2api 对普通 OpenAI OAuth 账号的 5h/7d 权威来源。
	appendUnique(probeWindows)
	// wham 仍保留套餐、月度及附加窗口，并在探针失败时作为官方回退。
	appendUnique(whamWindows)
	return result
}

func parseCodexProbeWindows(headers map[string][]string, base time.Time) ([]model.ProAccountUsageWindow, string) {
	rawHeaders := make(map[string]any, len(headers))
	for key, values := range headers {
		rawHeaders[key] = values
	}
	metadata := usageheaders.ParseResponseHeaderMetadata(rawHeaders, base)
	if metadata == nil || metadata.Quota == nil {
		return nil, ""
	}
	quota := metadata.Quota
	fiveHour, weekly := normalizeCodexProbeQuota(quota.Primary, quota.Secondary)
	windows := make([]model.ProAccountUsageWindow, 0, 2)
	if fiveHour != nil {
		windows = append(windows, codexProbeWindow("five_hour", "5h", fiveHour, base))
	}
	if weekly != nil {
		windows = append(windows, codexProbeWindow("weekly", "7d", weekly, base))
	}
	return windows, firstNonEmptyPlanType(quota.PlanType)
}

func normalizeCodexProbeQuota(primary *usageheaders.HeaderQuotaWindow, secondary *usageheaders.HeaderQuotaWindow) (fiveHour *usageheaders.HeaderQuotaWindow, weekly *usageheaders.HeaderQuotaWindow) {
	primaryMinutes, hasPrimaryMinutes := codexProbeWindowMinutes(primary)
	secondaryMinutes, hasSecondaryMinutes := codexProbeWindowMinutes(secondary)
	useFiveHourFromPrimary := false
	useWeeklyFromPrimary := false
	switch {
	case hasPrimaryMinutes && hasSecondaryMinutes:
		useFiveHourFromPrimary = primaryMinutes < secondaryMinutes
		useWeeklyFromPrimary = !useFiveHourFromPrimary
	case hasPrimaryMinutes:
		useFiveHourFromPrimary = primaryMinutes <= 360
		useWeeklyFromPrimary = !useFiveHourFromPrimary
	case hasSecondaryMinutes:
		// 与 sub2api 保持一致：仅 secondary 有窗口长度时，据此推断 primary 的槽位。
		useWeeklyFromPrimary = secondaryMinutes <= 360
		useFiveHourFromPrimary = !useWeeklyFromPrimary
	default:
		// 旧响应没有 window-minutes 时沿用 primary=7d、secondary=5h。
		useWeeklyFromPrimary = true
	}
	if useFiveHourFromPrimary {
		return primary, secondary
	}
	if useWeeklyFromPrimary {
		return secondary, primary
	}
	return nil, nil
}

func codexProbeWindowMinutes(window *usageheaders.HeaderQuotaWindow) (float64, bool) {
	if window == nil || window.WindowMinutes == nil {
		return 0, false
	}
	return *window.WindowMinutes, true
}

func codexProbeWindow(id string, label string, window *usageheaders.HeaderQuotaWindow, base time.Time) model.ProAccountUsageWindow {
	result := model.ProAccountUsageWindow{ID: id, Label: label, Source: "official"}
	if window == nil {
		return result
	}
	applyUsedPercent(&result, window.UsedPercent, window.UsedPercent != nil)
	result.ResetAtMS = window.ResetAtMS
	if result.ResetAtMS == 0 && window.ResetAfterSeconds != nil && !base.IsZero() {
		seconds := *window.ResetAfterSeconds
		if seconds < 0 {
			seconds = 0
		}
		result.ResetAtMS = base.Add(time.Duration(seconds * float64(time.Second))).UnixMilli()
	}
	return result
}

func parseCodexWindows(payload map[string]any, now time.Time, planType string) []model.ProAccountUsageWindow {
	windows := make([]model.ProAccountUsageWindow, 0, 8)
	appendCodexRateLimitWindows(&windows, mapValue(payload, "rate_limit", "rateLimit"), "", "", now, planType)
	appendCodexRateLimitWindows(&windows, mapValue(payload, "code_review_rate_limit", "codeReviewRateLimit"), "code_review_", "CR ", now, planType)
	for index, raw := range sliceValue(payload, "additional_rate_limits", "additionalRateLimits") {
		additional, ok := raw.(map[string]any)
		if !ok {
			continue
		}
		name := stringValue(additional, "limit_name", "limitName", "metered_feature", "meteredFeature")
		if name == "" {
			name = fmt.Sprintf("额外 %d", index+1)
		}
		idPrefix := stableUsageID(name)
		if idPrefix == "" {
			idPrefix = fmt.Sprintf("additional_%d", index+1)
		}
		appendCodexRateLimitWindows(&windows, mapValue(additional, "rate_limit", "rateLimit"), idPrefix+"_", name+" ", now, planType)
	}
	return windows
}

func appendCodexRateLimitWindows(windows *[]model.ProAccountUsageWindow, rateLimit map[string]any, idPrefix string, labelPrefix string, now time.Time, planType string) {
	if len(rateLimit) == 0 {
		return
	}
	for index, key := range []string{"primary_window", "secondary_window"} {
		window := mapValue(rateLimit, key)
		if len(window) == 0 {
			camel := "primaryWindow"
			if index == 1 {
				camel = "secondaryWindow"
			}
			window = mapValue(rateLimit, camel)
		}
		if len(window) == 0 {
			continue
		}
		used, hasUsed := numberPointer(window, "used_percent", "usedPercent")
		resetAt := int64(0)
		if raw, ok := numberPointer(window, "reset_at", "resetAt"); ok {
			resetAt = normalizeUnixTimeMS(*raw)
		} else if raw, ok := numberPointer(window, "reset_after_seconds", "resetAfterSeconds"); ok {
			resetAt = now.Add(time.Duration(*raw * float64(time.Second))).UnixMilli()
		}
		label := "5h"
		id := "five_hour"
		if seconds, ok := numberPointer(window, "limit_window_seconds", "limitWindowSeconds"); ok {
			switch {
			case *seconds >= 28*24*60*60:
				label, id = "30d", "monthly"
			case *seconds >= 6*24*60*60:
				label, id = "7d", "weekly"
			case *seconds >= 4*60*60 && *seconds <= 6*60*60:
				label, id = "5h", "five_hour"
			default:
				label, id = fmt.Sprintf("%.0fh", *seconds/3600), fmt.Sprintf("window_%d", index+1)
			}
		} else if index == 1 {
			if strings.EqualFold(strings.TrimSpace(planType), "team") {
				label, id = "30d", "monthly"
			} else {
				label, id = "7d", "weekly"
			}
		}
		item := model.ProAccountUsageWindow{ID: idPrefix + id, Label: labelPrefix + label, ResetAtMS: resetAt, Source: "official"}
		applyUsedPercent(&item, used, hasUsed)
		*windows = append(*windows, item)
	}
}

func parseAnthropicWindows(payload map[string]any) []model.ProAccountUsageWindow {
	definitions := []struct {
		key   string
		id    string
		label string
	}{
		{key: "five_hour", id: "five_hour", label: "5h"},
		{key: "seven_day", id: "seven_day", label: "7d"},
		{key: "seven_day_oauth_apps", id: "seven_day_oauth_apps", label: "7d OAuth"},
		{key: "seven_day_opus", id: "seven_day_opus", label: "7d Opus"},
		{key: "seven_day_sonnet", id: "seven_day_sonnet", label: "7d Sonnet"},
		{key: "seven_day_cowork", id: "seven_day_cowork", label: "7d Cowork"},
		{key: "iguana_necktie", id: "iguana_necktie", label: "Iguana"},
	}
	windows := make([]model.ProAccountUsageWindow, 0, len(definitions))
	seen := map[string]int{}
	for _, definition := range definitions {
		window := mapValue(payload, definition.key)
		if len(window) == 0 {
			continue
		}
		used, hasUsed := numberPointer(window, "utilization", "used_percent", "usedPercent")
		resetAt := parseUsageTimeMS(firstValue(window, "resets_at", "resetsAt", "reset_at", "resetAt"))
		if !hasUsed && resetAt == 0 {
			continue
		}
		item := model.ProAccountUsageWindow{ID: definition.id, Label: definition.label, ResetAtMS: resetAt, Source: "official"}
		applyUsedPercent(&item, used, hasUsed)
		seen[item.ID] = len(windows)
		windows = append(windows, item)
	}
	for index, raw := range sliceValue(payload, "limits") {
		limit, ok := raw.(map[string]any)
		if !ok || explicitFalse(limit, "is_active", "isActive") {
			continue
		}
		used, hasUsed := numberPointer(limit, "percent", "utilization", "used_percent", "usedPercent")
		resetAt := parseUsageTimeMS(firstValue(limit, "resets_at", "resetsAt", "reset_at", "resetAt"))
		if !hasUsed && resetAt == 0 {
			continue
		}
		id, label := anthropicLimitIdentity(limit, index)
		if id == "" {
			continue
		}
		item := model.ProAccountUsageWindow{ID: id, Label: label, ResetAtMS: resetAt, Source: "official"}
		applyUsedPercent(&item, used, hasUsed)
		if existing, exists := seen[id]; exists {
			if windows[existing].ResetAtMS == 0 || item.ResetAtMS > windows[existing].ResetAtMS {
				windows[existing] = item
			}
			continue
		}
		seen[id] = len(windows)
		windows = append(windows, item)
	}
	return windows
}

func anthropicLimitIdentity(limit map[string]any, index int) (string, string) {
	kind := normalizeToken(stringValue(limit, "kind"))
	group := normalizeToken(stringValue(limit, "group"))
	scope := mapValue(limit, "scope")
	if len(scope) == 0 {
		switch {
		case kind == "session" && (group == "" || group == "session"):
			return "five_hour", "5h"
		case (kind == "weekly" || kind == "weekly_all") && (group == "" || group == "weekly" || group == "weekly_all"):
			return "seven_day", "7d"
		default:
			return "", ""
		}
	}
	modelScope := mapValue(scope, "model")
	if len(modelScope) == 0 || !strings.Contains(kind+"_"+group, "weekly") {
		return "", ""
	}
	modelID := stringValue(modelScope, "id", "model_id", "modelId")
	label := stringValue(modelScope, "display_name", "displayName")
	if label == "" {
		label = modelID
	}
	if label == "" {
		return "", ""
	}
	idPart := stableUsageID(modelID)
	if idPart == "" {
		idPart = stableUsageID(label)
	}
	if idPart == "" {
		idPart = strconv.Itoa(index + 1)
	}
	return "weekly_model_" + idPart, label
}

func parseAntigravityWindows(payload map[string]any) []model.ProAccountUsageWindow {
	windows := make([]model.ProAccountUsageWindow, 0)
	seen := map[string]struct{}{}
	for groupIndex, rawGroup := range sliceValue(payload, "groups") {
		group, ok := rawGroup.(map[string]any)
		if !ok {
			continue
		}
		groupLabel := stringValue(group, "displayName", "display_name")
		if groupLabel == "" {
			groupLabel = fmt.Sprintf("Quota Group %d", groupIndex+1)
		}
		for bucketIndex, rawBucket := range sliceValue(group, "buckets") {
			bucket, ok := rawBucket.(map[string]any)
			if !ok {
				continue
			}
			remaining, ok := remainingFraction(bucket, "remainingFraction", "remaining_fraction")
			if !ok {
				continue
			}
			id := stringValue(bucket, "bucketId", "bucket_id")
			if id == "" {
				id = fmt.Sprintf("group_%d_bucket_%d", groupIndex+1, bucketIndex+1)
			}
			label := stringValue(bucket, "displayName", "display_name")
			if label == "" {
				label = groupLabel
			} else if !strings.EqualFold(label, groupLabel) {
				label = groupLabel + " · " + label
			}
			appendRemainingWindow(&windows, seen, id, label, remaining, firstValue(bucket, "resetTime", "reset_time"))
		}
	}
	models := mapValue(payload, "models")
	modelIDs := make([]string, 0, len(models))
	for modelID := range models {
		modelIDs = append(modelIDs, modelID)
	}
	sort.Strings(modelIDs)
	for _, modelID := range modelIDs {
		entry, ok := models[modelID].(map[string]any)
		if !ok {
			continue
		}
		quota := mapValue(entry, "quotaInfo", "quota_info")
		remaining, ok := remainingFraction(quota, "remainingFraction", "remaining_fraction", "remaining")
		if !ok {
			continue
		}
		label := stringValue(entry, "displayName", "display_name")
		if label == "" {
			label = modelID
		}
		appendRemainingWindow(&windows, seen, "model_"+stableUsageID(modelID), label, remaining, firstValue(quota, "resetTime", "reset_time"))
	}
	return windows
}

func appendRemainingWindow(windows *[]model.ProAccountUsageWindow, seen map[string]struct{}, id string, label string, remaining float64, reset any) {
	id = strings.TrimSpace(id)
	if id == "" {
		return
	}
	if _, exists := seen[id]; exists {
		return
	}
	seen[id] = struct{}{}
	remainingPercent := clampPercent(remaining * 100)
	usedPercent := clampPercent(100 - remainingPercent)
	*windows = append(*windows, model.ProAccountUsageWindow{
		ID: id, Label: label, UsedPercent: &usedPercent, RemainingPercent: &remainingPercent,
		ResetAtMS: parseUsageTimeMS(reset), Source: "official",
	})
}

func xaiWindowLabel(window model.CodexInspectionQuotaWindow) string {
	switch window.LabelKey {
	case "xai_quota.weekly_limit":
		return "7d"
	case "xai_quota.monthly_limit":
		return "30d"
	case "xai_quota.on_demand_cap":
		return "按需"
	case "xai_quota.product_usage":
		if product := strings.TrimSpace(fmt.Sprint(window.LabelParams["product"])); product != "" && product != "<nil>" {
			return product
		}
	}
	if label := strings.TrimSpace(window.LabelKey); label != "" {
		return label
	}
	return strings.TrimSpace(window.ID)
}

func mapValue(raw map[string]any, keys ...string) map[string]any {
	for _, key := range keys {
		if value, ok := raw[key].(map[string]any); ok {
			return value
		}
	}
	return nil
}

func sliceValue(raw map[string]any, keys ...string) []any {
	for _, key := range keys {
		if value, ok := raw[key].([]any); ok {
			return value
		}
	}
	return nil
}

func firstValue(raw map[string]any, keys ...string) any {
	for _, key := range keys {
		if value, ok := raw[key]; ok {
			return value
		}
	}
	return nil
}

func stringValue(raw map[string]any, keys ...string) string {
	for _, key := range keys {
		if value, ok := raw[key]; ok {
			switch typed := value.(type) {
			case string:
				if typed = strings.TrimSpace(typed); typed != "" {
					return typed
				}
			case json.Number:
				return typed.String()
			}
		}
	}
	return ""
}

func numberPointer(raw map[string]any, keys ...string) (*float64, bool) {
	for _, key := range keys {
		if value, ok := raw[key]; ok {
			if number, valid := numberValue(value); valid {
				return &number, true
			}
		}
	}
	return nil, false
}

func numberValue(value any) (float64, bool) {
	switch typed := value.(type) {
	case float64:
		return typed, true
	case float32:
		return float64(typed), true
	case json.Number:
		parsed, err := typed.Float64()
		return parsed, err == nil
	case int:
		return float64(typed), true
	case int64:
		return float64(typed), true
	case string:
		parsed, err := strconv.ParseFloat(strings.TrimSpace(typed), 64)
		return parsed, err == nil
	default:
		return 0, false
	}
}

func remainingFraction(raw map[string]any, keys ...string) (float64, bool) {
	for _, key := range keys {
		value, exists := raw[key]
		if !exists {
			continue
		}
		if text, ok := value.(string); ok && strings.HasSuffix(strings.TrimSpace(text), "%") {
			parsed, err := strconv.ParseFloat(strings.TrimSpace(strings.TrimSuffix(strings.TrimSpace(text), "%")), 64)
			if err == nil {
				return clampFraction(parsed / 100), true
			}
		}
		parsed, ok := numberValue(value)
		if !ok {
			continue
		}
		if parsed > 1 && parsed <= 100 {
			parsed /= 100
		}
		return clampFraction(parsed), true
	}
	return 0, false
}

func explicitFalse(raw map[string]any, keys ...string) bool {
	for _, key := range keys {
		value, exists := raw[key]
		if !exists {
			continue
		}
		switch typed := value.(type) {
		case bool:
			return !typed
		case string:
			parsed, err := strconv.ParseBool(strings.TrimSpace(typed))
			return err == nil && !parsed
		}
	}
	return false
}

func normalizedBool(raw map[string]any, keys ...string) (bool, bool) {
	for _, key := range keys {
		value, exists := raw[key]
		if !exists || value == nil {
			continue
		}
		switch typed := value.(type) {
		case bool:
			return typed, true
		case json.Number:
			parsed, err := typed.Int64()
			if err == nil {
				return parsed != 0, true
			}
		case float64:
			return typed != 0, true
		case string:
			normalized := strings.ToLower(strings.TrimSpace(typed))
			switch normalized {
			case "true", "1", "yes", "y", "on":
				return true, true
			case "false", "0", "no", "n", "off":
				return false, true
			}
		}
	}
	return false, false
}

func applyUsedPercent(window *model.ProAccountUsageWindow, used *float64, ok bool) {
	if !ok || used == nil {
		return
	}
	usedPercent := clampPercent(*used)
	remainingPercent := clampPercent(100 - usedPercent)
	window.UsedPercent = &usedPercent
	window.RemainingPercent = &remainingPercent
}

func parseUsageTimeMS(value any) int64 {
	if value == nil {
		return 0
	}
	if number, ok := numberValue(value); ok {
		return normalizeUnixTimeMS(number)
	}
	text, ok := value.(string)
	if !ok {
		return 0
	}
	text = strings.TrimSpace(text)
	for _, layout := range []string{time.RFC3339Nano, time.RFC3339} {
		if parsed, err := time.Parse(layout, text); err == nil {
			return parsed.UnixMilli()
		}
	}
	return 0
}

func normalizeUnixTimeMS(value float64) int64 {
	if value <= 0 {
		return 0
	}
	if value < 10_000_000_000 {
		return int64(value * 1000)
	}
	return int64(value)
}

func clampPercent(value float64) float64 {
	if value < 0 {
		return 0
	}
	if value > 100 {
		return 100
	}
	return value
}

func clampFraction(value float64) float64 {
	if value < 0 {
		return 0
	}
	if value > 1 {
		return 1
	}
	return value
}

func normalizeToken(value string) string {
	var result strings.Builder
	for index, current := range strings.TrimSpace(value) {
		if unicode.IsUpper(current) && index > 0 {
			result.WriteByte('_')
		}
		if unicode.IsLetter(current) || unicode.IsDigit(current) {
			result.WriteRune(unicode.ToLower(current))
		} else if result.Len() > 0 && !strings.HasSuffix(result.String(), "_") {
			result.WriteByte('_')
		}
	}
	return strings.Trim(result.String(), "_")
}

func stableUsageID(value string) string {
	var result strings.Builder
	for _, current := range strings.ToLower(strings.TrimSpace(value)) {
		if (current >= 'a' && current <= 'z') || (current >= '0' && current <= '9') {
			result.WriteRune(current)
		} else if result.Len() > 0 {
			text := result.String()
			if text[len(text)-1] != '_' {
				result.WriteByte('_')
			}
		}
	}
	return strings.Trim(result.String(), "_")
}
