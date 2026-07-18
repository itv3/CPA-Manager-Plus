package proaccountusage

import (
	"context"
	"math"
	"path/filepath"
	"testing"
	"time"

	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/model"
	proaccountrepo "github.com/seakee/cpa-manager-plus/apps/manager-server/internal/repository/proaccount"
	sqliterepo "github.com/seakee/cpa-manager-plus/apps/manager-server/internal/repository/sqlite"
	proaccountsvc "github.com/seakee/cpa-manager-plus/apps/manager-server/internal/service/proaccount"
)

type staticModelPriceLoader map[string]model.ModelPrice

func (p staticModelPriceLoader) LoadAll(context.Context) (map[string]model.ModelPrice, error) {
	return p, nil
}

func TestGetForceBypassesCachedRetryableError(t *testing.T) {
	db, err := sqliterepo.Open(filepath.Join(t.TempDir(), "manager.sqlite"))
	if err != nil {
		t.Fatalf("打开 SQLite：%v", err)
	}
	defer db.Close()
	repository := proaccountrepo.New(db)
	synced, err := repository.Sync(context.Background(), []model.ProAccountDiscovery{{
		Platform: "openai", AuthType: "oauth", SourceType: "auth_file", Name: "alpha",
		Enabled: true, AuthIndex: "auth-alpha", SourceLocator: "alpha.json",
	}}, 1000, false)
	if err != nil || len(synced.Items) != 1 {
		t.Fatalf("创建测试账号：result=%#v err=%v", synced, err)
	}

	service := New(repository, proaccountsvc.New(repository, nil), nil, nil)
	queryCount := 0
	service.adapters["openai:oauth"] = officialUsageAdapterFunc(func(context.Context, model.ProAccount) (officialUsageResult, error) {
		queryCount++
		if queryCount == 1 {
			return officialUsageResult{}, &usageQueryError{message: "临时失败", retryable: true}
		}
		return officialUsageResult{Windows: []model.ProAccountUsageWindow{{
			ID: "five_hour", Label: "5h", Source: "official",
		}}}, nil
	})

	accountID := synced.Items[0].ProAccountID
	first, err := service.Get(context.Background(), accountID, "active", false)
	if err != nil || first.ErrorCode == "" || !first.Retryable || queryCount != 1 {
		t.Fatalf("首次主动查询：result=%#v count=%d err=%v", first, queryCount, err)
	}
	cached, err := service.Get(context.Background(), accountID, "active", false)
	if err != nil || cached.ErrorCode == "" || queryCount != 1 {
		t.Fatalf("普通查询应命中错误缓存：result=%#v count=%d err=%v", cached, queryCount, err)
	}
	refreshed, err := service.Get(context.Background(), accountID, "active", true)
	if err != nil || refreshed.ErrorCode != "" || refreshed.Source != "official" || queryCount != 2 {
		t.Fatalf("强制查询应跳过错误缓存：result=%#v count=%d err=%v", refreshed, queryCount, err)
	}
}

func TestGetCalculatesOfficialGPT56CostAndIgnoresUnknownZeroTokenEvent(t *testing.T) {
	db, err := sqliterepo.Open(filepath.Join(t.TempDir(), "manager.sqlite"))
	if err != nil {
		t.Fatalf("打开 SQLite：%v", err)
	}
	defer db.Close()
	now := time.Now()
	nowMS := now.UnixMilli()
	if _, err := db.Exec(`insert into usage_events (
		event_hash, timestamp_ms, timestamp, model, resolved_model, auth_index,
		input_tokens, output_tokens, total_tokens, failed, created_at_ms
	) values
		('unknown-zero', ?, ?, 'gpt-5.3-codex-spark', 'gpt-5.3-codex-spark', 'auth-cost', 0, 0, 0, 1, ?),
		('luna-cost', ?, ?, 'gpt-5.6-luna', 'gpt-5.6-luna', 'auth-cost', 7, 13, 20, 0, ?),
		('terra-cost', ?, ?, 'gpt-5.6-terra', 'gpt-5.6-terra', 'auth-cost', 7, 13, 20, 0, ?)`,
		nowMS, now.Format(time.RFC3339Nano), nowMS,
		nowMS, now.Format(time.RFC3339Nano), nowMS,
		nowMS, now.Format(time.RFC3339Nano), nowMS); err != nil {
		t.Fatalf("写入成本事件：%v", err)
	}
	repository := proaccountrepo.New(db)
	synced, err := repository.Sync(context.Background(), []model.ProAccountDiscovery{{
		Platform: "openai", AuthType: "oauth", SourceType: "auth_file", Name: "cost-account",
		Enabled: true, AuthIndex: "auth-cost", SourceLocator: "cost.json",
	}}, nowMS, false)
	if err != nil || len(synced.Items) != 1 {
		t.Fatalf("创建成本账号：result=%#v err=%v", synced, err)
	}
	service := New(repository, proaccountsvc.New(repository, nil), nil, nil)
	result, err := service.Get(context.Background(), synced.Items[0].ProAccountID, "passive", false)
	if err != nil {
		t.Fatalf("查询账号用量：%v", err)
	}
	if !result.Local.CostKnown || result.Local.EstimatedCost == nil {
		t.Fatalf("GPT-5.6 成本应完整：%#v", result.Local)
	}
	if math.Abs(*result.Local.EstimatedCost-0.0002975) > 0.000000001 {
		t.Fatalf("GPT-5.6 成本 = %v，期望 0.0002975", *result.Local.EstimatedCost)
	}
}

