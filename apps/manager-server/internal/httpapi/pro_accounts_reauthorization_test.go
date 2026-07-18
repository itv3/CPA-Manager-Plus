package httpapi

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/testutil"
)

type reauthorizationGatewayState struct {
	mu sync.Mutex

	replacementEmail       string
	oldExists              bool
	oldDisabled            bool
	replacementExists      bool
	replacementDisabled    bool
	replacementDraft       bool
	replacementAllowed     []string
	replacementRuleVersion string
	callbackCode           string
	callbackState          string
}

func newReauthorizationGatewayState(replacementEmail string) *reauthorizationGatewayState {
	return &reauthorizationGatewayState{
		replacementEmail:       replacementEmail,
		oldExists:              true,
		replacementDisabled:    true,
		replacementDraft:       true,
		replacementAllowed:     []string{},
		replacementRuleVersion: "draft-rules-v1",
	}
}

func (s *reauthorizationGatewayState) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Header.Get("Authorization") != "Bearer management-key" {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	switch r.URL.Path {
	case "/v0/management/auth-files":
		s.handleAuthFiles(w, r)
	case "/v0/management/codex-auth-url":
		if r.URL.Query().Get("credential_draft") != "true" {
			http.Error(w, "draft required", http.StatusBadRequest)
			return
		}
		_, _ = w.Write([]byte(`{"status":"ok","url":"https://login.example/authorize","state":"reauth-state"}`))
	case "/v0/management/oauth-callback":
		var request map[string]string
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			http.Error(w, "bad callback", http.StatusBadRequest)
			return
		}
		s.callbackCode = request["code"]
		s.callbackState = request["state"]
		_, _ = w.Write([]byte(`{"status":"ok"}`))
	case "/v0/management/get-auth-status":
		s.replacementExists = true
		_, _ = w.Write([]byte(`{"status":"ok"}`))
	case "/v0/management/oauth-session":
		_, _ = w.Write([]byte(`{"status":"ok","cancelled":true}`))
	case "/v0/management/auth-files/fields":
		var request struct {
			Name          string   `json:"name"`
			AllowedModels []string `json:"allowed_models"`
		}
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil || request.Name != "replacement.json" {
			http.Error(w, "bad rules", http.StatusBadRequest)
			return
		}
		s.replacementAllowed = append([]string(nil), request.AllowedModels...)
		s.replacementRuleVersion = "replacement-rules-v2"
		_, _ = w.Write([]byte(`{"status":"ok"}`))
	case "/v0/management/auth-files/models":
		if r.URL.Query().Get("auth_index") != "replacement-index" {
			http.Error(w, "bad auth index", http.StatusBadRequest)
			return
		}
		_, _ = w.Write([]byte(`{"models":[{"id":"gpt-5.5"}]}`))
	case "/v0/management/account-test":
		var request map[string]any
		_ = json.NewDecoder(r.Body).Decode(&request)
		if request["auth_index"] != "replacement-index" || request["model"] != "gpt-5.5" {
			http.Error(w, "wrong test target", http.StatusBadRequest)
			return
		}
		_, _ = w.Write([]byte(`{"success":true,"status_code":200,"protocol":"responses","mode":"default","model":"gpt-5.5","duration_ms":5}`))
	case "/v0/management/auth-files/status":
		var request struct {
			Name     string `json:"name"`
			Disabled bool   `json:"disabled"`
		}
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			http.Error(w, "bad status", http.StatusBadRequest)
			return
		}
		switch request.Name {
		case "old.json":
			s.oldDisabled = request.Disabled
		case "replacement.json":
			s.replacementDisabled = request.Disabled
			if !request.Disabled {
				s.replacementDraft = false
			}
		default:
			http.NotFound(w, r)
			return
		}
		_, _ = w.Write([]byte(`{"status":"ok"}`))
	default:
		http.NotFound(w, r)
	}
}

