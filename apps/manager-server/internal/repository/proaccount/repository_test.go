package proaccount_test

import (
	"context"
	"errors"
	"path/filepath"
	"testing"

	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/model"
	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/repository/proaccount"
	sqliterepo "github.com/seakee/cpa-manager-plus/apps/manager-server/internal/repository/sqlite"
	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/usage"
)

func TestRepositorySyncIsIdempotentAndSupportsDryRun(t *testing.T) {
	db, err := sqliterepo.Open(filepath.Join(t.TempDir(), "usage.sqlite"))
	if err != nil {
		t.Fatalf("打开 SQLite：%v", err)
	}
	defer db.Close()
	repo := proaccount.New(db)
	ctx := context.Background()
	discoveries := []model.ProAccountDiscovery{
		{
			Platform: "openai", AuthType: "oauth", SourceType: "auth_file",
			PlanType: "free",
			Name:     "alpha@example.com", Email: "alpha@example.com", Enabled: true,
			HealthStatus: model.ProAccountHealthUnknown, AuthIndex: "auth-alpha",
			SourceLocator: "alpha.json", SourceFingerprint: "fingerprint-alpha",
		},
		{
			Platform: "anthropic", AuthType: "oauth", SourceType: "auth_file",
			Name: "beta@example.com", Email: "beta@example.com", Enabled: false,
			HealthStatus: "error", AuthIndex: "auth-beta",
			SourceLocator: "beta.json", SourceFingerprint: "fingerprint-beta",
		},
	}

	first, err := repo.Sync(ctx, discoveries, 1000, false)
	if err != nil {
		t.Fatalf("首次同步：%v", err)
	}
	if first.Created != 2 || first.Updated != 0 {
		t.Fatalf("首次同步结果 = %#v", first)
	}

	second, err := repo.Sync(ctx, discoveries, 2000, false)
	if err != nil {
		t.Fatalf("重复同步：%v", err)
	}
	if second.Created != 0 || second.Updated != 2 {
		t.Fatalf("重复同步结果 = %#v", second)
	}
	firstAccount, ok, err := repo.Get(ctx, first.Items[0].ProAccountID)
	if err != nil || !ok {
		t.Fatalf("读取重复同步账号：ok=%v err=%v", ok, err)
	}
	if firstAccount.Version != 1 {
		t.Fatalf("无变化同步不应递增资源版本，version = %d", firstAccount.Version)
	}
	if firstAccount.PlanType != "free" {
		t.Fatalf("套餐类型 = %q", firstAccount.PlanType)
	}
	unknownPlanDiscoveries := append([]model.ProAccountDiscovery(nil), discoveries...)
	unknownPlanDiscoveries[0].PlanType = ""
	if _, err := repo.Sync(ctx, unknownPlanDiscoveries, 2500, false); err != nil {
		t.Fatalf("未知套餐快照同步：%v", err)
	}
	firstAccount, ok, err = repo.Get(ctx, first.Items[0].ProAccountID)
	if err != nil || !ok {
		t.Fatalf("读取未知套餐快照后的账号：ok=%v err=%v", ok, err)
	}
	if firstAccount.PlanType != "free" || firstAccount.Version != 1 {
		t.Fatalf("未知套餐不应清空已知套餐或递增版本：%#v", firstAccount)
	}
	if err := repo.UpdatePlanType(ctx, firstAccount.ID, "Pro"); err != nil {
		t.Fatalf("持久化主动查询套餐：%v", err)
	}
	firstAccount, ok, err = repo.Get(ctx, firstAccount.ID)
	if err != nil || !ok {
		t.Fatalf("读取主动套餐后的账号：ok=%v err=%v", ok, err)
	}
	if firstAccount.PlanType != "pro" || firstAccount.Version != 1 {
		t.Fatalf("派生套餐应持久化且不影响编辑版本：%#v", firstAccount)
	}

	list, err := repo.List(ctx, model.ProAccountListFilter{Limit: 50})
	if err != nil {
		t.Fatalf("查询账号：%v", err)
	}
	if list.Total != 2 || len(list.Items) != 2 {
		t.Fatalf("账号数量 = total:%d items:%d", list.Total, len(list.Items))
	}

	dryRunDiscovery := append([]model.ProAccountDiscovery{}, discoveries...)
	dryRunDiscovery = append(dryRunDiscovery, model.ProAccountDiscovery{
		Platform: "gemini", AuthType: "oauth", SourceType: "auth_file",
		Name: "gamma@example.com", AuthIndex: "auth-gamma", SourceLocator: "gamma.json",
	})
	preview, err := repo.Sync(ctx, dryRunDiscovery, 3000, true)
	if err != nil {
		t.Fatalf("预演同步：%v", err)
	}
	if !preview.DryRun || preview.Created != 1 || preview.Updated != 2 {
		t.Fatalf("预演结果 = %#v", preview)
	}
	list, err = repo.List(ctx, model.ProAccountListFilter{Limit: 50})
	if err != nil {
		t.Fatalf("预演后查询账号：%v", err)
	}
	if list.Total != 2 {
		t.Fatalf("预演不应落库，total = %d", list.Total)
	}
}