func TestGetTreatsUnknownZeroTokenAccountAsKnownZeroCost(t *testing.T) {
	db, err := sqliterepo.Open(filepath.Join(t.TempDir(), "manager.sqlite"))
	if err != nil {
		t.Fatalf("打开 SQLite：%v", err)
	}
	defer db.Close()
	now := time.Now()
	nowMS := now.UnixMilli()
	if _, err := db.Exec(`insert into usage_events (
		event_hash, timestamp_ms, timestamp, model, auth_index, total_tokens, failed, created_at_ms
	) values ('zero-only', ?, ?, 'unknown-model', 'auth-zero', 0, 1, ?)`,
		nowMS, now.Format(time.RFC3339Nano), nowMS); err != nil {
		t.Fatalf("写入零 Token 事件：%v", err)
	}
	repository := proaccountrepo.New(db)
	synced, err := repository.Sync(context.Background(), []model.ProAccountDiscovery{{
		Platform: "openai", AuthType: "api", SourceType: "config_codex_api_key", Name: "zero-account",
		Enabled: true, AuthIndex: "auth-zero", SourceLocator: "index:0",
	}}, nowMS, false)
	if err != nil || len(synced.Items) != 1 {
		t.Fatalf("创建零 Token 账号：result=%#v err=%v", synced, err)
	}
	service := New(repository, proaccountsvc.New(repository, nil), nil, nil)
	result, err := service.Get(context.Background(), synced.Items[0].ProAccountID, "passive", false)
	if err != nil || !result.Local.CostKnown || result.Local.EstimatedCost == nil || *result.Local.EstimatedCost != 0 {
		t.Fatalf("零 Token 成本：result=%#v err=%v", result.Local, err)
	}
}

func TestGetRequiresConfiguredPriceForPositiveTokenModel(t *testing.T) {
	db, err := sqliterepo.Open(filepath.Join(t.TempDir(), "manager.sqlite"))
	if err != nil {
		t.Fatalf("打开 SQLite：%v", err)
	}
	defer db.Close()
	now := time.Now()
	nowMS := now.UnixMilli()
	if _, err := db.Exec(`insert into usage_events (
		event_hash, timestamp_ms, timestamp, model, auth_index, input_tokens, total_tokens, failed, created_at_ms
	) values ('priced-event', ?, ?, 'custom-model', 'auth-priced', 1000000, 1000000, 0, ?)`,
		nowMS, now.Format(time.RFC3339Nano), nowMS); err != nil {
		t.Fatalf("写入正 Token 事件：%v", err)
	}
	repository := proaccountrepo.New(db)
	synced, err := repository.Sync(context.Background(), []model.ProAccountDiscovery{{
		Platform: "openai", AuthType: "api", SourceType: "config_codex_api_key", Name: "priced-account",
		Enabled: true, AuthIndex: "auth-priced", SourceLocator: "index:1",
	}}, nowMS, false)
	if err != nil || len(synced.Items) != 1 {
		t.Fatalf("创建正 Token 账号：result=%#v err=%v", synced, err)
	}
	accountService := proaccountsvc.New(repository, nil)
	withoutPrice, err := New(repository, accountService, nil, nil).Get(
		context.Background(), synced.Items[0].ProAccountID, "passive", false,
	)
	if err != nil || withoutPrice.Local.CostKnown || withoutPrice.Local.EstimatedCost != nil {
		t.Fatalf("缺价时不应返回部分成本：result=%#v err=%v", withoutPrice.Local, err)
	}
	withPrice, err := New(repository, accountService, nil, staticModelPriceLoader{
		"custom-model": {Prompt: 2, PromptConfigured: true},
	}).Get(context.Background(), synced.Items[0].ProAccountID, "passive", false)
	if err != nil || !withPrice.Local.CostKnown || withPrice.Local.EstimatedCost == nil || *withPrice.Local.EstimatedCost != 2 {
		t.Fatalf("配置价格后的成本：result=%#v err=%v", withPrice.Local, err)
	}
}
