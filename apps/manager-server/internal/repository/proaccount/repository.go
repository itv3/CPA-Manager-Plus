package proaccount

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/model"
)

type Repository interface {
	List(ctx context.Context, filter model.ProAccountListFilter) (model.ProAccountListResult, error)
	Get(ctx context.Context, id string) (model.ProAccount, bool, error)
	Sync(ctx context.Context, discoveries []model.ProAccountDiscovery, nowMS int64, dryRun bool) (model.ProAccountSyncResult, error)
	Usage(ctx context.Context, accountID string, fromMS int64, toMS int64) (model.ProAccountLocalUsage, []model.ProAccountUsageWindow, error)
	UpdateModelRules(ctx context.Context, accountID string, expectedVersion int64, allowedModels []string, modelMapping map[string]string, modelRuleVersion string, nowMS int64) (model.ProAccount, error)
	RecordTestResult(ctx context.Context, accountID string, success bool, errorCode string, nowMS int64) (model.ProAccount, error)
	RebindManaged(ctx context.Context, accountID string, expectedVersion int64, discovery model.ProAccountDiscovery, nowMS int64) (model.ProAccount, error)
	SoftDelete(ctx context.Context, accountID string, expectedVersion int64, nowMS int64) (model.ProAccount, error)
	ListBindingReviews(ctx context.Context, statuses []string, limit int) ([]model.ProAccountBindingReview, error)
	GetBindingReview(ctx context.Context, reviewID int64) (model.ProAccountBindingReview, bool, error)
	RebindFromReview(ctx context.Context, reviewID int64, accountID string, expectedVersion int64, discovery model.ProAccountDiscovery, nowMS int64) (model.ProAccount, error)
}

var (
	ErrAccountNotFound = errors.New("pro account not found")
	ErrVersionConflict = errors.New("pro account version conflict")
)

type repository struct {
	db *sql.DB
}

type listCursor struct {
	UpdatedAtMS int64  `json:"updatedAtMs"`
	ID          string `json:"id"`
}

func New(db *sql.DB) Repository {
	return &repository{db: db}
}

func (r *repository) List(ctx context.Context, filter model.ProAccountListFilter) (model.ProAccountListResult, error) {
	if filter.Limit <= 0 || filter.Limit > 200 {
		filter.Limit = 50
	}
	where, args, err := buildListWhere(filter)
	if err != nil {
		return model.ProAccountListResult{}, err
	}

	var total int64
	if err := r.db.QueryRowContext(ctx, `select count(*) from pro_accounts a `+where, args...).Scan(&total); err != nil {
		return model.ProAccountListResult{}, err
	}

	query := `select
		a.id, a.platform, a.auth_type, a.source_type, a.name, a.email, a.enabled,
		a.health_status, a.last_error, a.allowed_models_json, a.model_mapping_json, a.model_rule_version,
		a.last_used_at_ms, a.last_tested_at_ms, a.expires_at_ms, a.deleted_at_ms, a.created_at_ms, a.updated_at_ms, a.version,
		b.id, b.auth_index, b.source_type, b.source_locator, b.source_fingerprint,
		b.binding_status, b.is_current, b.valid_from_ms, b.valid_to_ms,
		b.first_seen_at_ms, b.last_seen_at_ms, b.created_at_ms, b.updated_at_ms
		from pro_accounts a
		left join pro_account_bindings b on b.pro_account_id = a.id and b.is_current = 1 ` +
		where + ` order by a.updated_at_ms desc, a.id desc limit ?`
	queryArgs := append(append([]any{}, args...), filter.Limit+1)
	rows, err := r.db.QueryContext(ctx, query, queryArgs...)
	if err != nil {
		return model.ProAccountListResult{}, err
	}
	defer rows.Close()

	items := make([]model.ProAccount, 0, filter.Limit)
	for rows.Next() {
		item, err := scanAccount(rows)
		if err != nil {
			return model.ProAccountListResult{}, err
		}
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		return model.ProAccountListResult{}, err
	}

	nextCursor := ""
	if len(items) > filter.Limit {
		last := items[filter.Limit-1]
		items = items[:filter.Limit]
		nextCursor = encodeCursor(listCursor{UpdatedAtMS: last.UpdatedAtMS, ID: last.ID})
	}
	return model.ProAccountListResult{Items: items, NextCursor: nextCursor, Total: total}, nil
}

