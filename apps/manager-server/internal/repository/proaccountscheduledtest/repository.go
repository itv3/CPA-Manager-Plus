package proaccountscheduledtest

import (
	"context"
	"database/sql"
	"errors"
	"strings"

	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/model"
)

var ErrPlanNotFound = errors.New("pro account scheduled test plan not found")

type Repository interface {
	CreatePlan(ctx context.Context, plan model.ProAccountScheduledTestPlan) (model.ProAccountScheduledTestPlan, error)
	GetPlan(ctx context.Context, id int64) (model.ProAccountScheduledTestPlan, bool, error)
	ListPlansByAccount(ctx context.Context, accountID string) ([]model.ProAccountScheduledTestPlan, error)
	ListDuePlans(ctx context.Context, nowMS int64, limit int) ([]model.ProAccountScheduledTestPlan, error)
	UpdatePlan(ctx context.Context, plan model.ProAccountScheduledTestPlan) (model.ProAccountScheduledTestPlan, error)
	DeletePlan(ctx context.Context, id int64) error
	CompleteRun(ctx context.Context, planID int64, result model.ProAccountScheduledTestResult, nextRunAtMS int64, maxResults int) (model.ProAccountScheduledTestResult, error)
	ListResults(ctx context.Context, planID int64, limit int) ([]model.ProAccountScheduledTestResult, error)
}

type repository struct {
	db *sql.DB
}

func New(db *sql.DB) Repository {
	return &repository{db: db}
}

