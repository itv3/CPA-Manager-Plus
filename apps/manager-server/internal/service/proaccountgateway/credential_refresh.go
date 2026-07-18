package proaccountgateway

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"
)

type CredentialRefreshResult struct {
	Status      string    `json:"status"`
	ID          string    `json:"id"`
	AuthIndex   string    `json:"auth_index"`
	Provider    string    `json:"provider"`
	RefreshedAt time.Time `json:"refreshed_at"`
}

func (c *Client) RefreshCredential(ctx context.Context, baseURL string, managementKey string, authIndex string, id string) (CredentialRefreshResult, error) {
	authIndex = strings.TrimSpace(authIndex)
	id = strings.TrimSpace(id)
	if authIndex == "" && id == "" {
		return CredentialRefreshResult{}, errors.New("auth index or id is required")
	}
	raw, _, err := c.requestJSON(ctx, baseURL, managementKey, http.MethodPost, "/v0/management/auth-files/refresh", map[string]string{
		"auth_index": authIndex,
		"id":         id,
	})
	if err != nil {
		return CredentialRefreshResult{}, err
	}
	var result CredentialRefreshResult
	if err := json.Unmarshal(raw, &result); err != nil {
		return CredentialRefreshResult{}, errors.New("gateway credential refresh returned an invalid response")
	}
	if result.Status != "ok" || strings.TrimSpace(result.ID) == "" || strings.TrimSpace(result.AuthIndex) == "" || result.RefreshedAt.IsZero() {
		return CredentialRefreshResult{}, errors.New("gateway credential refresh returned an incomplete response")
	}
	if authIndex != "" && result.AuthIndex != authIndex {
		return CredentialRefreshResult{}, errors.New("gateway credential refresh returned a mismatched auth index")
	}
	if id != "" && result.ID != id {
		return CredentialRefreshResult{}, errors.New("gateway credential refresh returned a mismatched auth id")
	}
	return result, nil
}
