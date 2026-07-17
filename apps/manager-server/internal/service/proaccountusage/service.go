package proaccountusage

import (
	"context"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/model"
	proaccountrepo "github.com/seakee/cpa-manager-plus/apps/manager-server/internal/repository/proaccount"
	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/service/managerconfig"
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
	gateway       *proaccountgateway.Client
	xaiUsage      XAIUsageQuerier
	adapters      map[string]officialUsageAdapter
	mu            sync.Mutex
	cache         map[string]cacheEntry
}

type XAIUsageQuerier interface {
	QueryXAIUsage(ctx context.Context, authIndex string) ([]model.CodexInspectionQuotaWindow, bool, error)
}

func New(repository proaccountrepo.Repository, accounts *proaccountsvc.Service, managerConfig *managerconfig.Service, clients ...*http.Client) *Service {
	client := &http.Client{Timeout: 30 * time.Second}
	if len(clients) > 0 && clients[0] != nil {
		client = clients[0]
	}
	service := &Service{
		repository: repository, accounts: accounts, managerConfig: managerConfig,
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
	local, passiveWindows, err := s.repository.Usage(ctx, accountID, from.UnixMilli(), to.UnixMilli())
	if err != nil {
		return model.ProAccountUsageResult{}, err
	}
	result := model.ProAccountUsageResult{
		Source: "local", UpdatedAtMS: now.UnixMilli(), OfficialWindows: passiveWindows,
		Local: local,
	}
	if len(passiveWindows) > 0 {
		result.Source = "passive"
	}
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

	if cached, ok := s.loadCache(accountID, now); ok {
		if !force || cached.ErrorCode != "" {
			cached.Local = local
			return cached, nil
		}
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
	result.OfficialWindows = official
	result.UpdatedAtMS = time.Now().UnixMilli()
	s.storeCache(accountID, result, now.Add(successCacheTTL))
	return result, nil
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
