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
	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/usage"
)

type Repository interface {
	List(ctx context.Context, filter model.ProAccountListFilter) (model.ProAccountListResult, error)
	Get(ctx context.Context, id string) (model.ProAccount, bool, error)
	Sync(ctx context.Context, discoveries []model.ProAccountDiscovery, nowMS int64, dryRun bool) (model.ProAccountSyncResult, error)
	Usage(ctx context.Context, accountID string, fromMS int64, toMS int64) (model.ProAccountLocalUsage, []model.ProAccountUsageWindow, string, error)
	UsageCostStats(ctx context.Context, accountID string, fromMS int64, toMS int64) ([]model.ProAccountUsageCostStat, error)
	UpdatePlanType(ctx context.Context, accountID string, planType string) error
	UpdateMetadata(ctx context.Context, accountID string, expectedVersion int64, name string, notes string, nowMS int64) (model.ProAccount, error)
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
	Version      int    `json:"version"`
	Enabled      int    `json:"enabled"`
	LastUsedAtMS int64  `json:"lastUsedAtMs"`
	ID           string `json:"id"`
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
		a.id, a.platform, a.auth_type, a.source_type, a.plan_type, a.name, a.notes, a.email, a.enabled,
		a.health_status, a.last_error, a.allowed_models_json, a.model_mapping_json, a.model_rule_version,
		a.last_used_at_ms, a.last_tested_at_ms, a.expires_at_ms, a.deleted_at_ms, a.created_at_ms, a.updated_at_ms, a.version,
		b.id, b.auth_index, b.source_type, b.source_locator, b.source_fingerprint,
		b.binding_status, b.is_current, b.valid_from_ms, b.valid_to_ms, b.attribution_quality,
		b.first_seen_at_ms, b.last_seen_at_ms, b.created_at_ms, b.updated_at_ms
		from pro_accounts a
		left join pro_account_bindings b on b.pro_account_id = a.id and b.is_current = 1 ` +
		where + ` order by a.enabled desc, coalesce(a.last_used_at_ms, 0) desc, a.id desc limit ?`
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
		nextCursor = encodeCursor(listCursor{
			Version: 1, Enabled: boolInt(last.Enabled), LastUsedAtMS: last.LastUsedAtMS, ID: last.ID,
		})
	}
	return model.ProAccountListResult{Items: items, NextCursor: nextCursor, Total: total}, nil
}

func buildListWhere(filter model.ProAccountListFilter) (string, []any, error) {
	clauses := []string{`a.deleted_at_ms is null`}
	args := make([]any, 0)
	if search := strings.TrimSpace(filter.Search); search != "" {
		clauses = append(clauses, `(lower(coalesce(a.name, '')) like ? or lower(coalesce(a.notes, '')) like ? or lower(coalesce(a.email, '')) like ? or lower(a.id) like ?)`)
		like := "%" + strings.ToLower(search) + "%"
		args = append(args, like, like, like, like)
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
		clauses = append(clauses, `(a.enabled < ? or (a.enabled = ? and (
			coalesce(a.last_used_at_ms, 0) < ? or
			(coalesce(a.last_used_at_ms, 0) = ? and a.id < ?)
		)))`)
		args = append(args, cursor.Enabled, cursor.Enabled, cursor.LastUsedAtMS, cursor.LastUsedAtMS, cursor.ID)
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
	if cursor.Version != 1 || (cursor.Enabled != 0 && cursor.Enabled != 1) || cursor.LastUsedAtMS < 0 || strings.TrimSpace(cursor.ID) == "" {
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
		a.id, a.platform, a.auth_type, a.source_type, a.plan_type, a.name, a.notes, a.email, a.enabled,
		a.health_status, a.last_error, a.allowed_models_json, a.model_mapping_json, a.model_rule_version,
		a.last_used_at_ms, a.last_tested_at_ms, a.expires_at_ms, a.deleted_at_ms, a.created_at_ms, a.updated_at_ms, a.version,
		b.id, b.auth_index, b.source_type, b.source_locator, b.source_fingerprint,
		b.binding_status, b.is_current, b.valid_from_ms, b.valid_to_ms, b.attribution_quality,
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

func (r *repository) Usage(ctx context.Context, accountID string, fromMS int64, toMS int64) (model.ProAccountLocalUsage, []model.ProAccountUsageWindow, string, error) {
	local := model.ProAccountLocalUsage{FromMS: fromMS, ToMS: toMS}
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
		coalesce(max(e.timestamp_ms), 0)
		from usage_events e
		join pro_account_bindings b on b.pro_account_id = ?
			and coalesce(b.auth_index, '') <> '' and e.auth_index = b.auth_index
			and b.attribution_quality <> ?
			and e.timestamp_ms >= b.valid_from_ms
			and (b.valid_to_ms is null or e.timestamp_ms < b.valid_to_ms)
		where e.timestamp_ms >= ? and e.timestamp_ms < ?`, accountID, model.ProAttributionQualityAmbiguous, fromMS, toMS).Scan(
		&local.Requests, &local.Successes, &local.Failures, &local.InputTokens, &local.OutputTokens,
		&local.CachedTokens, &local.CacheReadTokens, &local.CacheCreationTokens, &local.ReasoningTokens,
		&local.TotalTokens, &local.LastActivityAtMS,
	)
	if err != nil {
		return local, nil, "", err
	}

	windows := make([]model.ProAccountUsageWindow, 0, 2)
	var usedPercent sql.NullFloat64
	var resetAt sql.NullInt64
	var metadataJSON sql.NullString
	err = r.db.QueryRowContext(ctx, `select
		e.header_quota_used_percent, e.header_quota_recover_at_ms, e.response_metadata_json
		from usage_events e
		join pro_account_bindings b on b.pro_account_id = ?
			and coalesce(b.auth_index, '') <> '' and e.auth_index = b.auth_index
			and b.attribution_quality <> ?
			and e.timestamp_ms >= b.valid_from_ms
			and (b.valid_to_ms is null or e.timestamp_ms < b.valid_to_ms)
		where e.timestamp_ms >= ? and e.timestamp_ms < ?
			and (e.header_quota_used_percent is not null or e.header_quota_recover_at_ms is not null
				or case when json_valid(e.response_metadata_json)
					then json_type(e.response_metadata_json, '$.quota.primary') = 'object'
						or json_type(e.response_metadata_json, '$.quota.secondary') = 'object'
					else 0 end)
		order by e.timestamp_ms desc, e.id desc limit 1`, accountID, model.ProAttributionQualityAmbiguous, fromMS, toMS).
		Scan(&usedPercent, &resetAt, &metadataJSON)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return local, nil, "", err
	}
	if err == nil {
		windows = passiveUsageWindows(metadataJSON.String, usedPercent, resetAt)
	}

	var planType sql.NullString
	err = r.db.QueryRowContext(ctx, `select e.header_quota_plan_type
		from usage_events e
		join pro_account_bindings b on b.pro_account_id = ?
			and coalesce(b.auth_index, '') <> '' and e.auth_index = b.auth_index
			and b.attribution_quality <> ?
			and e.timestamp_ms >= b.valid_from_ms
			and (b.valid_to_ms is null or e.timestamp_ms < b.valid_to_ms)
		where e.timestamp_ms >= ? and e.timestamp_ms < ?
			and coalesce(e.header_quota_plan_type, '') <> ''
		order by e.timestamp_ms desc, e.id desc limit 1`, accountID, model.ProAttributionQualityAmbiguous, fromMS, toMS).
		Scan(&planType)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return local, nil, "", err
	}
	return local, windows, normalizePlanType(planType.String), nil
}