func (r *repository) CreatePlan(ctx context.Context, plan model.ProAccountScheduledTestPlan) (model.ProAccountScheduledTestPlan, error) {
	result, err := r.db.ExecContext(ctx, `insert into pro_account_scheduled_tests (
		account_id, model, cron_expression, enabled, max_results, auto_recover,
		next_run_at_ms, created_at_ms, updated_at_ms
	) values (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		strings.TrimSpace(plan.AccountID), strings.TrimSpace(plan.Model), strings.TrimSpace(plan.CronExpression),
		boolInt(plan.Enabled), plan.MaxResults, boolInt(plan.AutoRecover), nullInt64(plan.NextRunAtMS),
		plan.CreatedAtMS, plan.UpdatedAtMS,
	)
	if err != nil {
		return model.ProAccountScheduledTestPlan{}, err
	}
	plan.ID, err = result.LastInsertId()
	if err != nil {
		return model.ProAccountScheduledTestPlan{}, err
	}
	created, ok, err := r.GetPlan(ctx, plan.ID)
	if err != nil {
		return model.ProAccountScheduledTestPlan{}, err
	}
	if !ok {
		return model.ProAccountScheduledTestPlan{}, ErrPlanNotFound
	}
	return created, nil
}

func (r *repository) GetPlan(ctx context.Context, id int64) (model.ProAccountScheduledTestPlan, bool, error) {
	if id <= 0 {
		return model.ProAccountScheduledTestPlan{}, false, nil
	}
	item, err := scanPlan(r.db.QueryRowContext(ctx, planSelect+` where id = ?`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return model.ProAccountScheduledTestPlan{}, false, nil
	}
	return item, err == nil, err
}

func (r *repository) ListPlansByAccount(ctx context.Context, accountID string) ([]model.ProAccountScheduledTestPlan, error) {
	rows, err := r.db.QueryContext(ctx, planSelect+` where account_id = ? order by created_at_ms desc, id desc`, strings.TrimSpace(accountID))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanPlans(rows)
}

func (r *repository) ListDuePlans(ctx context.Context, nowMS int64, limit int) ([]model.ProAccountScheduledTestPlan, error) {
	if limit <= 0 || limit > 500 {
		limit = 100
	}
	rows, err := r.db.QueryContext(ctx, `select
		p.id, p.account_id, p.model, p.cron_expression, p.enabled, p.max_results,
		p.auto_recover, p.last_run_at_ms, p.next_run_at_ms, p.created_at_ms, p.updated_at_ms
		from pro_account_scheduled_tests p
		join pro_accounts a on a.id = p.account_id and a.deleted_at_ms is null
		where p.enabled = 1 and p.next_run_at_ms is not null and p.next_run_at_ms <= ?
		order by p.next_run_at_ms asc, p.id asc limit ?`, nowMS, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanPlans(rows)
}

func (r *repository) UpdatePlan(ctx context.Context, plan model.ProAccountScheduledTestPlan) (model.ProAccountScheduledTestPlan, error) {
	result, err := r.db.ExecContext(ctx, `update pro_account_scheduled_tests set
		model = ?, cron_expression = ?, enabled = ?, max_results = ?, auto_recover = ?,
		next_run_at_ms = ?, updated_at_ms = ? where id = ?`,
		strings.TrimSpace(plan.Model), strings.TrimSpace(plan.CronExpression), boolInt(plan.Enabled),
		plan.MaxResults, boolInt(plan.AutoRecover), nullInt64(plan.NextRunAtMS), plan.UpdatedAtMS, plan.ID,
	)
	if err != nil {
		return model.ProAccountScheduledTestPlan{}, err
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return model.ProAccountScheduledTestPlan{}, err
	}
	if affected == 0 {
		return model.ProAccountScheduledTestPlan{}, ErrPlanNotFound
	}
	updated, ok, err := r.GetPlan(ctx, plan.ID)
	if err != nil {
		return model.ProAccountScheduledTestPlan{}, err
	}
	if !ok {
		return model.ProAccountScheduledTestPlan{}, ErrPlanNotFound
	}
	return updated, nil
}

func (r *repository) DeletePlan(ctx context.Context, id int64) error {
	result, err := r.db.ExecContext(ctx, `delete from pro_account_scheduled_tests where id = ?`, id)
	if err != nil {
		return err
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if affected == 0 {
		return ErrPlanNotFound
	}
	return nil
}

func (r *repository) CompleteRun(
	ctx context.Context,
	planID int64,
	result model.ProAccountScheduledTestResult,
	nextRunAtMS int64,
	maxResults int,
) (model.ProAccountScheduledTestResult, error) {
	if maxResults <= 0 {
		maxResults = 50
	}
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return model.ProAccountScheduledTestResult{}, err
	}
	defer tx.Rollback()

	inserted, err := tx.ExecContext(ctx, `insert into pro_account_scheduled_test_results (
		plan_id, status, status_code, response_text, error_code, error_message, retryable,
		latency_ms, started_at_ms, finished_at_ms, created_at_ms
	) values (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		planID, result.Status, nullInt(result.StatusCode), result.ResponseText, result.ErrorCode,
		result.ErrorMessage, boolInt(result.Retryable), result.LatencyMS, result.StartedAtMS,
		result.FinishedAtMS, result.CreatedAtMS,
	)
	if err != nil {
		return model.ProAccountScheduledTestResult{}, err
	}
	result.ID, err = inserted.LastInsertId()
	if err != nil {
		return model.ProAccountScheduledTestResult{}, err
	}
	result.PlanID = planID

	var updated sql.Result
	if nextRunAtMS > 0 {
		updated, err = tx.ExecContext(ctx, `update pro_account_scheduled_tests set
			last_run_at_ms = ?, next_run_at_ms = ?, updated_at_ms = ? where id = ?`,
			result.FinishedAtMS, nextRunAtMS, result.FinishedAtMS, planID)
	} else {
		updated, err = tx.ExecContext(ctx, `update pro_account_scheduled_tests set
			last_run_at_ms = ?, updated_at_ms = ? where id = ?`,
			result.FinishedAtMS, result.FinishedAtMS, planID)
	}
	if err != nil {
		return model.ProAccountScheduledTestResult{}, err
	}
	if affected, rowsErr := updated.RowsAffected(); rowsErr != nil {
		return model.ProAccountScheduledTestResult{}, rowsErr
	} else if affected == 0 {
		return model.ProAccountScheduledTestResult{}, ErrPlanNotFound
	}

	if _, err := tx.ExecContext(ctx, `delete from pro_account_scheduled_test_results
		where plan_id = ? and id not in (
			select id from pro_account_scheduled_test_results
			where plan_id = ? order by created_at_ms desc, id desc limit ?
		)`, planID, planID, maxResults); err != nil {
		return model.ProAccountScheduledTestResult{}, err
	}
	if err := tx.Commit(); err != nil {
		return model.ProAccountScheduledTestResult{}, err
	}
	return result, nil
}

func (r *repository) ListResults(ctx context.Context, planID int64, limit int) ([]model.ProAccountScheduledTestResult, error) {
	if limit <= 0 || limit > 500 {
		limit = 50
	}
	rows, err := r.db.QueryContext(ctx, resultSelect+`
		where plan_id = ? order by created_at_ms desc, id desc limit ?`, planID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := make([]model.ProAccountScheduledTestResult, 0)
	for rows.Next() {
		item, err := scanResult(rows)
		if err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	return items, rows.Err()
}

const planSelect = `select id, account_id, model, cron_expression, enabled, max_results,
	auto_recover, last_run_at_ms, next_run_at_ms, created_at_ms, updated_at_ms
	from pro_account_scheduled_tests`

const resultSelect = `select id, plan_id, status, status_code, response_text, error_code,
	error_message, retryable, latency_ms, started_at_ms, finished_at_ms, created_at_ms
	from pro_account_scheduled_test_results`

type scanner interface {
	Scan(dest ...any) error
}

func scanPlan(row scanner) (model.ProAccountScheduledTestPlan, error) {
	var item model.ProAccountScheduledTestPlan
	var enabled, autoRecover int
	var lastRunAtMS, nextRunAtMS sql.NullInt64
	err := row.Scan(
		&item.ID, &item.AccountID, &item.Model, &item.CronExpression, &enabled, &item.MaxResults,
		&autoRecover, &lastRunAtMS, &nextRunAtMS, &item.CreatedAtMS, &item.UpdatedAtMS,
	)
	item.Enabled = enabled != 0
	item.AutoRecover = autoRecover != 0
	item.LastRunAtMS = lastRunAtMS.Int64
	item.NextRunAtMS = nextRunAtMS.Int64
	return item, err
}

func scanPlans(rows *sql.Rows) ([]model.ProAccountScheduledTestPlan, error) {
	items := make([]model.ProAccountScheduledTestPlan, 0)
	for rows.Next() {
		item, err := scanPlan(rows)
		if err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	return items, rows.Err()
}

func scanResult(row scanner) (model.ProAccountScheduledTestResult, error) {
	var item model.ProAccountScheduledTestResult
	var statusCode sql.NullInt64
	var retryable int
	err := row.Scan(
		&item.ID, &item.PlanID, &item.Status, &statusCode, &item.ResponseText, &item.ErrorCode,
		&item.ErrorMessage, &retryable, &item.LatencyMS, &item.StartedAtMS, &item.FinishedAtMS,
		&item.CreatedAtMS,
	)
	item.StatusCode = int(statusCode.Int64)
	item.Retryable = retryable != 0
	return item, err
}

func boolInt(value bool) int {
	if value {
		return 1
	}
	return 0
}

func nullInt(value int) any {
	if value <= 0 {
		return nil
	}
	return value
}

func nullInt64(value int64) any {
	if value <= 0 {
		return nil
	}
	return value
}
