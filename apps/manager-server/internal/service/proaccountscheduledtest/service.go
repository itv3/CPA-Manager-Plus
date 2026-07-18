package proaccountscheduledtest

import (
	"context"
	"errors"
	"fmt"
	"log"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/robfig/cron/v3"
	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/model"
	proaccountscheduledtestrepo "github.com/seakee/cpa-manager-plus/apps/manager-server/internal/repository/proaccountscheduledtest"
	proaccountsvc "github.com/seakee/cpa-manager-plus/apps/manager-server/internal/service/proaccount"
	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/service/proaccountgateway"
	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/service/proaccounttest"
)

const (
	defaultMaxResults  = 50
	maxMaxResults      = 500
	maxDuePlans        = 100
	maxRunWorkers      = 10
	minimumRunInterval = 5 * time.Minute
)

var (
	ErrInvalidPlan     = errors.New("pro account scheduled test plan is invalid")
	ErrModelNotAllowed = errors.New("scheduled test model is not allowed for this account")
	ErrVersionConflict = errors.New("pro account version conflict")
	ErrPlanNotFound    = proaccountscheduledtestrepo.ErrPlanNotFound
)

var cronParser = cron.NewParser(cron.Minute | cron.Hour | cron.Dom | cron.Month | cron.Dow)

type AccountReader interface {
	Get(ctx context.Context, id string) (model.ProAccount, error)
}

type TestExecutor interface {
	Test(ctx context.Context, input proaccounttest.Input) (proaccounttest.Result, error)
}

// AccountRuntimeRecoverer 只负责清理可恢复的错误或限流运行时状态，不得改变人工停用状态。
type AccountRuntimeRecoverer interface {
	RecoverAfterSuccessfulTest(ctx context.Context, account model.ProAccount) error
}

type CreateInput struct {
	AccountID       string
	Model           string
	CronExpression  string
	Enabled         bool
	MaxResults      int
	AutoRecover     bool
	ExpectedVersion int64
}

type UpdateInput struct {
	Model          *string
	CronExpression *string
	Enabled        *bool
	MaxResults     *int
	AutoRecover    *bool
}

type DueRunSummary struct {
	Due       int
	Completed int
	Failed    int
}

type Service struct {
	repository proaccountscheduledtestrepo.Repository
	accounts   AccountReader
	tester     TestExecutor
	recoverer  AccountRuntimeRecoverer
	now        func() time.Time
	sequence   atomic.Uint64
	dueMu      sync.Mutex
	planLocks  sync.Map
}

func New(
	repository proaccountscheduledtestrepo.Repository,
	accounts AccountReader,
	tester TestExecutor,
	recoverer ...AccountRuntimeRecoverer,
) *Service {
	var runtimeRecoverer AccountRuntimeRecoverer
	if len(recoverer) > 0 {
		runtimeRecoverer = recoverer[0]
	}
	return &Service{
		repository: repository,
		accounts:   accounts,
		tester:     tester,
		recoverer:  runtimeRecoverer,
		now:        time.Now,
	}
}

func (s *Service) Create(ctx context.Context, input CreateInput) (model.ProAccountScheduledTestPlan, error) {
	if input.AutoRecover && s.recoverer == nil {
		return model.ProAccountScheduledTestPlan{}, fmt.Errorf("%w: 当前部署未启用自动恢复能力", ErrInvalidPlan)
	}
	account, err := s.loadAccount(ctx, input.AccountID)
	if err != nil {
		return model.ProAccountScheduledTestPlan{}, err
	}
	if input.ExpectedVersion > 0 && input.ExpectedVersion != account.Version {
		return model.ProAccountScheduledTestPlan{}, ErrVersionConflict
	}
	modelName, cronExpression, maxResults, nextRunAtMS, err := s.validatePlan(
		account, input.Model, input.CronExpression, input.MaxResults, s.now(),
	)
	if err != nil {
		return model.ProAccountScheduledTestPlan{}, err
	}
	nowMS := s.now().UnixMilli()
	return s.repository.CreatePlan(ctx, model.ProAccountScheduledTestPlan{
		AccountID: account.ID, Model: modelName, CronExpression: cronExpression,
		Enabled: input.Enabled, MaxResults: maxResults, AutoRecover: input.AutoRecover,
		NextRunAtMS: nextRunAtMS, CreatedAtMS: nowMS, UpdatedAtMS: nowMS,
	})
}