func (s *reauthorizationGatewayState) handleAuthFiles(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("X-CPA-SUPPORT-CREDENTIAL-DRAFT", "true")
	w.Header().Set("X-CPA-SUPPORT-CREDENTIAL-REFRESH", "true")
	w.Header().Set("X-CPA-SUPPORT-TARGETED-REAUTH", "true")
	w.Header().Set("X-CPA-SUPPORT-ALLOWED-MODELS", "true")
	if r.Method == http.MethodDelete {
		switch r.URL.Query().Get("name") {
		case "old.json":
			s.oldExists = false
		case "replacement.json":
			s.replacementExists = false
		default:
			http.NotFound(w, r)
			return
		}
		_, _ = w.Write([]byte(`{"status":"ok"}`))
		return
	}
	files := make([]map[string]any, 0, 2)
	if s.oldExists {
		files = append(files, map[string]any{
			"id": "old-auth-id", "name": "old.json", "provider": "codex", "auth_index": "old-index",
			"email": "owner@example.com", "account": "owner@example.com", "account_id": "upstream-account",
			"disabled": s.oldDisabled, "status": "active", "credential_draft": false,
			"allowed_models": []string{"gpt-5.5"}, "model_mapping": map[string]string{}, "model_rule_version": "old-rules-v1",
		})
	}
	if s.replacementExists {
		files = append(files, map[string]any{
			"id": "replacement-auth-id", "name": "replacement.json", "provider": "codex", "auth_index": "replacement-index",
			"email": s.replacementEmail, "account": s.replacementEmail, "account_id": "upstream-account",
			"disabled": s.replacementDisabled, "status": "active", "credential_draft": s.replacementDraft,
			"allowed_models": s.replacementAllowed, "model_mapping": map[string]string{}, "model_rule_version": s.replacementRuleVersion,
		})
	}
	_ = json.NewEncoder(w).Encode(map[string]any{"files": files})
}

func TestProAccountReauthorizationSafelyRebindsMatchingCodexIdentity(t *testing.T) {
	state := newReauthorizationGatewayState("owner@example.com")
	gateway := httptest.NewServer(state)
	t.Cleanup(gateway.Close)
	handler := newTestHandler(t, gateway.URL, true)
	accountID, version := syncSingleProAccount(t, handler)

	startBody := fmt.Sprintf(`{"operation_id":"reauthorize-operation","idempotency_key":"reauthorize-key","expected_version":%d}`, version)
	startRR := testutil.Request(t, handler, http.MethodPost, "/v0/pro/accounts/"+accountID+"/reauthorize/start", startBody, testutil.AdminKey)
	testutil.RequireStatus(t, startRR, http.StatusOK)
	var startResult struct {
		OAuth struct {
			State string `json:"state"`
		} `json:"oauth"`
	}
	testutil.DecodeJSON(t, startRR, &startResult)
	if startResult.OAuth.State != "reauth-state" {
		t.Fatalf("unexpected start result: %s", startRR.Body.String())
	}
	callbackRR := testutil.Request(t, handler, http.MethodPost, "/v0/pro/accounts/"+accountID+"/reauthorize/callback",
		`{"operation_id":"reauthorize-operation","callback_input":"http://localhost/callback?code=oauth-code&state=reauth-state","callback_state":"reauth-state"}`, testutil.AdminKey)
	testutil.RequireStatus(t, callbackRR, http.StatusOK)
	statusRR := testutil.Request(t, handler, http.MethodGet, "/v0/pro/accounts/"+accountID+"/reauthorize/status?operation_id=reauthorize-operation", "", testutil.AdminKey)
	testutil.RequireStatus(t, statusRR, http.StatusOK)
	var result struct {
		Status  string `json:"status"`
		Account struct {
			ID      string `json:"id"`
			Binding struct {
				AuthIndex string `json:"authIndex"`
			} `json:"binding"`
		} `json:"account"`
	}
	testutil.DecodeJSON(t, statusRR, &result)
	if result.Status != "ok" || result.Account.ID != accountID || result.Account.Binding.AuthIndex != "replacement-index" {
		t.Fatalf("unexpected status result: %s", statusRR.Body.String())
	}
	state.mu.Lock()
	defer state.mu.Unlock()
	if state.oldExists || !state.replacementExists || state.replacementDisabled || state.replacementDraft || state.callbackCode != "oauth-code" || state.callbackState != "reauth-state" {
		t.Fatalf("unexpected final gateway state: %#v", state)
	}
}