func TestRepositorySyncRequiresConfirmationForBindingDrift(t *testing.T) {
	db, err := sqliterepo.Open(filepath.Join(t.TempDir(), "usage.sqlite"))
	if err != nil {
		t.Fatalf("打开 SQLite：%v", err)
	}
	defer db.Close()
	repo := proaccount.New(db)
	ctx := context.Background()
	original := model.ProAccountDiscovery{
		Platform: "openai", AuthType: "oauth", SourceType: "auth_file",
		Name: "alpha@example.com", Email: "alpha@example.com", Enabled: true,
		AuthIndex: "auth-alpha", SourceLocator: "/old/alpha.json", SourceFingerprint: "identity-alpha",
	}
	created, err := repo.Sync(ctx, []model.ProAccountDiscovery{original}, 1000, false)
	if err != nil || created.Created != 1 {
		t.Fatalf("创建账号：result=%#v err=%v", created, err)
	}

	drifted := original
	drifted.AuthIndex = "auth-alpha-new"
	drifted.SourceLocator = "/new/alpha.json"
	result, err := repo.Sync(ctx, []model.ProAccountDiscovery{drifted}, 2000, false)
	if err != nil {
		t.Fatalf("漂移同步：%v", err)
	}
	if result.Pending != 1 || result.Created != 0 || len(result.Items) != 1 {
		t.Fatalf("漂移结果 = %#v", result)
	}
	if len(result.Items[0].CandidateIDs) != 1 || result.Items[0].CandidateIDs[0] != created.Items[0].ProAccountID {
		t.Fatalf("漂移候选 = %#v", result.Items[0])
	}
}

func TestRepositoryListFiltersAndCursor(t *testing.T) {
	db, err := sqliterepo.Open(filepath.Join(t.TempDir(), "usage.sqlite"))
	if err != nil {
		t.Fatalf("打开 SQLite：%v", err)
	}
	defer db.Close()
	repo := proaccount.New(db)
	ctx := context.Background()
	for i, discovery := range []model.ProAccountDiscovery{
		{Platform: "openai", AuthType: "oauth", SourceType: "auth_file", Name: "alpha", Enabled: true, SourceLocator: "a.json"},
		{Platform: "gemini", AuthType: "api", SourceType: "auth_file", Name: "beta", Enabled: false, SourceLocator: "b.json"},
	} {
		if _, err := repo.Sync(ctx, []model.ProAccountDiscovery{discovery}, int64(1000+i), false); err != nil {
			t.Fatalf("写入账号：%v", err)
		}
	}

	filtered, err := repo.List(ctx, model.ProAccountListFilter{Platform: "openai", Limit: 10})
	if err != nil || filtered.Total != 1 || filtered.Items[0].Name != "alpha" {
		t.Fatalf("平台筛选：result=%#v err=%v", filtered, err)
	}
	firstPage, err := repo.List(ctx, model.ProAccountListFilter{Limit: 1})
	if err != nil || len(firstPage.Items) != 1 || firstPage.NextCursor == "" {
		t.Fatalf("第一页：result=%#v err=%v", firstPage, err)
	}
	secondPage, err := repo.List(ctx, model.ProAccountListFilter{Limit: 1, Cursor: firstPage.NextCursor})
	if err != nil || len(secondPage.Items) != 1 || secondPage.Items[0].ID == firstPage.Items[0].ID {
		t.Fatalf("第二页：result=%#v err=%v", secondPage, err)
	}
}

