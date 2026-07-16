package proaccountgateway

import (
	"context"
	"errors"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/service/cpaauthfiles"
)

var ErrGatewayAccountNotFound = errors.New("gateway account not found")

func (c *Client) ReadModelRules(ctx context.Context, baseURL string, managementKey string, sourceType string, sourceLocator string) (ModelRules, error) {
	switch sourceType {
	case SourceAuthFile:
		raw, _, err := c.get(ctx, baseURL, managementKey, "/v0/management/auth-files")
		if err != nil {
			return ModelRules{}, err
		}
		files, err := cpaauthfiles.Parse(raw)
		if err != nil {
			return ModelRules{}, err
		}
		file, ok := cpaauthfiles.Find(files, sourceLocator, "")
		if !ok {
			return ModelRules{}, ErrGatewayAccountNotFound
		}
		return NormalizeModelRules(ModelRules{
			AllowedModels:    mapStringSlice(file.Raw, "allowed_models", "allowed-models", "allowedModels"),
			ModelMapping:     mapStringMap(file.Raw, "model_mapping", "modelMapping"),
			ModelRuleVersion: mapString(file.Raw, "model_rule_version", "model-rule-version", "modelRuleVersion"),
		})
	case SourceOpenAICompatibility:
		return c.readOpenAICompatibilityRules(ctx, baseURL, managementKey, sourceLocator)
	default:
		endpoint, ok := endpointForSource(sourceType)
		if !ok {
			return ModelRules{}, ErrUnsupportedSource
		}
		index, err := parseIndexLocator(sourceLocator)
		if err != nil {
			return ModelRules{}, err
		}
		raw, _, err := c.get(ctx, baseURL, managementKey, endpoint.Path)
		if err != nil {
			return ModelRules{}, err
		}
		payload, err := decodeObject(raw)
		if err != nil {
			return ModelRules{}, err
		}
		entries := mapSlice(payload, endpoint.ResponseKey, "items", "data")
		if index < 0 || index >= len(entries) {
			return ModelRules{}, ErrGatewayAccountNotFound
		}
		entry := entries[index]
		return NormalizeModelRules(ModelRules{
			AllowedModels:    mapStringSlice(entry, "allowed-models", "allowed_models", "allowedModels"),
			ModelMapping:     modelMappingFromList(mapSlice(entry, "models")),
			ModelRuleVersion: mapString(entry, "model-rule-version", "model_rule_version", "modelRuleVersion"),
		})
	}
}

func (c *Client) WriteAndVerifyModelRules(ctx context.Context, baseURL string, managementKey string, sourceType string, sourceLocator string, desired ModelRules) (ModelRules, ModelRules, error) {
	desired, err := NormalizeModelRules(desired)
	if err != nil {
		return ModelRules{}, ModelRules{}, err
	}
	previous, err := c.ReadModelRules(ctx, baseURL, managementKey, sourceType, sourceLocator)
	if err != nil {
		return ModelRules{}, ModelRules{}, err
	}
	if err := c.writeModelRules(ctx, baseURL, managementKey, sourceType, sourceLocator, desired); err != nil {
		return previous, ModelRules{}, err
	}
	deadline := time.Now().Add(3 * time.Second)
	for {
		current, readErr := c.ReadModelRules(ctx, baseURL, managementKey, sourceType, sourceLocator)
		if readErr == nil && RulesEqual(current, desired) && strings.TrimSpace(current.ModelRuleVersion) != "" {
			return previous, current, nil
		}
		if time.Now().After(deadline) {
			return previous, current, ErrRuleReadbackMismatch
		}
		timer := time.NewTimer(100 * time.Millisecond)
		select {
		case <-ctx.Done():
			timer.Stop()
			return previous, ModelRules{}, ctx.Err()
		case <-timer.C:
		}
	}
}

func (c *Client) RestoreModelRules(ctx context.Context, baseURL string, managementKey string, sourceType string, sourceLocator string, previous ModelRules) error {
	previous, err := NormalizeModelRules(previous)
	if err != nil {
		return err
	}
	if err := c.writeModelRules(ctx, baseURL, managementKey, sourceType, sourceLocator, previous); err != nil {
		return err
	}
	deadline := time.Now().Add(3 * time.Second)
	for {
		current, readErr := c.ReadModelRules(ctx, baseURL, managementKey, sourceType, sourceLocator)
		if readErr == nil && RulesEqual(current, previous) {
			return nil
		}
		if time.Now().After(deadline) {
			return ErrRuleReadbackMismatch
		}
		timer := time.NewTimer(100 * time.Millisecond)
		select {
		case <-ctx.Done():
			timer.Stop()
			return ctx.Err()
		case <-timer.C:
		}
	}
}