// UsageCostStats 按统一账号的历史绑定有效期聚合计费所需 Token。
// 这里与 Monitoring 使用相同的输入、缓存和长上下文口径，但不在数据层解析价格。
func (r *repository) UsageCostStats(ctx context.Context, accountID string, fromMS int64, toMS int64) ([]model.ProAccountUsageCostStat, error) {
	rows, err := r.db.QueryContext(ctx, fmt.Sprintf(`select
		e.model,
		coalesce(nullif(e.resolved_model, ''), e.model) as billing_model,
		coalesce(e.service_tier, '') as service_tier,
		count(e.id),
		coalesce(sum(coalesce(e.normalized_total_input_tokens, e.input_tokens)), 0),
		coalesce(sum(e.output_tokens), 0),
		coalesce(sum(max(max(e.cached_tokens, e.cache_tokens) - max(e.cache_read_tokens, 0) - max(e.cache_creation_tokens, 0), 0)), 0),
		coalesce(sum(e.cache_read_tokens), 0),
		coalesce(sum(e.cache_creation_tokens), 0),
		coalesce(sum(case when coalesce(e.normalized_total_input_tokens, e.input_tokens) > %[1]d then coalesce(e.normalized_total_input_tokens, e.input_tokens) else 0 end), 0),
		coalesce(sum(case when coalesce(e.normalized_total_input_tokens, e.input_tokens) > %[1]d then e.output_tokens else 0 end), 0),
		coalesce(sum(case when coalesce(e.normalized_total_input_tokens, e.input_tokens) > %[1]d then max(max(e.cached_tokens, e.cache_tokens) - max(e.cache_read_tokens, 0) - max(e.cache_creation_tokens, 0), 0) else 0 end), 0),
		coalesce(sum(case when coalesce(e.normalized_total_input_tokens, e.input_tokens) > %[1]d then e.cache_read_tokens else 0 end), 0),
		coalesce(sum(case when coalesce(e.normalized_total_input_tokens, e.input_tokens) > %[1]d then e.cache_creation_tokens else 0 end), 0),
		coalesce(sum(e.total_tokens), 0)
		from usage_events e
		join pro_account_bindings b on b.pro_account_id = ?
			and coalesce(b.auth_index, '') <> '' and e.auth_index = b.auth_index
			and b.attribution_quality <> ?
			and e.timestamp_ms >= b.valid_from_ms
			and (b.valid_to_ms is null or e.timestamp_ms < b.valid_to_ms)
		where e.timestamp_ms >= ? and e.timestamp_ms < ?
		group by e.model, billing_model, service_tier
		order by e.model, billing_model, service_tier`, usage.LongContextInputTokenThreshold),
		accountID, model.ProAttributionQualityAmbiguous, fromMS, toMS)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	stats := make([]model.ProAccountUsageCostStat, 0)
	for rows.Next() {
		var stat model.ProAccountUsageCostStat
		if err := rows.Scan(
			&stat.Model,
			&stat.BillingModel,
			&stat.ServiceTier,
			&stat.Calls,
			&stat.InputTokens,
			&stat.OutputTokens,
			&stat.CachedTokens,
			&stat.CacheReadTokens,
			&stat.CacheCreationTokens,
			&stat.LongInputTokens,
			&stat.LongOutputTokens,
			&stat.LongCachedTokens,
			&stat.LongCacheReadTokens,
			&stat.LongCacheCreationTokens,
			&stat.TotalTokens,
		); err != nil {
			return nil, err
		}
		stats = append(stats, stat)
	}
	return stats, rows.Err()
}