func TestRepositoryUsageBackfillsRetainedHistoryAndUsesBindingValidity(t *testing.T) {
	db, err := sqliterepo.Open(filepath.Join(t.TempDir(), "usage.sqlite"))
	if err != nil {
		t.Fatalf("打开 SQLite：%v", err)
	}
	defer db.Close()
	repo := proaccount.New(db)
	ctx := context.Background()
	for index, timestamp := range []int64{500, 1500} {
		if _, err := db.Exec(`insert into usage_events (
			event_hash, timestamp_ms, timestamp, model, auth_index, input_tokens, output_tokens,
			total_tokens, failed, created_at_ms
		) values (?, ?, ?, 'gpt-test', 'auth-alpha', 1000000, 1000000, 2000000, ?, ?)`,
			"event-"+string(rune('a'+index)), timestamp, "2026-07-16T00:00:00Z", index, timestamp); err != nil {
			t.Fatalf("写入用量事件：%v", err)
		}
	}
	created, err := repo.Sync(ctx, []model.ProAccountDiscovery{{
		Platform: "openai", AuthType: "oauth", SourceType: "auth_file", Name: "alpha",
		Enabled: true, AuthIndex: "auth-alpha", SourceLocator: "alpha.json",
	}}, 1000, false)
	if err != nil {
		t.Fatalf("创建账号：%v", err)
	}
	accountID := created.Items[0].ProAccountID

	usage, _, _, err := repo.Usage(ctx, accountID, 0, 5000)
	if err != nil {
		t.Fatalf("查询用量：%v", err)
	}
	if usage.Requests != 2 || usage.Successes != 1 || usage.Failures != 1 || usage.TotalTokens != 4000000 {
		t.Fatalf("用量聚合 = %#v", usage)
	}
	if usage.CostKnown || usage.EstimatedCost != nil {
		t.Fatalf("数据层不应直接解析价格：%#v", usage)
	}
	costStats, err := repo.UsageCostStats(ctx, accountID, 0, 5000)
	if err != nil || len(costStats) != 1 {
		t.Fatalf("查询成本聚合：stats=%#v err=%v", costStats, err)
	}
	if stat := costStats[0]; stat.Model != "gpt-test" || stat.BillingModel != "gpt-test" ||
		stat.Calls != 2 || stat.InputTokens != 2000000 || stat.OutputTokens != 2000000 || stat.TotalTokens != 4000000 {
		t.Fatalf("成本 Token 聚合 = %#v", stat)
	}
	account, ok, err := repo.Get(ctx, accountID)
	if err != nil || !ok || account.Binding == nil {
		t.Fatalf("读取回填账号：item=%#v ok=%v err=%v", account, ok, err)
	}
	if account.Binding.ValidFromMS != 500 || account.Binding.AttributionQuality != model.ProAttributionQualityRetainedHistory || account.LastUsedAtMS != 1500 {
		t.Fatalf("首次归属回填错误：%#v", account)
	}
}

func TestRepositoryUsageCostStatsUsesMonitoringTokenSemantics(t *testing.T) {
	db, err := sqliterepo.Open(filepath.Join(t.TempDir(), "usage.sqlite"))
	if err != nil {
		t.Fatalf("打开 SQLite：%v", err)
	}
	defer db.Close()
	longInput := usage.LongContextInputTokenThreshold + 1
	if _, err := db.Exec(`insert into usage_events (
		event_hash, timestamp_ms, timestamp, model, requested_model, resolved_model, service_tier,
		auth_index, input_tokens, output_tokens, cached_tokens, cache_tokens, cache_read_tokens,
		cache_creation_tokens, normalized_total_input_tokens, total_tokens, failed, created_at_ms
	) values
		('long-event', 1000, 'long', 'alias-model', 'alias-model', 'billing-model', 'priority',
		 'auth-cost', ?, 20, 1000, 1000, 600, 100, ?, ?, 0, 1000),
		('short-event', 1500, 'short', 'alias-model', 'alias-model', 'billing-model', 'priority',
		 'auth-cost', 10, 5, 0, 0, 0, 0, 10, 15, 0, 1500)`,
		longInput, longInput, longInput+20); err != nil {
		t.Fatalf("写入成本事件：%v", err)
	}
	repo := proaccount.New(db)
	created, err := repo.Sync(context.Background(), []model.ProAccountDiscovery{{
		Platform: "openai", AuthType: "api", SourceType: "config_codex_api_key", Name: "cost-account",
		Enabled: true, AuthIndex: "auth-cost", SourceLocator: "index:0",
	}}, 2000, false)
	if err != nil || len(created.Items) != 1 {
		t.Fatalf("创建成本账号：result=%#v err=%v", created, err)
	}
	stats, err := repo.UsageCostStats(context.Background(), created.Items[0].ProAccountID, 0, 3000)
	if err != nil || len(stats) != 1 {
		t.Fatalf("查询成本 Token：stats=%#v err=%v", stats, err)
	}
	stat := stats[0]
	if stat.Model != "alias-model" || stat.BillingModel != "billing-model" || stat.ServiceTier != "priority" || stat.Calls != 2 {
		t.Fatalf("成本分组错误：%#v", stat)
	}
	if stat.InputTokens != longInput+10 || stat.OutputTokens != 25 || stat.CachedTokens != 300 ||
		stat.CacheReadTokens != 600 || stat.CacheCreationTokens != 100 || stat.TotalTokens != longInput+35 {
		t.Fatalf("规范化 Token 聚合错误：%#v", stat)
	}
	if stat.LongInputTokens != longInput || stat.LongOutputTokens != 20 || stat.LongCachedTokens != 300 ||
		stat.LongCacheReadTokens != 600 || stat.LongCacheCreationTokens != 100 {
		t.Fatalf("长上下文 Token 聚合错误：%#v", stat)
	}
}

