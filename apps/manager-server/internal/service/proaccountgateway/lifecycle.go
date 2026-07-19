package proaccountgateway

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

var ErrCredentialNotReady = errors.New("gateway credential is not ready")

func (c *Client) CreateDisabledAPI(ctx context.Context, baseURL string, managementKey string, input CreateAPIInput) (AccountSnapshot, error) {
	input.Platform = strings.ToLower(strings.TrimSpace(input.Platform))
	input.SourceType = strings.TrimSpace(input.SourceType)
	input.APIKey = strings.TrimSpace(input.APIKey)
	input.BaseURL = strings.TrimRight(strings.TrimSpace(input.BaseURL), "/")
	input.ProxyURL = strings.TrimSpace(input.ProxyURL)
	if input.APIKey == "" || input.BaseURL == "" {
		return AccountSnapshot{}, errors.New("api key and base url are required")
	}
	rules, err := NormalizeModelRules(ModelRules{AllowedModels: input.AllowedModels, ModelMapping: input.ModelMapping})
	if err != nil {
		return AccountSnapshot{}, err
	}
	models := modelListForRules(nil, rules, input.CatalogModels)
	if input.OfficialClientCompatibility != nil {
		if !SupportsOfficialClientCompatibility(input.SourceType) {
			return AccountSnapshot{}, ErrOfficialClientCompatibilityUnsupported
		}
		capabilities, capabilityErr := c.Capabilities(ctx, baseURL, managementKey)
		if capabilityErr != nil {
			return AccountSnapshot{}, capabilityErr
		}
		if !capabilities.OfficialClientCompatibility {
			return AccountSnapshot{}, ErrOfficialClientCompatibilityUnsupported
		}
	}
	locator := ""
	if input.SourceType == SourceOpenAICompatibility {
		providers, err := c.loadConfigEntries(ctx, baseURL, managementKey, "/v0/management/openai-compatibility", "openai-compatibility")
		if err != nil {
			return AccountSnapshot{}, err
		}
		providerIndex := len(providers)
		name := strings.TrimSpace(input.Name)
		if name == "" {
			name = "pro-openai-" + strconv.FormatInt(time.Now().UnixNano(), 36)
		}
		keyEntry := map[string]any{"api-key": input.APIKey, "allowed-models": rules.AllowedModels}
		if input.ProxyURL != "" {
			keyEntry["proxy-url"] = input.ProxyURL
		}
		providers = append(providers, map[string]any{
			"name": name, "base-url": input.BaseURL, "disabled": true,
			"api-key-entries": []map[string]any{keyEntry},
			"models":          models, "headers": cloneHeaders(input.Headers),
		})
		if _, _, err := c.requestJSON(ctx, baseURL, managementKey, http.MethodPut, "/v0/management/openai-compatibility", stripReadOnlyList(providers)); err != nil {
			return AccountSnapshot{}, err
		}
		locator = fmt.Sprintf("provider:%d:key:0", providerIndex)
	} else {
		endpoint, ok := endpointForSource(input.SourceType)
		if !ok || input.SourceType == SourceVertexAPIKey {
			return AccountSnapshot{}, ErrUnsupportedSource
		}
		entries, err := c.loadConfigEntries(ctx, baseURL, managementKey, endpoint.Path, endpoint.ResponseKey)
		if err != nil {
			return AccountSnapshot{}, err
		}
		index := len(entries)
		entry := map[string]any{
			"api-key": input.APIKey, "base-url": input.BaseURL, "headers": cloneHeaders(input.Headers),
			"excluded-models": []string{"*"}, "allowed-models": rules.AllowedModels, "models": models,
		}
		if input.ProxyURL != "" {
			entry["proxy-url"] = input.ProxyURL
		}
		if input.OfficialClientCompatibility != nil {
			entry["official-client-compatibility"] = officialClientCompatibilityPayload(*input.OfficialClientCompatibility)
		}
		entries = append(entries, entry)
		if _, _, err := c.requestJSON(ctx, baseURL, managementKey, http.MethodPut, endpoint.Path, stripReadOnlyList(entries)); err != nil {
			return AccountSnapshot{}, err
		}
		locator = fmt.Sprintf("index:%d", index)
	}
	if input.OfficialClientCompatibility != nil {
		applied, readbackErr := c.waitForOfficialClientCompatibility(ctx, baseURL, managementKey, input.SourceType, locator, *input.OfficialClientCompatibility)
		if readbackErr != nil {
			return AccountSnapshot{
				Platform: input.Platform, AuthType: "api", SourceType: input.SourceType,
				SourceLocator: locator, Name: strings.TrimSpace(input.Name), Enabled: false,
				BaseURL: input.BaseURL,
			}, readbackErr
		}
		input.OfficialClientCompatibility = &applied
	}
	snapshot, waitErr := c.waitForAccount(ctx, baseURL, managementKey, input.SourceType, locator, false)
	if waitErr != nil && snapshot.SourceLocator == "" {
		snapshot = AccountSnapshot{
			Platform: input.Platform, AuthType: "api", SourceType: input.SourceType,
			SourceLocator: locator, Name: strings.TrimSpace(input.Name), Enabled: false,
			BaseURL: input.BaseURL,
		}
	}
	return snapshot, waitErr
}

