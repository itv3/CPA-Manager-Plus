package proaccountgateway

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"reflect"
	"sync"
	"testing"
	"time"
)

func TestWriteAndVerifyOfficialClientCompatibilityAssignsGatewayProfile(t *testing.T) {
	for _, testCase := range []struct {
		name        string
		path        string
		responseKey string
		sourceType  string
		profile     string
	}{
		{name: "Claude", path: "/v0/management/claude-api-key", responseKey: "claude-api-key", sourceType: SourceClaudeAPIKey, profile: "claude-desktop-2.1.215-v1"},
		{name: "Codex", path: "/v0/management/codex-api-key", responseKey: "codex-api-key", sourceType: SourceCodexAPIKey, profile: "codex-desktop-0.145.0-alpha.18-v1"},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			var mu sync.Mutex
			compatibility := map[string]any{"enabled": false, "profile": testCase.profile, "tls-profile": ""}
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				mu.Lock()
				defer mu.Unlock()
				switch {
				case r.Method == http.MethodGet && r.URL.Path == "/v0/management/auth-files":
					w.Header().Set("X-CPA-SUPPORT-OFFICIAL-CLIENT-COMPATIBILITY", "true")
					_, _ = w.Write([]byte(`{"files":[]}`))
				case r.Method == http.MethodGet && r.URL.Path == testCase.path:
					_ = json.NewEncoder(w).Encode(map[string]any{testCase.responseKey: []map[string]any{{"official-client-compatibility": compatibility}}})
				case r.Method == http.MethodPatch && r.URL.Path == testCase.path:
					var payload struct {
						Value map[string]map[string]any `json:"value"`
					}
					if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
						t.Fatalf("解析兼容配置 PATCH：%v", err)
					}
					compatibility = payload.Value["official-client-compatibility"]
					if mapBool(compatibility, "enabled") && mapString(compatibility, "profile") == "" {
						compatibility["profile"] = testCase.profile
					}
					_, _ = w.Write([]byte(`{"status":"ok"}`))
				default:
					http.NotFound(w, r)
				}
			}))
			defer server.Close()

			previous, applied, err := New(nil).WriteAndVerifyOfficialClientCompatibility(
				context.Background(), server.URL, "management-key", testCase.sourceType, "index:0",
				OfficialClientCompatibility{Enabled: true},
			)
			if err != nil {
				t.Fatalf("写入并回读兼容配置：%v", err)
			}
			if previous.Enabled || previous.Profile != testCase.profile {
				t.Fatalf("旧配置 = %#v", previous)
			}
			if !applied.Enabled || applied.Profile != testCase.profile || applied.TLSProfile != "" {
				t.Fatalf("生效配置 = %#v", applied)
			}
		})
	}
}

func TestWriteOfficialClientCompatibilityRefusesOldGatewayBeforePatch(t *testing.T) {
	patchCalls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPatch {
			patchCalls++
		}
		_, _ = w.Write([]byte(`{"files":[]}`))
	}))
	defer server.Close()

	_, _, err := New(nil).WriteAndVerifyOfficialClientCompatibility(
		context.Background(), server.URL, "management-key", SourceClaudeAPIKey, "index:0",
		OfficialClientCompatibility{Enabled: true},
	)
	if !errors.Is(err, ErrOfficialClientCompatibilityUnsupported) {
		t.Fatalf("旧 Gateway 错误 = %v", err)
	}
	if patchCalls != 0 {
		t.Fatalf("旧 Gateway 收到了 %d 次 PATCH", patchCalls)
	}
}