func TestRepositoryUsageReadsQuotaAndPlanFromIndependentLatestEvents(t *testing.T) {
	db, err := sqliterepo.Open(filepath.Join(t.TempDir(), "usage.sqlite"))
	if err != nil {
		t.Fatalf("打开 SQLite：%v", err)
	}
	defer db.Close()
	repo := proaccount.New(db)
	ctx := context.Background()
	created, err := repo.Sync(ctx, []model.ProAccountDiscovery{{
		Platform: "openai", AuthType: "oauth", SourceType: "auth_file", Name: "alpha",
		Enabled: true, AuthIndex: "auth-alpha", SourceLocator: "alpha.json",
	}}, 1000, false)
	if err != nil || len(created.Items) != 1 {
		t.Fatalf("创建账号：result=%#v err=%v", created, err)
	}
	statements := []struct {
		hash      string
		timestamp int64
		metadata  string
		planType  any
	}{
		{
			hash: "quota-event", timestamp: 1100,
			metadata: `{"quota":{"primary":{"used_percent":37,"reset_at_ms":1900,"window_minutes":300}}}`,
			planType: nil,
		},
		{
			hash: "plan-event", timestamp: 1200,
			metadata: `{"quota":{"plan_type":"pro"}}`,
			planType: "pro",
		},
		{
			hash: "trace-event", timestamp: 1300,
			metadata: `{"trace":{"request_id":"trace-only"}}`,
			planType: nil,
		},
	}
	for _, statement := range statements {
		if _, err := db.Exec(`insert into usage_events (
			event_hash, timestamp_ms, timestamp, model, auth_index,
			response_metadata_json, header_quota_plan_type, created_at_ms
		) values (?, ?, '2026-07-18T00:00:00Z', 'gpt-test', 'auth-alpha', ?, ?, ?)`,
			statement.hash, statement.timestamp, statement.metadata, statement.planType, statement.timestamp); err != nil {
			t.Fatalf("写入 %s：%v", statement.hash, err)
		}
	}

	_, windows, planType, err := repo.Usage(ctx, created.Items[0].ProAccountID, 1000, 2000)
	if err != nil {
		t.Fatalf("查询被动配额：%v", err)
	}
	if planType != "pro" {
		t.Fatalf("套餐类型 = %q，期望 pro", planType)
	}
	if len(windows) != 1 || windows[0].ID != "five_hour" || windows[0].UsedPercent == nil || *windows[0].UsedPercent != 37 {
		t.Fatalf("配额窗口被较新的非配额事件遮蔽：%#v", windows)
	}
}

func TestRepositoryMarksPartialWhenRollupPredatesRetainedEvents(t *testing.T) {
	db, err := sqliterepo.Open(filepath.Join(t.TempDir(), "usage.sqlite"))
	if err != nil {
		t.Fatalf("打开 SQLite：%v", err)
	}
	defer db.Close()
	if _, err := db.Exec(`insert into usage_events (
		event_hash, timestamp_ms, timestamp, model, auth_index, total_tokens, created_at_ms
	) values ('retained-event', 500, 'retained', 'gpt-test', 'auth-partial', 10, 500)`); err != nil {
		t.Fatalf("写入保留事件：%v", err)
	}
	if _, err := db.Exec(`insert into usage_account_model_rollups (
		account_key, auth_index, model, billing_model, service_tier, first_seen_ms, last_seen_ms, updated_at_ms
	) values ('legacy-account', 'auth-partial', 'gpt-test', 'gpt-test', '', 100, 500, 500)`); err != nil {
		t.Fatalf("写入历史汇总：%v", err)
	}
	repo := proaccount.New(db)
	created, err := repo.Sync(context.Background(), []model.ProAccountDiscovery{{
		Platform: "openai", AuthType: "oauth", SourceType: "auth_file", Name: "partial",
		AuthIndex: "auth-partial", SourceLocator: "partial.json",
	}}, 1000, false)
	if err != nil {
		t.Fatalf("同步部分历史账号：%v", err)
	}
	account, ok, err := repo.Get(context.Background(), created.Items[0].ProAccountID)
	if err != nil || !ok || account.Binding == nil {
		t.Fatalf("读取部分历史账号：item=%#v ok=%v err=%v", account, ok, err)
	}
	if account.Binding.ValidFromMS != 500 || account.Binding.AttributionQuality != model.ProAttributionQualityPartial {
		t.Fatalf("部分归属标记错误：%#v", account.Binding)
	}
	usage, _, _, err := repo.Usage(context.Background(), account.ID, 0, 2000)
	if err != nil || usage.Requests != 1 {
		t.Fatalf("部分历史只能聚合保留事件：usage=%#v err=%v", usage, err)
	}
}

