package proaccountgateway

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"time"
)

const officialClientCompatibilityReadbackTimeout = 3 * time.Second

// SupportsOfficialClientCompatibility 将 Manager 的功能范围限制在 Gateway 已支持的两类 API Key。
func SupportsOfficialClientCompatibility(sourceType string) bool {
	switch strings.TrimSpace(sourceType) {
	case SourceClaudeAPIKey, SourceCodexAPIKey:
		return true
	default:
		return false
	}
}

// ReadOfficialClientCompatibility 从 Gateway 配置实时读取兼容设置。
// 配置块缺失与显式关闭在运行语义上等价，统一投影为零值关闭态。
func (c *Client) ReadOfficialClientCompatibility(ctx context.Context, baseURL string, managementKey string, sourceType string, sourceLocator string) (OfficialClientCompatibility, error) {
	endpoint, index, err := officialClientCompatibilityLocation(sourceType, sourceLocator)
	if err != nil {
		return OfficialClientCompatibility{}, err
	}
	raw, _, err := c.get(ctx, baseURL, managementKey, endpoint.Path)
	if err != nil {
		return OfficialClientCompatibility{}, err
	}
	payload, err := decodeObject(raw)
	if err != nil {
		return OfficialClientCompatibility{}, err
	}
	entries := mapSlice(payload, endpoint.ResponseKey, "items", "data")
	if index < 0 || index >= len(entries) {
		return OfficialClientCompatibility{}, ErrGatewayAccountNotFound
	}
	compatibility, _ := entries[index]["official-client-compatibility"].(map[string]any)
	if compatibility == nil {
		return OfficialClientCompatibility{}, nil
	}
	return normalizeOfficialClientCompatibility(OfficialClientCompatibility{
		Enabled:    mapBool(compatibility, "enabled"),
		Profile:    mapString(compatibility, "profile"),
		TLSProfile: mapString(compatibility, "tls-profile", "tls_profile", "tlsProfile"),
	}), nil
}

// WriteAndVerifyOfficialClientCompatibility 执行定点写入并回读。
// 写入失败、回读不一致或结果不确定时会立即恢复旧值；恢复失败则返回状态不确定错误。
func (c *Client) WriteAndVerifyOfficialClientCompatibility(ctx context.Context, baseURL string, managementKey string, sourceType string, sourceLocator string, desired OfficialClientCompatibility) (OfficialClientCompatibility, OfficialClientCompatibility, error) {
	if !SupportsOfficialClientCompatibility(sourceType) {
		return OfficialClientCompatibility{}, OfficialClientCompatibility{}, ErrOfficialClientCompatibilityUnsupported
	}
	capabilities, err := c.Capabilities(ctx, baseURL, managementKey)
	if err != nil {
		return OfficialClientCompatibility{}, OfficialClientCompatibility{}, err
	}
	if !capabilities.OfficialClientCompatibility {
		return OfficialClientCompatibility{}, OfficialClientCompatibility{}, ErrOfficialClientCompatibilityUnsupported
	}
	desired = normalizeOfficialClientCompatibility(desired)
	previous, err := c.ReadOfficialClientCompatibility(ctx, baseURL, managementKey, sourceType, sourceLocator)
	if err != nil {
		return OfficialClientCompatibility{}, OfficialClientCompatibility{}, err
	}
	if err = c.writeOfficialClientCompatibility(ctx, baseURL, managementKey, sourceType, sourceLocator, desired); err == nil {
		var applied OfficialClientCompatibility
		applied, err = c.waitForOfficialClientCompatibility(ctx, baseURL, managementKey, sourceType, sourceLocator, desired)
		if err == nil {
			return previous, applied, nil
		}
	}
	writeErr := err
	if restoreErr := c.RestoreOfficialClientCompatibility(ctx, baseURL, managementKey, sourceType, sourceLocator, previous); restoreErr != nil {
		return previous, OfficialClientCompatibility{}, fmt.Errorf("%w: write=%v restore=%v", ErrOfficialClientCompatibilityStateUncertain, writeErr, restoreErr)
	}
	return previous, OfficialClientCompatibility{}, writeErr
}

