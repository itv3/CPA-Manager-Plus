package proaccountusage

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/model"
	proaccountrepo "github.com/seakee/cpa-manager-plus/apps/manager-server/internal/repository/proaccount"
	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/service/cpa"
	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/service/managerconfig"
	proaccountsvc "github.com/seakee/cpa-manager-plus/apps/manager-server/internal/service/proaccount"
)

const (
	codexUsageURL          = "https://chatgpt.com/backend-api/wham/usage"
	maxAPICallResponseSize = 16 * 1024 * 1024
	successCacheTTL        = 3 * time.Minute
	errorCacheTTL          = time.Minute
)

type cacheEntry struct {
	result    model.ProAccountUsageResult
	expiresAt time.Time
}

type Service struct {
	repository    proaccountrepo.Repository
	accounts      *proaccountsvc.Service
	managerConfig *managerconfig.Service
	client        *http.Client
	mu            sync.Mutex
	cache         map[string]cacheEntry
}

func New(repository proaccountrepo.Repository, accounts *proaccountsvc.Service, managerConfig *managerconfig.Service, clients ...*http.Client) *Service {
	client := &http.Client{Timeout: 30 * time.Second}
	if len(clients) > 0 && clients[0] != nil {
		client = clients[0]
	}
	return &Service{repository: repository, accounts: accounts, managerConfig: managerConfig, client: client, cache: map[string]cacheEntry{}}
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
	if account.Platform != "openai" || account.AuthType != "oauth" || account.Binding == nil || strings.TrimSpace(account.Binding.AuthIndex) == "" {
		result.ErrorCode = "official_usage_unsupported"
		result.ErrorMessage = "该账号类型暂不支持官方主动用量查询"
		return result, nil
	}

	if !force {
		if cached, ok := s.loadCache(accountID, now); ok {
			cached.Local = local
			return cached, nil
		}
	}
	official, err := s.queryCodexUsage(ctx, account.Binding.AuthIndex)
	if err != nil {
		result.ErrorCode = "official_usage_unknown"
		result.ErrorMessage = err.Error()
		result.Retryable = true
		s.storeCache(accountID, result, now.Add(errorCacheTTL))
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

func (s *Service) queryCodexUsage(ctx context.Context, authIndex string) ([]model.ProAccountUsageWindow, error) {
	setup, _, ok, err := s.managerConfig.ResolveSetupWithSource(ctx)
	if err != nil {
		return nil, err
	}
	if !ok {
		return nil, errors.New("usage service is not configured")
	}
	payload := map[string]any{
		"authIndex": authIndex,
		"method":    http.MethodGet,
		"url":       codexUsageURL,
		"header": map[string]string{
			"Authorization": "Bearer $TOKEN$",
			"Content-Type":  "application/json",
			"User-Agent":    "CLIProxyAPI-Pro/1.0",
		},
	}
	data, err := json.Marshal(payload)
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, cpa.NormalizeBaseURL(setup.CPAUpstreamURL)+"/v0/management/api-call", bytes.NewReader(data))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+setup.ManagementKey)
	req.Header.Set("Content-Type", "application/json")
	res, err := s.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer res.Body.Close()
	if res.StatusCode < 200 || res.StatusCode >= 300 {
		body, _ := io.ReadAll(io.LimitReader(res.Body, 2048))
		return nil, fmt.Errorf("Gateway api-call 返回 %s：%s", res.Status, strings.TrimSpace(string(body)))
	}
	limited := io.LimitReader(res.Body, maxAPICallResponseSize+1)
	body, err := io.ReadAll(limited)
	if err != nil {
		return nil, err
	}
	if len(body) > maxAPICallResponseSize {
		return nil, errors.New("Gateway api-call 响应过大")
	}
	var envelope map[string]any
	if err := json.Unmarshal(body, &envelope); err != nil {
		return nil, err
	}
	statusCode := int(numberValue(envelope["status_code"], numberValue(envelope["statusCode"], 0)))
	if statusCode < 200 || statusCode >= 300 {
		return nil, fmt.Errorf("官方用量接口返回 HTTP %d", statusCode)
	}
	bodyValue := envelope["body"]
	usage, err := normalizeJSONMap(bodyValue)
	if err != nil {
		return nil, err
	}
	windows := parseCodexWindows(usage, time.Now())
	if len(windows) == 0 {
		return nil, errors.New("官方用量响应没有可识别的配额窗口")
	}
	return windows, nil
}

func normalizeJSONMap(value any) (map[string]any, error) {
	switch typed := value.(type) {
	case map[string]any:
		return typed, nil
	case string:
		var result map[string]any
		if err := json.Unmarshal([]byte(typed), &result); err != nil {
			return nil, err
		}
		return result, nil
	default:
		return nil, errors.New("官方用量响应格式不受支持")
	}
}

func parseCodexWindows(payload map[string]any, now time.Time) []model.ProAccountUsageWindow {
	rateLimit := mapValue(payload, "rate_limit", "rateLimit")
	windows := make([]model.ProAccountUsageWindow, 0, 2)
	for index, key := range []string{"primary_window", "secondary_window"} {
		window := mapValue(rateLimit, key, strings.ReplaceAll(key, "_", ""))
		if len(window) == 0 {
			camel := "primaryWindow"
			if index == 1 {
				camel = "secondaryWindow"
			}
			window = mapValue(rateLimit, camel)
		}
		if len(window) == 0 {
			continue
		}
		used, hasUsed := numberPointer(window, "used_percent", "usedPercent")
		resetAt := int64(0)
		if raw, ok := numberPointer(window, "reset_at", "resetAt"); ok {
			resetAt = int64(*raw * 1000)
		} else if raw, ok := numberPointer(window, "reset_after_seconds", "resetAfterSeconds"); ok {
			resetAt = now.Add(time.Duration(*raw * float64(time.Second))).UnixMilli()
		}
		label := "5h"
		id := "five_hour"
		if seconds, ok := numberPointer(window, "limit_window_seconds", "limitWindowSeconds"); ok {
			switch {
			case *seconds >= 28*24*60*60:
				label, id = "30d", "monthly"
			case *seconds >= 6*24*60*60:
				label, id = "7d", "weekly"
			case *seconds >= 4*60*60 && *seconds <= 6*60*60:
				label, id = "5h", "five_hour"
			default:
				label, id = fmt.Sprintf("%.0fh", *seconds/3600), fmt.Sprintf("window_%d", index+1)
			}
		} else if index == 1 {
			label, id = "7d", "weekly"
		}
		result := model.ProAccountUsageWindow{ID: id, Label: label, ResetAtMS: resetAt, Source: "official"}
		if hasUsed {
			result.UsedPercent = used
			remaining := 100 - *used
			if remaining < 0 {
				remaining = 0
			}
			result.RemainingPercent = &remaining
		}
		windows = append(windows, result)
	}
	return windows
}

func mapValue(raw map[string]any, keys ...string) map[string]any {
	for _, key := range keys {
		if value, ok := raw[key].(map[string]any); ok {
			return value
		}
	}
	return nil
}

func numberPointer(raw map[string]any, keys ...string) (*float64, bool) {
	for _, key := range keys {
		if value, ok := raw[key]; ok {
			number := numberValue(value, 0)
			return &number, true
		}
	}
	return nil, false
}

func numberValue(value any, fallback float64) float64 {
	switch typed := value.(type) {
	case float64:
		return typed
	case json.Number:
		if parsed, err := typed.Float64(); err == nil {
			return parsed
		}
	case int:
		return float64(typed)
	case int64:
		return float64(typed)
	}
	return fallback
}