func buildListWhere(filter model.ProAccountListFilter) (string, []any, error) {
	clauses := []string{`a.deleted_at_ms is null`}
	args := make([]any, 0)
	if search := strings.TrimSpace(filter.Search); search != "" {
		clauses = append(clauses, `(lower(coalesce(a.name, '')) like ? or lower(coalesce(a.email, '')) like ? or lower(a.id) like ?)`)
		like := "%" + strings.ToLower(search) + "%"
		args = append(args, like, like, like)
	}
	if value := strings.TrimSpace(filter.Platform); value != "" {
		clauses = append(clauses, `a.platform = ?`)
		args = append(args, strings.ToLower(value))
	}
	if value := strings.TrimSpace(filter.AuthType); value != "" {
		clauses = append(clauses, `a.auth_type = ?`)
		args = append(args, strings.ToLower(value))
	}
	if filter.Enabled != nil {
		clauses = append(clauses, `a.enabled = ?`)
		args = append(args, boolInt(*filter.Enabled))
	}
	if value := strings.TrimSpace(filter.HealthStatus); value != "" {
		clauses = append(clauses, `a.health_status = ?`)
		args = append(args, strings.ToLower(value))
	}
	if raw := strings.TrimSpace(filter.Cursor); raw != "" {
		cursor, err := decodeCursor(raw)
		if err != nil {
			return "", nil, fmt.Errorf("invalid cursor: %w", err)
		}
		clauses = append(clauses, `(a.updated_at_ms < ? or (a.updated_at_ms = ? and a.id < ?))`)
		args = append(args, cursor.UpdatedAtMS, cursor.UpdatedAtMS, cursor.ID)
	}
	return "where " + strings.Join(clauses, " and "), args, nil
}

func encodeCursor(cursor listCursor) string {
	data, _ := json.Marshal(cursor)
	return base64.RawURLEncoding.EncodeToString(data)
}

func decodeCursor(raw string) (listCursor, error) {
	data, err := base64.RawURLEncoding.DecodeString(raw)
	if err != nil {
		return listCursor{}, err
	}
	var cursor listCursor
	if err := json.Unmarshal(data, &cursor); err != nil {
		return listCursor{}, err
	}
	if cursor.UpdatedAtMS <= 0 || strings.TrimSpace(cursor.ID) == "" {
		return listCursor{}, errors.New("cursor fields are required")
	}
	return cursor, nil
}

func (r *repository) Get(ctx context.Context, id string) (model.ProAccount, bool, error) {
	id = strings.TrimSpace(id)
	if id == "" {
		return model.ProAccount{}, false, nil
	}
	row := r.db.QueryRowContext(ctx, `select
		a.id, a.platform, a.auth_type, a.source_type, a.name, a.email, a.enabled,
		a.health_status, a.last_error, a.allowed_models_json, a.model_mapping_json, a.model_rule_version,
		a.last_used_at_ms, a.last_tested_at_ms, a.expires_at_ms, a.deleted_at_ms, a.created_at_ms, a.updated_at_ms, a.version,
		b.id, b.auth_index, b.source_type, b.source_locator, b.source_fingerprint,
		b.binding_status, b.is_current, b.valid_from_ms, b.valid_to_ms,
		b.first_seen_at_ms, b.last_seen_at_ms, b.created_at_ms, b.updated_at_ms
		from pro_accounts a
		left join pro_account_bindings b on b.pro_account_id = a.id and b.is_current = 1
		where a.id = ?`, id)
	item, err := scanAccount(row)
	if errors.Is(err, sql.ErrNoRows) {
		return model.ProAccount{}, false, nil
	}
	return item, err == nil, err
}

