package proaccountusage

import (
	"context"
	"net/http"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/model"
	proaccountrepo "github.com/seakee/cpa-manager-plus/apps/manager-server/internal/repository/proaccount"
	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/service/managerconfig"
	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/service/pricing"
	proaccountsvc "github.com/seakee/cpa-manager-plus/apps/manager-server/internal/service/proaccount"
	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/service/proaccountgateway"
)

const (
	successCacheTTL = 3 * time.Minute
	errorCacheTTL   = time.Minute
)

type cacheEntry struct {
	result    model.ProAccountUsageResult
	expiresAt time.Time
}

type Service struct {
	repository    proaccountrepo.Repository
	accounts      *proaccountsvc.Service
	managerConfig *managerconfig.Service
	modelPrices   modelPriceLoader
	gateway       usageGateway
	xaiUsage      XAIUsageQuerier
	adapters      map[string]officialUsageAdapter
	mu            sync.Mutex
	cache         map[string]cacheEntry
}

type usageGateway interface {
	APICall(ctx context.Context, baseURL string, managementKey string, input proaccountgateway.APICallRequest) (proaccountgateway.APICallResult, error)
	FindAccountByAuthIndex(ctx context.Context, baseURL string, managementKey string, authIndex string) (proaccountgateway.AccountSnapshot, error)
	ResolveAccountRuntime(ctx context.Context, baseURL string, managementKey string, sourceType string, sourceLocator string) (proaccountgateway.AccountRuntime, error)
}

type XAIUsageQuerier interface {
	QueryXAIUsage(ctx context.Context, authIndex string) ([]model.CodexInspectionQuotaWindow, bool, error)
}

type modelPriceLoader interface {
	LoadAll(ctx context.Context) (map[string]model.ModelPrice, error)
}

func New(repository proaccountrepo.Repository, accounts *proaccountsvc.Service, managerConfig *managerconfig.Service, modelPrices modelPriceLoader, clients ...*http.Client) *Service {
	client := &http.Client{Timeout: 30 * time.Second}
	if len(clients) > 0 && clients[0] != nil {
		client = clients[0]
	}
	service := &Service{
		repository: repository, accounts: accounts, managerConfig: managerConfig, modelPrices: modelPrices,
		gateway: proaccountgateway.New(client), cache: map[string]cacheEntry{},
	}
	service.adapters = newOfficialUsageAdapters(service)
	return service
}

func (s *Service) SetXAIUsageQuerier(querier XAIUsageQuerier) {
	s.xaiUsage = querier
}

func (s *Service) Get(ctx context.Context, accountID string, source string, force bool) (model.ProAccountUsageResult, error) {
	account, err := s.accounts.Get(ctx, accountID)
	if err != nil {
		return model.ProAccountUsageResult{}, err
	}
	now := time.Now()
	location := time.Local
	localNow := now.In(location)
	from := time.Date(localNow.Year(), localNow.Month(), localNow.Day(), 0, 0, 0, 0, location)
	to := from.AddDate(0, 0, 1)
	local, passiveWindows, passivePlanType, err := s.repository.Usage(ctx, accountID, from.UnixMilli(), to.UnixMilli())
	if err != nil {
		return model.ProAccountUsageResult{}, err
	}
	costStats, err := s.repository.UsageCostStats(ctx, accountID, from.UnixMilli(), to.UnixMilli())
	if err != nil {
		return model.ProAccountUsageResult{}, err
	}
	if err := s.applyLocalCost(ctx, &local, costStats); err != nil {
		return model.ProAccountUsageResult{}, err
	}
	result := model.ProAccountUsageResult{
		Source: "local", UpdatedAtMS: now.UnixMilli(), OfficialWindows: passiveWindows,
		Local: local, PlanType: firstNonEmptyPlanType(passivePlanType, account.PlanType),
	}
	if len(passiveWindows) > 0 {
		result.Source = "passive"
	}
	result.OfficialWindows = ensureReferenceUsageWindows(account, result.OfficialWindows)
	if strings.ToLower(strings.TrimSpace(source)) != "active" {
		return result, nil
	}
	adapter, supported := s.adapters[officialUsageAdapterKey(account)]
	if !supported {
		result.ErrorCode = "official_usage_unsupported"
		result.ErrorMessage = "该账号类型暂不支持官方主动用量查询"
		return result, nil
	}
	if account.Binding == nil || strings.TrimSpace(account.Binding.AuthIndex) == "" {
		result.ErrorCode = "official_usage_unknown"
		result.ErrorMessage = "账号缺少可用的 Gateway 运行时绑定"
		return result, nil
	}

	if cached, ok := s.loadCache(accountID, now); ok && !force {
		cached.Local = local
		return cached, nil
	}
	official, err := adapter.Query(ctx, account)
	if err != nil {
		result.ErrorCode = "official_usage_unknown"
		result.ErrorMessage = safeUsageError(err)
		result.Retryable = usageErrorRetryable(err)
		if result.Retryable {
			s.storeCache(accountID, result, now.Add(errorCacheTTL))
		}
		return result, nil
	}
	result.Source = "official"
	result.OfficialWindows = ensureReferenceUsageWindows(account, official.Windows)
	result.PlanType = firstNonEmptyPlanType(official.PlanType, passivePlanType, account.PlanType)
	if official.PlanType != "" {
		// 主动查询得到的套餐信息要持久化，避免后续无套餐字段的快照让表格徽标消失。
		_ = s.repository.UpdatePlanType(ctx, accountID, result.PlanType)
	}
	result.UpdatedAtMS = time.Now().UnixMilli()
	s.storeCache(accountID, result, now.Add(successCacheTTL))
	return result, nil
}