func (c *Client) SetAccountEnabled(ctx context.Context, baseURL string, managementKey string, sourceType string, sourceLocator string, enabled bool) (AccountSnapshot, error) {
	switch sourceType {
	case SourceAuthFile:
		_, _, err := c.requestJSON(ctx, baseURL, managementKey, http.MethodPatch, "/v0/management/auth-files/status", map[string]any{
			"name": sourceLocator, "disabled": !enabled,
		})
		if err != nil {
			return AccountSnapshot{}, err
		}
	case SourceOpenAICompatibility:
		providerIndex, _, err := parseOpenAICompatibilityLocator(sourceLocator)
		if err != nil {
			return AccountSnapshot{}, err
		}
		_, _, err = c.requestJSON(ctx, baseURL, managementKey, http.MethodPatch, "/v0/management/openai-compatibility", map[string]any{
			"index": providerIndex, "value": map[string]any{"disabled": !enabled},
		})
		if err != nil {
			return AccountSnapshot{}, err
		}
	default:
		endpoint, ok := endpointForSource(sourceType)
		if !ok {
			return AccountSnapshot{}, ErrUnsupportedSource
		}
		index, err := parseIndexLocator(sourceLocator)
		if err != nil {
			return AccountSnapshot{}, err
		}
		entries, err := c.loadConfigEntries(ctx, baseURL, managementKey, endpoint.Path, endpoint.ResponseKey)
		if err != nil || index >= len(entries) {
			if err == nil {
				err = ErrGatewayAccountNotFound
			}
			return AccountSnapshot{}, err
		}
		excluded := mapStringSlice(entries[index], "excluded-models", "excluded_models", "excludedModels")
		if enabled {
			excluded = removeWildcardAll(excluded)
		} else if !containsWildcardAll(excluded) {
			excluded = append(excluded, "*")
		}
		_, _, err = c.requestJSON(ctx, baseURL, managementKey, http.MethodPatch, endpoint.Path, map[string]any{
			"index": index, "value": map[string]any{"excluded-models": excluded},
		})
		if err != nil {
			return AccountSnapshot{}, err
		}
	}
	return c.waitForAccount(ctx, baseURL, managementKey, sourceType, sourceLocator, enabled)
}