func (s *Service) Get(ctx context.Context, id int64) (model.ProAccountScheduledTestPlan, error) {
	item, ok, err := s.repository.GetPlan(ctx, id)
	if err != nil {
		return model.ProAccountScheduledTestPlan{}, err
	}
	if !ok {
		return model.ProAccountScheduledTestPlan{}, ErrPlanNotFound
	}
	return item, nil
}

func (s *Service) ListByAccount(ctx context.Context, accountID string) ([]model.ProAccountScheduledTestPlan, error) {
	account, err := s.loadAccount(ctx, accountID)
	if err != nil {
		return nil, err
	}
	return s.repository.ListPlansByAccount(ctx, account.ID)
}

func (s *Service) Update(ctx context.Context, id int64, input UpdateInput) (model.ProAccountScheduledTestPlan, error) {
	plan, err := s.Get(ctx, id)
	if err != nil {
		return model.ProAccountScheduledTestPlan{}, err
	}
	account, err := s.loadAccount(ctx, plan.AccountID)
	if err != nil {
		return model.ProAccountScheduledTestPlan{}, err
	}
	if input.Model != nil {
		plan.Model = *input.Model
	}
	if input.CronExpression != nil {
		plan.CronExpression = *input.CronExpression
	}
	if input.Enabled != nil {
		plan.Enabled = *input.Enabled
	}
	if input.MaxResults != nil {
		plan.MaxResults = *input.MaxResults
	}
	if input.AutoRecover != nil {
		plan.AutoRecover = *input.AutoRecover
	}
	if plan.AutoRecover && s.recoverer == nil {
		return model.ProAccountScheduledTestPlan{}, fmt.Errorf("%w: 当前部署未启用自动恢复能力", ErrInvalidPlan)
	}
	modelName, cronExpression, maxResults, nextRunAtMS, err := s.validatePlan(
		account, plan.Model, plan.CronExpression, plan.MaxResults, s.now(),
	)
	if err != nil {
		return model.ProAccountScheduledTestPlan{}, err
	}
	plan.Model = modelName
	plan.CronExpression = cronExpression
	plan.MaxResults = maxResults
	plan.NextRunAtMS = nextRunAtMS
	plan.UpdatedAtMS = s.now().UnixMilli()
	return s.repository.UpdatePlan(ctx, plan)
}

func (s *Service) Delete(ctx context.Context, id int64) error {
	if id <= 0 {
		return ErrPlanNotFound
	}
	err := s.repository.DeletePlan(ctx, id)
	if err == nil {
		s.planLocks.Delete(id)
	}
	return err
}

func (s *Service) ListResults(ctx context.Context, planID int64, limit int) ([]model.ProAccountScheduledTestResult, error) {
	if _, err := s.Get(ctx, planID); err != nil {
		return nil, err
	}
	return s.repository.ListResults(ctx, planID, limit)
}

// RunNow 立即执行计划，但不改变原计划的下一次周期执行时间。
func (s *Service) RunNow(ctx context.Context, planID int64) (model.ProAccountScheduledTestResult, error) {
	return s.run(ctx, planID, false, 0)
}

