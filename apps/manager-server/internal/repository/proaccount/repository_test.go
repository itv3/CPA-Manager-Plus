package proaccount_test

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/model"
	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/repository/proaccount"
	sqliterepo "github.com/seakee/cpa-manager-plus/apps/manager-server/internal/repository/sqlite"
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
			Name: "alpha@example.com", Email: "alpha@example.com", Enabled: true,
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
