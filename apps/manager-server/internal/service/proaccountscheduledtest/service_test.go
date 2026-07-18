package proaccountscheduledtest

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/model"
	proaccountscheduledtestrepo "github.com/seakee/cpa-manager-plus/apps/manager-server/internal/repository/proaccountscheduledtest"
	sqliterepo "github.com/seakee/cpa-manager-plus/apps/manager-server/internal/repository/sqlite"
	proaccountsvc "github.com/seakee/cpa-manager-plus/apps/manager-server/internal/service/proaccount"
	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/service/proaccountgateway"
	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/service/proaccounttest"
)

type scheduledAccountReaderStub struct {
	mu      sync.RWMutex
	account model.ProAccount
}

func (s *scheduledAccountReaderStub) Get(_ context.Context, id string) (model.ProAccount, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if id != s.account.ID || s.account.DeletedAtMS > 0 {
		return model.ProAccount{}, proaccountsvc.ErrAccountNotFound
	}
	return s.account, nil
}

func (s *scheduledAccountReaderStub) setEnabled(enabled bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.account.Enabled = enabled
}

type scheduledTestExecutorStub struct {
	mu        sync.Mutex
	calls     int
	active    int
	maxActive int
	started   chan struct{}
	release   chan struct{}
	result    proaccounttest.Result
	err       error
}

func (s *scheduledTestExecutorStub) Test(ctx context.Context, _ proaccounttest.Input) (proaccounttest.Result, error) {
	s.mu.Lock()
	s.calls++
	s.active++
	if s.active > s.maxActive {
		s.maxActive = s.active
	}
	s.mu.Unlock()
	defer func() {
		s.mu.Lock()
		s.active--
		s.mu.Unlock()
	}()
	if s.started != nil {
		s.started <- struct{}{}
	}
	if s.release != nil {
		select {
		case <-s.release:
		case <-ctx.Done():
			return proaccounttest.Result{}, ctx.Err()
		}
	}
	return s.result, s.err
}

type scheduledRecovererStub struct {
	mu       sync.Mutex
	accounts []model.ProAccount
}

func (s *scheduledRecovererStub) RecoverAfterSuccessfulTest(_ context.Context, account model.ProAccount) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.accounts = append(s.accounts, account)
	return nil
}

func TestServiceValidatesCronModelDefaultsAndVersion(t *testing.T) {
	service, _, _, accountReader := newScheduledTestService(t, nil)
	_, err := service.Create(context.Background(), CreateInput{
		AccountID: accountReader.account.ID, Model: "gpt-5", CronExpression: "* * * * *", Enabled: true,
	})
	if !errors.Is(err, ErrInvalidPlan) {
		t.Fatalf("分钟级 Cron 错误 = %v，期望 ErrInvalidPlan", err)
	}
	_, err = service.Create(context.Background(), CreateInput{
		AccountID: accountReader.account.ID, Model: "not-allowed", CronExpression: "*/5 * * * *", Enabled: true,
	})
	if !errors.Is(err, ErrModelNotAllowed) {
		t.Fatalf("未授权模型错误 = %v，期望 ErrModelNotAllowed", err)
	}
	_, err = service.Create(context.Background(), CreateInput{
		AccountID: accountReader.account.ID, Model: "gpt-5", CronExpression: "*/5 * * * *",
		Enabled: true, ExpectedVersion: accountReader.account.Version + 1,
	})
	if !errors.Is(err, ErrVersionConflict) {
		t.Fatalf("版本冲突错误 = %v，期望 ErrVersionConflict", err)
	}
	_, err = service.Create(context.Background(), CreateInput{
		AccountID: accountReader.account.ID, Model: "gpt-5", CronExpression: "*/5 * * * *",
		Enabled: true, AutoRecover: true,
	})
	if !errors.Is(err, ErrInvalidPlan) {
		t.Fatalf("未接入恢复器时的自动恢复错误 = %v，期望 ErrInvalidPlan", err)
	}
	created, err := service.Create(context.Background(), CreateInput{
		AccountID: accountReader.account.ID, Model: "gpt-5", CronExpression: "*/5 * * * *", Enabled: true,
		ExpectedVersion: accountReader.account.Version,
	})
	if err != nil {
		t.Fatalf("创建合法计划：%v", err)
	}
	if created.MaxResults != defaultMaxResults || created.AutoRecover || created.NextRunAtMS <= 0 {
		t.Fatalf("计划默认值 = %#v", created)
	}
}