func (c *Client) writeModelRules(ctx context.Context, baseURL string, managementKey string, sourceType string, sourceLocator string, rules ModelRules) error {
	models := modelListFromMapping(rules.ModelMapping)
	switch sourceType {
	case SourceAuthFile:
		aliases := make([]map[string]any, 0, len(models))
		for _, current := range models {
			aliases = append(aliases, map[string]any{"name": current["name"], "alias": current["alias"]})
		}
		_, _, err := c.requestJSON(ctx, baseURL, managementKey, http.MethodPatch, "/v0/management/auth-files/fields", map[string]any{
			"name": sourceLocator, "allowed_models": rules.AllowedModels, "model_aliases": aliases,
		})
		return err
	case SourceOpenAICompatibility:
		providerIndex, keyIndex, err := parseOpenAICompatibilityLocator(sourceLocator)
		if err != nil {
			return err
		}
		if keyIndex < 0 && len(rules.AllowedModels) > 0 {
			return ErrUnsupportedSource
		}
		value := map[string]any{"models": models}
		payload := map[string]any{"index": providerIndex, "value": value}
		if keyIndex >= 0 {
			payload["key-index"] = keyIndex
			value["allowed-models"] = rules.AllowedModels
		}
		_, _, err = c.requestJSON(ctx, baseURL, managementKey, http.MethodPatch, "/v0/management/openai-compatibility", payload)
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
			"index": index,
			"value": map[string]any{"allowed-models": rules.AllowedModels, "models": models},
		})
		return err
	}
}

func (c *Client) readOpenAICompatibilityRules(ctx context.Context, baseURL string, managementKey string, locator string) (ModelRules, error) {
	providerIndex, keyIndex, err := parseOpenAICompatibilityLocator(locator)
	if err != nil {
		return ModelRules{}, err
	}
	raw, _, err := c.get(ctx, baseURL, managementKey, "/v0/management/openai-compatibility")
	if err != nil {
		return ModelRules{}, err
	}
	payload, err := decodeObject(raw)
	if err != nil {
		return ModelRules{}, err
	}
	providers := mapSlice(payload, "openai-compatibility", "items", "data")
	if providerIndex < 0 || providerIndex >= len(providers) {
		return ModelRules{}, ErrGatewayAccountNotFound
	}
	provider := providers[providerIndex]
	rules := ModelRules{ModelMapping: modelMappingFromList(mapSlice(provider, "models"))}
	if keyIndex >= 0 {
		keys := mapSlice(provider, "api-key-entries", "api_key_entries", "apiKeyEntries")
		if keyIndex >= len(keys) {
			return ModelRules{}, ErrGatewayAccountNotFound
		}
		rules.AllowedModels = mapStringSlice(keys[keyIndex], "allowed-models", "allowed_models", "allowedModels")
		rules.ModelRuleVersion = mapString(keys[keyIndex], "model-rule-version", "model_rule_version", "modelRuleVersion")
	} else {
		rules.ModelRuleVersion = mapString(provider, "model-rule-version", "model_rule_version", "modelRuleVersion")
	}
	return NormalizeModelRules(rules)
}

func endpointForSource(sourceType string) (configEndpoint, bool) {
	for _, endpoint := range configEndpoints {
		if endpoint.SourceType == sourceType {
			return endpoint, true
		}
	}
	return configEndpoint{}, false
}

func parseIndexLocator(locator string) (int, error) {
	parts := strings.Split(strings.TrimSpace(locator), ":")
	if len(parts) != 2 || parts[0] != "index" {
		return 0, ErrInvalidSourceLocator
	}
	index, err := strconv.Atoi(parts[1])
	if err != nil || index < 0 {
		return 0, ErrInvalidSourceLocator
	}
	return index, nil
}

func parseOpenAICompatibilityLocator(locator string) (int, int, error) {
	parts := strings.Split(strings.TrimSpace(locator), ":")
	if len(parts) != 4 || parts[0] != "provider" || parts[2] != "key" {
		return 0, 0, ErrInvalidSourceLocator
	}
	providerIndex, err := strconv.Atoi(parts[1])
	if err != nil || providerIndex < 0 {
		return 0, 0, ErrInvalidSourceLocator
	}
	if parts[3] == "none" {
		return providerIndex, -1, nil
	}
	keyIndex, err := strconv.Atoi(parts[3])
	if err != nil || keyIndex < 0 {
		return 0, 0, ErrInvalidSourceLocator
	}
	return providerIndex, keyIndex, nil
}

func modelListFromMapping(mapping map[string]string) []map[string]any {
	aliases := make([]string, 0, len(mapping))
	for alias := range mapping {
		aliases = append(aliases, alias)
	}
	sort.Strings(aliases)
	result := make([]map[string]any, 0, len(aliases))
	for _, alias := range aliases {
		result = append(result, map[string]any{"name": mapping[alias], "alias": alias})
	}
	return result
}
