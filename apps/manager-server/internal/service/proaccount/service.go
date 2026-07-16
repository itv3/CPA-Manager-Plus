package proaccount

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/model"
	proaccountrepo "github.com/seakee/cpa-manager-plus/apps/manager-server/internal/repository/proaccount"
	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/service/cpaauthfiles"
	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/service/managerconfig"
	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/service/proaccountgateway"
)

var ErrAccountNotFound = errors.New("pro account not found")

type GatewaySnapshotter interface {
	Snapshot(ctx context.Context, baseURL string, managementKey string) (proaccountgateway.SnapshotResult, error)
}

type Service struct {
	repository    proaccountrepo.Repository
	managerConfig *managerconfig.Service
	gateway       GatewaySnapshotter
}

func New(repository proaccountrepo.Repository, managerConfig *managerconfig.Service, gateways ...GatewaySnapshotter) *Service {
	client := GatewaySnapshotter(proaccountgateway.New(nil))
	if len(gateways) > 0 && gateways[0] != nil {
		client = gateways[0]
	}
	return &Service{repository: repository, managerConfig: managerConfig, gateway: client}
}

func (s *Service) List(ctx context.Context, filter model.ProAccountListFilter) (model.ProAccountListResult, error) {
	return s.repository.List(ctx, filter)
}

func (s *Service) Get(ctx context.Context, id string) (model.ProAccount, error) {
	item, ok, err := s.repository.Get(ctx, id)
	if err != nil {
		return model.ProAccount{}, err
	}
	if !ok {
		return model.ProAccount{}, ErrAccountNotFound
	}
	return item, nil
}

func (s *Service) Sync(ctx context.Context, dryRun bool) (model.ProAccountSyncResult, error) {
	if s.managerConfig == nil {
		return model.ProAccountSyncResult{}, errors.New("manager config service is required")
	}
	setup, _, ok, err := s.managerConfig.ResolveSetupWithSource(ctx)
	if err != nil {
		return model.ProAccountSyncResult{}, err
	}
	if !ok || strings.TrimSpace(setup.CPAUpstreamURL) == "" || strings.TrimSpace(setup.ManagementKey) == "" {
		return model.ProAccountSyncResult{}, errors.New("usage service is not configured")
	}
	snapshot, err := s.gateway.Snapshot(ctx, setup.CPAUpstreamURL, setup.ManagementKey)
	if err != nil {
		return model.ProAccountSyncResult{}, fmt.Errorf("fetch gateway accounts: %w", err)
	}
	discoveries := make([]model.ProAccountDiscovery, 0, len(snapshot.Accounts))
	for _, account := range snapshot.Accounts {
		discoveries = append(discoveries, discoveryFromSnapshot(account))
	}
	result, err := s.repository.Sync(ctx, discoveries, time.Now().UnixMilli(), dryRun)
	if err != nil {
		return result, err
	}
	result.Capabilities = model.ProAccountCapabilities{
		CredentialDraft: snapshot.Capabilities.CredentialDraft,
		AllowedModels:   snapshot.Capabilities.AllowedModels,
	}
	result.Warnings = append([]string(nil), snapshot.Warnings...)
	return result, nil
}

func discoveryFromSnapshot(account proaccountgateway.AccountSnapshot) model.ProAccountDiscovery {
	return model.ProAccountDiscovery{
		Platform: account.Platform, AuthType: account.AuthType, SourceType: account.SourceType,
		Name: account.Name, Email: account.Email, Enabled: account.Enabled,
		HealthStatus: account.HealthStatus, LastError: account.LastError,
		AllowedModels: append([]string(nil), account.AllowedModels...), ModelMapping: cloneStringMap(account.ModelMapping),
		ModelRuleVersion: account.ModelRuleVersion, ExpiresAtMS: account.ExpiresAtMS,
		AuthIndex: account.AuthIndex, SourceLocator: account.SourceLocator,
		SourceFingerprint: account.SourceFingerprint,
	}
}

func cloneStringMap(value map[string]string) map[string]string {
	result := make(map[string]string, len(value))
	for key, item := range value {
		result[key] = item
	}
	return result
}

