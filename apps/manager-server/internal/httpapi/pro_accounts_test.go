package httpapi

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/testutil"
)

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
