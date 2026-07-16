package proaccountgateway

import (
	"context"
	"errors"
	"strings"

	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/service/cpaauthfiles"
)

func (c *Client) ResolveAccountRuntime(ctx context.Context, baseURL string, managementKey string, sourceType string, sourceLocator string) (AccountRuntime, error) {
	switch sourceType {
	case SourceAuthFile:
		raw, _, err := c.get(ctx, baseURL, managementKey, "/v0/management/auth-files")
		if err != nil {
			return AccountRuntime{}, err
		}
		files, err := cpaauthfiles.Parse(raw)
		if err != nil {
			return AccountRuntime{}, err
		}
		file, ok := cpaauthfiles.Find(files, sourceLocator, "")
		if !ok {
			return AccountRuntime{}, ErrGatewayAccountNotFound
		}
		platform := normalizedPlatform(file.Provider, file.Raw)
		return AccountRuntime{
			Platform: platform,
			BaseURL:  valueOr(mapString(file.Raw, "base_url", "base-url", "baseUrl"), defaultBaseURL(platform, sourceType)),
			Headers:  mapStringMap(file.Raw, "headers"),
		}, nil
	case SourceOpenAICompatibility:
		providerIndex, _, err := parseOpenAICompatibilityLocator(sourceLocator)
		if err != nil {
			return AccountRuntime{}, err
		}
		raw, _, err := c.get(ctx, baseURL, managementKey, "/v0/management/openai-compatibility")
		if err != nil {
			return AccountRuntime{}, err
		}
		payload, err := decodeObject(raw)
		if err != nil {
			return AccountRuntime{}, err
		}
		providers := mapSlice(payload, "openai-compatibility", "items", "data")
		if providerIndex >= len(providers) {
			return AccountRuntime{}, ErrGatewayAccountNotFound
		}
		provider := providers[providerIndex]
		return AccountRuntime{Platform: "openai", BaseURL: mapString(provider, "base-url", "base_url", "baseUrl"), Headers: mapStringMap(provider, "headers")}, nil
	default:
		endpoint, ok := endpointForSource(sourceType)
		if !ok {
			return AccountRuntime{}, ErrUnsupportedSource
		}
		index, err := parseIndexLocator(sourceLocator)
		if err != nil {
			return AccountRuntime{}, err
		}
		raw, _, err := c.get(ctx, baseURL, managementKey, endpoint.Path)
		if err != nil {
			return AccountRuntime{}, err
		}
		payload, err := decodeObject(raw)
		if err != nil {
			return AccountRuntime{}, err
		}
		entries := mapSlice(payload, endpoint.ResponseKey, "items", "data")
		if index >= len(entries) {
			return AccountRuntime{}, ErrGatewayAccountNotFound
		}
		entry := entries[index]
		return AccountRuntime{
			Platform: endpoint.Platform,
			BaseURL:  valueOr(mapString(entry, "base-url", "base_url", "baseUrl"), defaultBaseURL(endpoint.Platform, sourceType)),
			Headers:  mapStringMap(entry, "headers"),
		}, nil
	}
}

func defaultBaseURL(platform string, sourceType string) string {
	switch strings.ToLower(strings.TrimSpace(platform)) {
	case "openai":
		if sourceType == SourceAuthFile {
			return "https://chatgpt.com/backend-api/codex"
		}
		return "https://api.openai.com/v1"
	case "anthropic":
		return "https://api.anthropic.com/v1"
	case "gemini":
		return "https://generativelanguage.googleapis.com/v1beta"
	case "xai":
		return "https://api.x.ai/v1"
	default:
		return ""
	}
}

func joinAPIPath(baseURL string, suffix string) (string, error) {
	baseURL = strings.TrimRight(strings.TrimSpace(baseURL), "/")
	suffix = "/" + strings.TrimLeft(strings.TrimSpace(suffix), "/")
	if baseURL == "" {
		return "", errors.New("account base url is unavailable")
	}
	return baseURL + suffix, nil
}
