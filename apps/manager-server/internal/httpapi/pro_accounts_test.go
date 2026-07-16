package httpapi

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/testutil"
)

func TestProAccountCapabilitiesAndConfigAccountSync(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v0/management/auth-files":
			w.Header().Set("X-CPA-SUPPORT-CREDENTIAL-DRAFT", "true")
			w.Header().Set("X-CPA-SUPPORT-ALLOWED-MODELS", "true")
			_, _ = w.Write([]byte(`{"files":[]}`))
		case "/v0/management/gemini-api-key":
			_, _ = w.Write([]byte(`{"gemini-api-key":[{"api-key":"secret-not-persisted","base-url":"https://gemini.example/v1beta","auth-index":"auth-gemini","allowed-models":["gemini-test"],"model-rule-version":"rule-gemini"}]}`))
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(upstream.Close)
	handler := newTestHandler(t, upstream.URL, true)

	capabilitiesRR := testutil.Request(t, handler, http.MethodGet, "/v0/pro/accounts/capabilities", "", testutil.AdminKey)
	testutil.RequireStatus(t, capabilitiesRR, http.StatusOK)
	if !strings.Contains(capabilitiesRR.Body.String(), `"credentialDraft":true`) || !strings.Contains(capabilitiesRR.Body.String(), `"allowedModels":true`) {
		t.Fatalf("capabilities body = %s", capabilitiesRR.Body.String())
	}
	syncRR := testutil.Request(t, handler, http.MethodPost, "/v0/pro/accounts/sync", `{}`, testutil.AdminKey)
	testutil.RequireStatus(t, syncRR, http.StatusOK)
	if strings.Contains(syncRR.Body.String(), "secret-not-persisted") {
		t.Fatalf("同步响应泄露了 API Key：%s", syncRR.Body.String())
	}
	listRR := testutil.Request(t, handler, http.MethodGet, "/v0/pro/accounts?auth_type=api", "", testutil.AdminKey)
	testutil.RequireStatus(t, listRR, http.StatusOK)
	if !strings.Contains(listRR.Body.String(), `"sourceType":"config_gemini_api_key"`) || !strings.Contains(listRR.Body.String(), `"authIndex":"auth-gemini"`) {
		t.Fatalf("配置账号未同步：%s", listRR.Body.String())
	}
}

func TestProAccountOperationRejectsSensitiveContextAndCanBeRead(t *testing.T) {
	handler := newTestHandler(t, "http://example.test", true)
	createdRR := testutil.Request(t, handler, http.MethodPost, "/v0/pro/accounts/operations", `{
		"operation_id":"operation-1","idempotency_key":"operation-key-1","operation_type":"add",
		"context":{"platform":"openai"}
	}`, testutil.AdminKey)
	testutil.RequireStatus(t, createdRR, http.StatusCreated)
	getRR := testutil.Request(t, handler, http.MethodGet, "/v0/pro/accounts/operations/operation-1", "", testutil.AdminKey)
	testutil.RequireStatus(t, getRR, http.StatusOK)
	if !strings.Contains(getRR.Body.String(), `"state":"draft_created"`) {
		t.Fatalf("operation body = %s", getRR.Body.String())
	}
	sensitiveRR := testutil.Request(t, handler, http.MethodPost, "/v0/pro/accounts/operations", `{
		"operation_id":"operation-2","idempotency_key":"operation-key-2","operation_type":"add",
		"context":{"access_token":"must-not-persist"}
	}`, testutil.AdminKey)
	testutil.RequireStatus(t, sensitiveRR, http.StatusBadRequest)
	if strings.Contains(sensitiveRR.Body.String(), "must-not-persist") || !strings.Contains(sensitiveRR.Body.String(), `"code":"sensitive_operation_context"`) {
		t.Fatalf("sensitive response = %s", sensitiveRR.Body.String())
	}
}

