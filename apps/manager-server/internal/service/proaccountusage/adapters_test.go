package proaccountusage

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/model"
	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/service/proaccountgateway"
)

type usageGatewayStub struct {
	results  map[string]proaccountgateway.APICallResult
	errors   map[string]error
	requests []proaccountgateway.APICallRequest
	snapshot proaccountgateway.AccountSnapshot
}

func (s *usageGatewayStub) APICall(_ context.Context, _ string, _ string, input proaccountgateway.APICallRequest) (proaccountgateway.APICallResult, error) {
	s.requests = append(s.requests, input)
	if err := s.errors[input.URL]; err != nil {
		return proaccountgateway.APICallResult{}, err
	}
	return s.results[input.URL], nil
}

func (s *usageGatewayStub) FindAccountByAuthIndex(context.Context, string, string, string) (proaccountgateway.AccountSnapshot, error) {
	if s.snapshot.AuthIndex == "" {
		return proaccountgateway.AccountSnapshot{}, errors.New("测试快照不存在")
	}
	return s.snapshot, nil
}

func (s *usageGatewayStub) ResolveAccountRuntime(context.Context, string, string, string, string) (proaccountgateway.AccountRuntime, error) {
	return proaccountgateway.AccountRuntime{}, nil
}

func TestParseCodexWindowsIncludesMonthlyCodeReviewAndAdditionalLimits(t *testing.T) {
	payload := map[string]any{}
	if err := json.Unmarshal([]byte(`{
		"plan_type":"free",
		"rate_limit":{"primary_window":{"used_percent":4,"limit_window_seconds":2592000,"reset_at":1786952716}},
		"code_review_rate_limit":{"primary_window":{"used_percent":12,"limit_window_seconds":18000}},
		"additional_rate_limits":[{"limit_name":"GPT-5","rate_limit":{"secondary_window":{"used_percent":25,"limit_window_seconds":604800}}}]
	}`), &payload); err != nil {
		t.Fatalf("解析测试数据失败：%v", err)
	}
	windows := parseCodexWindows(payload, time.Unix(1_700_000_000, 0), "free")
	if len(windows) != 3 {
		t.Fatalf("Codex 窗口数 = %d，期望 3：%#v", len(windows), windows)
	}
	if windows[0].ID != "monthly" || windows[0].Label != "30d" {
		t.Fatalf("月窗口 = %#v", windows[0])
	}
	if windows[1].ID != "code_review_five_hour" || windows[1].Label != "CR 5h" {
		t.Fatalf("代码审查窗口 = %#v", windows[1])
	}
	if windows[2].ID != "gpt_5_weekly" || windows[2].Label != "GPT-5 7d" {
		t.Fatalf("附加窗口 = %#v", windows[2])
	}
}

func TestEnsureReferenceUsageWindowsAddsLocalFiveHourAndWeekly(t *testing.T) {
	used := float64(4)
	windows := ensureReferenceUsageWindows(model.ProAccount{Platform: "openai", AuthType: "oauth"}, []model.ProAccountUsageWindow{{
		ID: "monthly", Label: "30d", UsedPercent: &used, Source: "official",
	}})
	if len(windows) != 3 || windows[0].ID != "five_hour" || windows[0].Source != "local" || windows[1].ID != "weekly" || windows[2].ID != "monthly" {
		t.Fatalf("补齐窗口 = %#v", windows)
	}
	if windows[0].UsedPercent != nil || windows[0].RemainingPercent != nil || windows[1].UsedPercent != nil || windows[1].RemainingPercent != nil {
		t.Fatalf("本地占位窗口不应伪造百分比：%#v", windows)
	}
}