func TestRepositoryDoesNotAttributeAmbiguousAuthIndex(t *testing.T) {
	db, err := sqliterepo.Open(filepath.Join(t.TempDir(), "usage.sqlite"))
	if err != nil {
		t.Fatalf("打开 SQLite：%v", err)
	}
	defer db.Close()
	if _, err := db.Exec(`insert into usage_events (
		event_hash, timestamp_ms, timestamp, model, auth_index, total_tokens, created_at_ms
	) values ('ambiguous-event', 500, 'ambiguous', 'gpt-test', 'auth-shared', 10, 500)`); err != nil {
		t.Fatalf("写入歧义事件：%v", err)
	}
	repo := proaccount.New(db)
	result, err := repo.Sync(context.Background(), []model.ProAccountDiscovery{
		{Platform: "openai", AuthType: "oauth", SourceType: "auth_file", Name: "first", AuthIndex: "auth-shared", SourceLocator: "first.json"},
		{Platform: "anthropic", AuthType: "oauth", SourceType: "auth_file", Name: "second", AuthIndex: "auth-shared", SourceLocator: "second.json"},
	}, 1000, false)
	if err != nil || result.Created != 2 {
		t.Fatalf("同步歧义账号：result=%#v err=%v", result, err)
	}
	for _, item := range result.Items {
		account, ok, getErr := repo.Get(context.Background(), item.ProAccountID)
		if getErr != nil || !ok || account.Binding == nil {
			t.Fatalf("读取歧义账号：item=%#v ok=%v err=%v", account, ok, getErr)
		}
		if account.Binding.ValidFromMS != 1000 || account.Binding.AttributionQuality != model.ProAttributionQualityAmbiguous {
			t.Fatalf("歧义绑定不应回溯：%#v", account.Binding)
		}
		usage, _, _, usageErr := repo.Usage(context.Background(), account.ID, 0, 2000)
		if usageErr != nil || usage.Requests != 0 {
			t.Fatalf("歧义事件不应归属：usage=%#v err=%v", usage, usageErr)
		}
		costStats, costErr := repo.UsageCostStats(context.Background(), account.ID, 0, 2000)
		if costErr != nil || len(costStats) != 0 {
			t.Fatalf("歧义事件不应进入成本聚合：stats=%#v err=%v", costStats, costErr)
		}
	}
}

func TestRepositoryRepairsLegacyUnknownCurrentBindingOnExactSync(t *testing.T) {
	db, err := sqliterepo.Open(filepath.Join(t.TempDir(), "usage.sqlite"))
	if err != nil {
		t.Fatalf("打开 SQLite：%v", err)
	}
	defer db.Close()
	statements := []string{
		`insert into pro_accounts (
			id, platform, auth_type, source_type, name, enabled, health_status, created_at_ms, updated_at_ms
		) values ('legacy-account', 'openai', 'oauth', 'auth_file', 'legacy', 1, 'unknown', 1000, 1000)`,
		`insert into pro_account_bindings (
			pro_account_id, auth_index, source_type, source_locator, binding_status, is_current,
			valid_from_ms, first_seen_at_ms, last_seen_at_ms, created_at_ms, updated_at_ms
		) values ('legacy-account', 'auth-legacy', 'auth_file', 'legacy.json', 'current', 1,
			1000, 1000, 1000, 1000, 1000)`,
		`insert into usage_events (
			event_hash, timestamp_ms, timestamp, model, auth_index, total_tokens, created_at_ms
		) values ('legacy-before-sync', 500, 'legacy', 'gpt-test', 'auth-legacy', 10, 500)`,
	}
	for _, statement := range statements {
		if _, err := db.Exec(statement); err != nil {
			t.Fatalf("写入旧绑定夹具：%v", err)
		}
	}
	repo := proaccount.New(db)
	result, err := repo.Sync(context.Background(), []model.ProAccountDiscovery{{
		Platform: "openai", AuthType: "oauth", SourceType: "auth_file", Name: "legacy",
		Enabled: true, AuthIndex: "auth-legacy", SourceLocator: "legacy.json",
	}}, 2000, false)
	if err != nil || result.Updated != 1 || result.Items[0].ProAccountID != "legacy-account" {
		t.Fatalf("修复旧绑定同步：result=%#v err=%v", result, err)
	}
	account, ok, err := repo.Get(context.Background(), "legacy-account")
	if err != nil || !ok || account.Binding == nil {
		t.Fatalf("读取修复账号：item=%#v ok=%v err=%v", account, ok, err)
	}
	if account.Binding.ValidFromMS != 500 || account.Binding.AttributionQuality != model.ProAttributionQualityRetainedHistory || account.LastUsedAtMS != 500 {
		t.Fatalf("旧绑定修复错误：%#v", account)
	}
}

