package httpapi

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"

	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/testutil"
)

type credentialRefreshGatewayState struct {
	mu        sync.Mutex
	requested map[string]string
}

func (s *credentialRefreshGatewayState) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Header.Get("Authorization") != "Bearer management-key" {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	switch r.URL.Path {
	case "/v0/management/auth-files":
		_, _ = w.Write([]byte(`{"files":[{"name":"codex-account.json","provider":"codex","auth_index":"stable-codex-index","account":"owner@example.com","account_id":"upstream-account","status":"active","disabled":false}]}`))
	case "/v0/management/auth-files/refresh":
		var request map[string]string
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			http.Error(w, "bad request", http.StatusBadRequest)
			return
		}
		s.mu.Lock()
		s.requested = request
		s.mu.Unlock()
		_, _ = w.Write([]byte(`{"status":"ok","id":"gateway-auth-id","auth_index":"stable-codex-index","provider":"codex","refreshed_at":"2026-07-18T10:00:00Z"}`))
	default:
		http.NotFound(w, r)
	}
}

func TestProAccountRefreshTokenUsesStableAuthIndexAndLifecycleResult(t *testing.T) {
	state := &credentialRefreshGatewayState{}
	gateway := httptest.NewServer(state)
	t.Cleanup(gateway.Close)
	handler := newTestHandler(t, gateway.URL, true)

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

	body := fmt.Sprintf(`{"operation_id":"refresh-token-operation","idempotency_key":"refresh-token-key","expected_version":%d}`, list.Items[0].Version)
	refreshRR := testutil.Request(t, handler, http.MethodPost, "/v0/pro/accounts/"+list.Items[0].ID+"/refresh-token", body, testutil.AdminKey)
	testutil.RequireStatus(t, refreshRR, http.StatusOK)
	var result struct {
		Account struct {
			ID string `json:"id"`
		} `json:"account"`
		Operation struct {
			State string `json:"state"`
		} `json:"operation"`
		CredentialRefresh struct {
			AuthIndex string `json:"auth_index"`
			Provider  string `json:"provider"`
		} `json:"credentialRefresh"`
	}
	testutil.DecodeJSON(t, refreshRR, &result)
	if result.Account.ID != list.Items[0].ID || result.Operation.State != "enabled" || result.CredentialRefresh.AuthIndex != "stable-codex-index" || result.CredentialRefresh.Provider != "codex" {
		t.Fatalf("unexpected refresh result: %s", refreshRR.Body.String())
	}
	state.mu.Lock()
	defer state.mu.Unlock()
	if state.requested["auth_index"] != "stable-codex-index" || state.requested["id"] != "" {
		t.Fatalf("unexpected gateway selector: %#v", state.requested)
	}
}