// RestoreOfficialClientCompatibility 恢复旧兼容设置并再次回读确认。
func (c *Client) RestoreOfficialClientCompatibility(ctx context.Context, baseURL string, managementKey string, sourceType string, sourceLocator string, previous OfficialClientCompatibility) error {
	previous = normalizeOfficialClientCompatibility(previous)
	if err := c.writeOfficialClientCompatibility(ctx, baseURL, managementKey, sourceType, sourceLocator, previous); err != nil {
		return err
	}
	_, err := c.waitForOfficialClientCompatibility(ctx, baseURL, managementKey, sourceType, sourceLocator, previous)
	return err
}

func (c *Client) writeOfficialClientCompatibility(ctx context.Context, baseURL string, managementKey string, sourceType string, sourceLocator string, compatibility OfficialClientCompatibility) error {
	endpoint, index, err := officialClientCompatibilityLocation(sourceType, sourceLocator)
	if err != nil {
		return err
	}
	_, _, err = c.requestJSON(ctx, baseURL, managementKey, http.MethodPatch, endpoint.Path, map[string]any{
		"index": index,
		"value": map[string]any{
			"official-client-compatibility": officialClientCompatibilityPayload(compatibility),
		},
	})
	return err
}

func (c *Client) waitForOfficialClientCompatibility(ctx context.Context, baseURL string, managementKey string, sourceType string, sourceLocator string, expected OfficialClientCompatibility) (OfficialClientCompatibility, error) {
	expected = normalizeOfficialClientCompatibility(expected)
	deadline := time.Now().Add(officialClientCompatibilityReadbackTimeout)
	var current OfficialClientCompatibility
	for {
		readback, err := c.ReadOfficialClientCompatibility(ctx, baseURL, managementKey, sourceType, sourceLocator)
		if err == nil {
			current = readback
			if officialClientCompatibilityMatches(current, expected) {
				return current, nil
			}
		}
		if time.Now().After(deadline) {
			return current, ErrOfficialClientCompatibilityReadbackMismatch
		}
		timer := time.NewTimer(100 * time.Millisecond)
		select {
		case <-ctx.Done():
			timer.Stop()
			return OfficialClientCompatibility{}, ctx.Err()
		case <-timer.C:
		}
	}
}

func officialClientCompatibilityLocation(sourceType string, sourceLocator string) (configEndpoint, int, error) {
	if !SupportsOfficialClientCompatibility(sourceType) {
		return configEndpoint{}, 0, ErrOfficialClientCompatibilityUnsupported
	}
	endpoint, ok := endpointForSource(sourceType)
	if !ok {
		return configEndpoint{}, 0, ErrUnsupportedSource
	}
	index, err := parseIndexLocator(sourceLocator)
	if err != nil {
		return configEndpoint{}, 0, err
	}
	return endpoint, index, nil
}

func officialClientCompatibilityPayload(value OfficialClientCompatibility) map[string]any {
	value = normalizeOfficialClientCompatibility(value)
	return map[string]any{
		"enabled":     value.Enabled,
		"profile":     value.Profile,
		"tls-profile": value.TLSProfile,
	}
}

func normalizeOfficialClientCompatibility(value OfficialClientCompatibility) OfficialClientCompatibility {
	value.Profile = strings.TrimSpace(value.Profile)
	value.TLSProfile = strings.TrimSpace(value.TLSProfile)
	return value
}

func officialClientCompatibilityMatches(current OfficialClientCompatibility, expected OfficialClientCompatibility) bool {
	current = normalizeOfficialClientCompatibility(current)
	expected = normalizeOfficialClientCompatibility(expected)
	if current.Enabled != expected.Enabled || current.TLSProfile != expected.TLSProfile {
		return false
	}
	if expected.Profile != "" {
		return current.Profile == expected.Profile
	}
	if expected.Enabled {
		return current.Profile != ""
	}
	return current.Profile == ""
}
