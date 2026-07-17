package proaccountgateway

import (
	"context"
	"encoding/json"
	"strings"
)

const (
	CapabilitySupported   = "supported"
	CapabilityUnsupported = "unsupported"
	CapabilityUnknown     = "unknown"
)

type pluginCapabilityResponse struct {
	PluginsEnabled bool `json:"plugins_enabled"`
	Plugins        []struct {
		ID               string `json:"id"`
		Registered       bool   `json:"registered"`
		Enabled          bool   `json:"enabled"`
		EffectiveEnabled bool   `json:"effective_enabled"`
		SupportsOAuth    bool   `json:"supports_oauth"`
		OAuthProvider    string `json:"oauth_provider"`
		Metadata         *struct {
			Version string `json:"version"`
		} `json:"metadata"`
	} `json:"plugins"`
}

// PlatformCapabilities 读取依赖插件的账号添加能力，不把插件缺失误报为可用。
func (c *Client) PlatformCapabilities(ctx context.Context, baseURL string, managementKey string) (PlatformCapabilities, error) {
	raw, _, err := c.get(ctx, baseURL, managementKey, "/v0/management/plugins")
	if err != nil {
		return PlatformCapabilities{}, err
	}
	var payload pluginCapabilityResponse
	if err := json.Unmarshal(raw, &payload); err != nil {
		return PlatformCapabilities{}, err
	}
	return PlatformCapabilities{GeminiOAuth: geminiOAuthCapability(payload)}, nil
}

func geminiOAuthCapability(payload pluginCapabilityResponse) AuthCapability {
	if !payload.PluginsEnabled {
		return AuthCapability{Status: CapabilityUnsupported, ReasonCode: "plugins_disabled", Provider: "gemini-cli"}
	}
	for _, plugin := range payload.Plugins {
		id := strings.TrimSpace(plugin.ID)
		provider := strings.TrimSpace(plugin.OAuthProvider)
		if !strings.EqualFold(id, "gemini-cli") && !strings.EqualFold(provider, "gemini-cli") {
			continue
		}
		capability := AuthCapability{PluginID: id, Provider: valueOr(provider, "gemini-cli")}
		if plugin.Metadata != nil {
			capability.Version = strings.TrimSpace(plugin.Metadata.Version)
		}
		switch {
		case !plugin.Registered:
			capability.Status = CapabilityUnsupported
			capability.ReasonCode = "plugin_not_registered"
		case !plugin.SupportsOAuth || !strings.EqualFold(capability.Provider, "gemini-cli"):
			capability.Status = CapabilityUnsupported
			capability.ReasonCode = "plugin_oauth_unavailable"
		case !plugin.Enabled:
			capability.Status = CapabilityUnsupported
			capability.ReasonCode = "plugin_disabled"
		case !plugin.EffectiveEnabled:
			capability.Status = CapabilityUnsupported
			capability.ReasonCode = "plugin_not_effective"
		default:
			capability.Status = CapabilitySupported
		}
		return capability
	}
	return AuthCapability{Status: CapabilityUnsupported, ReasonCode: "gemini_cli_plugin_missing", Provider: "gemini-cli"}
}
