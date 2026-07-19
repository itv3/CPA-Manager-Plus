package httpapi

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/testutil"
)

const codexResponsesURLForTest = "https://chatgpt.com/backend-api/codex/responses"

func TestProAccountLifecycleCreateMigrateToggleAndDelete(t *testing.T) {
	responsesUpstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1/models":
			_, _ = w.Write([]byte(`{"data":[{"id":"upstream-model"}]}`))
		case "/v1/responses":
			_, _ = w.Write([]byte(`{"output":[{"type":"function_call","name":"probe_account"}]}`))
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(responsesUpstream.Close)
	chatUpstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1/models":
			_, _ = w.Write([]byte(`{"data":[{"id":"upstream-model"}]}`))
		case "/v1/responses":
			http.NotFound(w, r)
		case "/v1/chat/completions":
			_, _ = w.Write([]byte(`{"choices":[{"message":{"content":"OK"}}]}`))
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(chatUpstream.Close)
	gatewayState := &lifecycleGatewayState{}
	gateway := httptest.NewServer(gatewayState)
	t.Cleanup(gateway.Close)
	handler := newTestHandler(t, gateway.URL, true)

	createRR := testutil.Request(t, handler, http.MethodPost, "/v0/pro/accounts", fmt.Sprintf(`{
		"operation_id":"create-operation","idempotency_key":"create-key","platform":"openai","auth_type":"api",
		"name":"主 OpenAI","base_url":%q,"api_key":"candidate-key","protocol_mode":"responses",
		"allowed_models":["client-model"],"model_mapping":{"client-model":"upstream-model"},"test_model":"client-model"
	}`, responsesUpstream.URL), testutil.AdminKey)
	testutil.RequireStatus(t, createRR, http.StatusCreated)
	var created struct {
		Account struct {
			ID         string `json:"id"`
			Version    int64  `json:"version"`
			SourceType string `json:"sourceType"`
			Enabled    bool   `json:"enabled"`
		} `json:"account"`
	}
	testutil.DecodeJSON(t, createRR, &created)
	if created.Account.ID == "" || created.Account.SourceType != "config_codex_api_key" || !created.Account.Enabled || created.Account.Version < 3 {
		t.Fatalf("create body = %s", createRR.Body.String())
	}
	accountID := created.Account.ID

	migrateRR := testutil.Request(t, handler, http.MethodPut, "/v0/pro/accounts/"+accountID, fmt.Sprintf(`{
		"operation_id":"migrate-operation","idempotency_key":"migrate-key","expected_version":%d,
		"base_url":%q,"api_key":"replacement-key","protocol_mode":"auto",
		"allowed_models":["client-model"],"model_mapping":{"client-model":"upstream-model"},"test_model":"client-model"
	}`, created.Account.Version, chatUpstream.URL), testutil.AdminKey)
	testutil.RequireStatus(t, migrateRR, http.StatusOK)
	var migrated struct {
		Account struct {
			ID         string `json:"id"`
			Version    int64  `json:"version"`
			SourceType string `json:"sourceType"`
			Enabled    bool   `json:"enabled"`
		} `json:"account"`
	}
	testutil.DecodeJSON(t, migrateRR, &migrated)
	if migrated.Account.ID != accountID || migrated.Account.SourceType != "config_openai_compatibility" || !migrated.Account.Enabled {
		t.Fatalf("migrate body = %s", migrateRR.Body.String())
	}
	gatewayState.mu.Lock()
	if len(gatewayState.codex) != 0 || len(gatewayState.compat) != 1 {
		gatewayState.mu.Unlock()
		t.Fatalf("迁移后 Gateway 配置 codex=%d compat=%d", len(gatewayState.codex), len(gatewayState.compat))
	}
	gatewayState.mu.Unlock()

	disableRR := testutil.Request(t, handler, http.MethodPut, "/v0/pro/accounts/"+accountID, fmt.Sprintf(`{
		"operation_id":"disable-operation","idempotency_key":"disable-key","expected_version":%d,"enabled":false
	}`, migrated.Account.Version), testutil.AdminKey)
	testutil.RequireStatus(t, disableRR, http.StatusOK)
	var disabled struct {
		Account struct {
			Version int64 `json:"version"`
			Enabled bool  `json:"enabled"`
		} `json:"account"`
	}
	testutil.DecodeJSON(t, disableRR, &disabled)
	if disabled.Account.Enabled {
		t.Fatalf("disable body = %s", disableRR.Body.String())
	}

	deleteRR := testutil.Request(t, handler, http.MethodDelete, "/v0/pro/accounts/"+accountID, fmt.Sprintf(`{
		"operation_id":"delete-operation","idempotency_key":"delete-key","expected_version":%d
	}`, disabled.Account.Version), testutil.AdminKey)
	testutil.RequireStatus(t, deleteRR, http.StatusOK)
	listRR := testutil.Request(t, handler, http.MethodGet, "/v0/pro/accounts", "", testutil.AdminKey)
	testutil.RequireStatus(t, listRR, http.StatusOK)
	if !strings.Contains(listRR.Body.String(), `"total":0`) {
		t.Fatalf("delete list body = %s", listRR.Body.String())
	}
	gatewayState.mu.Lock()
	defer gatewayState.mu.Unlock()
	if gatewayState.apiCalls < 2 || len(gatewayState.compat) != 0 {
		t.Fatalf("Gateway 最终状态 apiCalls=%d compat=%d", gatewayState.apiCalls, len(gatewayState.compat))
	}
}