func (r *repository) UpdatePlanType(ctx context.Context, accountID string, planType string) error {
	accountID = strings.TrimSpace(accountID)
	planType = normalizePlanType(planType)
	if accountID == "" || planType == "" {
		return nil
	}
	// 套餐属于上游派生元数据，不参与账号编辑版本冲突；只更新实际发生变化的有效账号。
	_, err := r.db.ExecContext(ctx, `update pro_accounts set plan_type = ?
		where id = ? and deleted_at_ms is null and plan_type is not ?`, planType, accountID, planType)
	return err
}

func (r *repository) UpdateMetadata(ctx context.Context, accountID string, expectedVersion int64, name string, notes string, nowMS int64) (model.ProAccount, error) {
	accountID = strings.TrimSpace(accountID)
	name = strings.TrimSpace(name)
	notes = strings.TrimSpace(notes)
	if accountID == "" {
		return model.ProAccount{}, ErrAccountNotFound
	}
	if expectedVersion <= 0 {
		return model.ProAccount{}, ErrVersionConflict
	}
	current, ok, err := r.Get(ctx, accountID)
	if err != nil {
		return model.ProAccount{}, err
	}
	if !ok {
		return model.ProAccount{}, ErrAccountNotFound
	}
	if current.Version != expectedVersion {
		return model.ProAccount{}, ErrVersionConflict
	}
	if name == "" {
		name = current.Name
	}
	if current.Name == name && current.Notes == notes {
		return current, nil
	}
	if nowMS <= 0 {
		nowMS = time.Now().UnixMilli()
	}
	result, err := r.db.ExecContext(ctx, `update pro_accounts set
		name = ?, notes = ?, updated_at_ms = ?, version = version + 1
		where id = ? and version = ? and deleted_at_ms is null`,
		nullString(name), nullString(notes), nowMS, accountID, expectedVersion)
	if err != nil {
		return model.ProAccount{}, err
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return model.ProAccount{}, err
	}
	if affected != 1 {
		return model.ProAccount{}, ErrVersionConflict
	}
	updated, ok, err := r.Get(ctx, accountID)
	if err != nil {
		return model.ProAccount{}, err
	}
	if !ok {
		return model.ProAccount{}, ErrAccountNotFound
	}
	return updated, nil
}