// FinalizeCredentialDraft 原子清除认证文件草稿标记，并直接写入最终启用状态。
func (c *Client) FinalizeCredentialDraft(ctx context.Context, baseURL string, managementKey string, sourceType string, sourceLocator string, enabled bool) (AccountSnapshot, error) {
	if sourceType != SourceAuthFile {
		return AccountSnapshot{}, ErrUnsupportedSource
	}
	_, _, err := c.requestJSON(ctx, baseURL, managementKey, http.MethodPatch, "/v0/management/auth-files/status", map[string]any{
		"name": sourceLocator, "disabled": !enabled, "finalize_draft": true,
	})
	if err != nil {
		return AccountSnapshot{}, err
	}
	return c.waitForAccount(ctx, baseURL, managementKey, sourceType, sourceLocator, enabled)
}

func (c *Client) DeleteAccount(ctx context.Context, baseURL string, managementKey string, sourceType string, sourceLocator string) error {
	switch sourceType {
	case SourceAuthFile:
		query := url.Values{"name": []string{sourceLocator}}
		_, _, err := c.request(ctx, baseURL, managementKey, http.MethodDelete, "/v0/management/auth-files?"+query.Encode(), nil)
		return err
	case SourceOpenAICompatibility:
		providerIndex, keyIndex, err := parseOpenAICompatibilityLocator(sourceLocator)
		if err != nil {
			return err
		}
		providers, err := c.loadConfigEntries(ctx, baseURL, managementKey, "/v0/management/openai-compatibility", "openai-compatibility")
		if err != nil || providerIndex >= len(providers) {
			if err == nil {
				err = ErrGatewayAccountNotFound
			}
			return err
		}
		keys := mapSlice(providers[providerIndex], "api-key-entries", "api_key_entries", "apiKeyEntries")
		if keyIndex >= 0 && len(keys) > 1 {
			if keyIndex >= len(keys) {
				return ErrGatewayAccountNotFound
			}
			keys = append(keys[:keyIndex], keys[keyIndex+1:]...)
			_, _, err = c.requestJSON(ctx, baseURL, managementKey, http.MethodPatch, "/v0/management/openai-compatibility", map[string]any{
				"index": providerIndex, "value": map[string]any{"api-key-entries": stripReadOnlyList(keys)},
			})
			return err
		}
		query := url.Values{"index": []string{strconv.Itoa(providerIndex)}}
		_, _, err = c.request(ctx, baseURL, managementKey, http.MethodDelete, "/v0/management/openai-compatibility?"+query.Encode(), nil)
		return err
	default:
		endpoint, ok := endpointForSource(sourceType)
		if !ok {
			return ErrUnsupportedSource
		}
		index, err := parseIndexLocator(sourceLocator)
		if err != nil {
			return err
		}
		query := url.Values{"index": []string{strconv.Itoa(index)}}
		_, _, err = c.request(ctx, baseURL, managementKey, http.MethodDelete, endpoint.Path+"?"+query.Encode(), nil)
		return err
	}
}

