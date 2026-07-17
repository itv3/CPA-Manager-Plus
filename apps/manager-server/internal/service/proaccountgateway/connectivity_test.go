package proaccountgateway

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestAntigravityConnectivityUsesRuntimeProjectAndGatewayAPICall(t *testing.T) {
	var apiCallCount int
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v0/management/auth-files":
			_, _ = w.Write([]byte(`{"files":[{"name":"antigravity.json","provider":"antigravity","auth_index":"auth-antigravity","metadata":{"project_id":"project-alpha","user_agent":"antigravity/hub/9.9.9 linux/amd64"}}]}`))
		case "/v0/management/api-call":
			apiCallCount++
			var payload struct {
				AuthIndex string            `json:"authIndex"`
				Method    string            `json:"method"`
				URL       string            `json:"url"`
				Header    map[string]string `json:"header"`
				Data      string            `json:"data"`
			}
			if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
				t.Errorf("解析 api-call 请求失败：%v", err)
				http.Error(w, "bad request", http.StatusBadRequest)
				return
			}
			if payload.AuthIndex != "auth-antigravity" || payload.Method != http.MethodPost || payload.URL != "https://daily-cloudcode-pa.googleapis.com/v1internal:generateContent" {
				t.Errorf("api-call 请求错误：%#v", payload)
			}
			if payload.Header["Authorization"] != "Bearer $TOKEN$" || payload.Header["User-Agent"] != "antigravity/hub/9.9.9 linux/amd64" {
				t.Errorf("api-call 请求头错误：%#v", payload.Header)
			}
			var body map[string]any
			if err := json.Unmarshal([]byte(payload.Data), &body); err != nil {
				t.Errorf("解析 Antigravity 请求体失败：%v", err)
			} else if body["project"] != "project-alpha" || body["model"] != "gemini-3-flash" {
				t.Errorf("Antigravity 请求体错误：%#v", body)
			}
			_, _ = w.Write([]byte(`{"status_code":200,"body":"{\"response\":{\"candidates\":[]}}"}`))
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(server.Close)

	result, err := New(server.Client()).TestAccount(context.Background(), server.URL, "management-key", AccountReference{
		Platform: "antigravity", AuthType: "oauth", SourceType: SourceAuthFile,
		SourceLocator: "antigravity.json", AuthIndex: "auth-antigravity",
	}, "gemini-3-flash")
	if err != nil {
		t.Fatalf("Antigravity 连通性测试失败：%v", err)
	}
	if !result.Success || result.Protocol != "generate_content" || result.Model != "gemini-3-flash" {
		t.Fatalf("连通性结果错误：%#v", result)
	}
	if apiCallCount != 1 {
		t.Fatalf("api-call 次数 = %d，期望 1", apiCallCount)
	}
}

func TestAntigravityConnectivityRejectsMissingProject(t *testing.T) {
	_, _, err := connectivityRequest(AccountRuntime{
		Platform: "antigravity",
		BaseURL:  "https://daily-cloudcode-pa.googleapis.com",
	}, AccountReference{AuthIndex: "auth-antigravity"}, "gemini-3-flash")
	if err == nil {
		t.Fatal("缺少 project_id 时不应构造 Antigravity 连通性请求")
	}
}