func TestProAccountModelRulesAndConnectivityUseSameAllowlist(t *testing.T) {
	var mu sync.Mutex
	allowed := []string{"old-model"}
	mapping := map[string]string{}
	ruleVersion := "rule-old"
	var apiCalls atomic.Int64
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/v0/management/auth-files":
			_ = json.NewEncoder(w).Encode(map[string]any{"files": []map[string]any{{
				"name": "codex.json", "auth_index": "auth-openai", "provider": "codex",
				"base_url": "https://api.example/v1", "allowed_models": allowed,
				"model_mapping": mapping, "model_rule_version": ruleVersion,
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
			ruleVersion = "rule-new"
			w.WriteHeader(http.StatusOK)
		case r.Method == http.MethodPost && r.URL.Path == "/v0/management/api-call":
			apiCalls.Add(1)
			var payload struct {
				AuthIndex string `json:"authIndex"`
				Data      string `json:"data"`
			}
			_ = json.NewDecoder(r.Body).Decode(&payload)
			if payload.AuthIndex != "auth-openai" || !strings.Contains(payload.Data, `"model":"upstream-model"`) {
				http.Error(w, "invalid test request", http.StatusBadRequest)
				return
			}
			_, _ = w.Write([]byte(`{"status_code":200,"header":{},"body":"{}"}`))
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(upstream.Close)
	handler := newTestHandler(t, upstream.URL, true)
	syncRR := testutil.Request(t, handler, http.MethodPost, "/v0/pro/accounts/sync", `{}`, testutil.AdminKey)
	testutil.RequireStatus(t, syncRR, http.StatusOK)
	var synced struct {
		Items []struct {
			ID string `json:"proAccountId"`
		} `json:"items"`
	}
	testutil.DecodeJSON(t, syncRR, &synced)
	accountID := synced.Items[0].ID

	modelsRR := testutil.Request(t, handler, http.MethodPut, "/v0/pro/accounts/"+accountID+"/models", `{
		"operation_id":"models-operation","idempotency_key":"models-key","expected_version":1,
		"allowed_models":["client-model"],"model_mapping":{"client-model":"upstream-model"}
	}`, testutil.AdminKey)
	testutil.RequireStatus(t, modelsRR, http.StatusOK)
	if !strings.Contains(modelsRR.Body.String(), `"modelRuleVersion":"rule-new"`) || !strings.Contains(modelsRR.Body.String(), `"state":"enabled"`) {
		t.Fatalf("models body = %s", modelsRR.Body.String())
	}

	disallowedRR := testutil.Request(t, handler, http.MethodPost, "/v0/pro/accounts/"+accountID+"/test", `{
		"operation_id":"test-disallowed","idempotency_key":"test-disallowed-key","expected_version":2,"model":"other-model"
	}`, testutil.AdminKey)
	testutil.RequireStatus(t, disallowedRR, http.StatusBadRequest)
	if apiCalls.Load() != 0 {
		t.Fatalf("白名单校验失败时不应调用 api-call，次数 = %d", apiCalls.Load())
	}
	allowedRR := testutil.Request(t, handler, http.MethodPost, "/v0/pro/accounts/"+accountID+"/test", `{
		"operation_id":"test-allowed","idempotency_key":"test-allowed-key","expected_version":2,"model":"client-model"
	}`, testutil.AdminKey)
	testutil.RequireStatus(t, allowedRR, http.StatusOK)
	if !strings.Contains(allowedRR.Body.String(), `"success":true`) || apiCalls.Load() != 1 {
		t.Fatalf("test body = %s apiCalls=%d", allowedRR.Body.String(), apiCalls.Load())
	}
}

func TestProAccountsSyncAndList(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v0/management/auth-files" {
			http.NotFound(w, r)
			return
		}
		if r.Header.Get("Authorization") != "Bearer management-key" {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"files":[{"name":"codex-alpha.json","auth_index":"auth-alpha","provider":"codex","account":"alpha@example.com","account_id":"account-alpha","status":"active"}]}`))
	}))
	t.Cleanup(upstream.Close)
	handler := newTestHandler(t, upstream.URL, true)

	syncRR := testutil.Request(t, handler, http.MethodPost, "/v0/pro/accounts/sync", `{"dry_run":false}`, testutil.AdminKey)
	testutil.RequireStatus(t, syncRR, http.StatusOK)
	var syncResult struct {
		Created int `json:"created"`
	}
	testutil.DecodeJSON(t, syncRR, &syncResult)
	if syncResult.Created != 1 {
		t.Fatalf("sync result = %s", syncRR.Body.String())
	}

	listRR := testutil.Request(t, handler, http.MethodGet, "/v0/pro/accounts?platform=openai", "", testutil.AdminKey)
	testutil.RequireStatus(t, listRR, http.StatusOK)
	var listResult struct {
		Total int64 `json:"total"`
		Items []struct {
			ID       string `json:"id"`
			Platform string `json:"platform"`
			Email    string `json:"email"`
		} `json:"items"`
	}
	testutil.DecodeJSON(t, listRR, &listResult)
	if listResult.Total != 1 || len(listResult.Items) != 1 || listResult.Items[0].Platform != "openai" {
		t.Fatalf("list result = %s", listRR.Body.String())
	}
	if listResult.Items[0].ID == "" || listResult.Items[0].Email != "alpha@example.com" {
		t.Fatalf("account = %#v", listResult.Items[0])
	}
}

func TestUnknownProRouteIsLocal404(t *testing.T) {
	var upstreamCalls atomic.Int64
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		upstreamCalls.Add(1)
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"proxied": true})
	}))
	t.Cleanup(upstream.Close)
	handler := newTestHandler(t, upstream.URL, true)

	rr := testutil.Request(t, handler, http.MethodGet, "/v0/pro/not-a-route", "", testutil.AdminKey)
	testutil.RequireStatus(t, rr, http.StatusNotFound)
	if upstreamCalls.Load() != 0 {
		t.Fatalf("unknown Pro route was proxied %d times", upstreamCalls.Load())
	}
	if !strings.Contains(rr.Body.String(), `"code":"pro_route_not_found"`) {
		t.Fatalf("response body = %s", rr.Body.String())
	}
}

func TestProRouteRequiresPanelAuthorization(t *testing.T) {
	handler := newTestHandler(t, "http://example.test", true)
	rr := testutil.Request(t, handler, http.MethodGet, "/v0/pro/accounts", "", "")
	testutil.RequireStatus(t, rr, http.StatusUnauthorized)
}

func TestProAccountActiveUsageUsesGatewayAPICall(t *testing.T) {
	var apiCalls atomic.Int64
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v0/management/auth-files":
			_, _ = w.Write([]byte(`{"files":[{"name":"codex-alpha.json","auth_index":"auth-alpha","provider":"codex","account":"alpha@example.com","account_id":"account-alpha"}]}`))
		case "/v0/management/api-call":
			apiCalls.Add(1)
			var request struct {
				AuthIndex string `json:"authIndex"`
			}
			if err := json.NewDecoder(r.Body).Decode(&request); err != nil || request.AuthIndex != "auth-alpha" {
				http.Error(w, "bad request", http.StatusBadRequest)
				return
			}
			_, _ = w.Write([]byte(`{"status_code":200,"body":{"rate_limit":{"primary_window":{"used_percent":62,"limit_window_seconds":18000,"reset_after_seconds":3600},"secondary_window":{"used_percent":4,"limit_window_seconds":604800,"reset_after_seconds":7200}}}}`))
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(upstream.Close)
	handler := newTestHandler(t, upstream.URL, true)

	syncRR := testutil.Request(t, handler, http.MethodPost, "/v0/pro/accounts/sync", `{}`, testutil.AdminKey)
	testutil.RequireStatus(t, syncRR, http.StatusOK)
	var syncResult struct {
		Items []struct {
			ID string `json:"proAccountId"`
		} `json:"items"`
	}
	testutil.DecodeJSON(t, syncRR, &syncResult)
	if len(syncResult.Items) != 1 || syncResult.Items[0].ID == "" {
		t.Fatalf("sync body = %s", syncRR.Body.String())
	}

	usageRR := testutil.Request(t, handler, http.MethodGet, "/v0/pro/accounts/"+syncResult.Items[0].ID+"/usage?source=active&force=true", "", testutil.AdminKey)
	testutil.RequireStatus(t, usageRR, http.StatusOK)
	var usageResult struct {
		Source          string `json:"source"`
		OfficialWindows []struct {
			ID          string   `json:"id"`
			UsedPercent *float64 `json:"usedPercent"`
		} `json:"officialWindows"`
	}
	testutil.DecodeJSON(t, usageRR, &usageResult)
	if usageResult.Source != "official" || len(usageResult.OfficialWindows) != 2 {
		t.Fatalf("usage body = %s", usageRR.Body.String())
	}
	if usageResult.OfficialWindows[0].ID != "five_hour" || usageResult.OfficialWindows[0].UsedPercent == nil || *usageResult.OfficialWindows[0].UsedPercent != 62 {
		t.Fatalf("usage windows = %#v", usageResult.OfficialWindows)
	}
	if apiCalls.Load() != 1 {
		t.Fatalf("api-call count = %d", apiCalls.Load())
	}
}

func TestUnsupportedActiveUsageDoesNotCallUpstream(t *testing.T) {
	var apiCalls atomic.Int64
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v0/management/auth-files":
			_, _ = w.Write([]byte(`{"files":[{"name":"gemini-alpha.json","auth_index":"gemini-alpha","provider":"gemini","account":"alpha@example.com"}]}`))
		case "/v0/management/api-call":
			apiCalls.Add(1)
			_, _ = w.Write([]byte(`{"status_code":200,"body":{}}`))
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(upstream.Close)
	handler := newTestHandler(t, upstream.URL, true)
	syncRR := testutil.Request(t, handler, http.MethodPost, "/v0/pro/accounts/sync", `{}`, testutil.AdminKey)
	testutil.RequireStatus(t, syncRR, http.StatusOK)
	var syncResult struct {
		Items []struct {
			ID string `json:"proAccountId"`
		} `json:"items"`
	}
	testutil.DecodeJSON(t, syncRR, &syncResult)
	usageRR := testutil.Request(t, handler, http.MethodGet, "/v0/pro/accounts/"+syncResult.Items[0].ID+"/usage?source=active&force=true", "", testutil.AdminKey)
	testutil.RequireStatus(t, usageRR, http.StatusOK)
	if !strings.Contains(usageRR.Body.String(), `"errorCode":"official_usage_unsupported"`) {
		t.Fatalf("usage body = %s", usageRR.Body.String())
	}
	if apiCalls.Load() != 0 {
		t.Fatalf("不支持的账号不应请求上游，api-call count = %d", apiCalls.Load())
	}
}
