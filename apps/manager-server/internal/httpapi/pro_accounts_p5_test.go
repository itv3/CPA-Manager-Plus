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

type p5GatewayState struct {
	mu                sync.Mutex
	files             []map[string]any
	resetCredits      int
	resetConsumeCalls int
}

func (s *p5GatewayState) ServeHTTP(w http.ResponseWriter, r *http.Request) {
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
		_ = json.NewEncoder(w).Encode(map[string]any{"files": s.files})
	case "/v0/management/auth-files/status":
		var payload struct {
			Name     string `json:"name"`
			Disabled bool   `json:"disabled"`
		}
		if json.NewDecoder(r.Body).Decode(&payload) != nil {
			http.Error(w, "bad request", http.StatusBadRequest)
			return
		}
		for _, file := range s.files {
			if file["name"] == payload.Name {
				file["disabled"] = payload.Disabled
			}
		}
		w.WriteHeader(http.StatusOK)
	case "/v0/management/api-call":
		var payload struct {
			Method string `json:"method"`
			URL    string `json:"url"`
			Data   string `json:"data"`
		}
		if json.NewDecoder(r.Body).Decode(&payload) != nil {
			http.Error(w, "bad request", http.StatusBadRequest)
			return
		}
		if strings.HasSuffix(payload.URL, "/rate-limit-reset-credits/consume") {
			if payload.Method != http.MethodPost || !strings.Contains(payload.Data, "redeem_request_id") {
				http.Error(w, "invalid reset", http.StatusBadRequest)
				return
			}
			s.resetConsumeCalls++
			if s.resetCredits > 0 {
				s.resetCredits--
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"status_code": 200, "body": `{}`})
			return
		}
		if strings.HasSuffix(payload.URL, "/rate-limit-reset-credits") {
			body := fmt.Sprintf(`{"available_count":%d,"credits":[]}`, s.resetCredits)
			_ = json.NewEncoder(w).Encode(map[string]any{"status_code": 200, "body": body})
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"status_code": 200, "body": `{}`})
	default:
		http.NotFound(w, r)
	}
}

func TestProAccountBatchReturnsPerItemErrors(t *testing.T) {
	state := &p5GatewayState{files: []map[string]any{
		{"name": "alpha.json", "provider": "codex", "auth_index": "auth-alpha", "account": "alpha@example.com", "account_id": "acct-alpha", "disabled": false, "model_rule_version": "rule-alpha"},
		{"name": "beta.json", "provider": "codex", "auth_index": "auth-beta", "account": "beta@example.com", "account_id": "acct-beta", "disabled": false, "model_rule_version": "rule-beta"},
	}}
	gateway := httptest.NewServer(state)
	t.Cleanup(gateway.Close)
	handler := newTestHandler(t, gateway.URL, true)
	syncRR := testutil.Request(t, handler, http.MethodPost, "/v0/pro/accounts/sync", `{}`, testutil.AdminKey)
	testutil.RequireStatus(t, syncRR, http.StatusOK)
	var synced struct {
		Items []struct {
			ID string `json:"proAccountId"`
		} `json:"items"`
	}
	testutil.DecodeJSON(t, syncRR, &synced)
	if len(synced.Items) != 2 {
		t.Fatalf("同步结果 = %s", syncRR.Body.String())
	}
	body := fmt.Sprintf(`{
		"operation_id":"batch-disable","idempotency_key":"batch-disable-key","action":"disable",
		"items":[
			{"pro_account_id":%q,"expected_version":1},
			{"pro_account_id":%q,"expected_version":99}
		]
	}`, synced.Items[0].ID, synced.Items[1].ID)
	batchRR := testutil.Request(t, handler, http.MethodPost, "/v0/pro/accounts/batch", body, testutil.AdminKey)
	testutil.RequireStatus(t, batchRR, http.StatusOK)
	if !strings.Contains(batchRR.Body.String(), `"succeeded":1`) || !strings.Contains(batchRR.Body.String(), `"failed":1`) || !strings.Contains(batchRR.Body.String(), `"code":"resource_version_conflict"`) {
		t.Fatalf("批量响应 = %s", batchRR.Body.String())
	}
}

