package proaccountgateway

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/service/cpaauthfiles"
)

type configEndpoint struct {
	Path        string
	ResponseKey string
	Platform    string
	SourceType  string
	Label       string
}

var configEndpoints = []configEndpoint{
	{Path: "/v0/management/gemini-api-key", ResponseKey: "gemini-api-key", Platform: "gemini", SourceType: SourceGeminiAPIKey, Label: "Gemini API"},
	{Path: "/v0/management/interactions-api-key", ResponseKey: "interactions-api-key", Platform: "openai", SourceType: SourceInteractionsAPIKey, Label: "OpenAI API"},
	{Path: "/v0/management/claude-api-key", ResponseKey: "claude-api-key", Platform: "anthropic", SourceType: SourceClaudeAPIKey, Label: "Anthropic API"},
	{Path: "/v0/management/codex-api-key", ResponseKey: "codex-api-key", Platform: "openai", SourceType: SourceCodexAPIKey, Label: "OpenAI API"},
	{Path: "/v0/management/xai-api-key", ResponseKey: "xai-api-key", Platform: "xai", SourceType: SourceXAIAPIKey, Label: "xAI API"},
	{Path: "/v0/management/vertex-api-key", ResponseKey: "vertex-api-key", Platform: "gemini", SourceType: SourceVertexAPIKey, Label: "Gemini Vertex"},
}

func (c *Client) Snapshot(ctx context.Context, baseURL string, managementKey string) (SnapshotResult, error) {
	result := SnapshotResult{Accounts: make([]AccountSnapshot, 0), Warnings: make([]string, 0)}
	raw, headers, err := c.get(ctx, baseURL, managementKey, "/v0/management/auth-files")
	if err != nil {
		return result, err
	}
	result.Capabilities = capabilitiesFromHeaders(headers)
	files, errParse := cpaauthfiles.Parse(raw)
	if errParse != nil {
		return result, fmt.Errorf("parse gateway auth files: %w", errParse)
	}
	for _, file := range files {
		if account, ok := accountFromAuthFile(file); ok {
			result.Accounts = append(result.Accounts, account)
			if account.ModelRuleVersion != "" {
				result.Capabilities.AllowedModels = true
			}
		}
	}

	for _, endpoint := range configEndpoints {
		accounts, supported, endpointErr := c.snapshotConfigEndpoint(ctx, baseURL, managementKey, endpoint)
		if endpointErr != nil {
			result.Warnings = append(result.Warnings, endpoint.ResponseKey+": "+endpointErr.Error())
			continue
		}
		if supported {
			result.Accounts = append(result.Accounts, accounts...)
		}
	}
	compatAccounts, supported, compatErr := c.snapshotOpenAICompatibility(ctx, baseURL, managementKey)
	if compatErr != nil {
		result.Warnings = append(result.Warnings, "openai-compatibility: "+compatErr.Error())
	} else if supported {
		result.Accounts = append(result.Accounts, compatAccounts...)
	}
	return result, nil
}

func capabilitiesFromHeaders(headers http.Header) Capabilities {
	return Capabilities{
		CredentialDraft: strings.EqualFold(strings.TrimSpace(headers.Get("X-CPA-SUPPORT-CREDENTIAL-DRAFT")), "true"),
		AllowedModels:   strings.EqualFold(strings.TrimSpace(headers.Get("X-CPA-SUPPORT-ALLOWED-MODELS")), "true"),
	}
}