func TestProAccountCreateDraftOnlyPersistsRulesWithoutConnectivityTest(t *testing.T) {
	const secret = "draft-only-secret-must-not-leak"
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1/models":
			_, _ = w.Write([]byte(`{"data":[{"id":"upstream-model"}]}`))
		case "/v1/responses":
			http.NotFound(w, r)
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(upstream.Close)
	gatewayState := &lifecycleGatewayState{}
	gateway := httptest.NewServer(gatewayState)
	t.Cleanup(gateway.Close)
	handler := newTestHandler(t, gateway.URL, true)
	body := fmt.Sprintf(`{
		"operation_id":"draft-only-operation","idempotency_key":"draft-only-key","platform":"openai","auth_type":"api",
		"name":"停用 OpenAI","base_url":%q,"api_key":%q,"protocol_mode":"responses","draft_only":true,
		"allowed_models":["client-model"],"model_mapping":{"client-model":"upstream-model"},"test_model":"client-model"
	}`, upstream.URL, secret)

	createRR := testutil.Request(t, handler, http.MethodPost, "/v0/pro/accounts", body, testutil.AdminKey)
	testutil.RequireStatus(t, createRR, http.StatusCreated)
	var created struct {
		Account struct {
			ID            string            `json:"id"`
			SourceType    string            `json:"sourceType"`
			Enabled       bool              `json:"enabled"`
			AllowedModels []string          `json:"allowedModels"`
			ModelMapping  map[string]string `json:"modelMapping"`
		} `json:"account"`
		Operation struct {
			State string `json:"state"`
		} `json:"operation"`
		SavedDisabled bool `json:"savedDisabled"`
	}
	testutil.DecodeJSON(t, createRR, &created)
	if created.Account.ID == "" || created.Account.SourceType != "config_codex_api_key" || created.Account.Enabled || created.Operation.State != "saved_disabled" || !created.SavedDisabled {
		t.Fatalf("draft_only 创建结果 = %s", createRR.Body.String())
	}
	if len(created.Account.AllowedModels) != 1 || created.Account.AllowedModels[0] != "client-model" || created.Account.ModelMapping["client-model"] != "upstream-model" {
		t.Fatalf("draft_only 模型规则 = %#v / %#v", created.Account.AllowedModels, created.Account.ModelMapping)
	}
	if strings.Contains(createRR.Body.String(), secret) {
		t.Fatalf("draft_only 响应泄露 API Key：%s", createRR.Body.String())
	}

	replayRR := testutil.Request(t, handler, http.MethodPost, "/v0/pro/accounts", body, testutil.AdminKey)
	testutil.RequireStatus(t, replayRR, http.StatusCreated)
	if !strings.Contains(replayRR.Body.String(), `"state":"saved_disabled"`) || !strings.Contains(replayRR.Body.String(), `"savedDisabled":true`) || strings.Contains(replayRR.Body.String(), secret) {
		t.Fatalf("draft_only 幂等重放结果 = %s", replayRR.Body.String())
	}

	gatewayState.mu.Lock()
	apiCalls := gatewayState.apiCalls
	codexCount := len(gatewayState.codex)
	var codexRaw []byte
	if codexCount == 1 {
		codexRaw, _ = json.Marshal(gatewayState.codex[0])
	}
	gatewayState.mu.Unlock()
	if apiCalls != 0 || codexCount != 1 {
		t.Fatalf("draft_only Gateway 调用 apiCalls=%d codex=%d", apiCalls, codexCount)
	}
	encodedRules := string(codexRaw)
	if !strings.Contains(encodedRules, `"allowed-models":["client-model"]`) || !strings.Contains(encodedRules, `"alias":"client-model"`) || !strings.Contains(encodedRules, `"name":"upstream-model"`) || !strings.Contains(encodedRules, `"excluded-models":["*"]`) {
		t.Fatalf("draft_only 未完整写入停用模型规则：%s", encodedRules)
	}
}

func TestProAccountCreateKeepsDisabledAfterFinalTestFailure(t *testing.T) {
	const secret = "failed-test-secret-must-not-leak"
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1/models":
			_, _ = w.Write([]byte(`{"data":[{"id":"upstream-model"}]}`))
		case "/v1/responses":
			_, _ = w.Write([]byte(`{"output":[{"type":"function_call","name":"probe_account"}]}`))
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(upstream.Close)
	gatewayState := &lifecycleGatewayState{apiCallStatus: http.StatusUnauthorized}
	gateway := httptest.NewServer(gatewayState)
	t.Cleanup(gateway.Close)
	handler := newTestHandler(t, gateway.URL, true)

	createRR := testutil.Request(t, handler, http.MethodPost, "/v0/pro/accounts", fmt.Sprintf(`{
		"operation_id":"failed-test-operation","idempotency_key":"failed-test-key","platform":"openai","auth_type":"api",
		"name":"测试失败 OpenAI","base_url":%q,"api_key":%q,"protocol_mode":"responses",
		"allowed_models":["client-model"],"model_mapping":{"client-model":"upstream-model"},"test_model":"client-model",
		"save_disabled_on_test_failure":true
	}`, upstream.URL, secret), testutil.AdminKey)
	testutil.RequireStatus(t, createRR, http.StatusCreated)
	var created struct {
		Account struct {
			ID      string `json:"id"`
			Enabled bool   `json:"enabled"`
		} `json:"account"`
		Operation struct {
			State     string `json:"state"`
			ErrorCode string `json:"errorCode"`
		} `json:"operation"`
		Connectivity struct {
			Success    bool   `json:"success"`
			StatusCode int    `json:"statusCode"`
			ErrorCode  string `json:"errorCode"`
		} `json:"connectivity"`
		SavedDisabled bool `json:"savedDisabled"`
	}
	testutil.DecodeJSON(t, createRR, &created)
	if created.Account.ID == "" || created.Account.Enabled || created.Operation.State != "saved_disabled" || created.Operation.ErrorCode != "authentication_failed" || !created.SavedDisabled {
		t.Fatalf("最终测试失败保留结果 = %s", createRR.Body.String())
	}
	if created.Connectivity.Success || created.Connectivity.StatusCode != http.StatusUnauthorized || created.Connectivity.ErrorCode != "authentication_failed" {
		t.Fatalf("最终测试诊断 = %#v", created.Connectivity)
	}
	if strings.Contains(createRR.Body.String(), secret) {
		t.Fatalf("最终测试失败响应泄露 API Key：%s", createRR.Body.String())
	}
	gatewayState.mu.Lock()
	apiCalls := gatewayState.apiCalls
	codexCount := len(gatewayState.codex)
	gatewayState.mu.Unlock()
	if apiCalls != 1 || codexCount != 1 {
		t.Fatalf("最终测试失败后的 Gateway 状态 apiCalls=%d codex=%d", apiCalls, codexCount)
	}
}