func discoveryFromAuthFile(file cpaauthfiles.File) (model.ProAccountDiscovery, bool) {
	locator := strings.TrimSpace(file.Name)
	if locator == "" {
		return model.ProAccountDiscovery{}, false
	}
	platform := normalizePlatform(file.Provider, file.Raw)
	authType := normalizeAuthType(file.Provider, file.Raw)
	email := rawString(file.Raw, "email", "account", "user_email", "userEmail")
	name := rawString(file.Raw, "label", "display_name", "displayName", "name")
	if name == "" {
		name = strings.TrimSpace(file.AccountSnapshot)
	}
	if name == "" {
		name = locator
	}
	healthStatus := model.ProAccountHealthUnknown
	lastError := rawString(file.Raw, "last_error", "lastError", "error")
	status := strings.ToLower(rawString(file.Raw, "status", "state"))
	if lastError != "" || status == "error" || status == "invalid" || status == "expired" {
		healthStatus = "error"
	} else if status == "active" || status == "healthy" || status == "ok" {
		healthStatus = "healthy"
	}
	allowedModels := rawStringSlice(file.Raw, "allowed_models", "allowedModels", "allowed-models")
	modelMapping := rawStringMap(file.Raw, "model_mapping", "modelMapping")
	expiresAtMS := rawTimeMS(file.Raw, "expires_at", "expiresAt", "expiry", "expired_at")
	fingerprint := identityFingerprint(platform, authType, email, file.AccountID, file.AccountSnapshot)
	return model.ProAccountDiscovery{
		Platform: platform, AuthType: authType, SourceType: "auth_file",
		Name: name, Email: email, Enabled: !file.Disabled, HealthStatus: healthStatus,
		LastError: lastError, AllowedModels: allowedModels, ModelMapping: modelMapping,
		ExpiresAtMS: expiresAtMS, AuthIndex: strings.TrimSpace(file.AuthIndex),
		SourceLocator: locator, SourceFingerprint: fingerprint,
	}, true
}

func normalizePlatform(provider string, raw map[string]any) string {
	value := strings.ToLower(strings.TrimSpace(provider))
	if value == "" {
		value = strings.ToLower(rawString(raw, "provider", "type"))
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

func normalizeAuthType(provider string, raw map[string]any) string {
	value := strings.ToLower(rawString(raw, "auth_type", "authType", "credential_type", "credentialType"))
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

func identityFingerprint(parts ...string) string {
	normalized := make([]string, 0, len(parts))
	hasIdentity := false
	for i, part := range parts {
		part = strings.ToLower(strings.TrimSpace(part))
		if i >= 2 && part != "" {
			hasIdentity = true
		}
		normalized = append(normalized, part)
	}
	if !hasIdentity {
		return ""
	}
	sum := sha256.Sum256([]byte(strings.Join(normalized, "\x00")))
	return hex.EncodeToString(sum[:16])
}

func rawString(raw map[string]any, keys ...string) string {
	for _, key := range keys {
		value, ok := raw[key]
		if !ok || value == nil {
			continue
		}
		text := strings.TrimSpace(fmt.Sprint(value))
		if text != "" && text != "<nil>" {
			return text
		}
	}
	return ""
}

func rawStringSlice(raw map[string]any, keys ...string) []string {
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
				appendUniqueString(&result, seen, fmt.Sprint(item))
			}
		case []string:
			for _, item := range typed {
				appendUniqueString(&result, seen, item)
			}
		case string:
			for _, item := range strings.Split(typed, ",") {
				appendUniqueString(&result, seen, item)
			}
		}
	}
	return result
}

func appendUniqueString(target *[]string, seen map[string]struct{}, value string) {
	value = strings.TrimSpace(value)
	if value == "" {
		return
	}
	if _, exists := seen[value]; exists {
		return
	}
	seen[value] = struct{}{}
	*target = append(*target, value)
}

func rawStringMap(raw map[string]any, keys ...string) map[string]string {
	result := map[string]string{}
	for _, key := range keys {
		value, ok := raw[key]
		if !ok || value == nil {
			continue
		}
		switch typed := value.(type) {
		case map[string]any:
			for source, target := range typed {
				if source = strings.TrimSpace(source); source != "" {
					result[source] = strings.TrimSpace(fmt.Sprint(target))
				}
			}
		case map[string]string:
			for source, target := range typed {
				if source = strings.TrimSpace(source); source != "" {
					result[source] = strings.TrimSpace(target)
				}
			}
		}
	}
	return result
}

func rawTimeMS(raw map[string]any, keys ...string) int64 {
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
		case int64:
			return normalizeUnixMS(typed)
		case string:
			text := strings.TrimSpace(typed)
			if parsed, err := strconv.ParseInt(text, 10, 64); err == nil {
				return normalizeUnixMS(parsed)
			}
			if parsed, err := time.Parse(time.RFC3339, text); err == nil {
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