func TestQueryOpenAIUsageMergesWhamPlanAndMonthlyWithProbeHeaders(t *testing.T) {
	gateway := &usageGatewayStub{
		results: map[string]proaccountgateway.APICallResult{
			codexUsageURL: {
				StatusCode: 200,
				Body:       `{"plan_type":"free","rate_limit":{"primary_window":{"used_percent":6,"limit_window_seconds":2592000,"reset_at":1786952716}}}`,
			},
			codexResponsesURL: {
				// sub2api 会在非 2xx 时先解析响应头；有配额头的 429 仍是有效快照。
				StatusCode: 429,
				Headers: map[string][]string{
					"X-Codex-Primary-Used-Percent":          {"6"},
					"X-Codex-Primary-Reset-At":              {"1786952716"},
					"X-Codex-Primary-Window-Minutes":        {"43200"},
					"X-Codex-Secondary-Used-Percent":        {"0"},
					"X-Codex-Secondary-Reset-After-Seconds": {"0"},
					"X-Codex-Secondary-Window-Minutes":      {"0"},
				},
			},
		},
		snapshot: proaccountgateway.AccountSnapshot{AuthIndex: "auth-openai", UpstreamAccountID: "account-upstream"},
	}
	service := &Service{gateway: gateway}
	account := model.ProAccount{
		Platform: "openai", AuthType: "oauth", AllowedModels: []string{"gpt-5.5"},
		Binding: &model.ProAccountBinding{AuthIndex: "auth-openai"},
	}
	result, err := service.queryOpenAIUsageWithSetup(context.Background(), account, gatewaySetup{baseURL: "http://gateway", managementKey: "management"})
	if err != nil {
		t.Fatalf("查询 OpenAI 用量失败：%v", err)
	}
	if result.PlanType != "free" {
		t.Fatalf("套餐 = %q，期望 free", result.PlanType)
	}
	if len(result.Windows) != 3 {
		t.Fatalf("合并窗口 = %#v，期望 5h/7d/30d", result.Windows)
	}
	byID := map[string]model.ProAccountUsageWindow{}
	for _, window := range result.Windows {
		byID[window.ID] = window
	}
	if byID["five_hour"].UsedPercent == nil || *byID["five_hour"].UsedPercent != 0 || byID["five_hour"].RemainingPercent == nil || *byID["five_hour"].RemainingPercent != 100 {
		t.Fatalf("5h 窗口 = %#v", byID["five_hour"])
	}
	if byID["weekly"].UsedPercent == nil || *byID["weekly"].UsedPercent != 6 || byID["weekly"].ResetAtMS != time.Unix(1786952716, 0).UnixMilli() {
		t.Fatalf("7d 窗口 = %#v", byID["weekly"])
	}
	if byID["monthly"].UsedPercent == nil || *byID["monthly"].UsedPercent != 6 {
		t.Fatalf("wham 月度窗口 = %#v", byID["monthly"])
	}
	if len(gateway.requests) != 2 {
		t.Fatalf("Gateway 请求数 = %d，期望 2", len(gateway.requests))
	}
	probeRequest := gateway.requests[1]
	if probeRequest.Method != "POST" || probeRequest.URL != codexResponsesURL || probeRequest.Headers["Authorization"] != "Bearer $TOKEN$" || probeRequest.Headers["Chatgpt-Account-Id"] != "account-upstream" {
		t.Fatalf("Codex 探针请求 = %#v", probeRequest)
	}
	payload, ok := probeRequest.Body.(map[string]any)
	if !ok || payload["model"] != "gpt-5.5" || payload["stream"] != true || payload["store"] != false || payload["instructions"] == "" {
		t.Fatalf("Codex 探针请求体 = %#v", probeRequest.Body)
	}
	if probeRequest.Headers["Originator"] != "codex_cli_rs" || probeRequest.Headers["Version"] != codexUsageProbeVersion || probeRequest.Headers["User-Agent"] != codexUsageProbeUserAgent {
		t.Fatalf("Codex 身份请求头 = %#v", probeRequest.Headers)
	}
}

func TestQueryOpenAIUsageFallsBackToWhamMonthlyWhenProbeHasNoQuotaHeaders(t *testing.T) {
	gateway := &usageGatewayStub{results: map[string]proaccountgateway.APICallResult{
		codexUsageURL: {
			StatusCode: 200,
			Body:       `{"plan_type":"free","rate_limit":{"primary_window":{"used_percent":4,"limit_window_seconds":2592000}}}`,
		},
		codexResponsesURL: {StatusCode: 200, Headers: map[string][]string{"Content-Type": {"text/event-stream"}}},
	}}
	service := &Service{gateway: gateway}
	account := model.ProAccount{Platform: "openai", AuthType: "oauth", Binding: &model.ProAccountBinding{AuthIndex: "auth-openai"}}
	result, err := service.queryOpenAIUsageWithSetup(context.Background(), account, gatewaySetup{baseURL: "http://gateway", managementKey: "management"})
	if err != nil {
		t.Fatalf("探针无头时不应丢弃 wham 回退：%v", err)
	}
	if result.PlanType != "free" || len(result.Windows) != 1 || result.Windows[0].ID != "monthly" || result.Windows[0].Source != "official" {
		t.Fatalf("wham 回退 = %#v", result)
	}
}