func (s *Service) applyLocalCost(ctx context.Context, local *model.ProAccountLocalUsage, stats []model.ProAccountUsageCostStat) error {
	local.EstimatedCost = nil
	local.CostKnown = false
	if local.Requests <= 0 || len(stats) == 0 {
		return nil
	}

	billableStats := make([]model.ProAccountUsageCostStat, 0, len(stats))
	for _, stat := range stats {
		if usageCostStatHasTokens(stat) {
			billableStats = append(billableStats, stat)
		}
	}
	if len(billableStats) == 0 {
		cost := 0.0
		local.EstimatedCost = &cost
		local.CostKnown = true
		return nil
	}

	prices := map[string]model.ModelPrice{}
	if s.modelPrices != nil {
		loaded, err := s.modelPrices.LoadAll(ctx)
		if err != nil {
			return err
		}
		prices = loaded
	}

	total := 0.0
	for _, stat := range billableStats {
		cost, known := pricing.CostForModelCandidatesWithServiceTierKnown(
			[]string{stat.BillingModel, stat.Model},
			stat.ServiceTier,
			pricing.ModelTokens{
				InputTokens:             stat.InputTokens,
				OutputTokens:            stat.OutputTokens,
				CachedTokens:            stat.CachedTokens,
				CacheReadTokens:         stat.CacheReadTokens,
				CacheCreationTokens:     stat.CacheCreationTokens,
				LongInputTokens:         stat.LongInputTokens,
				LongOutputTokens:        stat.LongOutputTokens,
				LongCachedTokens:        stat.LongCachedTokens,
				LongCacheReadTokens:     stat.LongCacheReadTokens,
				LongCacheCreationTokens: stat.LongCacheCreationTokens,
			},
			prices,
		)
		if !known {
			// 正 Token 模型缺价时不返回部分成本，避免把已知小计误认为完整账号成本。
			return nil
		}
		total += cost
	}
	local.EstimatedCost = &total
	local.CostKnown = true
	return nil
}

func usageCostStatHasTokens(stat model.ProAccountUsageCostStat) bool {
	return stat.TotalTokens > 0 || stat.InputTokens > 0 || stat.OutputTokens > 0 ||
		stat.CachedTokens > 0 || stat.CacheReadTokens > 0 || stat.CacheCreationTokens > 0
}

func ensureReferenceUsageWindows(account model.ProAccount, windows []model.ProAccountUsageWindow) []model.ProAccountUsageWindow {
	platform := strings.ToLower(strings.TrimSpace(account.Platform))
	authType := strings.ToLower(strings.TrimSpace(account.AuthType))
	if authType != "oauth" || (platform != "openai" && platform != "anthropic") {
		return windows
	}
	result := append([]model.ProAccountUsageWindow(nil), windows...)
	for _, expected := range []struct {
		id    string
		label string
	}{
		{id: "five_hour", label: "5h"},
		{id: "weekly", label: "7d"},
	} {
		found := false
		for _, window := range result {
			if window.ID == expected.id || strings.EqualFold(strings.TrimSpace(window.Label), expected.label) {
				found = true
				break
			}
		}
		if found {
			continue
		}
		result = append(result, model.ProAccountUsageWindow{
			ID: expected.id, Label: expected.label, Source: "local",
		})
	}
	sortUsageWindows(result)
	return result
}

func sortUsageWindows(windows []model.ProAccountUsageWindow) {
	priority := func(window model.ProAccountUsageWindow) int {
		id := strings.ToLower(strings.TrimSpace(window.ID))
		label := strings.ToLower(strings.TrimSpace(window.Label))
		switch {
		case id == "five_hour" || label == "5h":
			return 10
		case id == "weekly" || id == "seven_day" || label == "7d":
			return 20
		case id == "monthly" || label == "30d":
			return 30
		case strings.HasPrefix(id, "code_review_"):
			return 40
		default:
			return 50
		}
	}
	sort.SliceStable(windows, func(i, j int) bool { return priority(windows[i]) < priority(windows[j]) })
}

func firstNonEmptyPlanType(values ...string) string {
	for _, value := range values {
		value = strings.ToLower(strings.TrimSpace(value))
		value = strings.ReplaceAll(value, "-", "_")
		value = strings.Join(strings.Fields(value), "_")
		if value != "" {
			return value
		}
	}
	return ""
}

func (s *Service) loadCache(accountID string, now time.Time) (model.ProAccountUsageResult, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	entry, ok := s.cache[accountID]
	if !ok || !now.Before(entry.expiresAt) {
		delete(s.cache, accountID)
		return model.ProAccountUsageResult{}, false
	}
	return entry.result, true
}

func (s *Service) storeCache(accountID string, result model.ProAccountUsageResult, expiresAt time.Time) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.cache[accountID] = cacheEntry{result: result, expiresAt: expiresAt}
}
