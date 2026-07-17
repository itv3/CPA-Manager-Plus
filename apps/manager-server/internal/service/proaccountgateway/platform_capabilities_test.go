package proaccountgateway

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestPlatformCapabilitiesDetectsGeminiOAuthPlugin(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v0/management/plugins" {
			http.NotFound(w, r)
			return
		}
		_, _ = w.Write([]byte(`{
			"plugins_enabled":true,
			"plugins":[{
				"id":"gemini-cli","registered":true,"enabled":true,"effective_enabled":true,
				"supports_oauth":true,"oauth_provider":"gemini-cli","metadata":{"version":"1.0.5"}
			}]
		}`))
	}))
	defer server.Close()

	result, err := New(server.Client()).PlatformCapabilities(context.Background(), server.URL, "management-key")
	if err != nil {
		t.Fatalf("读取平台能力：%v", err)
	}
	if result.GeminiOAuth.Status != CapabilitySupported || result.GeminiOAuth.Version != "1.0.5" {
		t.Fatalf("Gemini OAuth 能力 = %#v", result.GeminiOAuth)
	}
}

func TestGeminiOAuthCapabilityExplainsUnavailableStates(t *testing.T) {
	tests := []struct {
		name   string
		input  pluginCapabilityResponse
		reason string
	}{
		{name: "全局插件关闭", input: pluginCapabilityResponse{}, reason: "plugins_disabled"},
		{name: "插件缺失", input: pluginCapabilityResponse{PluginsEnabled: true}, reason: "gemini_cli_plugin_missing"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			result := geminiOAuthCapability(test.input)
			if result.Status != CapabilityUnsupported || result.ReasonCode != test.reason {
				t.Fatalf("能力结果 = %#v", result)
			}
		})
	}
}