func passiveUsageWindows(metadataJSON string, usedPercent sql.NullFloat64, resetAt sql.NullInt64) []model.ProAccountUsageWindow {
	metadata := usage.ResponseHeaderMetadata{}
	if strings.TrimSpace(metadataJSON) != "" {
		_ = json.Unmarshal([]byte(metadataJSON), &metadata)
	}
	windows := make([]model.ProAccountUsageWindow, 0, 2)
	if metadata.Quota != nil {
		for index, quotaWindow := range []*usage.HeaderQuotaWindow{metadata.Quota.Primary, metadata.Quota.Secondary} {
			if quotaWindow == nil {
				continue
			}
			id, label := passiveWindowIdentity(quotaWindow.WindowMinutes, index)
			item := model.ProAccountUsageWindow{ID: id, Label: label, ResetAtMS: quotaWindow.ResetAtMS, Source: "passive"}
			if quotaWindow.UsedPercent != nil {
				used := clampUsagePercent(*quotaWindow.UsedPercent)
				remaining := 100 - used
				item.UsedPercent = &used
				item.RemainingPercent = &remaining
			}
			windows = append(windows, item)
		}
	}
	if len(windows) == 0 && (usedPercent.Valid || resetAt.Valid) {
		item := model.ProAccountUsageWindow{ID: "response_header", Label: "配额", ResetAtMS: resetAt.Int64, Source: "passive"}
		if usedPercent.Valid {
			used := clampUsagePercent(usedPercent.Float64)
			remaining := 100 - used
			item.UsedPercent = &used
			item.RemainingPercent = &remaining
		}
		windows = append(windows, item)
	}
	return windows
}