func accountFromAuthFile(file cpaauthfiles.File) (AccountSnapshot, bool) {
	locator := strings.TrimSpace(file.Name)
	if locator == "" || mapBool(file.Raw, "runtime_only", "runtimeOnly", "plugin_virtual", "pluginVirtual") {
		return AccountSnapshot{}, false
	}
	platform := normalizedPlatform(file.Provider, file.Raw)
	email := mapString(file.Raw, "email", "account", "user_email", "userEmail")
	name := mapString(file.Raw, "label", "display_name", "displayName")
	if name == "" {
		name = strings.TrimSpace(file.AccountSnapshot)
	}
	if name == "" {
		name = locator
	}
	health, lastError := normalizedHealth(file.Raw)
	return AccountSnapshot{
		Platform: platform, AuthType: normalizedAuthType(file.Provider, file.Raw),
		SourceType: SourceAuthFile, SourceLocator: locator, Name: name, Email: email,
		Enabled: !file.Disabled, HealthStatus: health, LastError: lastError,
		AuthIndex:         strings.TrimSpace(file.AuthIndex),
		SourceFingerprint: fingerprint(platform, email, file.AccountID, file.AccountSnapshot),
		AllowedModels:     mapStringSlice(file.Raw, "allowed_models", "allowedModels", "allowed-models"),
		ModelMapping:      mapStringMap(file.Raw, "model_mapping", "modelMapping"),
		ModelRuleVersion:  mapString(file.Raw, "model_rule_version", "modelRuleVersion", "model-rule-version"),
		ExpiresAtMS:       mapTimeMS(file.Raw, "expires_at", "expiresAt", "expired_at"),
		BaseURL:           mapString(file.Raw, "base_url", "base-url", "baseUrl"),
		Headers:           mapStringMap(file.Raw, "headers"),
		CredentialDraft:   mapBool(file.Raw, "credential_draft", "credentialDraft", "pro_draft"),
		UpstreamAccountID: strings.TrimSpace(file.AccountID),
	}, true
}

func (c *Client) snapshotConfigEndpoint(ctx context.Context, baseURL string, managementKey string, endpoint configEndpoint) ([]AccountSnapshot, bool, error) {
	raw, _, err := c.get(ctx, baseURL, managementKey, endpoint.Path)
	if err != nil {
		var gatewayErr *GatewayError
		if errors.As(err, &gatewayErr) && gatewayErr.StatusCode == http.StatusNotFound {
			return nil, false, nil
		}
		return nil, true, err
	}
	payload, err := decodeObject(raw)
	if err != nil {
		return nil, true, err
	}
	entries := mapSlice(payload, endpoint.ResponseKey, "items", "data")
	accounts := make([]AccountSnapshot, 0, len(entries))
	for index, entry := range entries {
		excluded := mapStringSlice(entry, "excluded-models", "excluded_models", "excludedModels")
		base := mapString(entry, "base-url", "base_url", "baseUrl")
		name := mapString(entry, "label", "name")
		if name == "" {
			name = configAccountName(endpoint.Label, base, index)
		}
		accounts = append(accounts, AccountSnapshot{
			Platform: endpoint.Platform, AuthType: "api", SourceType: endpoint.SourceType,
			SourceLocator: fmt.Sprintf("index:%d", index), Name: name, Enabled: !containsWildcardAll(excluded),
			HealthStatus: "unknown", AuthIndex: mapString(entry, "auth-index", "auth_index", "authIndex"),
			SourceFingerprint: fingerprint(endpoint.Platform, base, name),
			AllowedModels:     mapStringSlice(entry, "allowed-models", "allowed_models", "allowedModels"),
			ModelMapping:      modelMappingFromList(mapSlice(entry, "models")),
			ModelRuleVersion:  mapString(entry, "model-rule-version", "model_rule_version", "modelRuleVersion"),
			BaseURL:           base,
			Headers:           mapStringMap(entry, "headers"),
		})
	}
	return accounts, true, nil
}