func TestWriteOfficialClientCompatibilityRestoresAfterUncertainWrite(t *testing.T) {
	var mu sync.Mutex
	patchCalls := 0
	compatibility := map[string]any{"enabled": false, "profile": "claude-desktop-2.1.215-v1", "tls-profile": ""}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/v0/management/auth-files":
			w.Header().Set("X-CPA-SUPPORT-OFFICIAL-CLIENT-COMPATIBILITY", "true")
			_, _ = w.Write([]byte(`{"files":[]}`))
		case r.Method == http.MethodGet && r.URL.Path == "/v0/management/claude-api-key":
			_ = json.NewEncoder(w).Encode(map[string]any{"claude-api-key": []map[string]any{{"official-client-compatibility": compatibility}}})
		case r.Method == http.MethodPatch && r.URL.Path == "/v0/management/claude-api-key":
			patchCalls++
			var payload struct {
				Value map[string]map[string]any `json:"value"`
			}
			_ = json.NewDecoder(r.Body).Decode(&payload)
			compatibility = payload.Value["official-client-compatibility"]
			if patchCalls == 1 {
				http.Error(w, "response lost after commit", http.StatusBadGateway)
				return
			}
			_, _ = w.Write([]byte(`{"status":"ok"}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	_, _, err := New(nil).WriteAndVerifyOfficialClientCompatibility(
		context.Background(), server.URL, "management-key", SourceClaudeAPIKey, "index:0",
		OfficialClientCompatibility{Enabled: true, Profile: "claude-desktop-2.1.215-v1"},
	)
	if err == nil || errors.Is(err, ErrOfficialClientCompatibilityStateUncertain) {
		t.Fatalf("写入失败应在恢复成功后返回原错误：%v", err)
	}
	mu.Lock()
	defer mu.Unlock()
	if patchCalls != 2 || mapBool(compatibility, "enabled") {
		t.Fatalf("恢复结果：patchCalls=%d compatibility=%#v", patchCalls, compatibility)
	}
}

func TestWriteOfficialClientCompatibilityReportsUncertainWhenRestoreFails(t *testing.T) {
	patchCalls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/v0/management/auth-files":
			w.Header().Set("X-CPA-SUPPORT-OFFICIAL-CLIENT-COMPATIBILITY", "true")
			_, _ = w.Write([]byte(`{"files":[]}`))
		case r.Method == http.MethodGet && r.URL.Path == "/v0/management/codex-api-key":
			_, _ = w.Write([]byte(`{"codex-api-key":[{"official-client-compatibility":{"enabled":false,"profile":"codex-desktop-0.145.0-alpha.18-v1","tls-profile":""}}]}`))
		case r.Method == http.MethodPatch && r.URL.Path == "/v0/management/codex-api-key":
			patchCalls++
			http.Error(w, "write failed", http.StatusBadGateway)
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	_, _, err := New(nil).WriteAndVerifyOfficialClientCompatibility(
		context.Background(), server.URL, "management-key", SourceCodexAPIKey, "index:0",
		OfficialClientCompatibility{Enabled: true},
	)
	if !errors.Is(err, ErrOfficialClientCompatibilityStateUncertain) || patchCalls != 2 {
		t.Fatalf("恢复失败结果：err=%v patchCalls=%d", err, patchCalls)
	}
}

func TestCreateDisabledAPIPreservesExistingUnknownCompatibilityBlock(t *testing.T) {
	var written []map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/v0/management/auth-files":
			w.Header().Set("X-CPA-SUPPORT-OFFICIAL-CLIENT-COMPATIBILITY", "true")
			_, _ = w.Write([]byte(`{"files":[]}`))
		case r.Method == http.MethodGet && r.URL.Path == "/v0/management/codex-api-key":
			if written == nil {
				_, _ = w.Write([]byte(`{"codex-api-key":[{"api-key":"old","base-url":"https://old.example/v1","auth-index":"read-only","official-client-compatibility":{"enabled":true,"profile":"future-profile","tls-profile":"","future":{"mode":"keep"}}}]}`))
				return
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"codex-api-key": written})
		case r.Method == http.MethodPut && r.URL.Path == "/v0/management/codex-api-key":
			if err := json.NewDecoder(r.Body).Decode(&written); err != nil {
				t.Fatalf("解析整表 PUT：%v", err)
			}
			if len(written) == 2 {
				compatibility := written[1]["official-client-compatibility"].(map[string]any)
				compatibility["profile"] = "codex-desktop-0.145.0-alpha.18-v1"
			}
			_, _ = w.Write([]byte(`{"status":"ok"}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 250*time.Millisecond)
	defer cancel()
	_, _ = New(nil).CreateDisabledAPI(ctx, server.URL, "management-key", CreateAPIInput{
		Platform: "openai", SourceType: SourceCodexAPIKey, BaseURL: "https://new.example/v1", APIKey: "new-key",
		OfficialClientCompatibility: &OfficialClientCompatibility{Enabled: true},
	})
	if len(written) != 2 {
		t.Fatalf("整表 PUT = %#v", written)
	}
	want := map[string]any{"enabled": true, "profile": "future-profile", "tls-profile": "", "future": map[string]any{"mode": "keep"}}
	if !reflect.DeepEqual(written[0]["official-client-compatibility"], want) {
		t.Fatalf("已有未知兼容块丢失：%#v", written[0])
	}
	if _, exists := written[0]["auth-index"]; exists {
		t.Fatalf("只读 auth-index 被整表写回：%#v", written[0])
	}
}
