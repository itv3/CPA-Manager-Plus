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