func TestProAccountOAuthDraftCanCompleteAndCancel(t *testing.T) {
	gatewayState := &oauthGatewayState{mapping: map[string]string{}}
	gateway := httptest.NewServer(gatewayState)
	t.Cleanup(gateway.Close)
	handler := newTestHandler(t, gateway.URL, true)

	startRR := testutil.Request(t, handler, http.MethodPost, "/v0/pro/accounts/oauth/start", `{
		"operation_id":"oauth-operation","idempotency_key":"oauth-key","platform":"openai"
	}`, testutil.AdminKey)
	testutil.RequireStatus(t, startRR, http.StatusCreated)
	if !strings.Contains(startRR.Body.String(), `"state":"oauth-state"`) || !strings.Contains(startRR.Body.String(), `"state":"probed"`) {
		t.Fatalf("oauth start body = %s", startRR.Body.String())
	}
	callbackRR := testutil.Request(t, handler, http.MethodPost, "/v0/pro/accounts/oauth/callback", `{
		"operation_id":"oauth-operation","callback_input":"authorization-code","callback_state":"oauth-state"
	}`, testutil.AdminKey)
	testutil.RequireStatus(t, callbackRR, http.StatusOK)
	gatewayState.mu.Lock()
	if gatewayState.callbackProvider != "codex" || gatewayState.callbackCode != "authorization-code" || gatewayState.callbackState != "oauth-state" {
		gatewayState.mu.Unlock()
		t.Fatalf("OAuth 回调未正确转发：%#v", gatewayState)
	}
	gatewayState.mu.Unlock()
	mismatchRR := testutil.Request(t, handler, http.MethodPost, "/v0/pro/accounts/oauth/callback", `{
		"operation_id":"oauth-operation","callback_input":"http://localhost/callback?code=wrong-code&state=other-state"
	}`, testutil.AdminKey)
	testutil.RequireStatus(t, mismatchRR, http.StatusBadRequest)
	if !strings.Contains(mismatchRR.Body.String(), `"code":"oauth_state_mismatch"`) {
		t.Fatalf("OAuth state 不匹配响应 = %s", mismatchRR.Body.String())
	}
	statusRR := testutil.Request(t, handler, http.MethodGet, "/v0/pro/accounts/oauth/status?operation_id=oauth-operation", "", testutil.AdminKey)
	testutil.RequireStatus(t, statusRR, http.StatusOK)
	var statusResult struct {
		Account struct {
			ID      string `json:"id"`
			Version int64  `json:"version"`
		} `json:"account"`
	}
	testutil.DecodeJSON(t, statusRR, &statusResult)
	if statusResult.Account.ID == "" || statusResult.Account.Version != 1 || !strings.Contains(statusRR.Body.String(), `"state":"credential_saved_disabled"`) {
		t.Fatalf("oauth status body = %s", statusRR.Body.String())
	}
	completeRR := testutil.Request(t, handler, http.MethodPost, "/v0/pro/accounts/"+statusResult.Account.ID+"/complete", `{
		"operation_id":"oauth-operation","expected_version":1,"allowed_models":["client-model"],
		"model_mapping":{"client-model":"upstream-model"},"test_model":"client-model"
	}`, testutil.AdminKey)
	testutil.RequireStatus(t, completeRR, http.StatusOK)
	if !strings.Contains(completeRR.Body.String(), `"state":"enabled"`) || !strings.Contains(completeRR.Body.String(), `"enabled":true`) {
		t.Fatalf("oauth complete body = %s", completeRR.Body.String())
	}
	gatewayState.mu.Lock()
	if gatewayState.credentialDraft || gatewayState.disabled || gatewayState.accountTestCalls != 0 {
		gatewayState.mu.Unlock()
		t.Fatalf("OAuth 应直接启用且不执行连通性测试：%#v", gatewayState)
	}
	gatewayState.mu.Unlock()

	cancelStartRR := testutil.Request(t, handler, http.MethodPost, "/v0/pro/accounts/oauth/start", `{
		"operation_id":"oauth-cancel-operation","idempotency_key":"oauth-cancel-key","platform":"openai"
	}`, testutil.AdminKey)
	testutil.RequireStatus(t, cancelStartRR, http.StatusCreated)
	cancelRR := testutil.Request(t, handler, http.MethodPost, "/v0/pro/accounts/drafts/cancel", `{
		"operation_id":"oauth-cancel-operation"
	}`, testutil.AdminKey)
	testutil.RequireStatus(t, cancelRR, http.StatusOK)
	if !strings.Contains(cancelRR.Body.String(), `"state":"cancelled"`) {
		t.Fatalf("oauth cancel body = %s", cancelRR.Body.String())
	}
	gatewayState.mu.Lock()
	defer gatewayState.mu.Unlock()
	if !gatewayState.cancelled {
		t.Fatal("OAuth 取消未转发到 Gateway")
	}
}

