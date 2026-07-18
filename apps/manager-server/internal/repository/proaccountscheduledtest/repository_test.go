package proaccountscheduledtest

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"

	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/model"
	sqliterepo "github.com/seakee/cpa-manager-plus/apps/manager-server/internal/repository/sqlite"
)

func TestRepositoryCRUDDueResultsAndCascade(t *testing.T) {
	db, err := sqliterepo.Open(filepath.Join(t.TempDir(), "manager.sqlite"))
	if err != nil {
		t.Fatalf("打开 SQLite：%v", err)
	}
	defer db.Close()
	insertTestAccount(t, db, "account-a")
	repository := New(db)
	ctx := context.Background()

	created, err := repository.CreatePlan(ctx, model.ProAccountScheduledTestPlan{
		AccountID: "account-a", Model: "gpt-5", CronExpression: "*/5 * * * *",
		Enabled: true, MaxResults: 2, NextRunAtMS: 900, CreatedAtMS: 100, UpdatedAtMS: 100,
	})
	if err != nil {
		t.Fatalf("创建计划：%v", err)
	}
	if created.ID <= 0 || created.AccountID != "account-a" || created.Model != "gpt-5" || !created.Enabled {
		t.Fatalf("创建结果 = %#v", created)
	}

	due, err := repository.ListDuePlans(ctx, 1000, 10)
	if err != nil {
		t.Fatalf("查询到期计划：%v", err)
	}
	if len(due) != 1 || due[0].ID != created.ID {
		t.Fatalf("到期计划 = %#v", due)
	}

	created.Model = "gpt-5.1"
	created.Enabled = false
	created.AutoRecover = true
	created.NextRunAtMS = 2000
	created.UpdatedAtMS = 200
	updated, err := repository.UpdatePlan(ctx, created)
	if err != nil {
		t.Fatalf("更新计划：%v", err)
	}
	if updated.Model != "gpt-5.1" || updated.Enabled || !updated.AutoRecover {
		t.Fatalf("更新结果 = %#v", updated)
	}

	for i := 1; i <= 3; i++ {
		_, err := repository.CompleteRun(ctx, created.ID, model.ProAccountScheduledTestResult{
			Status: model.ProAccountScheduledTestStatusSuccess, ResponseText: "ok",
			LatencyMS: int64(i), StartedAtMS: int64(i * 1000), FinishedAtMS: int64(i*1000 + 10),
			CreatedAtMS: int64(i * 1000),
		}, 0, 2)
		if err != nil {
			t.Fatalf("保存第 %d 次结果：%v", i, err)
		}
	}
	results, err := repository.ListResults(ctx, created.ID, 50)
	if err != nil {
		t.Fatalf("查询结果：%v", err)
	}
	if len(results) != 2 || results[0].LatencyMS != 3 || results[1].LatencyMS != 2 {
		t.Fatalf("结果裁剪 = %#v", results)
	}

	if err := repository.DeletePlan(ctx, created.ID); err != nil {
		t.Fatalf("删除计划：%v", err)
	}
	results, err = repository.ListResults(ctx, created.ID, 50)
	if err != nil {
		t.Fatalf("查询级联删除结果：%v", err)
	}
	if len(results) != 0 {
		t.Fatalf("删除计划后仍有结果：%#v", results)
	}
}

func TestRepositoryAccountDeleteCascadesPlans(t *testing.T) {
	db, err := sqliterepo.Open(filepath.Join(t.TempDir(), "manager.sqlite"))
	if err != nil {
		t.Fatalf("打开 SQLite：%v", err)
	}
	defer db.Close()
	insertTestAccount(t, db, "account-cascade")
	repository := New(db)
	created, err := repository.CreatePlan(context.Background(), model.ProAccountScheduledTestPlan{
		AccountID: "account-cascade", Model: "gpt-5", CronExpression: "*/5 * * * *",
		Enabled: true, MaxResults: 50, NextRunAtMS: 1000, CreatedAtMS: 1, UpdatedAtMS: 1,
	})
	if err != nil {
		t.Fatalf("创建计划：%v", err)
	}
	if _, err := db.Exec(`delete from pro_accounts where id = ?`, "account-cascade"); err != nil {
		t.Fatalf("删除账号：%v", err)
	}
	if _, ok, err := repository.GetPlan(context.Background(), created.ID); err != nil || ok {
		t.Fatalf("账号删除后的计划 ok=%t err=%v", ok, err)
	}
}

func TestRepositoryListDuePlansSkipsSoftDeletedAccounts(t *testing.T) {
	db, err := sqliterepo.Open(filepath.Join(t.TempDir(), "manager.sqlite"))
	if err != nil {
		t.Fatalf("打开 SQLite：%v", err)
	}
	defer db.Close()
	insertTestAccount(t, db, "account-soft-deleted")
	repository := New(db)
	created, err := repository.CreatePlan(context.Background(), model.ProAccountScheduledTestPlan{
		AccountID: "account-soft-deleted", Model: "gpt-5", CronExpression: "*/5 * * * *",
		Enabled: true, MaxResults: 50, NextRunAtMS: 1000, CreatedAtMS: 1, UpdatedAtMS: 1,
	})
	if err != nil {
		t.Fatalf("创建计划：%v", err)
	}
	if _, err := db.Exec(`update pro_accounts set
		enabled = 0, health_status = 'deleted', deleted_at_ms = 900 where id = ?`, "account-soft-deleted"); err != nil {
		t.Fatalf("软删除账号：%v", err)
	}

	due, err := repository.ListDuePlans(context.Background(), 1000, 10)
	if err != nil {
		t.Fatalf("查询到期计划：%v", err)
	}
	if len(due) != 0 {
		t.Fatalf("软删除账号的计划不应继续执行：%#v", due)
	}
	if _, ok, err := repository.GetPlan(context.Background(), created.ID); err != nil || !ok {
		t.Fatalf("软删除不应破坏历史计划：ok=%t err=%v", ok, err)
	}
}

func insertTestAccount(t *testing.T, db *sql.DB, id string) {
	t.Helper()
	_, err := db.Exec(`insert into pro_accounts (
		id, platform, auth_type, source_type, enabled, health_status,
		allowed_models_json, model_mapping_json, created_at_ms, updated_at_ms, version
	) values (?, 'openai', 'oauth', 'auth_file', 1, 'healthy', '["gpt-5","gpt-5.1"]', '{}', 1, 1, 1)`, id)
	if err != nil {
		t.Fatalf("写入测试账号：%v", err)
	}
}