func TestRepositoryManagedRebindRebuildsSummaryAcrossBindingHistory(t *testing.T) {
	db, err := sqliterepo.Open(filepath.Join(t.TempDir(), "usage.sqlite"))
	if err != nil {
		t.Fatalf("打开 SQLite：%v", err)
	}
	defer db.Close()
	if _, err := db.Exec(`insert into usage_events (
		event_hash, timestamp_ms, timestamp, model, auth_index, total_tokens, created_at_ms
	) values ('old-binding-event', 500, 'old', 'gpt-test', 'old-auth', 10, 500)`); err != nil {
		t.Fatalf("写入旧绑定事件：%v", err)
	}
	repo := proaccount.New(db)
	created, err := repo.Sync(context.Background(), []model.ProAccountDiscovery{{
		Platform: "openai", AuthType: "api", SourceType: "config_codex_api_key",
		Name: "OpenAI API", Enabled: true, AuthIndex: "old-auth", SourceLocator: "index:0",
	}}, 1000, false)
	if err != nil {
		t.Fatalf("创建账号：%v", err)
	}
	accountID := created.Items[0].ProAccountID
	rebound, err := repo.RebindManaged(context.Background(), accountID, 1, model.ProAccountDiscovery{
		Platform: "openai", AuthType: "api", SourceType: "config_openai_compatibility",
		Name: "OpenAI API", Enabled: true, AuthIndex: "new-auth", SourceLocator: "provider:0:key:0",
	}, 2000)
	if err != nil {
		t.Fatalf("轮换绑定：%v", err)
	}
	if _, err := db.Exec(`insert into usage_events (
		event_hash, timestamp_ms, timestamp, model, auth_index, total_tokens, created_at_ms
	) values ('new-binding-event', 2500, 'new', 'gpt-test', 'new-auth', 20, 2500)`); err != nil {
		t.Fatalf("写入新绑定事件：%v", err)
	}
	if _, err := repo.Sync(context.Background(), []model.ProAccountDiscovery{{
		Platform: "openai", AuthType: "api", SourceType: "config_openai_compatibility",
		Name: "OpenAI API", Enabled: true, AuthIndex: "new-auth", SourceLocator: "provider:0:key:0",
	}}, 3000, false); err != nil {
		t.Fatalf("刷新新绑定汇总：%v", err)
	}
	account, ok, err := repo.Get(context.Background(), accountID)
	if err != nil || !ok || account.Binding == nil {
		t.Fatalf("读取轮换账号：item=%#v ok=%v err=%v", account, ok, err)
	}
	if account.ID != rebound.ID || account.LastUsedAtMS != 2500 || account.Binding.AttributionQuality != model.ProAttributionQualityExact {
		t.Fatalf("轮换后汇总错误：%#v", account)
	}
	usage, _, _, err := repo.Usage(context.Background(), accountID, 0, 4000)
	if err != nil || usage.Requests != 2 || usage.TotalTokens != 30 {
		t.Fatalf("绑定历史聚合错误：usage=%#v err=%v", usage, err)
	}
	var oldValidFrom, oldValidTo int64
	var oldQuality string
	if err := db.QueryRow(`select valid_from_ms, valid_to_ms, attribution_quality from pro_account_bindings
		where pro_account_id = ? and auth_index = 'old-auth'`, accountID).Scan(&oldValidFrom, &oldValidTo, &oldQuality); err != nil {
		t.Fatalf("读取旧绑定历史：%v", err)
	}
	if oldValidFrom != 500 || oldValidTo != 2000 || oldQuality != model.ProAttributionQualityRetainedHistory {
		t.Fatalf("旧绑定时间窗错误：from=%d to=%d quality=%s", oldValidFrom, oldValidTo, oldQuality)
	}
}