func (c *Client) UpdateDisabledAPI(ctx context.Context, baseURL string, managementKey string, sourceType string, sourceLocator string, input UpdateAPIInput) (AccountSnapshot, error) {
	if sourceType == SourceAuthFile {
		return AccountSnapshot{}, ErrUnsupportedSource
	}
	value := map[string]any{}
	if key := strings.TrimSpace(input.APIKey); key != "" {
		value["api-key"] = key
	}
	if input.BaseURL != nil {
		value["base-url"] = strings.TrimRight(strings.TrimSpace(*input.BaseURL), "/")
	}
	if input.ProxyURL != nil {
		value["proxy-url"] = strings.TrimSpace(*input.ProxyURL)
	}
	if input.Headers != nil {
		value["headers"] = cloneHeaders(*input.Headers)
	}
	if sourceType == SourceOpenAICompatibility {
		providerIndex, keyIndex, err := parseOpenAICompatibilityLocator(sourceLocator)
		if err != nil || keyIndex < 0 {
			return AccountSnapshot{}, ErrInvalidSourceLocator
		}
		providers, err := c.loadConfigEntries(ctx, baseURL, managementKey, "/v0/management/openai-compatibility", "openai-compatibility")
		if err != nil || providerIndex >= len(providers) {
			if err == nil {
				err = ErrGatewayAccountNotFound
			}
			return AccountSnapshot{}, err
		}
		keys := mapSlice(providers[providerIndex], "api-key-entries", "api_key_entries", "apiKeyEntries")
		if keyIndex >= len(keys) {
			return AccountSnapshot{}, ErrGatewayAccountNotFound
		}
		if key, ok := value["api-key"]; ok {
			keys[keyIndex]["api-key"] = key
		}
		// openai-compatibility 的代理属于 Key 级配置,写入对应 api-key-entry
		if proxyValue, ok := value["proxy-url"]; ok {
			keys[keyIndex]["proxy-url"] = proxyValue
		}
		patch := map[string]any{"disabled": true, "api-key-entries": stripReadOnlyList(keys)}
		if baseValue, ok := value["base-url"]; ok {
			patch["base-url"] = baseValue
		}
		if headers, ok := value["headers"]; ok {
			patch["headers"] = headers
		}
		_, _, err = c.requestJSON(ctx, baseURL, managementKey, http.MethodPatch, "/v0/management/openai-compatibility", map[string]any{"index": providerIndex, "value": patch})
		if err != nil {
			return AccountSnapshot{}, err
		}
	} else {
		endpoint, ok := endpointForSource(sourceType)
		if !ok {
			return AccountSnapshot{}, ErrUnsupportedSource
		}
		index, err := parseIndexLocator(sourceLocator)
		if err != nil {
			return AccountSnapshot{}, err
		}
		value["excluded-models"] = []string{"*"}
		_, _, err = c.requestJSON(ctx, baseURL, managementKey, http.MethodPatch, endpoint.Path, map[string]any{"index": index, "value": value})
		if err != nil {
			return AccountSnapshot{}, err
		}
	}
	return c.waitForAccount(ctx, baseURL, managementKey, sourceType, sourceLocator, false)
}

// UpdateAccountProxy 仅更新账号级代理,不重建凭证、不改变启停状态。
// auth-index 不包含代理信息,因此该操作不会导致绑定漂移。
func (c *Client) UpdateAccountProxy(ctx context.Context, baseURL string, managementKey string, sourceType string, sourceLocator string, proxyURL string) error {
	proxyURL = strings.TrimSpace(proxyURL)
	switch sourceType {
	case SourceAuthFile:
		_, _, err := c.requestJSON(ctx, baseURL, managementKey, http.MethodPatch, "/v0/management/auth-files/fields", map[string]any{
			"name": sourceLocator, "proxy_url": proxyURL,
		})
		return err
	case SourceOpenAICompatibility:
		providerIndex, keyIndex, err := parseOpenAICompatibilityLocator(sourceLocator)
		if err != nil || keyIndex < 0 {
			return ErrInvalidSourceLocator
		}
		providers, err := c.loadConfigEntries(ctx, baseURL, managementKey, "/v0/management/openai-compatibility", "openai-compatibility")
		if err != nil || providerIndex >= len(providers) {
			if err == nil {
				err = ErrGatewayAccountNotFound
			}
			return err
		}
		keys := mapSlice(providers[providerIndex], "api-key-entries", "api_key_entries", "apiKeyEntries")
		if keyIndex >= len(keys) {
			return ErrGatewayAccountNotFound
		}
		keys[keyIndex]["proxy-url"] = proxyURL
		_, _, err = c.requestJSON(ctx, baseURL, managementKey, http.MethodPatch, "/v0/management/openai-compatibility", map[string]any{
			"index": providerIndex, "value": map[string]any{"api-key-entries": stripReadOnlyList(keys)},
		})
		return err
	default:
		endpoint, ok := endpointForSource(sourceType)
		if !ok {
			return ErrUnsupportedSource
		}
		index, err := parseIndexLocator(sourceLocator)
		if err != nil {
			return err
		}
		_, _, err = c.requestJSON(ctx, baseURL, managementKey, http.MethodPatch, endpoint.Path, map[string]any{
			"index": index, "value": map[string]any{"proxy-url": proxyURL},
		})
		return err
	}
}