func TestProAccountCapabilitiesDeclareAccountActions(t *testing.T) {
	state := newReauthorizationGatewayState("owner@example.com")
	gateway := httptest.NewServer(state)
	t.Cleanup(gateway.Close)
	handler := newTestHandler(t, gateway.URL, true)

	result := testutil.Request(t, handler, http.MethodGet, "/v0/pro/accounts/capabilities", "", testutil.AdminKey)
	testutil.RequireStatus(t, result, http.StatusOK)
	for _, expected := range []string{
		`"refreshToken":{"status":"supported"}`,
		`"reauthorize":{"status":"supported","provider":"codex"}`,
		`"scheduledTests":{"status":"supported","provider":"manager","version":"manager-scheduled-test-v1"}`,
	} {
		if !strings.Contains(result.Body.String(), expected) {
			t.Fatalf("capabilities missing %s: %s", expected, result.Body.String())
		}
	}
}

func TestProAccountReauthorizationRejectsDifferentIdentityAndKeepsOldCredential(t *testing.T) {
	state := newReauthorizationGatewayState("other@example.com")
	gateway := httptest.NewServer(state)
	t.Cleanup(gateway.Close)
	handler := newTestHandler(t, gateway.URL, true)
	accountID, version := syncSingleProAccount(t, handler)
	startBody := fmt.Sprintf(`{"operation_id":"reauthorize-mismatch","idempotency_key":"reauthorize-mismatch-key","expected_version":%d}`, version)
	startRR := testutil.Request(t, handler, http.MethodPost, "/v0/pro/accounts/"+accountID+"/reauthorize/start", startBody, testutil.AdminKey)
	testutil.RequireStatus(t, startRR, http.StatusOK)
	statusRR := testutil.Request(t, handler, http.MethodGet, "/v0/pro/accounts/"+accountID+"/reauthorize/status?operation_id=reauthorize-mismatch", "", testutil.AdminKey)
	testutil.RequireStatus(t, statusRR, http.StatusConflict)
	if !strings.Contains(statusRR.Body.String(), `"code":"oauth_identity_mismatch"`) {
		t.Fatalf("unexpected mismatch response: %s", statusRR.Body.String())
	}
	state.mu.Lock()
	defer state.mu.Unlock()
	if !state.oldExists || state.oldDisabled || state.replacementExists {
		t.Fatalf("identity mismatch changed old credential or leaked draft: %#v", state)
	}
}

func syncSingleProAccount(t *testing.T, handler http.Handler) (string, int64) {
	t.Helper()
	syncRR := testutil.Request(t, handler, http.MethodPost, "/v0/pro/accounts/sync", `{}`, testutil.AdminKey)
	testutil.RequireStatus(t, syncRR, http.StatusOK)
	listRR := testutil.Request(t, handler, http.MethodGet, "/v0/pro/accounts?limit=10", "", testutil.AdminKey)
	testutil.RequireStatus(t, listRR, http.StatusOK)
	var list struct {
		Items []struct {
			ID      string `json:"id"`
			Version int64  `json:"version"`
		} `json:"items"`
	}
	testutil.DecodeJSON(t, listRR, &list)
	if len(list.Items) != 1 {
		t.Fatalf("unexpected account list: %s", listRR.Body.String())
	}
	return list.Items[0].ID, list.Items[0].Version
}