func TestRepositoryManagedRebindPreservesUUIDAndSoftDeleteKeepsHistory(t *testing.T) {
	db, err := sqliterepo.Open(filepath.Join(t.TempDir(), "usage.sqlite"))
	if err != nil {
		t.Fatalf("打开 SQLite：%v", err)
	}
	defer db.Close()
	repo := proaccount.New(db)
	ctx := context.Background()
	created, err := repo.Sync(ctx, []model.ProAccountDiscovery{{
		Platform: "openai", AuthType: "api", SourceType: "config_codex_api_key",
		Name: "OpenAI API", Enabled: true, AuthIndex: "old-auth", SourceLocator: "index:0",
	}}, 1000, false)
	if err != nil {
		t.Fatalf("创建账号：%v", err)
	}
	accountID := created.Items[0].ProAccountID
	rebound, err := repo.RebindManaged(ctx, accountID, 1, model.ProAccountDiscovery{
		Platform: "openai", AuthType: "api", SourceType: "config_openai_compatibility",
		Name: "OpenAI API", Enabled: true, AuthIndex: "new-auth", SourceLocator: "provider:0:key:0",
	}, 2000)
	if err != nil {
		t.Fatalf("轮换绑定：%v", err)
	}
	if rebound.ID != accountID || rebound.Binding == nil || rebound.Binding.AuthIndex != "new-auth" || rebound.Version != 2 {
		t.Fatalf("轮换结果 = %#v", rebound)
	}
	var historicalCount int
	if err := db.QueryRow(`select count(*) from pro_account_bindings where pro_account_id = ? and is_current = 0 and auth_index = 'old-auth' and valid_to_ms = 2000`, accountID).Scan(&historicalCount); err != nil {
		t.Fatalf("读取历史绑定：%v", err)
	}
	if historicalCount != 1 {
		t.Fatalf("历史绑定数量 = %d", historicalCount)
	}
	deleted, err := repo.SoftDelete(ctx, accountID, rebound.Version, 3000)
	if err != nil {
		t.Fatalf("软删除：%v", err)
	}
	if deleted.DeletedAtMS != 3000 || deleted.Binding != nil {
		t.Fatalf("软删除结果 = %#v", deleted)
	}
	list, err := repo.List(ctx, model.ProAccountListFilter{Limit: 10})
	if err != nil || list.Total != 0 || len(list.Items) != 0 {
		t.Fatalf("软删除后列表 = %#v err=%v", list, err)
	}
	var bindings int
	if err := db.QueryRow(`select count(*) from pro_account_bindings where pro_account_id = ?`, accountID).Scan(&bindings); err != nil {
		t.Fatalf("读取保留绑定：%v", err)
	}
	if bindings != 2 {
		t.Fatalf("软删除不应移除绑定历史，数量 = %d", bindings)
	}
}

func TestRepositorySyncIgnoresDeletedFingerprintCandidates(t *testing.T) {
	db, err := sqliterepo.Open(filepath.Join(t.TempDir(), "usage.sqlite"))
	if err != nil {
		t.Fatalf("打开 SQLite：%v", err)
	}
	defer db.Close()
	repo := proaccount.New(db)
	ctx := context.Background()
	firstDiscovery := model.ProAccountDiscovery{
		Platform: "openai", AuthType: "api", SourceType: "config_openai_compatibility",
		Name: "OpenAI API", Enabled: false, AuthIndex: "old-auth",
		SourceLocator: "provider:0:key:0", SourceFingerprint: "same-deleted-identity",
	}
	created, err := repo.Sync(ctx, []model.ProAccountDiscovery{firstDiscovery}, 1000, false)
	if err != nil || created.Created != 1 {
		t.Fatalf("创建待删除账号：result=%#v err=%v", created, err)
	}
	account, ok, err := repo.Get(ctx, created.Items[0].ProAccountID)
	if err != nil || !ok {
		t.Fatalf("读取待删除账号：ok=%t err=%v", ok, err)
	}
	if _, err = repo.SoftDelete(ctx, account.ID, account.Version, 2000); err != nil {
		t.Fatalf("软删除账号：%v", err)
	}

	retryDiscovery := firstDiscovery
	retryDiscovery.AuthIndex = "new-auth"
	retryDiscovery.SourceLocator = "provider:1:key:0"
	retried, err := repo.Sync(ctx, []model.ProAccountDiscovery{retryDiscovery}, 3000, false)
	if err != nil {
		t.Fatalf("重新同步相同凭证：%v", err)
	}
	if retried.Created != 1 || retried.Pending != 0 || retried.Items[0].ProAccountID == "" {
		t.Fatalf("重新同步结果 = %#v", retried)
	}
	if retried.Items[0].ProAccountID == account.ID {
		t.Fatal("重新同步错误复用了已软删除账号")
	}
}