// RunDue 扫描到期计划，并以有限并发执行，避免定时任务压垮 Gateway。
func (s *Service) RunDue(ctx context.Context) (DueRunSummary, error) {
	s.dueMu.Lock()
	defer s.dueMu.Unlock()
	nowMS := s.now().UnixMilli()
	plans, err := s.repository.ListDuePlans(ctx, nowMS, maxDuePlans)
	if err != nil {
		return DueRunSummary{}, err
	}
	summary := DueRunSummary{Due: len(plans)}
	if len(plans) == 0 {
		return summary, nil
	}

	sem := make(chan struct{}, maxRunWorkers)
	var wg sync.WaitGroup
	var summaryMu sync.Mutex
	for _, plan := range plans {
		if ctx.Err() != nil {
			break
		}
		plan := plan
		sem <- struct{}{}
		wg.Add(1)
		go func() {
			defer wg.Done()
			defer func() { <-sem }()
			_, runErr := s.run(ctx, plan.ID, true, nowMS)
			summaryMu.Lock()
			defer summaryMu.Unlock()
			if runErr != nil {
				summary.Failed++
				if ctx.Err() == nil {
					log.Printf("执行统一账号定时测试失败 plan_id=%d: %v", plan.ID, runErr)
				}
				return
			}
			summary.Completed++
		}()
	}
	wg.Wait()
	return summary, ctx.Err()
}

func (s *Service) run(ctx context.Context, planID int64, scheduled bool, dueAtMS int64) (model.ProAccountScheduledTestResult, error) {
	lockValue, _ := s.planLocks.LoadOrStore(planID, &sync.Mutex{})
	lock := lockValue.(*sync.Mutex)
	lock.Lock()
	defer lock.Unlock()

	plan, err := s.Get(ctx, planID)
	if err != nil {
		return model.ProAccountScheduledTestResult{}, err
	}
	if scheduled && (!plan.Enabled || plan.NextRunAtMS <= 0 || plan.NextRunAtMS > dueAtMS) {
		return model.ProAccountScheduledTestResult{}, nil
	}
	started := s.now()
	result := model.ProAccountScheduledTestResult{
		PlanID: plan.ID, Status: model.ProAccountScheduledTestStatusFailed,
		StartedAtMS: started.UnixMilli(),
	}

	account, accountErr := s.loadAccount(ctx, plan.AccountID)
	if accountErr != nil {
		result.ErrorCode = "account_unavailable"
		result.ErrorMessage = truncate(accountErr.Error(), 1024)
	} else if s.tester == nil {
		result.ErrorCode = "test_service_unavailable"
		result.ErrorMessage = "统一账号测试服务不可用"
	} else {
		operationKey := s.operationKey(plan.ID, started)
		testResult, testErr := s.tester.Test(ctx, proaccounttest.Input{
			AccountID: account.ID, OperationID: operationKey,
			IdempotencyKey: operationKey, ExpectedVersion: account.Version,
			Model: plan.Model, Mode: proaccountgateway.ConnectivityModeDefault,
		})
		result.StatusCode = testResult.Connectivity.StatusCode
		result.ResponseText = truncate(testResult.Connectivity.ResponsePreview, 4096)
		result.ErrorCode = strings.TrimSpace(testResult.Connectivity.ErrorCode)
		result.ErrorMessage = truncate(testResult.Connectivity.ErrorMessage, 1024)
		result.Retryable = testResult.Connectivity.Retryable
		result.LatencyMS = testResult.Connectivity.DurationMS
		if testErr != nil {
			if result.ErrorCode == "" {
				result.ErrorCode = classifyTestError(testErr)
			}
			if result.ErrorMessage == "" {
				result.ErrorMessage = truncate(testErr.Error(), 1024)
			}
		} else if testResult.Connectivity.Success {
			result.Status = model.ProAccountScheduledTestStatusSuccess
			result.ErrorCode = ""
			result.ErrorMessage = ""
			result.Retryable = false
			s.tryAutoRecover(ctx, plan, account)
		}
	}

	finished := s.now()
	result.FinishedAtMS = finished.UnixMilli()
	result.CreatedAtMS = result.FinishedAtMS
	if result.LatencyMS <= 0 {
		result.LatencyMS = finished.Sub(started).Milliseconds()
		if result.LatencyMS < 0 {
			result.LatencyMS = 0
		}
	}
	nextRunAtMS := int64(0)
	if scheduled {
		nextRunAtMS, err = computeNextRunMS(plan.CronExpression, finished)
		if err != nil {
			return model.ProAccountScheduledTestResult{}, err
		}
	}
	return s.repository.CompleteRun(ctx, plan.ID, result, nextRunAtMS, plan.MaxResults)
}