func passiveWindowIdentity(minutes *float64, index int) (string, string) {
	if minutes != nil {
		switch {
		case *minutes >= 28*24*60:
			return "monthly", "30d"
		case *minutes >= 6*24*60:
			return "weekly", "7d"
		case *minutes >= 4*60 && *minutes <= 6*60:
			return "five_hour", "5h"
		case *minutes >= 60:
			return fmt.Sprintf("window_%d", index+1), fmt.Sprintf("%.0fh", *minutes/60)
		case *minutes > 0:
			return fmt.Sprintf("window_%d", index+1), fmt.Sprintf("%.0fm", *minutes)
		}
	}
	if index == 0 {
		return "five_hour", "5h"
	}
	return "weekly", "7d"
}

func clampUsagePercent(value float64) float64 {
	if value < 0 {
		return 0
	}
	if value > 100 {
		return 100
	}
	return value
}

func normalizePlanType(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	value = strings.ReplaceAll(value, "-", "_")
	return strings.Join(strings.Fields(value), "_")
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

	authIndexCounts := make(map[string]int, len(discoveries))
	for _, discovery := range discoveries {
		if authIndex := strings.TrimSpace(discovery.AuthIndex); authIndex != "" {
			authIndexCounts[authIndex]++
		}
	}
	for _, discovery := range discoveries {
		authIndex := strings.TrimSpace(discovery.AuthIndex)
		uniqueAuthIndex := authIndex == "" || authIndexCounts[authIndex] == 1
		item, err := syncDiscovery(ctx, tx, discovery, nowMS, uniqueAuthIndex)
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

func syncDiscovery(ctx context.Context, tx *sql.Tx, discovery model.ProAccountDiscovery, nowMS int64, uniqueAuthIndex bool) (model.ProAccountSyncItem, error) {
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
		if err := repairCurrentBindingAttribution(ctx, tx, accountID, discovery.AuthIndex, nowMS, uniqueAuthIndex); err != nil {
			return item, err
		}
		if err := backfillAccountUsageSummary(ctx, tx, accountID); err != nil {
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
		id, platform, auth_type, source_type, plan_type, name, notes, email, enabled, health_status, last_error,
		allowed_models_json, model_mapping_json, model_rule_version, expires_at_ms, created_at_ms, updated_at_ms
	) values (?, ?, ?, ?, ?, ?, null, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		accountID, strings.ToLower(discovery.Platform), strings.ToLower(discovery.AuthType), discovery.SourceType,
		nullString(discovery.PlanType), nullString(discovery.Name), nullString(discovery.Email), boolInt(discovery.Enabled), valueOr(discovery.HealthStatus, model.ProAccountHealthUnknown),
		nullString(discovery.LastError), allowedJSON, mappingJSON, nullString(discovery.ModelRuleVersion), nullInt64(discovery.ExpiresAtMS), nowMS, nowMS); err != nil {
		return item, err
	}
	attribution, err := resolveInitialAttribution(ctx, tx, accountID, discovery.AuthIndex, nowMS, uniqueAuthIndex)
	if err != nil {
		return item, err
	}
	if _, err := tx.ExecContext(ctx, `insert into pro_account_bindings (
		pro_account_id, auth_index, source_type, source_locator, source_fingerprint, binding_status,
		is_current, valid_from_ms, attribution_quality, first_seen_at_ms, last_seen_at_ms, created_at_ms, updated_at_ms
	) values (?, ?, ?, ?, ?, ?, 1, ?, ?, ?, ?, ?, ?)`,
		accountID, nullString(discovery.AuthIndex), discovery.SourceType, discovery.SourceLocator,
		nullString(discovery.SourceFingerprint), model.ProBindingStatusCurrent,
		attribution.ValidFromMS, attribution.Quality, nowMS, nowMS, nowMS, nowMS); err != nil {
		return item, err
	}
	if err := backfillAccountUsageSummary(ctx, tx, accountID); err != nil {
		return item, err
	}
	item.Resolution = model.ProBindingResolutionCreated
	item.ProAccountID = accountID
	return item, nil
}

type bindingAttribution struct {
	ValidFromMS int64
	Quality     string
}

func resolveInitialAttribution(ctx context.Context, tx *sql.Tx, accountID string, authIndex string, nowMS int64, uniqueAuthIndex bool) (bindingAttribution, error) {
	authIndex = strings.TrimSpace(authIndex)
	result := bindingAttribution{ValidFromMS: nowMS, Quality: model.ProAttributionQualityExact}
	if authIndex == "" {
		return result, nil
	}
	if !uniqueAuthIndex {
		result.Quality = model.ProAttributionQualityAmbiguous
		return result, nil
	}
	duplicate, err := hasOtherCurrentBinding(ctx, tx, accountID, authIndex)
	if err != nil {
		return bindingAttribution{}, err
	}
	if duplicate {
		result.Quality = model.ProAttributionQualityAmbiguous
		return result, nil
	}

	var rawFrom sql.NullInt64
	if err := tx.QueryRowContext(ctx, `select min(timestamp_ms) from usage_events
		where auth_index = ? and timestamp_ms > 0`, authIndex).Scan(&rawFrom); err != nil {
		return bindingAttribution{}, err
	}
	if rawFrom.Valid && rawFrom.Int64 < result.ValidFromMS {
		result.ValidFromMS = rawFrom.Int64
		result.Quality = model.ProAttributionQualityRetainedHistory
	}

	var rollupFrom sql.NullInt64
	if err := tx.QueryRowContext(ctx, `select min(first_seen_ms) from usage_account_model_rollups
		where auth_index = ? and first_seen_ms > 0`, authIndex).Scan(&rollupFrom); err != nil {
		return bindingAttribution{}, err
	}
	if rollupFrom.Valid && rollupFrom.Int64 < result.ValidFromMS {
		result.Quality = model.ProAttributionQualityPartial
	}
	return result, nil
}

func repairCurrentBindingAttribution(ctx context.Context, tx *sql.Tx, accountID string, authIndex string, nowMS int64, uniqueAuthIndex bool) error {
	var bindingID, validFromMS int64
	var quality string
	err := tx.QueryRowContext(ctx, `select id, valid_from_ms, attribution_quality
		from pro_account_bindings where pro_account_id = ? and is_current = 1
			and coalesce(auth_index, '') = ? limit 1`, accountID, strings.TrimSpace(authIndex)).Scan(&bindingID, &validFromMS, &quality)
	if err != nil {
		return err
	}
	attribution, err := resolveInitialAttribution(ctx, tx, accountID, authIndex, nowMS, uniqueAuthIndex)
	if err != nil {
		return err
	}
	quality = valueOr(strings.TrimSpace(quality), model.ProAttributionQualityUnknown)
	if attribution.Quality == model.ProAttributionQualityAmbiguous {
		quality = model.ProAttributionQualityAmbiguous
	} else {
		switch quality {
		case model.ProAttributionQualityUnknown:
			if attribution.ValidFromMS < validFromMS {
				validFromMS = attribution.ValidFromMS
				quality = attribution.Quality
			} else if attribution.Quality == model.ProAttributionQualityPartial {
				quality = model.ProAttributionQualityPartial
			} else {
				quality = model.ProAttributionQualityExact
			}
		case model.ProAttributionQualityRetainedHistory:
			if attribution.ValidFromMS < validFromMS {
				validFromMS = attribution.ValidFromMS
			}
			if attribution.Quality == model.ProAttributionQualityPartial {
				quality = model.ProAttributionQualityPartial
			}
		case model.ProAttributionQualityPartial:
			if attribution.ValidFromMS < validFromMS {
				validFromMS = attribution.ValidFromMS
			}
		}
	}
	_, err = tx.ExecContext(ctx, `update pro_account_bindings set
		valid_from_ms = ?, attribution_quality = ?, updated_at_ms = ? where id = ?`,
		validFromMS, quality, nowMS, bindingID)
	return err
}

func managedAttributionQuality(ctx context.Context, tx *sql.Tx, accountID string, authIndex string) (string, error) {
	authIndex = strings.TrimSpace(authIndex)
	if authIndex == "" {
		return model.ProAttributionQualityExact, nil
	}
	duplicate, err := hasOtherCurrentBinding(ctx, tx, accountID, authIndex)
	if err != nil {
		return "", err
	}
	if duplicate {
		return model.ProAttributionQualityAmbiguous, nil
	}
	return model.ProAttributionQualityExact, nil
}

func hasOtherCurrentBinding(ctx context.Context, tx *sql.Tx, accountID string, authIndex string) (bool, error) {
	var count int
	err := tx.QueryRowContext(ctx, `select count(*) from pro_account_bindings
		where is_current = 1 and coalesce(auth_index, '') = ? and pro_account_id <> ?`,
		strings.TrimSpace(authIndex), strings.TrimSpace(accountID)).Scan(&count)
	return count > 0, err
}

func backfillAccountUsageSummary(ctx context.Context, tx *sql.Tx, accountID string) error {
	_, err := tx.ExecContext(ctx, `update pro_accounts set last_used_at_ms = (
		select max(e.timestamp_ms) from usage_events e
		join pro_account_bindings b on b.pro_account_id = pro_accounts.id
			and coalesce(b.auth_index, '') <> '' and e.auth_index = b.auth_index
			and b.attribution_quality <> ?
			and e.timestamp_ms >= b.valid_from_ms
			and (b.valid_to_ms is null or e.timestamp_ms < b.valid_to_ms)
			and lower(coalesce(e.endpoint, '')) not like '%/v0/management/account-test%'
			and lower(coalesce(e.path, '')) not like '%/v0/management/account-test%'
			and not exists (
				select 1 from pro_account_drafts d
				where d.pro_account_id = pro_accounts.id
					and d.operation_type = 'test'
					and e.timestamp_ms between d.created_at_ms and d.updated_at_ms
			)
		) where id = ?`, model.ProAttributionQualityAmbiguous, accountID)
	return err
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
		platform = ?, auth_type = ?, source_type = ?, plan_type = coalesce(?, plan_type), email = ?, enabled = ?,
		health_status = ?, last_error = ?, allowed_models_json = ?, model_mapping_json = ?, model_rule_version = ?,
		expires_at_ms = ?, updated_at_ms = ?, version = version + 1
		where id = ? and (
			platform is not ? or auth_type is not ? or source_type is not ? or plan_type is not coalesce(?, plan_type) or email is not ? or
			enabled is not ? or health_status is not ? or last_error is not ? or allowed_models_json is not ? or
			model_mapping_json is not ? or model_rule_version is not ? or expires_at_ms is not ?
		)`,
		strings.ToLower(discovery.Platform), strings.ToLower(discovery.AuthType), discovery.SourceType,
		nullString(discovery.PlanType), nullString(discovery.Email), boolInt(discovery.Enabled),
		valueOr(discovery.HealthStatus, model.ProAccountHealthUnknown), nullString(discovery.LastError),
		allowedJSON, mappingJSON, nullString(discovery.ModelRuleVersion), nullInt64(discovery.ExpiresAtMS), nowMS, accountID,
		strings.ToLower(discovery.Platform), strings.ToLower(discovery.AuthType), discovery.SourceType,
		nullString(discovery.PlanType), nullString(discovery.Email), boolInt(discovery.Enabled),
		valueOr(discovery.HealthStatus, model.ProAccountHealthUnknown), nullString(discovery.LastError),
		allowedJSON, mappingJSON, nullString(discovery.ModelRuleVersion), nullInt64(discovery.ExpiresAtMS))
	return err
}

func findFingerprintCandidates(ctx context.Context, tx *sql.Tx, fingerprint string) ([]string, error) {
	fingerprint = strings.TrimSpace(fingerprint)
	if fingerprint == "" {
		return nil, nil
	}
	rows, err := tx.QueryContext(ctx, `select distinct b.pro_account_id from pro_account_bindings b
		join pro_accounts a on a.id = b.pro_account_id and a.deleted_at_ms is null
		where b.source_fingerprint = ? order by b.pro_account_id limit 20`, fingerprint)
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
	var planType, name, notes, email, lastError, allowedJSON, mappingJSON, modelRuleVersion sql.NullString
	var lastUsed, lastTested, expires, deletedAt sql.NullInt64
	var enabled int
	var bindingID sql.NullInt64
	var authIndex, bindingSourceType, sourceLocator, sourceFingerprint, bindingStatus, attributionQuality sql.NullString
	var isCurrent sql.NullInt64
	var validFrom, validTo, firstSeen, lastSeen, bindingCreated, bindingUpdated sql.NullInt64
	if err := row.Scan(
		&item.ID, &item.Platform, &item.AuthType, &item.SourceType, &planType, &name, &notes, &email, &enabled,
		&item.HealthStatus, &lastError, &allowedJSON, &mappingJSON, &modelRuleVersion,
		&lastUsed, &lastTested, &expires, &deletedAt, &item.CreatedAtMS, &item.UpdatedAtMS, &item.Version,
		&bindingID, &authIndex, &bindingSourceType, &sourceLocator, &sourceFingerprint,
		&bindingStatus, &isCurrent, &validFrom, &validTo, &attributionQuality,
		&firstSeen, &lastSeen, &bindingCreated, &bindingUpdated,
	); err != nil {
		return model.ProAccount{}, err
	}
	item.Name = name.String
	item.Notes = notes.String
	item.PlanType = planType.String
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
			AttributionQuality: valueOr(attributionQuality.String, model.ProAttributionQualityUnknown),
			FirstSeenAtMS:      firstSeen.Int64, LastSeenAtMS: lastSeen.Int64,
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
		if err := repairCurrentBindingAttribution(ctx, tx, accountID, discovery.AuthIndex, nowMS, true); err != nil {
			return err
		}
	} else {
		if err == nil {
			if _, err := tx.ExecContext(ctx, `update pro_account_bindings set
				binding_status = 'historical', is_current = 0, valid_to_ms = ?, updated_at_ms = ? where id = ?`, nowMS, nowMS, bindingID); err != nil {
				return err
			}
		}
		quality, err := managedAttributionQuality(ctx, tx, accountID, discovery.AuthIndex)
		if err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `insert into pro_account_bindings (
			pro_account_id, auth_index, source_type, source_locator, source_fingerprint, binding_status,
			is_current, valid_from_ms, attribution_quality, first_seen_at_ms, last_seen_at_ms, created_at_ms, updated_at_ms
		) values (?, ?, ?, ?, ?, ?, 1, ?, ?, ?, ?, ?, ?)`,
			accountID, nullString(discovery.AuthIndex), discovery.SourceType, discovery.SourceLocator,
			nullString(discovery.SourceFingerprint), model.ProBindingStatusCurrent,
			nowMS, quality, nowMS, nowMS, nowMS, nowMS); err != nil {
			return err
		}
	}
	result, err := tx.ExecContext(ctx, `update pro_accounts set
		platform = ?, auth_type = ?, source_type = ?, plan_type = coalesce(?, plan_type), name = ?, email = ?, enabled = ?,
		health_status = ?, last_error = ?, allowed_models_json = ?, model_mapping_json = ?,
		model_rule_version = ?, expires_at_ms = ?, updated_at_ms = ?, version = version + 1
		where id = ? and version = ? and deleted_at_ms is null`,
		strings.ToLower(discovery.Platform), strings.ToLower(discovery.AuthType), discovery.SourceType,
		nullString(discovery.PlanType), nullString(discovery.Name), nullString(discovery.Email), boolInt(discovery.Enabled),
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
	if err := backfillAccountUsageSummary(ctx, tx, accountID); err != nil {
		return err
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
