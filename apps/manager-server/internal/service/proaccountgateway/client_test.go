package proaccountgateway

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync"
	"testing"
)

func TestSnapshotIncludesConfigAccountsAndCapabilities(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer management-key" {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		switch r.URL.Path {
		case "/v0/management/auth-files":
			w.Header().Set("X-CPA-SUPPORT-CREDENTIAL-DRAFT", "true")
			w.Header().Set("X-CPA-SUPPORT-ALLOWED-MODELS", "true")
			_, _ = w.Write([]byte(`{"files":[{"name":"codex.json","provider":"codex","auth_index":"auth-oauth","id_token":{"plan_type":"free"},"allowed_models":["gpt-oauth"],"model_rule_version":"rule-oauth"}]}`))
		case "/v0/management/gemini-api-key":
			_, _ = w.Write([]byte(`{"gemini-api-key":[{"api-key":"不得返回","base-url":"https://gemini.example/v1beta","auth-index":"auth-gemini","allowed-models":["gemini-test"],"model-rule-version":"rule-gemini"}]}`))
		case "/v0/management/claude-api-key":
			http.Error(w, "temporary", http.StatusInternalServerError)
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	result, err := New(nil).Snapshot(context.Background(), server.URL, "management-key")
	if err != nil {
		t.Fatalf("读取快照：%v", err)
	}
	if !result.Capabilities.CredentialDraft || !result.Capabilities.AllowedModels {
		t.Fatalf("能力 = %#v", result.Capabilities)
	}
	if len(result.Accounts) != 2 {
		t.Fatalf("账号 = %#v", result.Accounts)
	}
	if result.Accounts[0].PlanType != "free" {
		t.Fatalf("套餐类型 = %q", result.Accounts[0].PlanType)
	}
	if result.Accounts[1].SourceType != SourceGeminiAPIKey || result.Accounts[1].AuthIndex != "auth-gemini" {
		t.Fatalf("配置账号 = %#v", result.Accounts[1])
	}
	if len(result.Warnings) != 1 {
		t.Fatalf("局部错误应形成一条告警：%#v", result.Warnings)
	}
}

func TestAPICallUsesGatewayContract(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var payload map[string]any
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			t.Fatalf("解析 api-call 请求：%v", err)
		}
		if payload["authIndex"] != "auth-1" || payload["method"] != "POST" || payload["data"] != `{"model":"gpt-test"}` {
			t.Fatalf("api-call 请求 = %#v", payload)
		}
		_, _ = w.Write([]byte(`{"status_code":200,"header":{"X-Test":["yes"]},"body":"{\"ok\":true}"}`))
	}))
	defer server.Close()
	result, err := New(nil).APICall(context.Background(), server.URL, "management-key", APICallRequest{
		AuthIndex: "auth-1", Method: http.MethodPost, URL: "https://upstream.example/v1/responses",
		Headers: map[string]string{"Authorization": "Bearer $TOKEN$"}, Body: map[string]any{"model": "gpt-test"},
	})
	if err != nil || result.StatusCode != 200 || result.Body != `{"ok":true}` {
		t.Fatalf("api-call 结果 = %#v err=%v", result, err)
	}
}

func TestWriteAndVerifyModelRulesReadsBackVersion(t *testing.T) {
	var mu sync.Mutex
	allowed := []string{"old"}
	mapping := map[string]string{"old": "upstream-old"}
	version := "rule-old"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/v0/management/auth-files":
			_ = json.NewEncoder(w).Encode(map[string]any{"files": []map[string]any{{
				"name": "account.json", "allowed_models": allowed, "model_mapping": mapping, "model_rule_version": version,
			}}})
		case r.Method == http.MethodPatch && r.URL.Path == "/v0/management/auth-files/fields":
			var payload struct {
				Allowed []string `json:"allowed_models"`
				Aliases []struct {
					Name  string `json:"name"`
					Alias string `json:"alias"`
				} `json:"model_aliases"`
			}
			_ = json.NewDecoder(r.Body).Decode(&payload)
			allowed = payload.Allowed
			mapping = map[string]string{}
			for _, item := range payload.Aliases {
				mapping[item.Alias] = item.Name
			}
			version = "rule-new"
			w.WriteHeader(http.StatusOK)
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	previous, applied, err := New(nil).WriteAndVerifyModelRules(context.Background(), server.URL, "management-key", SourceAuthFile, "account.json", ModelRules{
		AllowedModels: []string{"new"}, ModelMapping: map[string]string{"alias": "upstream-new"},
	})
	if err != nil {
		t.Fatalf("写入并回读规则：%v", err)
	}
	if previous.ModelRuleVersion != "rule-old" || applied.ModelRuleVersion != "rule-new" {
		t.Fatalf("规则版本 = previous:%#v applied:%#v", previous, applied)
	}
	if !reflect.DeepEqual(applied.AllowedModels, []string{"new"}) || applied.ModelMapping["alias"] != "upstream-new" {
		t.Fatalf("生效规则 = %#v", applied)
	}
}

func TestEditableHeadersExcludeCredentialValues(t *testing.T) {
	result := editableHeaders(map[string]string{
		"Authorization":  "Bearer secret",
		"X-API-Key":      "secret-key",
		"Cookie":         "session=secret",
		"X-Tenant":       "tenant-a",
		"X-Feature-Flag": "enabled",
	})
	if !reflect.DeepEqual(result, map[string]string{"X-Tenant": "tenant-a", "X-Feature-Flag": "enabled"}) {
		t.Fatalf("可编辑 Header = %#v", result)
	}
}

func TestClientReadsRuntimeAndBuiltInModels(t *testing.T) {
	httpClient := &http.Client{Transport: rulesRoundTripFunc(func(r *http.Request) (*http.Response, error) {
		status := http.StatusOK
		payload := ""
		switch r.URL.Path {
		case "/v0/management/auth-files/models":
			if r.URL.Query().Get("auth_index") != "auth index" {
				status = http.StatusBadRequest
				payload = `{"error":"invalid auth index"}`
				break
			}
			payload = `{"models":[{"id":"runtime-a"},{"id":"runtime-b"}]}`
		case "/v0/management/model-definitions/codex":
			payload = `{"models":[{"id":"built-in-a"}]}`
		default:
			status = http.StatusNotFound
			payload = `{"error":"not found"}`
		}
		return &http.Response{
			StatusCode: status,
			Header:     make(http.Header),
			Body:       io.NopCloser(strings.NewReader(payload)),
			Request:    r,
		}, nil
	})}

	client := New(httpClient)
	runtime, err := client.ListRuntimeModels(context.Background(), "http://gateway.test", "management-key", "auth index", "")
	if err != nil || !reflect.DeepEqual(runtime, []string{"runtime-a", "runtime-b"}) {
		t.Fatalf("运行时模型 = %#v, err=%v", runtime, err)
	}
	builtIn, err := client.ListBuiltInModels(context.Background(), "http://gateway.test", "management-key", "codex")
	if err != nil || !reflect.DeepEqual(builtIn, []string{"built-in-a"}) {
		t.Fatalf("内置模型 = %#v, err=%v", builtIn, err)
	}
}