func (s *Service) tryAutoRecover(ctx context.Context, plan model.ProAccountScheduledTestPlan, testedAccount model.ProAccount) {
	if !plan.AutoRecover || !testedAccount.Enabled || s.recoverer == nil {
		return
	}
	current, err := s.loadAccount(ctx, testedAccount.ID)
	if err != nil || !current.Enabled {
		return
	}
	if err := s.recoverer.RecoverAfterSuccessfulTest(ctx, current); err != nil && ctx.Err() == nil {
		log.Printf("统一账号定时测试自动恢复失败 plan_id=%d account_id=%s: %v", plan.ID, current.ID, err)
	}
}

func (s *Service) validatePlan(
	account model.ProAccount,
	modelName string,
	cronExpression string,
	maxResults int,
	from time.Time,
) (string, string, int, int64, error) {
	modelName = strings.TrimSpace(modelName)
	cronExpression = strings.TrimSpace(cronExpression)
	if modelName == "" || len(modelName) > 200 || cronExpression == "" || len(cronExpression) > 100 {
		return "", "", 0, 0, ErrInvalidPlan
	}
	if maxResults <= 0 {
		maxResults = defaultMaxResults
	}
	if maxResults > maxMaxResults {
		return "", "", 0, 0, fmt.Errorf("%w: maxResults 必须在 1 到 %d 之间", ErrInvalidPlan, maxMaxResults)
	}
	rules, err := proaccountgateway.NormalizeModelRules(proaccountgateway.ModelRules{
		AllowedModels: account.AllowedModels,
		ModelMapping:  account.ModelMapping,
	})
	if err != nil {
		return "", "", 0, 0, err
	}
	if !proaccountgateway.ModelAllowed(modelName, rules) {
		return "", "", 0, 0, ErrModelNotAllowed
	}
	nextRunAtMS, err := computeNextRunMS(cronExpression, from)
	if err != nil {
		return "", "", 0, 0, fmt.Errorf("%w: cronExpression 无效: %v", ErrInvalidPlan, err)
	}
	return modelName, cronExpression, maxResults, nextRunAtMS, nil
}

func (s *Service) loadAccount(ctx context.Context, accountID string) (model.ProAccount, error) {
	accountID = strings.TrimSpace(accountID)
	if accountID == "" || s.accounts == nil {
		return model.ProAccount{}, proaccountsvc.ErrAccountNotFound
	}
	account, err := s.accounts.Get(ctx, accountID)
	if err != nil {
		return model.ProAccount{}, err
	}
	if account.DeletedAtMS > 0 {
		return model.ProAccount{}, proaccountsvc.ErrAccountNotFound
	}
	return account, nil
}

func (s *Service) operationKey(planID int64, started time.Time) string {
	sequence := s.sequence.Add(1)
	return fmt.Sprintf("scheduled-test-%d-%d-%d", planID, started.UnixNano(), sequence)
}

func computeNextRunMS(expression string, from time.Time) (int64, error) {
	schedule, err := cronParser.Parse(strings.TrimSpace(expression))
	if err != nil {
		return 0, err
	}
	next := schedule.Next(from)
	afterNext := schedule.Next(next)
	if afterNext.Sub(next) < minimumRunInterval {
		return 0, fmt.Errorf("执行间隔不得小于 %s", minimumRunInterval)
	}
	return next.UnixMilli(), nil
}

func classifyTestError(err error) string {
	switch {
	case errors.Is(err, proaccounttest.ErrResourceVersionConflict):
		return "version_conflict"
	case errors.Is(err, proaccounttest.ErrModelNotAllowed):
		return "model_not_allowed"
	case errors.Is(err, proaccounttest.ErrInvalidTestMode):
		return "invalid_test_mode"
	case errors.Is(err, proaccounttest.ErrAccountBindingMissing):
		return "binding_missing"
	default:
		return "connectivity_test_failed"
	}
}

func truncate(value string, limit int) string {
	value = strings.TrimSpace(value)
	if limit <= 0 || len(value) <= limit {
		return value
	}
	return value[:limit]
}