type lifecycleGatewayState struct {
	mu            sync.Mutex
	codex         []map[string]any
	compat        []map[string]any
	apiCalls      int
	apiCallStatus int
}

type oauthGatewayState struct {
	mu               sync.Mutex
	draftCreated     bool
	disabled         bool
	credentialDraft  bool
	allowed          []string
	mapping          map[string]string
	cancelled        bool
	callbackCode     string
	callbackState    string
	callbackProvider string
	accountTestCalls int
}

func (s *oauthGatewayState) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	defer s.mu.Unlock()
	switch r.URL.Path {
	case "/v0/management/auth-files":
		w.Header().Set("X-CPA-SUPPORT-CREDENTIAL-DRAFT", "true")
		w.Header().Set("X-CPA-SUPPORT-ALLOWED-MODELS", "true")
		files := []map[string]any{}
		if s.draftCreated {
			files = append(files, map[string]any{
				"name": "oauth-account.json", "provider": "codex", "auth_index": "oauth-auth-index",
				"account": "oauth@example.com", "disabled": s.disabled,
				"credential_draft": s.credentialDraft, "allowed_models": s.allowed,
				"model_mapping": s.mapping, "model_rule_version": "oauth-rule-version",
			})
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"files": files})
	case "/v0/management/codex-auth-url":
		if r.URL.Query().Get("credential_draft") != "true" {
			http.Error(w, "draft required", http.StatusBadRequest)
			return
		}
		_, _ = w.Write([]byte(`{"url":"https://login.example/authorize","state":"oauth-state"}`))
	case "/v0/management/get-auth-status":
		s.draftCreated = true
		s.disabled = true
		s.credentialDraft = true
		_, _ = w.Write([]byte(`{"status":"ok"}`))
	case "/v0/management/oauth-callback":
		var payload struct {
			Provider string `json:"provider"`
			Code     string `json:"code"`
			State    string `json:"state"`
		}
		if json.NewDecoder(r.Body).Decode(&payload) != nil {
			http.Error(w, "bad callback", http.StatusBadRequest)
			return
		}
		s.callbackProvider = payload.Provider
		s.callbackCode = payload.Code
		s.callbackState = payload.State
		_, _ = w.Write([]byte(`{"status":"ok"}`))
	case "/v0/management/oauth-session":
		s.cancelled = true
		_, _ = w.Write([]byte(`{"status":"ok","cancelled":true}`))
	case "/v0/management/auth-files/fields":
		var payload struct {
			Allowed []string `json:"allowed_models"`
			Aliases []struct {
				Name  string `json:"name"`
				Alias string `json:"alias"`
			} `json:"model_aliases"`
		}
		_ = json.NewDecoder(r.Body).Decode(&payload)
		s.allowed = payload.Allowed
		s.mapping = map[string]string{}
		for _, item := range payload.Aliases {
			s.mapping[item.Alias] = item.Name
		}
		w.WriteHeader(http.StatusOK)
	case "/v0/management/auth-files/status":
		var payload struct {
			Disabled bool `json:"disabled"`
		}
		_ = json.NewDecoder(r.Body).Decode(&payload)
		s.disabled = payload.Disabled
		if !payload.Disabled {
			s.credentialDraft = false
		}
		w.WriteHeader(http.StatusOK)
	case "/v0/management/api-call":
		_, _ = w.Write([]byte(`{"status_code":200,"header":{},"body":"{}"}`))
	case "/v0/management/account-test":
		s.accountTestCalls++
		writeAccountTestResponse(w, r, http.StatusUnauthorized)
	default:
		http.NotFound(w, r)
	}
}

func (s *lifecycleGatewayState) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Header.Get("Authorization") != "Bearer management-key" {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	switch r.URL.Path {
	case "/v0/management/auth-files":
		w.Header().Set("X-CPA-SUPPORT-CREDENTIAL-DRAFT", "true")
		w.Header().Set("X-CPA-SUPPORT-ALLOWED-MODELS", "true")
		_, _ = w.Write([]byte(`{"files":[]}`))
	case "/v0/management/codex-api-key":
		s.handleConfigList(w, r, "codex-api-key", &s.codex)
	case "/v0/management/openai-compatibility":
		s.handleCompatibility(w, r)
	case "/v0/management/api-call":
		s.apiCalls++
		status := s.apiCallStatus
		if status == 0 {
			status = http.StatusOK
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"status_code": status, "header": map[string]string{}, "body": "{}"})
	case "/v0/management/account-test":
		s.apiCalls++
		status := s.apiCallStatus
		if status == 0 {
			status = http.StatusOK
		}
		writeAccountTestResponse(w, r, status)
	default:
		http.NotFound(w, r)
	}
}