func TestBindingReviewRequiresCandidateAndResolvesIdempotently(t *testing.T) {
	db, err := sqliterepo.Open(filepath.Join(t.TempDir(), "manager.sqlite"))
	if err != nil {
		t.Fatalf("打开 SQLite：%v", err)
	}
	defer db.Close()
	repository := proaccount.New(db)
	created, err := repository.Sync(context.Background(), []model.ProAccountDiscovery{{
		Platform: "openai", AuthType: "oauth", SourceType: "auth_file", Name: "旧账号",
		AuthIndex: "auth-old", SourceLocator: "/old/account.json", SourceFingerprint: "same-identity",
	}}, 1000, false)
	if err != nil || len(created.Items) != 1 || created.Items[0].ProAccountID == "" {
		t.Fatalf("创建账号：result=%#v err=%v", created, err)
	}
	accountID := created.Items[0].ProAccountID
	pending, err := repository.Sync(context.Background(), []model.ProAccountDiscovery{{
		Platform: "openai", AuthType: "oauth", SourceType: "auth_file", Name: "漂移账号",
		AuthIndex: "auth-new", SourceLocator: "/new/account.json", SourceFingerprint: "same-identity",
	}}, 2000, false)
	if err != nil || pending.Pending != 1 || pending.Items[0].ReasonCode != "file_path_drift_confirmation" {
		t.Fatalf("漂移识别：result=%#v err=%v", pending, err)
	}
	reviews, err := repository.ListBindingReviews(context.Background(), nil, 100)
	if err != nil || len(reviews) != 1 || reviews[0].DriftType != "file_path" {
		t.Fatalf("待确认列表：items=%#v err=%v", reviews, err)
	}
	driftedDiscovery := model.ProAccountDiscovery{
		Platform: "openai", AuthType: "oauth", SourceType: "auth_file", Name: "漂移账号",
		AuthIndex: "auth-new", SourceLocator: "/new/account.json", SourceFingerprint: "same-identity",
	}
	if _, err := repository.RebindFromReview(context.Background(), reviews[0].ID, "not-a-candidate", 1, driftedDiscovery, 3000); !errors.Is(err, proaccount.ErrBindingReviewCandidateInvalid) {
		t.Fatalf("无效候选错误 = %v", err)
	}
	if _, err := repository.RebindFromReview(context.Background(), reviews[0].ID, accountID, 99, driftedDiscovery, 3000); !errors.Is(err, proaccount.ErrVersionConflict) {
		t.Fatalf("错误版本重绑错误 = %v", err)
	}
	stillPending, ok, err := repository.GetBindingReview(context.Background(), reviews[0].ID)
	if err != nil || !ok || stillPending.ResolutionStatus != model.ProBindingResolutionPending {
		t.Fatalf("重绑失败必须回滚确认记录：item=%#v ok=%v err=%v", stillPending, ok, err)
	}
	rebound, err := repository.RebindFromReview(context.Background(), reviews[0].ID, accountID, 1, driftedDiscovery, 3000)
	if err != nil || rebound.ID != accountID || rebound.Binding == nil || rebound.Binding.SourceLocator != "/new/account.json" {
		t.Fatalf("原子重绑结果：item=%#v err=%v", rebound, err)
	}
	resolved, ok, err := repository.GetBindingReview(context.Background(), reviews[0].ID)
	if err != nil || !ok || resolved.ResolutionStatus != "resolved" || resolved.ResolvedAccountID != accountID || resolved.ResolvedAtMS != 3000 {
		t.Fatalf("确认结果：item=%#v ok=%v err=%v", resolved, ok, err)
	}
	replayed, err := repository.RebindFromReview(context.Background(), reviews[0].ID, accountID, 1, driftedDiscovery, 4000)
	if err != nil || replayed.ID != accountID || replayed.Version != rebound.Version {
		t.Fatalf("幂等确认：item=%#v err=%v", replayed, err)
	}
}