func (c *Client) snapshotOpenAICompatibility(ctx context.Context, baseURL string, managementKey string) ([]AccountSnapshot, bool, error) {
	raw, _, err := c.get(ctx, baseURL, managementKey, "/v0/management/openai-compatibility")
	if err != nil {
		var gatewayErr *GatewayError
		if errors.As(err, &gatewayErr) && gatewayErr.StatusCode == http.StatusNotFound {
			return nil, false, nil
		}
		return nil, true, err
	}
	payload, err := decodeObject(raw)
	if err != nil {
		return nil, true, err
	}
	providers := mapSlice(payload, "openai-compatibility", "items", "data")
	accounts := make([]AccountSnapshot, 0)
	for providerIndex, provider := range providers {
		providerName := mapString(provider, "name")
		base := mapString(provider, "base-url", "base_url", "baseUrl")
		mapping := modelMappingFromList(mapSlice(provider, "models"))
		disabled := mapBool(provider, "disabled")
		keyEntries := mapSlice(provider, "api-key-entries", "api_key_entries", "apiKeyEntries")
		if len(keyEntries) == 0 {
			accounts = append(accounts, AccountSnapshot{
				Platform: "openai", AuthType: "api", SourceType: SourceOpenAICompatibility,
				SourceLocator: fmt.Sprintf("provider:%d:key:none", providerIndex),
				Name:          valueOr(providerName, configAccountName("OpenAI API", base, providerIndex)), Enabled: !disabled,
				HealthStatus: "unknown", AuthIndex: mapString(provider, "auth-index", "auth_index", "authIndex"),
				SourceFingerprint: fingerprint("openai", providerName, base), ModelMapping: mapping,
				ModelRuleVersion: mapString(provider, "model-rule-version", "model_rule_version", "modelRuleVersion"),
				SharedProvider:   true,
				BaseURL:          base,
				Headers:          mapStringMap(provider, "headers"),
			})
			continue
		}
		for keyIndex, keyEntry := range keyEntries {
			accounts = append(accounts, AccountSnapshot{
				Platform: "openai", AuthType: "api", SourceType: SourceOpenAICompatibility,
				SourceLocator: fmt.Sprintf("provider:%d:key:%d", providerIndex, keyIndex),
				Name:          valueOr(providerName, configAccountName("OpenAI API", base, keyIndex)), Enabled: !disabled,
				HealthStatus: "unknown", AuthIndex: mapString(keyEntry, "auth-index", "auth_index", "authIndex"),
				SourceFingerprint: fingerprint("openai", providerName, base),
				AllowedModels:     mapStringSlice(keyEntry, "allowed-models", "allowed_models", "allowedModels"),
				ModelMapping:      mapping,
				ModelRuleVersion:  mapString(keyEntry, "model-rule-version", "model_rule_version", "modelRuleVersion"),
				SharedProvider:    true,
				BaseURL:           base,
				Headers:           mapStringMap(provider, "headers"),
			})
		}
	}
	return accounts, true, nil
}

func decodeObject(raw []byte) (map[string]any, error) {
	payload := map[string]any{}
	decoder := json.NewDecoder(strings.NewReader(string(raw)))
	decoder.UseNumber()
	if err := decoder.Decode(&payload); err != nil {
		return nil, fmt.Errorf("decode gateway response: %w", err)
	}
	return payload, nil
}

func mapSlice(raw map[string]any, keys ...string) []map[string]any {
	for _, key := range keys {
		value, ok := raw[key]
		if !ok {
			continue
		}
		switch typed := value.(type) {
		case []any:
			result := make([]map[string]any, 0, len(typed))
			for _, item := range typed {
				if object, okObject := item.(map[string]any); okObject {
					result = append(result, object)
				}
			}
			return result
		case []map[string]any:
			return typed
		}
	}
	return nil
}

func mapString(raw map[string]any, keys ...string) string {
	for _, key := range keys {
		if value, ok := raw[key]; ok && value != nil {
			if text, okString := value.(string); okString {
				return strings.TrimSpace(text)
			}
		}
	}
	return ""
}

func mapBool(raw map[string]any, keys ...string) bool {
	for _, key := range keys {
		if value, ok := raw[key]; ok {
			switch typed := value.(type) {
			case bool:
				return typed
			case string:
				parsed, _ := strconv.ParseBool(strings.TrimSpace(typed))
				return parsed
			}
		}
	}
	return false
}

func mapStringSlice(raw map[string]any, keys ...string) []string {
	result := make([]string, 0)
	seen := map[string]struct{}{}
	for _, key := range keys {
		value, ok := raw[key]
		if !ok {
			continue
		}
		switch typed := value.(type) {
		case []any:
			for _, item := range typed {
				appendUnique(&result, seen, fmt.Sprint(item))
			}
		case []string:
			for _, item := range typed {
				appendUnique(&result, seen, item)
			}
		case string:
			for _, item := range strings.Split(typed, ",") {
				appendUnique(&result, seen, item)
			}
		}
	}
	return result
}

func mapStringMap(raw map[string]any, keys ...string) map[string]string {
	result := map[string]string{}
	for _, key := range keys {
		value, ok := raw[key]
		if !ok {
			continue
		}
		if typed, okMap := value.(map[string]any); okMap {
			for source, target := range typed {
				if source = strings.TrimSpace(source); source != "" {
					result[source] = strings.TrimSpace(fmt.Sprint(target))
				}
			}
		}
	}
	return result
}