func (s *lifecycleGatewayState) handleConfigList(w http.ResponseWriter, r *http.Request, responseKey string, entries *[]map[string]any) {
	switch r.Method {
	case http.MethodGet:
		result := make([]map[string]any, 0, len(*entries))
		for index, entry := range *entries {
			copyEntry := cloneTestMap(entry)
			copyEntry["auth-index"] = fmt.Sprintf("codex-auth-%d", index)
			copyEntry["model-rule-version"] = fmt.Sprintf("codex-rule-%d", index)
			result = append(result, copyEntry)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{responseKey: result})
	case http.MethodPut:
		var payload []map[string]any
		if json.NewDecoder(r.Body).Decode(&payload) != nil {
			http.Error(w, "bad request", http.StatusBadRequest)
			return
		}
		*entries = payload
		w.WriteHeader(http.StatusOK)
	case http.MethodPatch:
		var payload struct {
			Index int            `json:"index"`
			Value map[string]any `json:"value"`
		}
		if json.NewDecoder(r.Body).Decode(&payload) != nil || payload.Index < 0 || payload.Index >= len(*entries) {
			http.Error(w, "bad request", http.StatusBadRequest)
			return
		}
		for key, value := range payload.Value {
			(*entries)[payload.Index][key] = value
		}
		w.WriteHeader(http.StatusOK)
	case http.MethodDelete:
		index, err := strconv.Atoi(r.URL.Query().Get("index"))
		if err != nil || index < 0 || index >= len(*entries) {
			http.Error(w, "not found", http.StatusNotFound)
			return
		}
		*entries = append((*entries)[:index], (*entries)[index+1:]...)
		w.WriteHeader(http.StatusOK)
	default:
		http.Error(w, "method", http.StatusMethodNotAllowed)
	}
}

func (s *lifecycleGatewayState) handleCompatibility(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		providers := make([]map[string]any, 0, len(s.compat))
		for providerIndex, provider := range s.compat {
			copyProvider := cloneTestMap(provider)
			keys, _ := provider["api-key-entries"].([]any)
			if keys == nil {
				if typed, ok := provider["api-key-entries"].([]map[string]any); ok {
					keys = make([]any, len(typed))
					for index := range typed {
						keys[index] = typed[index]
					}
				}
			}
			responseKeys := make([]map[string]any, 0, len(keys))
			for keyIndex, raw := range keys {
				entry, _ := raw.(map[string]any)
				copyKey := cloneTestMap(entry)
				copyKey["auth-index"] = fmt.Sprintf("compat-auth-%d-%d", providerIndex, keyIndex)
				copyKey["model-rule-version"] = fmt.Sprintf("compat-rule-%d-%d", providerIndex, keyIndex)
				responseKeys = append(responseKeys, copyKey)
			}
			copyProvider["api-key-entries"] = responseKeys
			providers = append(providers, copyProvider)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"openai-compatibility": providers})
	case http.MethodPut:
		if json.NewDecoder(r.Body).Decode(&s.compat) != nil {
			http.Error(w, "bad request", http.StatusBadRequest)
			return
		}
		w.WriteHeader(http.StatusOK)
	case http.MethodPatch:
		var payload struct {
			Index    int            `json:"index"`
			KeyIndex *int           `json:"key-index"`
			Value    map[string]any `json:"value"`
		}
		if json.NewDecoder(r.Body).Decode(&payload) != nil || payload.Index < 0 || payload.Index >= len(s.compat) {
			http.Error(w, "bad request", http.StatusBadRequest)
			return
		}
		for key, value := range payload.Value {
			if key == "allowed-models" && payload.KeyIndex != nil {
				keys, _ := s.compat[payload.Index]["api-key-entries"].([]any)
				if *payload.KeyIndex >= 0 && *payload.KeyIndex < len(keys) {
					keys[*payload.KeyIndex].(map[string]any)["allowed-models"] = value
				}
				continue
			}
			s.compat[payload.Index][key] = value
		}
		w.WriteHeader(http.StatusOK)
	case http.MethodDelete:
		index, err := strconv.Atoi(r.URL.Query().Get("index"))
		if err != nil || index < 0 || index >= len(s.compat) {
			http.Error(w, "not found", http.StatusNotFound)
			return
		}
		s.compat = append(s.compat[:index], s.compat[index+1:]...)
		w.WriteHeader(http.StatusOK)
	}
}

func cloneTestMap(value map[string]any) map[string]any {
	raw, _ := json.Marshal(value)
	result := map[string]any{}
	_ = json.Unmarshal(raw, &result)
	return result
}

func writeAccountTestResponse(w http.ResponseWriter, r *http.Request, status int) {
	var request struct {
		Model    string `json:"model"`
		Protocol string `json:"protocol"`
		Mode     string `json:"mode"`
	}
	_ = json.NewDecoder(r.Body).Decode(&request)
	errorCode := ""
	errorMessage := ""
	retryable := false
	switch {
	case status == http.StatusUnauthorized || status == http.StatusForbidden:
		errorCode = "authentication_failed"
		errorMessage = "上游拒绝了账号凭证"
	case status == http.StatusNotFound || status == http.StatusMethodNotAllowed:
		errorCode = "protocol_not_supported"
		errorMessage = "上游不支持所选协议"
	case status == http.StatusTooManyRequests:
		errorCode = "rate_limited"
		errorMessage = "上游正在限流"
		retryable = true
	case status >= http.StatusInternalServerError:
		errorCode = "upstream_unavailable"
		errorMessage = "上游服务暂时不可用"
		retryable = true
	case status < http.StatusOK || status >= http.StatusMultipleChoices:
		errorCode = "connectivity_test_failed"
		errorMessage = "账号连通性测试失败"
	}
	_ = json.NewEncoder(w).Encode(map[string]any{
		"success":     status >= http.StatusOK && status < http.StatusMultipleChoices,
		"status_code": status, "protocol": request.Protocol, "mode": request.Mode,
		"model": request.Model, "upstream_model": request.Model, "duration_ms": 1,
		"error_code": errorCode, "error_message": errorMessage, "retryable": retryable,
	})
}