func TestQueryOpenAIUsageKeepsWhamPlanWhenProbeFails(t *testing.T) {
	gateway := &usageGatewayStub{
		results: map[string]proaccountgateway.APICallResult{
			codexUsageURL: {StatusCode: 200, Body: `{"plan_type":"pro"}`},
		},
		errors: map[string]error{codexResponsesURL: context.DeadlineExceeded},
	}
	service := &Service{gateway: gateway}
	account := model.ProAccount{Platform: "openai", AuthType: "oauth", Binding: &model.ProAccountBinding{AuthIndex: "auth-openai"}}
	result, err := service.queryOpenAIUsageWithSetup(context.Background(), account, gatewaySetup{baseURL: "http://gateway", managementKey: "management"})
	if err != nil {
		t.Fatalf("探针失败时应保留 wham 套餐：%v", err)
	}
	if result.PlanType != "pro" || len(result.Windows) != 0 {
		t.Fatalf("套餐回退 = %#v", result)
	}
	if modelName := openAIUsageProbeModel(account); modelName != codexUsageProbeDefaultModel {
		t.Fatalf("空模型目录的探针模型 = %q，期望 %q", modelName, codexUsageProbeDefaultModel)
	}
}

func TestParseCodexProbeWindowsNormalizesWindowOrderAndLegacyFallback(t *testing.T) {
	base := time.Unix(1_780_000_000, 0)
	windows, _ := parseCodexProbeWindows(map[string][]string{
		"x-codex-primary-used-percent":          {"20"},
		"x-codex-primary-window-minutes":        {"300"},
		"x-codex-secondary-used-percent":        {"80"},
		"x-codex-secondary-window-minutes":      {"10080"},
		"x-codex-secondary-reset-after-seconds": {"60"},
	}, base)
	if len(windows) != 2 || windows[0].ID != "five_hour" || windows[0].UsedPercent == nil || *windows[0].UsedPercent != 20 || windows[1].ID != "weekly" || windows[1].UsedPercent == nil || *windows[1].UsedPercent != 80 {
		t.Fatalf("反向窗口归一化 = %#v", windows)
	}
	if windows[1].ResetAtMS != base.Add(time.Minute).UnixMilli() {
		t.Fatalf("7d 重置时间 = %d", windows[1].ResetAtMS)
	}

	legacy, _ := parseCodexProbeWindows(map[string][]string{
		"x-codex-primary-used-percent":   {"70"},
		"x-codex-secondary-used-percent": {"10"},
	}, base)
	if len(legacy) != 2 || legacy[0].UsedPercent == nil || *legacy[0].UsedPercent != 10 || legacy[1].UsedPercent == nil || *legacy[1].UsedPercent != 70 {
		t.Fatalf("旧响应槽位回退 = %#v", legacy)
	}
}

func TestParseAnthropicWindowsSupportsTopLevelAndLimitsFallback(t *testing.T) {
	payload := map[string]any{}
	if err := json.Unmarshal([]byte(`{
		"five_hour":{"utilization":12,"resets_at":"2026-07-17T12:00:00Z"},
		"limits":[
			{"kind":"weekly_all","group":"weekly","percent":34,"resets_at":"2026-07-24T12:00:00Z","is_active":true},
			{"kind":"weekly_model_scoped","group":"weekly","percent":56,"resets_at":"2026-07-25T12:00:00Z","scope":{"model":{"id":"claude-sonnet-4-6","display_name":"Claude Sonnet 4.6"}}}
		]
	}`), &payload); err != nil {
		t.Fatalf("解析测试数据失败：%v", err)
	}
	windows := parseAnthropicWindows(payload)
	if len(windows) != 3 {
		t.Fatalf("Anthropic 窗口数 = %d，期望 3：%#v", len(windows), windows)
	}
	byID := map[string]int{}
	for index, window := range windows {
		byID[window.ID] = index
	}
	for _, id := range []string{"five_hour", "seven_day", "weekly_model_claude_sonnet_4_6"} {
		if _, ok := byID[id]; !ok {
			t.Fatalf("缺少 Anthropic 窗口 %q：%#v", id, windows)
		}
	}
	modelWindow := windows[byID["weekly_model_claude_sonnet_4_6"]]
	if modelWindow.UsedPercent == nil || *modelWindow.UsedPercent != 56 || modelWindow.RemainingPercent == nil || *modelWindow.RemainingPercent != 44 {
		t.Fatalf("模型窗口百分比错误：%#v", modelWindow)
	}
}

