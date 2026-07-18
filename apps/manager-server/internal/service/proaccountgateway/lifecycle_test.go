package proaccountgateway

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"
)

func TestFinalizeCredentialDraftWritesFinalDisabledStateAtomically(t *testing.T) {
	var requestBody map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPatch && r.URL.Path == "/v0/management/auth-files/status":
			if err := json.NewDecoder(r.Body).Decode(&requestBody); err != nil {
				t.Fatalf("解析草稿终结请求：%v", err)
			}
			_, _ = w.Write([]byte(`{"status":"ok"}`))
		case r.Method == http.MethodGet && r.URL.Path == "/v0/management/auth-files":
			_, _ = w.Write([]byte(`{"files":[{"name":"codex.json","provider":"codex","disabled":true,"auth_index":"auth-1","model_rule_version":"rules-1"}]}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	snapshot, err := New(nil).FinalizeCredentialDraft(context.Background(), server.URL, "management-key", SourceAuthFile, "codex.json", false)
	if err != nil {
		t.Fatalf("原子终结停用草稿：%v", err)
	}
	if snapshot.Enabled || snapshot.SourceLocator != "codex.json" {
		t.Fatalf("草稿终态快照 = %#v", snapshot)
	}
	if requestBody["name"] != "codex.json" || requestBody["disabled"] != true || requestBody["finalize_draft"] != true {
		t.Fatalf("草稿终结请求 = %#v", requestBody)
	}
}

func TestCreateDisabledAPIReturnsLocatorWhenRuntimeIsNotReady(t *testing.T) {
	var stored atomic.Bool
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPut && r.URL.Path == "/v0/management/openai-compatibility":
			stored.Store(true)
			w.WriteHeader(http.StatusOK)
		case r.Method == http.MethodGet && r.URL.Path == "/v0/management/openai-compatibility":
			if stored.Load() {
				_, _ = w.Write([]byte(`{"openai-compatibility":[{"name":"draft","disabled":true,"base-url":"https://upstream.example/v1","api-key-entries":[{"api-key":"test-key"}]}]}`))
				return
			}
			_, _ = w.Write([]byte(`{"openai-compatibility":[]}`))
		case r.Method == http.MethodGet && r.URL.Path == "/v0/management/auth-files":
			_, _ = w.Write([]byte(`{"files":[]}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 250*time.Millisecond)
	defer cancel()
	snapshot, err := New(nil).CreateDisabledAPI(ctx, server.URL, "management-key", CreateAPIInput{
		Platform: "openai", SourceType: SourceOpenAICompatibility, Name: "draft",
		BaseURL: "https://upstream.example/v1", APIKey: "test-key",
	})
	if err == nil {
		t.Fatal("运行时未就绪时应返回错误")
	}
	if snapshot.SourceType != SourceOpenAICompatibility || snapshot.SourceLocator != "provider:0:key:0" {
		t.Fatalf("失败快照未保留清理定位：%#v", snapshot)
	}
}