func TestProAccountCapabilitiesAndConfigAccountSync(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v0/management/auth-files":
			w.Header().Set("X-CPA-SUPPORT-CREDENTIAL-DRAFT", "true")
			w.Header().Set("X-CPA-SUPPORT-ALLOWED-MODELS", "true")
			w.Header().Set("X-CPA-SUPPORT-OFFICIAL-CLIENT-COMPATIBILITY", "true")
			_, _ = w.Write([]byte(`{"files":[]}`))
		case "/v0/management/gemini-api-key":
			_, _ = w.Write([]byte(`{"gemini-api-key":[{"api-key":"secret-not-persisted","base-url":"https://gemini.example/v1beta","auth-index":"auth-gemini","allowed-models":["gemini-test"],"model-rule-version":"rule-gemini"}]}`))
		case "/v0/management/plugins":
			_, _ = w.Write([]byte(`{"plugins_enabled":true,"plugins":[{"id":"gemini-cli","registered":true,"enabled":true,"effective_enabled":true,"supports_oauth":true,"oauth_provider":"gemini-cli","metadata":{"version":"1.0.5"}}]}`))
		case "/v0/management/auth-files/models":
			if r.URL.Query().Get("auth_index") != "auth-gemini" {
				http.Error(w, "invalid auth index", http.StatusBadRequest)
				return
			}
			_, _ = w.Write([]byte(`{"models":[{"id":"upstream-gemini"}]}`))
		case "/v0/management/model-definitions/aistudio":
			_, _ = w.Write([]byte(`{"models":[{"id":"built-in-gemini"}]}`))
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(upstream.Close)
	handler := newTestHandler(t, upstream.URL, true)

	capabilitiesRR := testutil.Request(t, handler, http.MethodGet, "/v0/pro/accounts/capabilities", "", testutil.AdminKey)
	testutil.RequireStatus(t, capabilitiesRR, http.StatusOK)
	if !strings.Contains(capabilitiesRR.Body.String(), `"credentialDraft":true`) || !strings.Contains(capabilitiesRR.Body.String(), `"allowedModels":true`) || !strings.Contains(capabilitiesRR.Body.String(), `"officialClientCompatibility":true`) || !strings.Contains(capabilitiesRR.Body.String(), `"version":"1.0.5"`) {
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
	var listResult struct {
		Items []struct {
			ID string `json:"id"`
		} `json:"items"`
	}
	testutil.DecodeJSON(t, listRR, &listResult)
	modelsRR := testutil.Request(t, handler, http.MethodGet, "/v0/pro/accounts/"+listResult.Items[0].ID+"/models", "", testutil.AdminKey)
	testutil.RequireStatus(t, modelsRR, http.StatusOK)
	if !strings.Contains(modelsRR.Body.String(), `"models":["upstream-gemini","built-in-gemini","gemini-test"]`) || !strings.Contains(modelsRR.Body.String(), `"upstreamStatus":"supported"`) {
		t.Fatalf("模型目录响应 = %s", modelsRR.Body.String())
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
		case r.Method == http.MethodPost && r.URL.Path == "/v0/management/account-test":
			apiCalls.Add(1)
			var payload struct {
				AuthIndex string `json:"auth_index"`
				Model     string `json:"model"`
				Protocol  string `json:"protocol"`
				Mode      string `json:"mode"`
			}
			_ = json.NewDecoder(r.Body).Decode(&payload)
			if payload.AuthIndex != "auth-openai" || payload.Model != "upstream-model" || payload.Protocol != "responses" || payload.Mode != "default" {
				http.Error(w, "invalid test request", http.StatusBadRequest)
				return
			}
			_, _ = w.Write([]byte(`{"success":true,"status_code":200,"protocol":"responses","mode":"default","model":"upstream-model","upstream_model":"upstream-model","duration_ms":1}`))
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
	var authFilesMu sync.Mutex
	authFilesPresent := true
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
		authFilesMu.Lock()
		present := authFilesPresent
		authFilesMu.Unlock()
		if present {
			_, _ = w.Write([]byte(`{"files":[{"name":"codex-alpha.json","auth_index":"auth-alpha","provider":"codex","account":"alpha@example.com","account_id":"account-alpha","status":"active"}]}`))
			return
		}
		_, _ = w.Write([]byte(`{"files":[]}`))
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
			Binding  *struct {
				AttributionQuality string `json:"attributionQuality"`
			} `json:"binding"`
		} `json:"items"`
	}
	testutil.DecodeJSON(t, listRR, &listResult)
	if listResult.Total != 1 || len(listResult.Items) != 1 || listResult.Items[0].Platform != "openai" {
		t.Fatalf("list result = %s", listRR.Body.String())
	}
	if listResult.Items[0].ID == "" || listResult.Items[0].Email != "alpha@example.com" {
		t.Fatalf("account = %#v", listResult.Items[0])
	}
	if listResult.Items[0].Binding == nil || listResult.Items[0].Binding.AttributionQuality != "exact" {
		t.Fatalf("binding attribution = %#v", listResult.Items[0].Binding)
	}

	authFilesMu.Lock()
	authFilesPresent = false
	authFilesMu.Unlock()
	removeRR := testutil.Request(t, handler, http.MethodPost, "/v0/pro/accounts/sync", `{}`, testutil.AdminKey)
	testutil.RequireStatus(t, removeRR, http.StatusOK)
	var removeResult struct {
		Removed int `json:"removed"`
	}
	testutil.DecodeJSON(t, removeRR, &removeResult)
	if removeResult.Removed != 1 {
		t.Fatalf("remove result = %s", removeRR.Body.String())
	}
	listAfterRemoveRR := testutil.Request(t, handler, http.MethodGet, "/v0/pro/accounts", "", testutil.AdminKey)
	testutil.RequireStatus(t, listAfterRemoveRR, http.StatusOK)
	var listAfterRemove struct {
		Total int64 `json:"total"`
	}
	testutil.DecodeJSON(t, listAfterRemoveRR, &listAfterRemove)
	if listAfterRemove.Total != 0 {
		t.Fatalf("missing auth file account was not removed: %s", listAfterRemoveRR.Body.String())
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
				URL       string `json:"url"`
			}
			if err := json.NewDecoder(r.Body).Decode(&request); err != nil || request.AuthIndex != "auth-alpha" {
				http.Error(w, "bad request", http.StatusBadRequest)
				return
			}
			if request.URL == codexResponsesURLForTest {
				_, _ = w.Write([]byte(`{"status_code":200,"header":{"X-Codex-Primary-Used-Percent":["62"],"X-Codex-Primary-Window-Minutes":["300"],"X-Codex-Primary-Reset-After-Seconds":["3600"],"X-Codex-Secondary-Used-Percent":["4"],"X-Codex-Secondary-Window-Minutes":["10080"],"X-Codex-Secondary-Reset-After-Seconds":["7200"]},"body":""}`))
				return
			}
			_, _ = w.Write([]byte(`{"status_code":200,"header":{},"body":"{\"rate_limit\":{\"primary_window\":{\"used_percent\":62,\"limit_window_seconds\":18000,\"reset_after_seconds\":3600},\"secondary_window\":{\"used_percent\":4,\"limit_window_seconds\":604800,\"reset_after_seconds\":7200}}}"}`))
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
	if apiCalls.Load() != 2 {
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

func TestProAccountActiveUsageSupportsAnthropicAntigravityAndXAI(t *testing.T) {
	var mu sync.Mutex
	callCounts := map[string]int{}
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v0/management/auth-files":
			_, _ = w.Write([]byte(`{"files":[
				{"name":"claude-alpha.json","auth_index":"auth-claude","provider":"claude","account":"claude@example.com"},
				{"name":"antigravity-alpha.json","auth_index":"auth-antigravity","provider":"antigravity","account":"antigravity@example.com","project_id":"project-alpha"},
				{"name":"xai-alpha.json","auth_index":"auth-xai","provider":"xai","account":"xai@example.com"},
				{"name":"gemini-alpha.json","auth_index":"auth-gemini","provider":"gemini","account":"gemini@example.com"}
			]}`))
		case "/v0/management/api-call":
			var request struct {
				AuthIndex string            `json:"authIndex"`
				URL       string            `json:"url"`
				Header    map[string]string `json:"header"`
				Data      string            `json:"data"`
			}
			if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
				http.Error(w, "bad request", http.StatusBadRequest)
				return
			}
			if request.Header["Authorization"] != "Bearer $TOKEN$" {
				http.Error(w, "token placeholder required", http.StatusBadRequest)
				return
			}
			mu.Lock()
			callCounts[request.URL]++
			mu.Unlock()
			body := ""
			switch {
			case request.URL == "https://api.anthropic.com/api/oauth/usage" && request.AuthIndex == "auth-claude":
				if request.Header["anthropic-beta"] != "oauth-2025-04-20" {
					http.Error(w, "anthropic beta header required", http.StatusBadRequest)
					return
				}
				body = `{"five_hour":{"utilization":12,"resets_at":"2026-07-17T12:00:00Z"},"seven_day":{"utilization":34,"resets_at":"2026-07-24T12:00:00Z"}}`
			case strings.Contains(request.URL, "retrieveUserQuotaSummary") && request.AuthIndex == "auth-antigravity":
				var data struct {
					Project string `json:"project"`
				}
				if json.Unmarshal([]byte(request.Data), &data) != nil || data.Project != "project-alpha" {
					http.Error(w, "project required", http.StatusBadRequest)
					return
				}
				body = `{"models":{"gemini-3-flash":{"displayName":"Gemini 3 Flash","quotaInfo":{"remainingFraction":0.7,"resetTime":"2026-07-17T13:00:00Z"}}}}`
			case request.URL == "https://cli-chat-proxy.grok.com/v1/billing?format=credits" && request.AuthIndex == "auth-xai":
				body = `{"config":{"credit_usage_percent":25,"current_period":{"type":"weekly","end":"2026-07-24T00:00:00Z"}}}`
			case request.URL == "https://cli-chat-proxy.grok.com/v1/billing" && request.AuthIndex == "auth-xai":
				body = `{"config":{"monthly_limit":10000,"used":4000,"billing_period_end":"2026-08-01T00:00:00Z"}}`
			default:
				_ = json.NewEncoder(w).Encode(map[string]any{"status_code": http.StatusNotFound, "header": map[string][]string{}, "body": `{}`})
				return
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"status_code": http.StatusOK, "header": map[string][]string{}, "body": body})
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(upstream.Close)
	handler := newTestHandler(t, upstream.URL, true)

	syncRR := testutil.Request(t, handler, http.MethodPost, "/v0/pro/accounts/sync", `{}`, testutil.AdminKey)
	testutil.RequireStatus(t, syncRR, http.StatusOK)
	listRR := testutil.Request(t, handler, http.MethodGet, "/v0/pro/accounts?limit=100", "", testutil.AdminKey)
	testutil.RequireStatus(t, listRR, http.StatusOK)
	var listResult struct {
		Items []struct {
			ID       string `json:"id"`
			Platform string `json:"platform"`
		} `json:"items"`
	}
	testutil.DecodeJSON(t, listRR, &listResult)
	accountIDs := map[string]string{}
	for _, account := range listResult.Items {
		accountIDs[account.Platform] = account.ID
	}
	for _, platform := range []string{"anthropic", "antigravity", "xai", "gemini"} {
		if accountIDs[platform] == "" {
			t.Fatalf("同步后缺少 %s 账号：%s", platform, listRR.Body.String())
		}
	}

	type usageResponse struct {
		Source          string `json:"source"`
		ErrorCode       string `json:"errorCode"`
		OfficialWindows []struct {
			ID               string   `json:"id"`
			UsedPercent      *float64 `json:"usedPercent"`
			RemainingPercent *float64 `json:"remainingPercent"`
		} `json:"officialWindows"`
	}
	requestUsage := func(platform string, query string) usageResponse {
		t.Helper()
		rr := testutil.Request(t, handler, http.MethodGet, "/v0/pro/accounts/"+accountIDs[platform]+"/usage?"+query, "", testutil.AdminKey)
		testutil.RequireStatus(t, rr, http.StatusOK)
		var response usageResponse
		testutil.DecodeJSON(t, rr, &response)
		return response
	}

	anthropic := requestUsage("anthropic", "source=active")
	if anthropic.Source != "official" || len(anthropic.OfficialWindows) != 2 || anthropic.OfficialWindows[0].ID != "five_hour" {
		t.Fatalf("Anthropic 用量响应错误：%#v", anthropic)
	}
	_ = requestUsage("anthropic", "source=active")
	mu.Lock()
	if callCounts["https://api.anthropic.com/api/oauth/usage"] != 1 {
		mu.Unlock()
		t.Fatalf("Anthropic 成功缓存未生效：%#v", callCounts)
	}
	mu.Unlock()
	_ = requestUsage("anthropic", "source=active&force=true")
	mu.Lock()
	if callCounts["https://api.anthropic.com/api/oauth/usage"] != 2 {
		mu.Unlock()
		t.Fatalf("force=true 未绕过成功缓存：%#v", callCounts)
	}
	mu.Unlock()

	antigravity := requestUsage("antigravity", "source=active&force=true")
	if antigravity.Source != "official" || len(antigravity.OfficialWindows) != 1 || antigravity.OfficialWindows[0].RemainingPercent == nil || *antigravity.OfficialWindows[0].RemainingPercent != 70 {
		t.Fatalf("Antigravity 用量响应错误：%#v", antigravity)
	}
	xai := requestUsage("xai", "source=active&force=true")
	if xai.Source != "official" || len(xai.OfficialWindows) != 2 {
		t.Fatalf("xAI 用量响应错误：%#v", xai)
	}

	mu.Lock()
	beforeUnsupported := 0
	for _, count := range callCounts {
		beforeUnsupported += count
	}
	mu.Unlock()
	gemini := requestUsage("gemini", "source=active&force=true")
	if gemini.ErrorCode != "official_usage_unsupported" || len(gemini.OfficialWindows) != 0 {
		t.Fatalf("Gemini 降级响应错误：%#v", gemini)
	}
	mu.Lock()
	afterUnsupported := 0
	for _, count := range callCounts {
		afterUnsupported += count
	}
	mu.Unlock()
	if afterUnsupported != beforeUnsupported {
		t.Fatalf("不支持的账号触发了上游请求：before=%d after=%d", beforeUnsupported, afterUnsupported)
	}
}

