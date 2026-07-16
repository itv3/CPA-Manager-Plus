package proaccount

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/model"
)

var (
	ErrBindingReviewNotFound         = errors.New("pro account binding review not found")
	ErrBindingReviewStateConflict    = errors.New("pro account binding review state conflict")
	ErrBindingReviewCandidateInvalid = errors.New("pro account binding review candidate is invalid")
)

func (r *repository) ListBindingReviews(ctx context.Context, statuses []string, limit int) ([]model.ProAccountBindingReview, error) {
	if limit <= 0 || limit > 500 {
		limit = 100
	}
	normalized := make([]string, 0, len(statuses))
	for _, status := range statuses {
		status = strings.ToLower(strings.TrimSpace(status))
		if status == model.ProBindingResolutionPending || status == model.ProBindingResolutionConflict || status == "resolved" {
			normalized = append(normalized, status)
		}
	}
	if len(normalized) == 0 {
		normalized = []string{model.ProBindingResolutionPending, model.ProBindingResolutionConflict}
	}
	placeholders := make([]string, len(normalized))
	args := make([]any, 0, len(normalized)+1)
	for index, status := range normalized {
		placeholders[index] = "?"
		args = append(args, status)
	}
	args = append(args, limit)
	rows, err := r.db.QueryContext(ctx, `select
		id, discovery_key, source_type, source_locator, auth_index, source_fingerprint,
		resolution_status, candidate_ids_json, reason_code, resolved_account_id, resolved_at_ms,
		first_seen_at_ms, last_seen_at_ms, created_at_ms, updated_at_ms
		from pro_account_binding_reviews where resolution_status in (`+strings.Join(placeholders, ",")+`)
		order by last_seen_at_ms desc, id desc limit ?`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := make([]model.ProAccountBindingReview, 0)
	for rows.Next() {
		item, err := scanBindingReview(rows)
		if err != nil {
			return nil, err
		}
		result = append(result, item)
	}
	return result, rows.Err()
}

func (r *repository) GetBindingReview(ctx context.Context, reviewID int64) (model.ProAccountBindingReview, bool, error) {
	if reviewID <= 0 {
		return model.ProAccountBindingReview{}, false, nil
	}
	item, err := scanBindingReview(r.db.QueryRowContext(ctx, `select
		id, discovery_key, source_type, source_locator, auth_index, source_fingerprint,
		resolution_status, candidate_ids_json, reason_code, resolved_account_id, resolved_at_ms,
		first_seen_at_ms, last_seen_at_ms, created_at_ms, updated_at_ms
		from pro_account_binding_reviews where id = ?`, reviewID))
	if errors.Is(err, sql.ErrNoRows) {
		return model.ProAccountBindingReview{}, false, nil
	}
	return item, err == nil, err
}

func (r *repository) RebindFromReview(ctx context.Context, reviewID int64, accountID string, expectedVersion int64, discovery model.ProAccountDiscovery, nowMS int64) (model.ProAccount, error) {
	accountID = strings.TrimSpace(accountID)
	if reviewID <= 0 || accountID == "" {
		return model.ProAccount{}, ErrBindingReviewNotFound
	}
	if nowMS <= 0 {
		nowMS = time.Now().UnixMilli()
	}
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return model.ProAccount{}, err
	}
	defer tx.Rollback()
	review, err := scanBindingReview(tx.QueryRowContext(ctx, `select
		id, discovery_key, source_type, source_locator, auth_index, source_fingerprint,
		resolution_status, candidate_ids_json, reason_code, resolved_account_id, resolved_at_ms,
		first_seen_at_ms, last_seen_at_ms, created_at_ms, updated_at_ms
		from pro_account_binding_reviews where id = ?`, reviewID))
	if errors.Is(err, sql.ErrNoRows) {
		return model.ProAccount{}, ErrBindingReviewNotFound
	}
	if err != nil {
		return model.ProAccount{}, err
	}
	if review.ResolutionStatus == "resolved" {
		if review.ResolvedAccountID != accountID {
			return model.ProAccount{}, ErrBindingReviewStateConflict
		}
		if err := tx.Commit(); err != nil {
			return model.ProAccount{}, err
		}
		item, ok, err := r.Get(ctx, accountID)
		if err != nil {
			return model.ProAccount{}, err
		}
		if !ok {
			return model.ProAccount{}, ErrAccountNotFound
		}
		return item, nil
	}
	if review.ResolutionStatus != model.ProBindingResolutionPending && review.ResolutionStatus != model.ProBindingResolutionConflict {
		return model.ProAccount{}, ErrBindingReviewStateConflict
	}
	validCandidate := false
	for _, candidateID := range review.CandidateIDs {
		if candidateID == accountID {
			validCandidate = true
			break
		}
	}
	if !validCandidate {
		return model.ProAccount{}, ErrBindingReviewCandidateInvalid
	}
	result, err := tx.ExecContext(ctx, `update pro_account_binding_reviews set
		resolution_status = 'resolved', reason_code = 'confirmed_rebind', resolved_account_id = ?,
		resolved_at_ms = ?, updated_at_ms = ? where id = ? and resolution_status in (?, ?)`,
		accountID, nowMS, nowMS, reviewID, model.ProBindingResolutionPending, model.ProBindingResolutionConflict)
	if err != nil {
		return model.ProAccount{}, err
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return model.ProAccount{}, err
	}
	if affected != 1 {
		return model.ProAccount{}, ErrBindingReviewStateConflict
	}
	if err := rebindManagedTx(ctx, tx, accountID, expectedVersion, discovery, nowMS); err != nil {
		return model.ProAccount{}, err
	}
	if err := tx.Commit(); err != nil {
		return model.ProAccount{}, err
	}
	item, ok, err := r.Get(ctx, accountID)
	if err != nil {
		return model.ProAccount{}, err
	}
	if !ok {
		return model.ProAccount{}, ErrAccountNotFound
	}
	return item, nil
}

func scanBindingReview(row rowScanner) (model.ProAccountBindingReview, error) {
	var item model.ProAccountBindingReview
	var authIndex, fingerprint, resolvedAccountID sql.NullString
	var resolvedAt sql.NullInt64
	var candidateJSON string
	if err := row.Scan(
		&item.ID, &item.DiscoveryKey, &item.SourceType, &item.SourceLocator, &authIndex, &fingerprint,
		&item.ResolutionStatus, &candidateJSON, &item.ReasonCode, &resolvedAccountID, &resolvedAt,
		&item.FirstSeenAtMS, &item.LastSeenAtMS, &item.CreatedAtMS, &item.UpdatedAtMS,
	); err != nil {
		return model.ProAccountBindingReview{}, err
	}
	item.AuthIndex = authIndex.String
	item.SourceFingerprint = fingerprint.String
	item.ResolvedAccountID = resolvedAccountID.String
	item.ResolvedAtMS = resolvedAt.Int64
	item.DriftType = "api_credential"
	if item.SourceType == "auth_file" {
		item.DriftType = "file_path"
	}
	item.CandidateIDs = []string{}
	if err := json.Unmarshal([]byte(candidateJSON), &item.CandidateIDs); err != nil {
		return model.ProAccountBindingReview{}, err
	}
	return item, nil
}
