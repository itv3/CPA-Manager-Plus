package proaccountdraft

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/model"
)

var (
	ErrNotFound            = errors.New("pro account operation not found")
	ErrVersionConflict     = errors.New("pro account operation version conflict")
	ErrIdempotencyConflict = errors.New("pro account operation idempotency conflict")
	ErrOperationIDConflict = errors.New("pro account operation id conflict")
)

type Repository interface {
	Create(ctx context.Context, input model.ProAccountDraftCreate, nowMS int64) (model.ProAccountDraft, bool, error)
	Get(ctx context.Context, operationID string) (model.ProAccountDraft, bool, error)
	GetByIdempotencyKey(ctx context.Context, idempotencyKey string) (model.ProAccountDraft, bool, error)
	Update(ctx context.Context, operationID string, expectedVersion int64, update model.ProAccountDraftUpdate, nowMS int64) (model.ProAccountDraft, error)
	ListRecoverable(ctx context.Context, nowMS int64, limit int) ([]model.ProAccountDraft, error)
}

type repository struct {
	db *sql.DB
}

func New(db *sql.DB) Repository {
	return &repository{db: db}
}

func (r *repository) Create(ctx context.Context, input model.ProAccountDraftCreate, nowMS int64) (model.ProAccountDraft, bool, error) {
	input.OperationID = strings.TrimSpace(input.OperationID)
	input.IdempotencyKey = strings.TrimSpace(input.IdempotencyKey)
	input.OperationType = strings.TrimSpace(input.OperationType)
	input.ProAccountID = strings.TrimSpace(input.ProAccountID)
	if input.OperationID == "" || input.IdempotencyKey == "" || input.OperationType == "" {
		return model.ProAccountDraft{}, false, errors.New("operation id, idempotency key and operation type are required")
	}
	contextJSON, err := marshalContext(input.Context)
	if err != nil {
		return model.ProAccountDraft{}, false, err
	}
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return model.ProAccountDraft{}, false, err
	}
	defer tx.Rollback()

	result, err := tx.ExecContext(ctx, `insert or ignore into pro_account_drafts (
		operation_id, idempotency_key, operation_type, pro_account_id, state, version,
		retry_count, cleanup_deadline_ms, context_json, created_at_ms, updated_at_ms
	) values (?, ?, ?, ?, ?, 1, 0, ?, ?, ?, ?)`,
		input.OperationID, input.IdempotencyKey, input.OperationType, nullableString(input.ProAccountID),
		model.ProOperationStateDraftCreated, input.CleanupDeadlineMS, contextJSON, nowMS, nowMS)
	if err != nil {
		return model.ProAccountDraft{}, false, err
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return model.ProAccountDraft{}, false, err
	}
	if affected == 1 {
		item, err := getDraft(ctx, tx, "operation_id", input.OperationID)
		if err != nil {
			return model.ProAccountDraft{}, false, err
		}
		if err := tx.Commit(); err != nil {
			return model.ProAccountDraft{}, false, err
		}
		return item, true, nil
	}

	existing, err := getDraft(ctx, tx, "idempotency_key", input.IdempotencyKey)
	if err == nil {
		if existing.OperationType != input.OperationType || existing.ProAccountID != input.ProAccountID {
			return model.ProAccountDraft{}, false, ErrIdempotencyConflict
		}
		if err := tx.Commit(); err != nil {
			return model.ProAccountDraft{}, false, err
		}
		return existing, false, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return model.ProAccountDraft{}, false, err
	}
	if _, err := getDraft(ctx, tx, "operation_id", input.OperationID); err == nil {
		return model.ProAccountDraft{}, false, ErrOperationIDConflict
	} else if !errors.Is(err, sql.ErrNoRows) {
		return model.ProAccountDraft{}, false, err
	}
	return model.ProAccountDraft{}, false, ErrOperationIDConflict
}

func (r *repository) Get(ctx context.Context, operationID string) (model.ProAccountDraft, bool, error) {
	item, err := getDraft(ctx, r.db, "operation_id", strings.TrimSpace(operationID))
	if errors.Is(err, sql.ErrNoRows) {
		return model.ProAccountDraft{}, false, nil
	}
	return item, err == nil, err
}

func (r *repository) GetByIdempotencyKey(ctx context.Context, idempotencyKey string) (model.ProAccountDraft, bool, error) {
	item, err := getDraft(ctx, r.db, "idempotency_key", strings.TrimSpace(idempotencyKey))
	if errors.Is(err, sql.ErrNoRows) {
		return model.ProAccountDraft{}, false, nil
	}
	return item, err == nil, err
}