func TestProAccountActiveUsageForceBypassesRecoverableErrorCache(t *testing.T) {
	var apiCalls atomic.Int64
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v0/management/auth-files":
			_, _ = w.Write([]byte(`{"files":[{"name":"claude-error.json","auth_index":"auth-claude-error","provider":"claude","account":"error@example.com"}]}`))
		case "/v0/management/api-call":
			apiCalls.Add(1)
			_ = json.NewEncoder(w).Encode(map[string]any{"status_code": http.StatusServiceUnavailable, "header": map[string][]string{}, "body": `{}`})
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
	if len(syncResult.Items) != 1 {
		t.Fatalf("同步响应错误：%s", syncRR.Body.String())
	}
	path := "/v0/pro/accounts/" + syncResult.Items[0].ID + "/usage?source=active&force=true"
	for range 2 {
		rr := testutil.Request(t, handler, http.MethodGet, path, "", testutil.AdminKey)
		testutil.RequireStatus(t, rr, http.StatusOK)
		if !strings.Contains(rr.Body.String(), `"errorCode":"official_usage_unknown"`) || !strings.Contains(rr.Body.String(), `"retryable":true`) {
			t.Fatalf("可恢复错误响应错误：%s", rr.Body.String())
		}
	}
	if apiCalls.Load() != 2 {
		t.Fatalf("强制查询未绕过可恢复错误缓存，api-call 次数 = %d", apiCalls.Load())
	}
}