func (c *Client) EditableAccount(ctx context.Context, baseURL string, managementKey string, sourceType string, sourceLocator string) (EditableAccount, error) {
	runtime, err := c.ResolveAccountRuntime(ctx, baseURL, managementKey, sourceType, sourceLocator)
	if err != nil {
		return EditableAccount{}, err
	}
	result := EditableAccount{BaseURL: runtime.BaseURL, ProxyURL: runtime.ProxyURL, Headers: editableHeaders(runtime.Headers), SharedProvider: sourceType == SourceOpenAICompatibility}
	if !SupportsOfficialClientCompatibility(sourceType) {
		return result, nil
	}
	capabilities, err := c.Capabilities(ctx, baseURL, managementKey)
	if err != nil {
		return EditableAccount{}, err
	}
	result.OfficialClientCompatibilitySupported = capabilities.OfficialClientCompatibility
	if !result.OfficialClientCompatibilitySupported {
		return result, nil
	}
	compatibility, err := c.ReadOfficialClientCompatibility(ctx, baseURL, managementKey, sourceType, sourceLocator)
	if err != nil {
		return EditableAccount{}, err
	}
	result.OfficialClientCompatibility = &compatibility
	return result, nil
}

func editableHeaders(headers map[string]string) map[string]string {
	result := make(map[string]string, len(headers))
	for key, value := range headers {
		normalized := strings.ToLower(strings.TrimSpace(key))
		switch normalized {
		case "authorization", "proxy-authorization", "x-api-key", "api-key", "x-goog-api-key", "anthropic-api-key", "cookie", "set-cookie":
			continue
		}
		if normalized != "" {
			result[key] = value
		}
	}
	return result
}

func (c *Client) FindAccountByAuthIndex(ctx context.Context, baseURL string, managementKey string, authIndex string) (AccountSnapshot, error) {
	authIndex = strings.TrimSpace(authIndex)
	if authIndex == "" {
		return AccountSnapshot{}, ErrGatewayAccountNotFound
	}
	result, err := c.Snapshot(ctx, baseURL, managementKey)
	if err != nil {
		return AccountSnapshot{}, err
	}
	for _, account := range result.Accounts {
		if account.AuthIndex == authIndex {
			return account, nil
		}
	}
	return AccountSnapshot{}, ErrGatewayAccountNotFound
}

// ProjectedLocatorAfterDelete 计算删除旧配置后替换凭证的稳定定位。
// 配置列表使用数组下标作为 Management API 定位，因此删除较早的元素时必须提前修正新定位。
func ProjectedLocatorAfterDelete(accounts []AccountSnapshot, oldAccount AccountSnapshot, replacement AccountSnapshot) (string, error) {
	if oldAccount.SourceType != replacement.SourceType || oldAccount.SourceType == SourceAuthFile {
		return replacement.SourceLocator, nil
	}
	if oldAccount.SourceType != SourceOpenAICompatibility {
		oldIndex, err := parseIndexLocator(oldAccount.SourceLocator)
		if err != nil {
			return "", err
		}
		replacementIndex, err := parseIndexLocator(replacement.SourceLocator)
		if err != nil {
			return "", err
		}
		if oldIndex < replacementIndex {
			replacementIndex--
		}
		return fmt.Sprintf("index:%d", replacementIndex), nil
	}

	oldProvider, oldKey, err := parseOpenAICompatibilityLocator(oldAccount.SourceLocator)
	if err != nil {
		return "", err
	}
	replacementProvider, replacementKey, err := parseOpenAICompatibilityLocator(replacement.SourceLocator)
	if err != nil {
		return "", err
	}
	providerAccountCount := 0
	for _, account := range accounts {
		if account.SourceType != SourceOpenAICompatibility {
			continue
		}
		providerIndex, _, parseErr := parseOpenAICompatibilityLocator(account.SourceLocator)
		if parseErr == nil && providerIndex == oldProvider {
			providerAccountCount++
		}
	}
	if oldProvider == replacementProvider {
		if providerAccountCount > 1 && oldKey >= 0 && oldKey < replacementKey {
			replacementKey--
		}
	} else if providerAccountCount <= 1 && oldProvider < replacementProvider {
		replacementProvider--
	}
	return fmt.Sprintf("provider:%d:key:%d", replacementProvider, replacementKey), nil
}