func TestServiceRunNowKeepsScheduleAndProtectsManualDisabledAccount(t *testing.T) {
	recoverer := &scheduledRecovererStub{}
	service, repository, _, accountReader := newScheduledTestService(t, recoverer)
	accountReader.setEnabled(false)
	created, err := service.Create(context.Background(), CreateInput{
		AccountID: accountReader.account.ID, Model: "gpt-5", CronExpression: "*/5 * * * *",
		Enabled: true, MaxResults: 3, AutoRecover: true,
	})
	if err != nil {
		t.Fatalf("创建计划：%v", err)
	}
	originalNextRun := created.NextRunAtMS
	result, err := service.RunNow(context.Background(), created.ID)
	if err != nil {
		t.Fatalf("立即执行停用账号：%v", err)
	}
	if result.Status != model.ProAccountScheduledTestStatusSuccess {
		t.Fatalf("立即执行结果 = %#v", result)
	}
	recoverer.mu.Lock()
	recoverCalls := len(recoverer.accounts)
	recoverer.mu.Unlock()
	if recoverCalls != 0 {
		t.Fatalf("人工停用账号触发了自动恢复，次数=%d", recoverCalls)
	}
	plan, ok, err := repository.GetPlan(context.Background(), created.ID)
	if err != nil || !ok {
		t.Fatalf("读取计划 ok=%t err=%v", ok, err)
	}
	if plan.NextRunAtMS != originalNextRun || plan.LastRunAtMS == 0 {
		t.Fatalf("立即执行后的计划 = %#v", plan)
	}

	accountReader.setEnabled(true)
	if _, err := service.RunNow(context.Background(), created.ID); err != nil {
		t.Fatalf("立即执行启用账号：%v", err)
	}
	recoverer.mu.Lock()
	defer recoverer.mu.Unlock()
	if len(recoverer.accounts) != 1 || !recoverer.accounts[0].Enabled {
		t.Fatalf("启用账号自动恢复调用 = %#v", recoverer.accounts)
	}
}