func modelMappingFromList(models []map[string]any) map[string]string {
	result := map[string]string{}
	for _, current := range models {
		name := mapString(current, "name")
		alias := mapString(current, "alias")
		if alias == "" {
			alias = name
		}
		if alias != "" && name != "" {
			result[alias] = name
		}
	}
	return result
}

func normalizedPlatform(provider string, raw map[string]any) string {
	value := strings.ToLower(strings.TrimSpace(provider))
	if value == "" {
		value = strings.ToLower(mapString(raw, "provider", "type"))
	}
	switch {
	case strings.Contains(value, "codex"), strings.Contains(value, "openai"):
		return "openai"
	case strings.Contains(value, "claude"), strings.Contains(value, "anthropic"):
		return "anthropic"
	case strings.Contains(value, "gemini"), strings.Contains(value, "vertex"):
		return "gemini"
	case strings.Contains(value, "antigravity"):
		return "antigravity"
	case strings.Contains(value, "xai"), strings.Contains(value, "grok"):
		return "xai"
	case value != "":
		return value
	default:
		return "unknown"
	}
}

func normalizedAuthType(provider string, raw map[string]any) string {
	value := strings.ToLower(mapString(raw, "auth_type", "authType", "credential_type", "credentialType"))
	if value == "" {
		value = strings.ToLower(strings.TrimSpace(provider))
	}
	switch {
	case strings.Contains(value, "vertex"), strings.Contains(value, "service_account"):
		return "vertex"
	case strings.Contains(value, "api-key"), strings.Contains(value, "api_key"), strings.Contains(value, "apikey"):
		return "api"
	default:
		return "oauth"
	}
}

func normalizedHealth(raw map[string]any) (string, string) {
	lastError := mapString(raw, "last_error", "lastError", "error")
	status := strings.ToLower(mapString(raw, "status", "state"))
	switch {
	case lastError != "", status == "error", status == "invalid", status == "expired":
		return "error", lastError
	case status == "active", status == "healthy", status == "ok":
		return "healthy", ""
	default:
		return "unknown", lastError
	}
}

func mapTimeMS(raw map[string]any, keys ...string) int64 {
	for _, key := range keys {
		value, ok := raw[key]
		if !ok || value == nil {
			continue
		}
		switch typed := value.(type) {
		case json.Number:
			if parsed, err := typed.Int64(); err == nil {
				return normalizeUnixMS(parsed)
			}
		case float64:
			return normalizeUnixMS(int64(typed))
		case string:
			if parsed, err := strconv.ParseInt(strings.TrimSpace(typed), 10, 64); err == nil {
				return normalizeUnixMS(parsed)
			}
			if parsed, err := time.Parse(time.RFC3339, strings.TrimSpace(typed)); err == nil {
				return parsed.UnixMilli()
			}
		}
	}
	return 0
}

func normalizeUnixMS(value int64) int64 {
	if value <= 0 {
		return 0
	}
	if value < 10_000_000_000 {
		return value * 1000
	}
	return value
}

func configAccountName(label string, baseURL string, index int) string {
	host := ""
	if parsed, err := url.Parse(strings.TrimSpace(baseURL)); err == nil {
		host = parsed.Hostname()
	}
	if host != "" {
		return fmt.Sprintf("%s · %s · #%d", label, host, index+1)
	}
	return fmt.Sprintf("%s #%d", label, index+1)
}

func fingerprint(parts ...string) string {
	normalized := make([]string, 0, len(parts))
	hasValue := false
	for _, part := range parts {
		part = strings.ToLower(strings.TrimSpace(part))
		if part != "" {
			hasValue = true
		}
		normalized = append(normalized, part)
	}
	if !hasValue {
		return ""
	}
	sum := sha256.Sum256([]byte(strings.Join(normalized, "\x00")))
	return hex.EncodeToString(sum[:16])
}

func containsWildcardAll(values []string) bool {
	for _, value := range values {
		if strings.TrimSpace(value) == "*" {
			return true
		}
	}
	return false
}

func appendUnique(target *[]string, seen map[string]struct{}, value string) {
	value = strings.TrimSpace(value)
	if value == "" {
		return
	}
	key := strings.ToLower(value)
	if _, exists := seen[key]; exists {
		return
	}
	seen[key] = struct{}{}
	*target = append(*target, value)
}

func valueOr(value string, fallback string) string {
	if strings.TrimSpace(value) == "" {
		return fallback
	}
	return value
}