func TestResolveAnthropicPlanType(t *testing.T) {
	if plan := resolveAnthropicPlanType(map[string]any{"account": map[string]any{"has_claude_max": true}}); plan != "max" {
		t.Fatalf("Max 套餐 = %q", plan)
	}
	if plan := resolveAnthropicPlanType(map[string]any{
		"account":      map[string]any{"has_claude_max": false, "has_claude_pro": false},
		"organization": map[string]any{"organization_type": "claude_team", "subscription_status": "active"},
	}); plan != "team" {
		t.Fatalf("Team 套餐 = %q", plan)
	}
	if plan := resolveAnthropicPlanType(map[string]any{"account": map[string]any{"has_claude_max": false, "has_claude_pro": false}}); plan != "free" {
		t.Fatalf("Free 套餐 = %q", plan)
	}
}

func TestParseAntigravityWindowsReturnsPerModelQuota(t *testing.T) {
	payload := map[string]any{}
	if err := json.Unmarshal([]byte(`{
		"models":{
			"gemini-3-flash":{"displayName":"Gemini 3 Flash","quotaInfo":{"remainingFraction":0.7,"resetTime":"2026-07-17T13:00:00Z"}},
			"claude-sonnet-4-6":{"displayName":"Claude Sonnet 4.6","quotaInfo":{"remaining_fraction":"25%","reset_time":"2026-07-17T14:00:00Z"}}
		}
	}`), &payload); err != nil {
		t.Fatalf("解析测试数据失败：%v", err)
	}
	windows := parseAntigravityWindows(payload)
	if len(windows) != 2 {
		t.Fatalf("Antigravity 窗口数 = %d，期望 2：%#v", len(windows), windows)
	}
	byID := map[string]int{}
	for index, window := range windows {
		byID[window.ID] = index
	}
	gemini := windows[byID["model_gemini_3_flash"]]
	if gemini.UsedPercent == nil || *gemini.UsedPercent != 30 || gemini.RemainingPercent == nil || *gemini.RemainingPercent != 70 {
		t.Fatalf("Gemini 配额百分比错误：%#v", gemini)
	}
	wantReset := time.Date(2026, 7, 17, 13, 0, 0, 0, time.UTC).UnixMilli()
	if gemini.ResetAtMS != wantReset {
		t.Fatalf("Gemini 重置时间 = %d，期望 %d", gemini.ResetAtMS, wantReset)
	}
	claude := windows[byID["model_claude_sonnet_4_6"]]
	if claude.RemainingPercent == nil || *claude.RemainingPercent != 25 {
		t.Fatalf("Claude 剩余配额错误：%#v", claude)
	}
}

func TestParseAntigravityWindowsSupportsQuotaSummaryGroups(t *testing.T) {
	payload := map[string]any{}
	if err := json.Unmarshal([]byte(`{
		"groups":[{"displayName":"Claude/GPT","buckets":[{"bucketId":"claude-weekly","displayName":"7d","remainingFraction":0.4,"resetTime":"2026-07-24T00:00:00Z"}]}]
	}`), &payload); err != nil {
		t.Fatalf("解析测试数据失败：%v", err)
	}
	windows := parseAntigravityWindows(payload)
	if len(windows) != 1 || windows[0].ID != "claude-weekly" || windows[0].UsedPercent == nil || *windows[0].UsedPercent != 60 {
		t.Fatalf("Antigravity 分组配额错误：%#v", windows)
	}
}