func TestServiceRunDueLimitsGlobalConcurrency(t *testing.T) {
	tester := &scheduledTestExecutorStub{
		started: make(chan struct{}, 20),
		release: make(chan struct{}),
		result: proaccounttest.Result{Connectivity: proaccountgateway.ConnectivityResult{
			Success: true, DurationMS: 10, ResponsePreview: "ok",
		}},
	}
	service, repository, _, accountReader := newScheduledTestService(t, nil)
	service.tester = tester
	nowMS := service.now().UnixMilli()
	for i := 0; i < 12; i++ {
		plan, err := service.Create(context.Background(), CreateInput{
			AccountID: accountReader.account.ID, Model: "gpt-5", CronExpression: "*/5 * * * *", Enabled: true,
		})
		if err != nil {
			t.Fatalf("创建第 %d 个计划：%v", i, err)
		}
		plan.NextRunAtMS = nowMS - 1
		if _, err := repository.UpdatePlan(context.Background(), plan); err != nil {
			t.Fatalf("设置第 %d 个到期计划：%v", i, err)
		}
	}

	done := make(chan DueRunSummary, 1)
	go func() {
		summary, _ := service.RunDue(context.Background())
		done <- summary
	}()
	for i := 0; i < maxRunWorkers; i++ {
		select {
		case <-tester.started:
		case <-time.After(2 * time.Second):
			t.Fatalf("等待第 %d 个并发测试超时", i+1)
		}
	}
	tester.mu.Lock()
	maxActive := tester.maxActive
	tester.mu.Unlock()
	if maxActive != maxRunWorkers {
		t.Fatalf("最大并发 = %d，期望 %d", maxActive, maxRunWorkers)
	}
	close(tester.release)
	select {
	case summary := <-done:
		if summary.Due != 12 || summary.Completed != 12 || summary.Failed != 0 {
			t.Fatalf("到期执行汇总 = %#v", summary)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("等待到期计划执行完成超时")
	}
}

func TestServiceSerializesSamePlanRuns(t *testing.T) {
	tester := &scheduledTestExecutorStub{
		started: make(chan struct{}, 2),
		release: make(chan struct{}),
		result:  proaccounttest.Result{Connectivity: proaccountgateway.ConnectivityResult{Success: true}},
	}
	service, _, _, accountReader := newScheduledTestService(t, nil)
	service.tester = tester
	plan, err := service.Create(context.Background(), CreateInput{
		AccountID: accountReader.account.ID, Model: "gpt-5", CronExpression: "*/5 * * * *", Enabled: true,
	})
	if err != nil {
		t.Fatalf("创建计划：%v", err)
	}
	var wg sync.WaitGroup
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, _ = service.RunNow(context.Background(), plan.ID)
		}()
	}
	select {
	case <-tester.started:
	case <-time.After(2 * time.Second):
		t.Fatal("等待首次执行超时")
	}
	select {
	case <-tester.started:
		t.Fatal("同一计划发生重入")
	case <-time.After(50 * time.Millisecond):
	}
	close(tester.release)
	wg.Wait()
	tester.mu.Lock()
	defer tester.mu.Unlock()
	if tester.calls != 2 || tester.maxActive != 1 {
		t.Fatalf("同计划执行 calls=%d maxActive=%d", tester.calls, tester.maxActive)
	}
}

func newScheduledTestService(
	t *testing.T,
	recoverer AccountRuntimeRecoverer,
) (*Service, proaccountscheduledtestrepo.Repository, *sql.DB, *scheduledAccountReaderStub) {
	t.Helper()
	db, err := sqliterepo.Open(filepath.Join(t.TempDir(), "manager.sqlite"))
	if err != nil {
		t.Fatalf("打开 SQLite：%v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	_, err = db.Exec(`insert into pro_accounts (
		id, platform, auth_type, source_type, enabled, health_status,
		allowed_models_json, model_mapping_json, created_at_ms, updated_at_ms, version
	) values ('account-a', 'openai', 'oauth', 'auth_file', 1, 'healthy', '["gpt-5"]', '{}', 1, 1, 7)`)
	if err != nil {
		t.Fatalf("写入测试账号：%v", err)
	}
	accountReader := &scheduledAccountReaderStub{account: model.ProAccount{
		ID: "account-a", Platform: "openai", AuthType: "oauth", SourceType: "auth_file",
		Enabled: true, AllowedModels: []string{"gpt-5"}, ModelMapping: map[string]string{}, Version: 7,
	}}
	tester := &scheduledTestExecutorStub{result: proaccounttest.Result{
		Connectivity: proaccountgateway.ConnectivityResult{
			Success: true, StatusCode: 200, DurationMS: 12, ResponsePreview: "ok",
		},
	}}
	repository := proaccountscheduledtestrepo.New(db)
	var service *Service
	if recoverer == nil {
		service = New(repository, accountReader, tester)
	} else {
		service = New(repository, accountReader, tester, recoverer)
	}
	fixedNow := time.Date(2026, 7, 18, 12, 1, 30, 0, time.UTC)
	service.now = func() time.Time { return fixedNow }
	return service, repository, db, accountReader
}
