package proaccountmodelcatalog

import (
	"context"
	"errors"
	"sort"
	"strings"

	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/model"
	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/service/managerconfig"
	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/store"
)

const (
	UpstreamSupported = "supported"
	UpstreamUnknown   = "unknown"
)

var ErrAccountBindingMissing = errors.New("pro account binding is unavailable")

type Result struct {
	Models         []string `json:"models"`
	Upstream       []string `json:"upstream"`
	BuiltIn        []string `json:"builtIn"`
	Manual         []string `json:"manual"`
	UpstreamStatus string   `json:"upstreamStatus"`
	Warnings       []string `json:"warnings"`
}

type AccountReader interface {
	Get(ctx context.Context, id string) (model.ProAccount, error)
}

type SetupResolver interface {
	ResolveSetupWithSource(ctx context.Context) (store.Setup, managerconfig.Source, bool, error)
}

type Gateway interface {
	ListRuntimeModels(ctx context.Context, baseURL string, managementKey string, authIndex string, sourceLocator string) ([]string, error)
	ListBuiltInModels(ctx context.Context, baseURL string, managementKey string, channel string) ([]string, error)
}

type Service struct {
	accounts AccountReader
	setup    SetupResolver
	gateway  Gateway
}

func New(accounts AccountReader, setup SetupResolver, gateway Gateway) *Service {
	return &Service{accounts: accounts, setup: setup, gateway: gateway}
}

func (s *Service) SyncAccount(ctx context.Context, accountID string) (Result, error) {
	account, err := s.accounts.Get(ctx, strings.TrimSpace(accountID))
	if err != nil {
		return Result{}, err
	}
	if account.Binding == nil {
		return Result{}, ErrAccountBindingMissing
	}
	setup, _, ok, err := s.setup.ResolveSetupWithSource(ctx)
	if err != nil {
		return Result{}, err
	}
	if !ok || strings.TrimSpace(setup.CPAUpstreamURL) == "" || strings.TrimSpace(setup.ManagementKey) == "" {
		return Result{}, errors.New("gateway connection is not configured")
	}

	result := Result{UpstreamStatus: UpstreamUnknown, Warnings: []string{}}
	upstream, upstreamErr := s.gateway.ListRuntimeModels(ctx, setup.CPAUpstreamURL, setup.ManagementKey, account.Binding.AuthIndex, account.Binding.SourceLocator)
	if upstreamErr == nil {
		result.Upstream = normalize(upstream)
		result.UpstreamStatus = UpstreamSupported
	} else {
		result.Upstream = []string{}
		result.Warnings = append(result.Warnings, "upstream_models_unavailable")
	}

	builtIn, builtInErr := s.gateway.ListBuiltInModels(ctx, setup.CPAUpstreamURL, setup.ManagementKey, channelFor(account.Platform, account.AuthType, account.SourceType))
	if builtInErr == nil {
		result.BuiltIn = normalize(builtIn)
	} else {
		result.BuiltIn = []string{}
		result.Warnings = append(result.Warnings, "built_in_models_unavailable")
	}
	result.Manual = manualModels(account.AllowedModels, account.ModelMapping)
	result.Models = merge(result.Upstream, result.BuiltIn, result.Manual)
	return result, nil
}

// BuiltIn 返回指定平台在当前 Gateway 版本中的内置模型目录。
func (s *Service) BuiltIn(ctx context.Context, platform string, authType string) ([]string, error) {
	setup, _, ok, err := s.setup.ResolveSetupWithSource(ctx)
	if err != nil {
		return nil, err
	}
	if !ok || strings.TrimSpace(setup.CPAUpstreamURL) == "" || strings.TrimSpace(setup.ManagementKey) == "" {
		return nil, errors.New("gateway connection is not configured")
	}
	models, err := s.gateway.ListBuiltInModels(ctx, setup.CPAUpstreamURL, setup.ManagementKey, channelFor(platform, authType, ""))
	if err != nil {
		return nil, err
	}
	return normalize(models), nil
}

func (s *Service) StaticCatalog(ctx context.Context, platform string, authType string) (Result, error) {
	builtIn, err := s.BuiltIn(ctx, platform, authType)
	if err != nil {
		return Result{}, err
	}
	return Result{
		Models: builtIn, Upstream: []string{}, BuiltIn: builtIn, Manual: []string{},
		UpstreamStatus: UpstreamUnknown, Warnings: []string{"upstream_models_require_saved_credential"},
	}, nil
}

func channelFor(platform string, authType string, sourceType string) string {
	switch strings.ToLower(strings.TrimSpace(platform)) {
	case "openai":
		if strings.EqualFold(strings.TrimSpace(authType), "oauth") || strings.TrimSpace(sourceType) == "config_codex_api_key" {
			return "codex"
		}
		// 共享 OpenAI Compatibility Provider 的模型目录以上游列表和手工配置为准
		if strings.TrimSpace(sourceType) == "config_openai_compatibility" {
			return ""
		}
		// 添加向导阶段底层类型未定:OpenAI API 默认按 Responses(codex)内置目录提供候选
		return "codex"
	case "anthropic":
		return "claude"
	case "gemini":
		switch strings.ToLower(strings.TrimSpace(authType)) {
		case "vertex":
			return "vertex"
		case "api":
			return "aistudio"
		default:
			return "gemini"
		}
	case "antigravity":
		return "antigravity"
	case "xai":
		return "xai"
	default:
		return ""
	}
}

func manualModels(allowed []string, mapping map[string]string) []string {
	items := make([]string, 0, len(allowed)+len(mapping)*2)
	for _, item := range allowed {
		if !strings.Contains(item, "*") {
			items = append(items, item)
		}
	}
	aliases := make([]string, 0, len(mapping))
	for alias := range mapping {
		aliases = append(aliases, alias)
	}
	sort.Strings(aliases)
	for _, alias := range aliases {
		items = append(items, alias, mapping[alias])
	}
	return normalize(items)
}

func normalize(items []string) []string {
	return merge(items)
}

func merge(groups ...[]string) []string {
	result := make([]string, 0)
	seen := map[string]struct{}{}
	for _, group := range groups {
		for _, raw := range group {
			value := strings.TrimSpace(raw)
			if value == "" {
				continue
			}
			key := strings.ToLower(value)
			if _, exists := seen[key]; exists {
				continue
			}
			seen[key] = struct{}{}
			result = append(result, value)
		}
	}
	return result
}
