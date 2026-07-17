package proaccountusage

import (
	"encoding/json"
	"testing"
	"time"
)

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
