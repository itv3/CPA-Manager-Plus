package proaccountgateway

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestConnectivityUsesPinnedGatewayAccountTest(t *testing.T) {
	var testCount int
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v0/management/account-test" {
			http.NotFound(w, r)
			return
		}
		testCount++
		var payload struct {
			AuthIndex string `json:"auth_index"`
			Model     string `json:"model"`
			Protocol  string `json:"protocol"`
			Mode      string `json:"mode"`
		}
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			t.Errorf("解析账号测试请求失败：%v", err)
			http.Error(w, "bad request", http.StatusBadRequest)
			return
		}
		if payload.AuthIndex != "auth-antigravity" || payload.Model != "gemini-3-flash" || payload.Protocol != "generate_content" || payload.Mode != "default" {
			t.Errorf("账号测试请求错误：%#v", payload)
		}
		_, _ = w.Write([]byte(`{"success":true,"status_code":200,"protocol":"generate_content","mode":"default","model":"gemini-3-flash","upstream_model":"gemini-3-flash","duration_ms":17,"response_preview":"OK"}`))
	}))
	t.Cleanup(server.Close)

	result, err := New(server.Client()).TestAccount(context.Background(), server.URL, "management-key", AccountReference{
		Platform: "antigravity", AuthType: "oauth", SourceType: SourceAuthFile,
		SourceLocator: "antigravity.json", AuthIndex: "auth-antigravity",
	}, "gemini-3-flash")
	if err != nil {
		t.Fatalf("Antigravity 连通性测试失败：%v", err)
	}
	if !result.Success || result.Protocol != "generate_content" || result.Model != "gemini-3-flash" || result.DurationMS != 17 || result.ResponsePreview != "OK" {
		t.Fatalf("连通性结果错误：%#v", result)
	}
	if testCount != 1 {
		t.Fatalf("account-test 次数 = %d，期望 1", testCount)
	}
}

func TestOpenAICompactConnectivityUsesResponsesCompactMode(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var payload map[string]any
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			t.Fatalf("解析 Compact 请求失败：%v", err)
		}
		if payload["protocol"] != "responses" || payload["mode"] != "compact" {
			t.Fatalf("Compact 请求错误：%#v", payload)
		}
		_, _ = w.Write([]byte(`{"success":false,"status_code":429,"protocol":"responses_compact","mode":"compact","model":"gpt-5","duration_ms":9,"error_code":"rate_limited","error_message":"{\"error\":\"rate limit\"}"}`))
	}))
	t.Cleanup(server.Close)

	// Compact 仅 Responses(codex)账号可用
	result, err := New(server.Client()).TestAccountWithMode(context.Background(), server.URL, "management-key", AccountReference{
		Platform: "openai", SourceType: SourceCodexAPIKey, AuthIndex: "auth-openai",
	}, "gpt-5", ConnectivityModeCompact)
	if err != nil {
		t.Fatalf("Compact 连通性测试请求失败：%v", err)
	}
	if result.Success || result.Protocol != "responses_compact" || result.ErrorCode != "rate_limited" || result.DurationMS != 9 {
		t.Fatalf("Compact 连通性结果错误：%#v", result)
	}
	// 错误信息应被归一为友好提示,上游 JSON 原文降级为 responsePreview
	if !strings.Contains(result.ErrorMessage, "负载已达上限") {
		t.Fatalf("错误信息未归一：%q", result.ErrorMessage)
	}
	if !strings.Contains(result.ResponsePreview, "rate limit") {
		t.Fatalf("上游原文未保留到 responsePreview：%q", result.ResponsePreview)
	}
}

func TestCompactConnectivityRejectedForChatCompletionsAccount(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, _ *http.Request) {
		t.Fatal("Chat Completions 账号不应发出 Compact 请求")
	}))
	t.Cleanup(server.Close)

	_, err := New(server.Client()).TestAccountWithMode(context.Background(), server.URL, "management-key", AccountReference{
		Platform: "openai", SourceType: SourceOpenAICompatibility, AuthIndex: "auth-openai",
	}, "gpt-5", ConnectivityModeCompact)
	if err == nil {
		t.Fatal("Chat Completions 账号的 Compact 测试应被拒绝")
	}
}