func (r *repository) Usage(ctx context.Context, accountID string, fromMS int64, toMS int64) (model.ProAccountLocalUsage, []model.ProAccountUsageWindow, error) {
	local := model.ProAccountLocalUsage{FromMS: fromMS, ToMS: toMS}
	var estimatedCost sql.NullFloat64
	var unknownPriceEvents int64
	err := r.db.QueryRowContext(ctx, `select
		count(e.id),
		coalesce(sum(case when e.failed = 0 then 1 else 0 end), 0),
		coalesce(sum(case when e.failed = 1 then 1 else 0 end), 0),
		coalesce(sum(e.input_tokens), 0),
		coalesce(sum(e.output_tokens), 0),
		coalesce(sum(e.cached_tokens), 0),
		coalesce(sum(e.cache_read_tokens), 0),
		coalesce(sum(e.cache_creation_tokens), 0),
		coalesce(sum(e.reasoning_tokens), 0),
		coalesce(sum(e.total_tokens), 0),
		coalesce(max(e.timestamp_ms), 0),
		coalesce(sum(case when mp.model is null then 1 else 0 end), 0),
		sum(case when mp.model is null then null else
			(coalesce(e.input_tokens, 0) * mp.prompt_per_1m +
			 coalesce(e.output_tokens, 0) * mp.completion_per_1m +
			 coalesce(e.cache_read_tokens, e.cached_tokens, 0) * mp.cache_read_per_1m +
			 coalesce(e.cache_creation_tokens, 0) * mp.cache_creation_per_1m) / 1000000.0 end)
		from usage_events e
		join pro_account_bindings b on b.pro_account_id = ?
			and coalesce(b.auth_index, '') <> '' and e.auth_index = b.auth_index
			and e.timestamp_ms >= b.valid_from_ms
			and (b.valid_to_ms is null or e.timestamp_ms < b.valid_to_ms)
		left join model_prices mp on mp.model = coalesce(nullif(e.resolved_model, ''), e.model)
		where e.timestamp_ms >= ? and e.timestamp_ms < ?`, accountID, fromMS, toMS).Scan(
		&local.Requests, &local.Successes, &local.Failures, &local.InputTokens, &local.OutputTokens,
		&local.CachedTokens, &local.CacheReadTokens, &local.CacheCreationTokens, &local.ReasoningTokens,
		&local.TotalTokens, &local.LastActivityAtMS, &unknownPriceEvents, &estimatedCost,
	)
	if err != nil {
		return local, nil, err
	}
	if local.Requests > 0 && unknownPriceEvents == 0 && estimatedCost.Valid {
		cost := estimatedCost.Float64
		local.EstimatedCost = &cost
		local.CostKnown = true
	}

	windows := make([]model.ProAccountUsageWindow, 0, 1)
	var usedPercent sql.NullFloat64
	var resetAt sql.NullInt64
	var label sql.NullString
	err = r.db.QueryRowContext(ctx, `select
		e.header_quota_used_percent, e.header_quota_recover_at_ms, e.header_quota_plan_type
		from usage_events e
		join pro_account_bindings b on b.pro_account_id = ?
			and coalesce(b.auth_index, '') <> '' and e.auth_index = b.auth_index
			and e.timestamp_ms >= b.valid_from_ms
			and (b.valid_to_ms is null or e.timestamp_ms < b.valid_to_ms)
		where e.timestamp_ms >= ? and e.timestamp_ms < ?
			and (e.header_quota_used_percent is not null or e.header_quota_recover_at_ms is not null)
		order by e.timestamp_ms desc, e.id desc limit 1`, accountID, fromMS, toMS).Scan(&usedPercent, &resetAt, &label)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return local, nil, err
	}
	if err == nil {
		window := model.ProAccountUsageWindow{ID: "response_header", Label: valueOr(label.String, "上游响应头"), ResetAtMS: resetAt.Int64, Source: "passive"}
		if usedPercent.Valid {
			used := usedPercent.Float64
			remaining := 100 - used
			if remaining < 0 {
				remaining = 0
			}
			window.UsedPercent = &used
			window.RemainingPercent = &remaining
		}
		windows = append(windows, window)
	}
	return local, windows, nil
}

func (r *repository) Sync(ctx context.Context, discoveries []model.ProAccountDiscovery, nowMS int64, dryRun bool) (model.ProAccountSyncResult, error) {
	if nowMS <= 0 {
		nowMS = time.Now().UnixMilli()
	}
	result := model.ProAccountSyncResult{DryRun: dryRun, Discovered: len(discoveries), Items: make([]model.ProAccountSyncItem, 0, len(discoveries))}
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return result, err
	}
	defer tx.Rollback()

	for _, discovery := range discoveries {
		item, err := syncDiscovery(ctx, tx, discovery, nowMS)
		if err != nil {
			return result, err
		}
		result.Items = append(result.Items, item)
		switch item.Resolution {
		case model.ProBindingResolutionCreated:
			result.Created++
		case model.ProBindingResolutionExact:
			result.Updated++
		case model.ProBindingResolutionPending:
			result.Pending++
		case model.ProBindingResolutionConflict:
			result.Conflicts++
		}
	}
	if dryRun {
		return result, nil
	}
	if err := tx.Commit(); err != nil {
		return result, err
	}
	return result, nil
}