// SharesEnabledState 判断账号启停是否会同时影响同一共享 Provider 的其他 Key。
func SharesEnabledState(accounts []AccountSnapshot, account AccountSnapshot) bool {
	if account.SourceType != SourceOpenAICompatibility {
		return false
	}
	providerIndex, _, err := parseOpenAICompatibilityLocator(account.SourceLocator)
	if err != nil {
		return false
	}
	count := 0
	for _, candidate := range accounts {
		if candidate.SourceType != SourceOpenAICompatibility {
			continue
		}
		candidateProvider, _, parseErr := parseOpenAICompatibilityLocator(candidate.SourceLocator)
		if parseErr == nil && candidateProvider == providerIndex {
			count++
		}
	}
	return count > 1
}

func (c *Client) loadConfigEntries(ctx context.Context, baseURL string, managementKey string, path string, responseKey string) ([]map[string]any, error) {
	raw, _, err := c.get(ctx, baseURL, managementKey, path)
	if err != nil {
		return nil, err
	}
	payload, err := decodeObject(raw)
	if err != nil {
		return nil, err
	}
	return mapSlice(payload, responseKey, "items", "data"), nil
}

func (c *Client) waitForAccount(ctx context.Context, baseURL string, managementKey string, sourceType string, sourceLocator string, enabled bool) (AccountSnapshot, error) {
	deadline := time.Now().Add(4 * time.Second)
	for {
		result, err := c.Snapshot(ctx, baseURL, managementKey)
		if err == nil {
			for _, account := range result.Accounts {
				if account.SourceType == sourceType && account.SourceLocator == sourceLocator && account.Enabled == enabled && account.AuthIndex != "" && account.ModelRuleVersion != "" {
					return account, nil
				}
			}
		}
		if time.Now().After(deadline) {
			return AccountSnapshot{}, ErrCredentialNotReady
		}
		timer := time.NewTimer(100 * time.Millisecond)
		select {
		case <-ctx.Done():
			timer.Stop()
			return AccountSnapshot{}, ctx.Err()
		case <-timer.C:
		}
	}
}

func stripReadOnlyList(items []map[string]any) []map[string]any {
	result := make([]map[string]any, 0, len(items))
	for _, item := range items {
		result = append(result, stripReadOnlyMap(item))
	}
	return result
}

func stripReadOnlyMap(item map[string]any) map[string]any {
	result := make(map[string]any, len(item))
	for key, value := range item {
		switch key {
		case "auth-index", "auth_index", "authIndex", "model-rule-version", "model_rule_version", "modelRuleVersion", "effective_allowed_models", "effective-allowed-models":
			continue
		}
		switch typed := value.(type) {
		case map[string]any:
			result[key] = stripReadOnlyMap(typed)
		case []any:
			values := make([]any, 0, len(typed))
			for _, child := range typed {
				if childMap, ok := child.(map[string]any); ok {
					values = append(values, stripReadOnlyMap(childMap))
				} else {
					values = append(values, child)
				}
			}
			result[key] = values
		default:
			result[key] = value
		}
	}
	return result
}

func removeWildcardAll(values []string) []string {
	result := make([]string, 0, len(values))
	for _, value := range values {
		if strings.TrimSpace(value) != "*" {
			result = append(result, value)
		}
	}
	return result
}
