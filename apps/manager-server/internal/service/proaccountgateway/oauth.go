package proaccountgateway

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"strings"
)

func (c *Client) StartOAuth(ctx context.Context, baseURL string, managementKey string, platform string) (OAuthStartResult, error) {
	provider := oauthProvider(platform)
	if provider == "" {
		return OAuthStartResult{}, ErrUnsupportedSource
	}
	query := url.Values{"credential_draft": []string{"true"}, "is_webui": []string{"true"}}
	raw, _, err := c.get(ctx, baseURL, managementKey, "/v0/management/"+provider+"-auth-url?"+query.Encode())
	if err != nil {
		return OAuthStartResult{}, err
	}
	var result OAuthStartResult
	if json.Unmarshal(raw, &result) != nil || strings.TrimSpace(result.URL) == "" || strings.TrimSpace(result.State) == "" {
		return OAuthStartResult{}, errors.New("gateway oauth start returned an invalid response")
	}
	return result, nil
}

func (c *Client) OAuthStatus(ctx context.Context, baseURL string, managementKey string, state string) (OAuthStatusResult, error) {
	query := url.Values{"state": []string{strings.TrimSpace(state)}}
	raw, _, err := c.get(ctx, baseURL, managementKey, "/v0/management/get-auth-status?"+query.Encode())
	if err != nil {
		return OAuthStatusResult{}, err
	}
	var result OAuthStatusResult
	if json.Unmarshal(raw, &result) != nil || result.Status == "" {
		return OAuthStatusResult{}, errors.New("gateway oauth status returned an invalid response")
	}
	return result, nil
}

func (c *Client) SubmitOAuthCallback(ctx context.Context, baseURL string, managementKey string, input OAuthCallbackInput) error {
	provider := oauthProvider(input.Platform)
	if provider == "" {
		return ErrUnsupportedSource
	}
	payload := map[string]string{
		"provider": provider,
		"code":     strings.TrimSpace(input.Code),
		"state":    strings.TrimSpace(input.State),
		"error":    strings.TrimSpace(input.Error),
	}
	_, _, err := c.requestJSON(ctx, baseURL, managementKey, http.MethodPost, "/v0/management/oauth-callback", payload)
	return err
}

func (c *Client) CancelOAuth(ctx context.Context, baseURL string, managementKey string, state string) error {
	query := url.Values{"state": []string{strings.TrimSpace(state)}}
	_, _, err := c.request(ctx, baseURL, managementKey, http.MethodDelete, "/v0/management/oauth-session?"+query.Encode(), nil)
	return err
}

func oauthProvider(platform string) string {
	switch strings.ToLower(strings.TrimSpace(platform)) {
	case "openai":
		return "codex"
	case "anthropic":
		return "anthropic"
	case "gemini":
		return "gemini-cli"
	case "antigravity":
		return "antigravity"
	case "xai":
		return "xai"
	default:
		return ""
	}
}