func syncDiscovery(ctx context.Context, tx *sql.Tx, discovery model.ProAccountDiscovery, nowMS int64) (model.ProAccountSyncItem, error) {
	discovery.SourceType = strings.TrimSpace(discovery.SourceType)
	discovery.SourceLocator = strings.TrimSpace(discovery.SourceLocator)
	discovery.AuthIndex = strings.TrimSpace(discovery.AuthIndex)
	item := model.ProAccountSyncItem{SourceLocator: discovery.SourceLocator, AuthIndex: discovery.AuthIndex}
	if discovery.SourceType == "" || discovery.SourceLocator == "" {
		return item, errors.New("source type and source locator are required")
	}

	var accountID string
	err := tx.QueryRowContext(ctx, `select pro_account_id from pro_account_bindings
		where source_type = ? and source_locator = ? and coalesce(auth_index, '') = ? and is_current = 1
		limit 1`, discovery.SourceType, discovery.SourceLocator, discovery.AuthIndex).Scan(&accountID)
	if err == nil {
		if err := updateDiscoveredAccount(ctx, tx, accountID, discovery, nowMS); err != nil {
			return item, err
		}
		if _, err := tx.ExecContext(ctx, `update pro_account_bindings set
			source_fingerprint = ?, binding_status = ?, last_seen_at_ms = ?, updated_at_ms = ?
			where pro_account_id = ? and source_type = ? and source_locator = ? and coalesce(auth_index, '') = ? and is_current = 1`,
			nullString(discovery.SourceFingerprint), model.ProBindingStatusCurrent, nowMS, nowMS,
			accountID, discovery.SourceType, discovery.SourceLocator, discovery.AuthIndex); err != nil {
			return item, err
		}
		item.Resolution = model.ProBindingResolutionExact
		item.ProAccountID = accountID
		return item, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return item, err
	}

	candidates, err := findFingerprintCandidates(ctx, tx, discovery.SourceFingerprint)
	if err != nil {
		return item, err
	}
	if len(candidates) > 0 {
		resolution := model.ProBindingResolutionPending
		reasonCode := bindingDriftReason(discovery.SourceType, false)
		if len(candidates) > 1 {
			resolution = model.ProBindingResolutionConflict
			reasonCode = bindingDriftReason(discovery.SourceType, true)
		}
		if err := upsertBindingReview(ctx, tx, discovery, candidates, resolution, reasonCode, nowMS); err != nil {
			return item, err
		}
		item.Resolution = resolution
		item.CandidateIDs = candidates
		item.ReasonCode = reasonCode
		return item, nil
	}

	accountID, err = newUUID()
	if err != nil {
		return item, err
	}
	allowedJSON, mappingJSON, err := marshalModelRules(discovery.AllowedModels, discovery.ModelMapping)
	if err != nil {
		return item, err
	}
	if _, err := tx.ExecContext(ctx, `insert into pro_accounts (
		id, platform, auth_type, source_type, name, email, enabled, health_status, last_error,
		allowed_models_json, model_mapping_json, model_rule_version, expires_at_ms, created_at_ms, updated_at_ms
	) values (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		accountID, strings.ToLower(discovery.Platform), strings.ToLower(discovery.AuthType), discovery.SourceType,
		nullString(discovery.Name), nullString(discovery.Email), boolInt(discovery.Enabled), valueOr(discovery.HealthStatus, model.ProAccountHealthUnknown),
		nullString(discovery.LastError), allowedJSON, mappingJSON, nullString(discovery.ModelRuleVersion), nullInt64(discovery.ExpiresAtMS), nowMS, nowMS); err != nil {
		return item, err
	}
	if _, err := tx.ExecContext(ctx, `insert into pro_account_bindings (
		pro_account_id, auth_index, source_type, source_locator, source_fingerprint, binding_status,
		is_current, valid_from_ms, first_seen_at_ms, last_seen_at_ms, created_at_ms, updated_at_ms
	) values (?, ?, ?, ?, ?, ?, 1, ?, ?, ?, ?, ?)`,
		accountID, nullString(discovery.AuthIndex), discovery.SourceType, discovery.SourceLocator,
		nullString(discovery.SourceFingerprint), model.ProBindingStatusCurrent, nowMS, nowMS, nowMS, nowMS, nowMS); err != nil {
		return item, err
	}
	item.Resolution = model.ProBindingResolutionCreated
	item.ProAccountID = accountID
	return item, nil
}

func bindingDriftReason(sourceType string, conflict bool) string {
	prefix := "api_credential_drift"
	if strings.TrimSpace(sourceType) == "auth_file" {
		prefix = "file_path_drift"
	}
	if conflict {
		return prefix + "_conflict"
	}
	return prefix + "_confirmation"
}

func updateDiscoveredAccount(ctx context.Context, tx *sql.Tx, accountID string, discovery model.ProAccountDiscovery, nowMS int64) error {
	allowedJSON, mappingJSON, err := marshalModelRules(discovery.AllowedModels, discovery.ModelMapping)
	if err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, `update pro_accounts set
		platform = ?, auth_type = ?, source_type = ?, name = ?, email = ?, enabled = ?,
		health_status = ?, last_error = ?, allowed_models_json = ?, model_mapping_json = ?, model_rule_version = ?,
		expires_at_ms = ?, updated_at_ms = ?, version = version + 1
		where id = ? and (
			platform is not ? or auth_type is not ? or source_type is not ? or name is not ? or email is not ? or
			enabled is not ? or health_status is not ? or last_error is not ? or allowed_models_json is not ? or
			model_mapping_json is not ? or model_rule_version is not ? or expires_at_ms is not ?
		)`,
		strings.ToLower(discovery.Platform), strings.ToLower(discovery.AuthType), discovery.SourceType,
		nullString(discovery.Name), nullString(discovery.Email), boolInt(discovery.Enabled),
		valueOr(discovery.HealthStatus, model.ProAccountHealthUnknown), nullString(discovery.LastError),
		allowedJSON, mappingJSON, nullString(discovery.ModelRuleVersion), nullInt64(discovery.ExpiresAtMS), nowMS, accountID,
		strings.ToLower(discovery.Platform), strings.ToLower(discovery.AuthType), discovery.SourceType,
		nullString(discovery.Name), nullString(discovery.Email), boolInt(discovery.Enabled),
		valueOr(discovery.HealthStatus, model.ProAccountHealthUnknown), nullString(discovery.LastError),
		allowedJSON, mappingJSON, nullString(discovery.ModelRuleVersion), nullInt64(discovery.ExpiresAtMS))
	return err
}

func findFingerprintCandidates(ctx context.Context, tx *sql.Tx, fingerprint string) ([]string, error) {
	fingerprint = strings.TrimSpace(fingerprint)
	if fingerprint == "" {
		return nil, nil
	}
	rows, err := tx.QueryContext(ctx, `select distinct pro_account_id from pro_account_bindings
		where source_fingerprint = ? order by pro_account_id limit 20`, fingerprint)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := make([]string, 0)
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		items = append(items, id)
	}
	return items, rows.Err()
}

func upsertBindingReview(ctx context.Context, tx *sql.Tx, discovery model.ProAccountDiscovery, candidates []string, resolution string, reasonCode string, nowMS int64) error {
	candidateJSON, err := json.Marshal(candidates)
	if err != nil {
		return err
	}
	discoveryKey := discovery.SourceType + "\x00" + discovery.SourceLocator + "\x00" + discovery.AuthIndex
	_, err = tx.ExecContext(ctx, `insert into pro_account_binding_reviews (
		discovery_key, source_type, source_locator, auth_index, source_fingerprint,
		resolution_status, candidate_ids_json, reason_code, first_seen_at_ms, last_seen_at_ms, created_at_ms, updated_at_ms
	) values (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		on conflict(discovery_key) do update set
		source_fingerprint = excluded.source_fingerprint,
		resolution_status = excluded.resolution_status,
		candidate_ids_json = excluded.candidate_ids_json,
		reason_code = excluded.reason_code,
		last_seen_at_ms = excluded.last_seen_at_ms,
		updated_at_ms = excluded.updated_at_ms`,
		discoveryKey, discovery.SourceType, discovery.SourceLocator, nullString(discovery.AuthIndex),
		nullString(discovery.SourceFingerprint), resolution, string(candidateJSON), reasonCode,
		nowMS, nowMS, nowMS, nowMS)
	return err
}

type rowScanner interface {
	Scan(dest ...any) error
}

func scanAccount(row rowScanner) (model.ProAccount, error) {
	var item model.ProAccount
	var name, email, lastError, allowedJSON, mappingJSON, modelRuleVersion sql.NullString
	var lastUsed, lastTested, expires, deletedAt sql.NullInt64
	var enabled int
	var bindingID sql.NullInt64
	var authIndex, bindingSourceType, sourceLocator, sourceFingerprint, bindingStatus sql.NullString
	var isCurrent sql.NullInt64
	var validFrom, validTo, firstSeen, lastSeen, bindingCreated, bindingUpdated sql.NullInt64
	if err := row.Scan(
		&item.ID, &item.Platform, &item.AuthType, &item.SourceType, &name, &email, &enabled,
		&item.HealthStatus, &lastError, &allowedJSON, &mappingJSON, &modelRuleVersion,
		&lastUsed, &lastTested, &expires, &deletedAt, &item.CreatedAtMS, &item.UpdatedAtMS, &item.Version,
		&bindingID, &authIndex, &bindingSourceType, &sourceLocator, &sourceFingerprint,
		&bindingStatus, &isCurrent, &validFrom, &validTo, &firstSeen, &lastSeen, &bindingCreated, &bindingUpdated,
	); err != nil {
		return model.ProAccount{}, err
	}
	item.Name = name.String
	item.Email = email.String
	item.Enabled = enabled != 0
	item.LastError = lastError.String
	item.ModelRuleVersion = modelRuleVersion.String
	item.LastUsedAtMS = lastUsed.Int64
	item.LastTestedAtMS = lastTested.Int64
	item.ExpiresAtMS = expires.Int64
	item.DeletedAtMS = deletedAt.Int64
	item.AllowedModels = []string{}
	item.ModelMapping = map[string]string{}
	if allowedJSON.Valid && allowedJSON.String != "" {
		_ = json.Unmarshal([]byte(allowedJSON.String), &item.AllowedModels)
	}
	if mappingJSON.Valid && mappingJSON.String != "" {
		_ = json.Unmarshal([]byte(mappingJSON.String), &item.ModelMapping)
	}
	if bindingID.Valid {
		item.Binding = &model.ProAccountBinding{
			ID: bindingID.Int64, ProAccountID: item.ID, AuthIndex: authIndex.String,
			SourceType: bindingSourceType.String, SourceLocator: sourceLocator.String,
			SourceFingerprint: sourceFingerprint.String, BindingStatus: bindingStatus.String,
			IsCurrent: isCurrent.Int64 != 0, ValidFromMS: validFrom.Int64, ValidToMS: validTo.Int64,
			FirstSeenAtMS: firstSeen.Int64, LastSeenAtMS: lastSeen.Int64,
			CreatedAtMS: bindingCreated.Int64, UpdatedAtMS: bindingUpdated.Int64,
		}
	}
	return item, nil
}

func (r *repository) UpdateModelRules(ctx context.Context, accountID string, expectedVersion int64, allowedModels []string, modelMapping map[string]string, modelRuleVersion string, nowMS int64) (model.ProAccount, error) {
	accountID = strings.TrimSpace(accountID)
	if accountID == "" {
		return model.ProAccount{}, ErrAccountNotFound
	}
	if expectedVersion <= 0 {
		return model.ProAccount{}, ErrVersionConflict
	}
	allowedJSON, mappingJSON, err := marshalModelRules(allowedModels, modelMapping)
	if err != nil {
		return model.ProAccount{}, err
	}
	result, err := r.db.ExecContext(ctx, `update pro_accounts set
		allowed_models_json = ?, model_mapping_json = ?, model_rule_version = ?, updated_at_ms = ?, version = version + 1
		where id = ? and version = ?`, allowedJSON, mappingJSON, nullString(modelRuleVersion), nowMS, accountID, expectedVersion)
	if err != nil {
		return model.ProAccount{}, err
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return model.ProAccount{}, err
	}
	if affected == 0 {
		if _, ok, getErr := r.Get(ctx, accountID); getErr != nil {
			return model.ProAccount{}, getErr
		} else if !ok {
			return model.ProAccount{}, ErrAccountNotFound
		}
		return model.ProAccount{}, ErrVersionConflict
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

func (r *repository) RecordTestResult(ctx context.Context, accountID string, success bool, errorCode string, nowMS int64) (model.ProAccount, error) {
	accountID = strings.TrimSpace(accountID)
	if accountID == "" {
		return model.ProAccount{}, ErrAccountNotFound
	}
	healthStatus := "error"
	if success {
		healthStatus = "healthy"
		errorCode = ""
	}
	if nowMS <= 0 {
		nowMS = time.Now().UnixMilli()
	}
	result, err := r.db.ExecContext(ctx, `update pro_accounts set
		health_status = ?, last_error = ?, last_tested_at_ms = ?, updated_at_ms = ? where id = ?`,
		healthStatus, nullString(errorCode), nowMS, nowMS, accountID)
	if err != nil {
		return model.ProAccount{}, err
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return model.ProAccount{}, err
	}
	if affected == 0 {
		return model.ProAccount{}, ErrAccountNotFound
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

func (r *repository) RebindManaged(ctx context.Context, accountID string, expectedVersion int64, discovery model.ProAccountDiscovery, nowMS int64) (model.ProAccount, error) {
	if nowMS <= 0 {
		nowMS = time.Now().UnixMilli()
	}
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return model.ProAccount{}, err
	}
	defer tx.Rollback()
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

func rebindManagedTx(ctx context.Context, tx *sql.Tx, accountID string, expectedVersion int64, discovery model.ProAccountDiscovery, nowMS int64) error {
	accountID = strings.TrimSpace(accountID)
	discovery.SourceType = strings.TrimSpace(discovery.SourceType)
	discovery.SourceLocator = strings.TrimSpace(discovery.SourceLocator)
	discovery.AuthIndex = strings.TrimSpace(discovery.AuthIndex)
	if accountID == "" || discovery.SourceType == "" || discovery.SourceLocator == "" {
		return ErrAccountNotFound
	}
	if expectedVersion <= 0 {
		return ErrVersionConflict
	}
	allowedJSON, mappingJSON, err := marshalModelRules(discovery.AllowedModels, discovery.ModelMapping)
	if err != nil {
		return err
	}
	var currentVersion int64
	if err := tx.QueryRowContext(ctx, `select version from pro_accounts where id = ? and deleted_at_ms is null`, accountID).Scan(&currentVersion); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return ErrAccountNotFound
		}
		return err
	}
	if currentVersion != expectedVersion {
		return ErrVersionConflict
	}
	var bindingID int64
	var currentSourceType, currentLocator, currentAuthIndex string
	err = tx.QueryRowContext(ctx, `select id, source_type, source_locator, coalesce(auth_index, '')
		from pro_account_bindings where pro_account_id = ? and is_current = 1 limit 1`, accountID).
		Scan(&bindingID, &currentSourceType, &currentLocator, &currentAuthIndex)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	sameBinding := err == nil && currentSourceType == discovery.SourceType && currentLocator == discovery.SourceLocator && currentAuthIndex == discovery.AuthIndex
	if sameBinding {
		if _, err := tx.ExecContext(ctx, `update pro_account_bindings set
			source_fingerprint = ?, binding_status = ?, last_seen_at_ms = ?, updated_at_ms = ? where id = ?`,
			nullString(discovery.SourceFingerprint), model.ProBindingStatusCurrent, nowMS, nowMS, bindingID); err != nil {
			return err
		}
	} else {
		if err == nil {
			if _, err := tx.ExecContext(ctx, `update pro_account_bindings set
				binding_status = 'historical', is_current = 0, valid_to_ms = ?, updated_at_ms = ? where id = ?`, nowMS, nowMS, bindingID); err != nil {
				return err
			}
		}
		if _, err := tx.ExecContext(ctx, `insert into pro_account_bindings (
			pro_account_id, auth_index, source_type, source_locator, source_fingerprint, binding_status,
			is_current, valid_from_ms, first_seen_at_ms, last_seen_at_ms, created_at_ms, updated_at_ms
		) values (?, ?, ?, ?, ?, ?, 1, ?, ?, ?, ?, ?)`,
			accountID, nullString(discovery.AuthIndex), discovery.SourceType, discovery.SourceLocator,
			nullString(discovery.SourceFingerprint), model.ProBindingStatusCurrent, nowMS, nowMS, nowMS, nowMS, nowMS); err != nil {
			return err
		}
	}
	result, err := tx.ExecContext(ctx, `update pro_accounts set
		platform = ?, auth_type = ?, source_type = ?, name = ?, email = ?, enabled = ?,
		health_status = ?, last_error = ?, allowed_models_json = ?, model_mapping_json = ?,
		model_rule_version = ?, expires_at_ms = ?, updated_at_ms = ?, version = version + 1
		where id = ? and version = ? and deleted_at_ms is null`,
		strings.ToLower(discovery.Platform), strings.ToLower(discovery.AuthType), discovery.SourceType,
		nullString(discovery.Name), nullString(discovery.Email), boolInt(discovery.Enabled),
		valueOr(discovery.HealthStatus, model.ProAccountHealthUnknown), nullString(discovery.LastError),
		allowedJSON, mappingJSON, nullString(discovery.ModelRuleVersion), nullInt64(discovery.ExpiresAtMS), nowMS,
		accountID, expectedVersion)
	if err != nil {
		return err
	}
	if affected, err := result.RowsAffected(); err != nil || affected != 1 {
		if err != nil {
			return err
		}
		return ErrVersionConflict
	}
	return nil
}

func (r *repository) SoftDelete(ctx context.Context, accountID string, expectedVersion int64, nowMS int64) (model.ProAccount, error) {
	accountID = strings.TrimSpace(accountID)
	if accountID == "" {
		return model.ProAccount{}, ErrAccountNotFound
	}
	if expectedVersion <= 0 {
		return model.ProAccount{}, ErrVersionConflict
	}
	if nowMS <= 0 {
		nowMS = time.Now().UnixMilli()
	}
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return model.ProAccount{}, err
	}
	defer tx.Rollback()
	result, err := tx.ExecContext(ctx, `update pro_accounts set
		enabled = 0, health_status = 'deleted', deleted_at_ms = ?, updated_at_ms = ?, version = version + 1
		where id = ? and version = ? and deleted_at_ms is null`, nowMS, nowMS, accountID, expectedVersion)
	if err != nil {
		return model.ProAccount{}, err
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return model.ProAccount{}, err
	}
	if affected == 0 {
		var exists int
		if scanErr := tx.QueryRowContext(ctx, `select count(*) from pro_accounts where id = ?`, accountID).Scan(&exists); scanErr != nil {
			return model.ProAccount{}, scanErr
		}
		if exists == 0 {
			return model.ProAccount{}, ErrAccountNotFound
		}
		return model.ProAccount{}, ErrVersionConflict
	}
	if _, err := tx.ExecContext(ctx, `update pro_account_bindings set
		binding_status = 'deleted', is_current = 0, valid_to_ms = ?, updated_at_ms = ?
		where pro_account_id = ? and is_current = 1`, nowMS, nowMS, accountID); err != nil {
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

func marshalModelRules(allowed []string, mapping map[string]string) (string, string, error) {
	if allowed == nil {
		allowed = []string{}
	}
	if mapping == nil {
		mapping = map[string]string{}
	}
	allowedData, err := json.Marshal(allowed)
	if err != nil {
		return "", "", err
	}
	mappingData, err := json.Marshal(mapping)
	if err != nil {
		return "", "", err
	}
	return string(allowedData), string(mappingData), nil
}

func newUUID() (string, error) {
	data := make([]byte, 16)
	if _, err := rand.Read(data); err != nil {
		return "", err
	}
	data[6] = (data[6] & 0x0f) | 0x40
	data[8] = (data[8] & 0x3f) | 0x80
	return fmt.Sprintf("%s-%s-%s-%s-%s",
		hex.EncodeToString(data[0:4]), hex.EncodeToString(data[4:6]), hex.EncodeToString(data[6:8]),
		hex.EncodeToString(data[8:10]), hex.EncodeToString(data[10:16])), nil
}

func nullString(value string) any {
	value = strings.TrimSpace(value)
	if value == "" {
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

func boolInt(value bool) int {
	if value {
		return 1
	}
	return 0
}

func valueOr(value string, fallback string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return fallback
	}
	return strings.ToLower(value)
}
