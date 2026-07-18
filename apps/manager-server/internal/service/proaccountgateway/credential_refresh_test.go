package proaccountgateway

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestRefreshCredentialSendsStableSelectorAndDecodesSafeResult(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/v0/management/auth-files/refresh" {
			http.NotFound(w, r)
			return
		}
		if r.Header.Get("Authorization") != "Bearer management-key" {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		var request map[string]string
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil || request["auth_index"] != "stable-index" || request["id"] != "" {
			http.Error(w, "bad request", http.StatusBadRequest)
			return
		}
		_, _ = w.Write([]byte(`{"status":"ok","id":"auth-id","auth_index":"stable-index","provider":"codex","refreshed_at":"2026-07-18T10:00:00Z","access_token":"must-not-be-forwarded"}`))
	}))
	t.Cleanup(server.Close)

	result, err := New(nil).RefreshCredential(context.Background(), server.URL, "management-key", "stable-index", "")
	if err != nil {
		t.Fatalf("RefreshCredential returned error: %v", err)
	}
	if result.ID != "auth-id" || result.AuthIndex != "stable-index" || result.Provider != "codex" || result.RefreshedAt.IsZero() {
		t.Fatalf("unexpected result: %#v", result)
	}
}

func TestRefreshCredentialRejectsMismatchedGatewayIdentity(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"status":"ok","id":"auth-id","auth_index":"different-index","provider":"codex","refreshed_at":"2026-07-18T10:00:00Z"}`))
	}))
	t.Cleanup(server.Close)

	if _, err := New(nil).RefreshCredential(context.Background(), server.URL, "management-key", "stable-index", ""); err == nil {
		t.Fatal("expected mismatched auth index to fail")
	}
}