func (r *repository) Update(ctx context.Context, operationID string, expectedVersion int64, update model.ProAccountDraftUpdate, nowMS int64) (model.ProAccountDraft, error) {
	contextJSON, err := marshalContext(update.Context)
	if err != nil {
		return model.ProAccountDraft{}, err
	}
	result, err := r.db.ExecContext(ctx, `update pro_account_drafts set
		state = ?, version = version + 1, retry_count = ?, cleanup_deadline_ms = ?,
		compensation_action = ?, error_code = ?, error_summary = ?, context_json = ?, updated_at_ms = ?
		where operation_id = ? and version = ?`,
		update.State, update.RetryCount, update.CleanupDeadlineMS,
		nullableString(update.CompensationAction), nullableString(update.ErrorCode), nullableString(update.ErrorSummary),
		contextJSON, nowMS, strings.TrimSpace(operationID), expectedVersion)
	if err != nil {
		return model.ProAccountDraft{}, err
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return model.ProAccountDraft{}, err
	}
	if affected == 0 {
		if _, ok, getErr := r.Get(ctx, operationID); getErr != nil {
			return model.ProAccountDraft{}, getErr
		} else if !ok {
			return model.ProAccountDraft{}, ErrNotFound
		}
		return model.ProAccountDraft{}, ErrVersionConflict
	}
	item, ok, err := r.Get(ctx, operationID)
	if err != nil {
		return model.ProAccountDraft{}, err
	}
	if !ok {
		return model.ProAccountDraft{}, ErrNotFound
	}
	return item, nil
}

func (r *repository) ListRecoverable(ctx context.Context, nowMS int64, limit int) ([]model.ProAccountDraft, error) {
	if limit <= 0 || limit > 500 {
		limit = 100
	}
	rows, err := r.db.QueryContext(ctx, `select `+draftColumns+` from pro_account_drafts
		where state not in (?, ?, ?) and cleanup_deadline_ms <= ?
		order by cleanup_deadline_ms asc, updated_at_ms asc limit ?`,
		model.ProOperationStateEnabled, model.ProOperationStateCancelled, model.ProOperationStateFailed, nowMS, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := make([]model.ProAccountDraft, 0)
	for rows.Next() {
		item, err := scanDraft(rows)
		if err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	return items, rows.Err()
}

const draftColumns = `operation_id, idempotency_key, operation_type, pro_account_id, state, version,
	retry_count, cleanup_deadline_ms, compensation_action, error_code, error_summary,
	context_json, created_at_ms, updated_at_ms`

type queryRower interface {
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
}

func getDraft(ctx context.Context, queryer queryRower, column string, value string) (model.ProAccountDraft, error) {
	if column != "operation_id" && column != "idempotency_key" {
		return model.ProAccountDraft{}, fmt.Errorf("unsupported draft lookup column %q", column)
	}
	return scanDraft(queryer.QueryRowContext(ctx, `select `+draftColumns+` from pro_account_drafts where `+column+` = ?`, value))
}

type rowScanner interface {
	Scan(dest ...any) error
}

func scanDraft(row rowScanner) (model.ProAccountDraft, error) {
	var item model.ProAccountDraft
	var accountID, compensation, errorCode, errorSummary sql.NullString
	var contextJSON string
	if err := row.Scan(
		&item.OperationID, &item.IdempotencyKey, &item.OperationType, &accountID,
		&item.State, &item.Version, &item.RetryCount, &item.CleanupDeadlineMS,
		&compensation, &errorCode, &errorSummary, &contextJSON, &item.CreatedAtMS, &item.UpdatedAtMS,
	); err != nil {
		return model.ProAccountDraft{}, err
	}
	item.ProAccountID = accountID.String
	item.CompensationAction = compensation.String
	item.ErrorCode = errorCode.String
	item.ErrorSummary = errorSummary.String
	item.Context = map[string]any{}
	if strings.TrimSpace(contextJSON) != "" {
		if err := json.Unmarshal([]byte(contextJSON), &item.Context); err != nil {
			return model.ProAccountDraft{}, fmt.Errorf("decode operation context: %w", err)
		}
	}
	return item, nil
}

func marshalContext(value map[string]any) (string, error) {
	if value == nil {
		return "{}", nil
	}
	raw, err := json.Marshal(value)
	if err != nil {
		return "", fmt.Errorf("encode operation context: %w", err)
	}
	return string(raw), nil
}

func nullableString(value string) any {
	value = strings.TrimSpace(value)
	if value == "" {
		return nil
	}
	return value
}