func TestProAccountBindingDriftCanBeConfirmedInBatch(t *testing.T) {
	state := &p5GatewayState{files: []map[string]any{{
		"name": "/old/codex.json", "provider": "codex", "auth_index": "auth-old",
		"account": "alpha@example.com", "account_id": "acct-alpha", "disabled": false,
	}}}
	gateway := httptest.NewServer(state)
	t.Cleanup(gateway.Close)
	handler := newTestHandler(t, gateway.URL, true)
	firstSync := testutil.Request(t, handler, http.MethodPost, "/v0/pro/accounts/sync", `{}`, testutil.AdminKey)
	testutil.RequireStatus(t, firstSync, http.StatusOK)
	var first struct {
		Items []struct {
			ID string `json:"proAccountId"`
		} `json:"items"`
	}
	testutil.DecodeJSON(t, firstSync, &first)
	accountID := first.Items[0].ID
	state.mu.Lock()
	state.files[0]["name"] = "/new/codex.json"
	state.files[0]["auth_index"] = "auth-new"
	state.mu.Unlock()
	secondSync := testutil.Request(t, handler, http.MethodPost, "/v0/pro/accounts/sync", `{}`, testutil.AdminKey)
	testutil.RequireStatus(t, secondSync, http.StatusOK)
	if !strings.Contains(secondSync.Body.String(), `"pending":1`) || !strings.Contains(secondSync.Body.String(), `"reasonCode":"file_path_drift_confirmation"`) {
		t.Fatalf("漂移同步 = %s", secondSync.Body.String())
	}
	reviewsRR := testutil.Request(t, handler, http.MethodGet, "/v0/pro/accounts/binding-reviews", "", testutil.AdminKey)
	testutil.RequireStatus(t, reviewsRR, http.StatusOK)
	var reviews struct {
		Items []struct {
			Review struct {
				ID int64 `json:"id"`
			} `json:"review"`
			Candidates []struct {
				ID      string `json:"id"`
				Version int64  `json:"version"`
			} `json:"candidates"`
		} `json:"items"`
	}
	testutil.DecodeJSON(t, reviewsRR, &reviews)
	if len(reviews.Items) != 1 || len(reviews.Items[0].Candidates) != 1 {
		t.Fatalf("待确认绑定 = %s", reviewsRR.Body.String())
	}
	rebindBody := fmt.Sprintf(`{
		"operation_id":"rebind-all","idempotency_key":"rebind-all-key",
		"items":[{"review_id":%d,"pro_account_id":%q,"expected_version":%d}]
	}`, reviews.Items[0].Review.ID, accountID, reviews.Items[0].Candidates[0].Version)
	rebindRR := testutil.Request(t, handler, http.MethodPost, "/v0/pro/accounts/rebind", rebindBody, testutil.AdminKey)
	testutil.RequireStatus(t, rebindRR, http.StatusOK)
	if !strings.Contains(rebindRR.Body.String(), `"succeeded":1`) || !strings.Contains(rebindRR.Body.String(), `"id":"`+accountID+`"`) {
		t.Fatalf("重绑响应 = %s", rebindRR.Body.String())
	}
	listRR := testutil.Request(t, handler, http.MethodGet, "/v0/pro/accounts", "", testutil.AdminKey)
	testutil.RequireStatus(t, listRR, http.StatusOK)
	if !strings.Contains(listRR.Body.String(), `"id":"`+accountID+`"`) || !strings.Contains(listRR.Body.String(), `"authIndex":"auth-new"`) {
		t.Fatalf("重绑后账号 = %s", listRR.Body.String())
	}
}

func TestProAccountOpenAIResetCreditsRequiresConfirmationAndConsumesOnce(t *testing.T) {
	state := &p5GatewayState{resetCredits: 2, files: []map[string]any{{
		"name": "codex.json", "provider": "codex", "auth_index": "auth-codex",
		"account": "alpha@example.com", "account_id": "acct-alpha", "disabled": false,
	}}}
	gateway := httptest.NewServer(state)
	t.Cleanup(gateway.Close)
	handler := newTestHandler(t, gateway.URL, true)
	syncRR := testutil.Request(t, handler, http.MethodPost, "/v0/pro/accounts/sync", `{}`, testutil.AdminKey)
	testutil.RequireStatus(t, syncRR, http.StatusOK)
	var synced struct {
		Items []struct {
			ID string `json:"proAccountId"`
		} `json:"items"`
	}
	testutil.DecodeJSON(t, syncRR, &synced)
	accountID := synced.Items[0].ID
	creditsRR := testutil.Request(t, handler, http.MethodGet, "/v0/pro/accounts/"+accountID+"/openai-reset-credits", "", testutil.AdminKey)
	testutil.RequireStatus(t, creditsRR, http.StatusOK)
	if !strings.Contains(creditsRR.Body.String(), `"capability":"supported"`) || !strings.Contains(creditsRR.Body.String(), `"availableCount":2`) {
		t.Fatalf("重置次数 = %s", creditsRR.Body.String())
	}
	unconfirmedRR := testutil.Request(t, handler, http.MethodPost, "/v0/pro/accounts/"+accountID+"/openai-reset", `{
		"operation_id":"reset-one","idempotency_key":"reset-one-key","expected_version":1,"confirmed":false
	}`, testutil.AdminKey)
	testutil.RequireStatus(t, unconfirmedRR, http.StatusBadRequest)
	resetBody := `{"operation_id":"reset-one","idempotency_key":"reset-one-key","expected_version":1,"confirmed":true}`
	resetRR := testutil.Request(t, handler, http.MethodPost, "/v0/pro/accounts/"+accountID+"/openai-reset", resetBody, testutil.AdminKey)
	testutil.RequireStatus(t, resetRR, http.StatusOK)
	replayRR := testutil.Request(t, handler, http.MethodPost, "/v0/pro/accounts/"+accountID+"/openai-reset", resetBody, testutil.AdminKey)
	testutil.RequireStatus(t, replayRR, http.StatusOK)
	state.mu.Lock()
	defer state.mu.Unlock()
	if state.resetCredits != 1 || state.resetConsumeCalls != 1 {
		t.Fatalf("重置后 credits=%d consumeCalls=%d", state.resetCredits, state.resetConsumeCalls)
	}
}
