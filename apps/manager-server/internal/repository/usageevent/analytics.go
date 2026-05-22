package usageevent

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
)

type AnalyticsFilter struct {
	FromMS            int64
	ToMS              int64
	SearchQuery       string
	SearchAPIKeyHash  string
	Models            []string
	AuthIndices       []string
	APIKeyHashes      []string
	SourceHashes      []string
	IncludeFailed     bool
	FailedOnly        bool
	ExcludeZeroTokens bool
}

type TimelinePoint struct {
	BucketMS int64
	Calls    int64
	Tokens   int64
	Success  int64
	Failure  int64
}

type HourlyPoint struct {
	Hour   int
	Calls  int64
	Tokens int64
}

type ChannelModelStat struct {
	AuthIndex    string
	Model        string
	BillingModel string
	Calls        int64
	SuccessCalls int64
	FailureCalls int64
	InputTokens  int64
	OutputTokens int64
	CachedTokens int64
	TotalTokens  int64
	AvgLatencyMS sql.NullFloat64
}

type FailureSourceStat struct {
	SourceHash   string
	AuthIndex    string
	Calls        int64
	FailureCalls int64
	LastSeenMS   int64
	AvgLatencyMS sql.NullFloat64
}

type TaskBucket struct {
	BucketKey    string
	Total        int64
	Success      int64
	Failure      int64
	FirstMS      int64
	LastMS       int64
	Source       string
	SourceHash   string
	AuthIndex    string
	Models       string
	Endpoints    string
	InputTokens  int64
	OutputTokens int64
	CachedTokens int64
	TotalTokens  int64
	AvgLatencyMS sql.NullFloat64
	MaxLatencyMS sql.NullInt64
}

type EventPageItem struct {
	EventHash             string
	TimestampMS           int64
	Timestamp             string
	Model                 string
	ResolvedModel         string
	Endpoint              string
	Method                string
	Path                  string
	AuthIndex             string
	Source                string
	SourceHash            string
	APIKeyHash            string
	AccountSnapshot       string
	AuthLabelSnapshot     string
	AuthProviderSnapshot  string
	AuthProjectIDSnapshot string
	InputTokens           int64
	OutputTokens          int64
	CachedTokens          int64
	ReasoningTokens       int64
	TotalTokens           int64
	LatencyMS             sql.NullInt64
	Failed                bool
}

type EventsPage struct {
	Items        []EventPageItem
	NextBeforeMS int64
	HasMore      bool
}

func (r *repository) AggregateWithFilter(ctx context.Context, filter AnalyticsFilter) (Aggregate, error) {
	where, args := analyticsWhere(filter)
	row := r.db.QueryRowContext(ctx, `select
	count(*),
	sum(case when failed = 0 then 1 else 0 end),
	sum(case when failed = 1 then 1 else 0 end),
	coalesce(sum(input_tokens), 0),
	coalesce(sum(output_tokens), 0),
	coalesce(sum(reasoning_tokens), 0),
	coalesce(sum(case when cached_tokens > cache_tokens then cached_tokens else cache_tokens end), 0),
	coalesce(sum(total_tokens), 0),
	avg(nullif(latency_ms, 0)),
	coalesce(sum(case when total_tokens = 0 and failed = 0 then 1 else 0 end), 0)
from usage_events `+where, args...)

	var agg Aggregate
	var success, failure sql.NullInt64
	if err := row.Scan(
		&agg.TotalCalls,
		&success,
		&failure,
		&agg.InputTokens,
		&agg.OutputTokens,
		&agg.ReasoningTokens,
		&agg.CachedTokens,
		&agg.TotalTokens,
		&agg.AvgLatencyMS,
		&agg.ZeroTokenCalls,
	); err != nil {
		return Aggregate{}, err
	}
	agg.SuccessCalls = success.Int64
	agg.FailureCalls = failure.Int64
	return agg, nil
}

func (r *repository) ModelStatsWithFilter(ctx context.Context, filter AnalyticsFilter, limit int) ([]ModelStat, error) {
	where, args := analyticsWhere(filter)
	query := `select
	model,
	coalesce(nullif(resolved_model, ''), model) as billing_model,
	count(*) as calls,
	sum(case when failed = 0 then 1 else 0 end) as success,
	coalesce(sum(input_tokens), 0),
	coalesce(sum(output_tokens), 0),
	coalesce(sum(reasoning_tokens), 0),
	coalesce(sum(case when cached_tokens > cache_tokens then cached_tokens else cache_tokens end), 0),
	coalesce(sum(total_tokens), 0)
from usage_events ` + where + `
group by model, billing_model
order by calls desc`
	if limit > 0 {
		query = `with filtered as (
	select * from usage_events ` + where + `
),
top_models as (
	select model, count(*) as model_calls
	from filtered
	group by model
	order by model_calls desc
	limit ?
)
select
	f.model,
	coalesce(nullif(f.resolved_model, ''), f.model) as billing_model,
	count(*) as calls,
	sum(case when f.failed = 0 then 1 else 0 end) as success,
	coalesce(sum(f.input_tokens), 0),
	coalesce(sum(f.output_tokens), 0),
	coalesce(sum(f.reasoning_tokens), 0),
	coalesce(sum(case when f.cached_tokens > f.cache_tokens then f.cached_tokens else f.cache_tokens end), 0),
	coalesce(sum(f.total_tokens), 0)
from filtered f
join top_models t on t.model = f.model
group by f.model, billing_model
order by max(t.model_calls) desc, f.model, calls desc`
		args = append(args, limit)
	}
	rows, err := r.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	stats := make([]ModelStat, 0)
	for rows.Next() {
		var stat ModelStat
		if err := rows.Scan(
			&stat.Model,
			&stat.BillingModel,
			&stat.Calls,
			&stat.SuccessCalls,
			&stat.InputTokens,
			&stat.OutputTokens,
			&stat.ReasoningTokens,
			&stat.CachedTokens,
			&stat.TotalTokens,
		); err != nil {
			return nil, err
		}
		stats = append(stats, stat)
	}
	return stats, rows.Err()
}

func (r *repository) TimelineWithFilter(ctx context.Context, filter AnalyticsFilter, granularity string) ([]TimelinePoint, error) {
	bucketSize := int64(60 * 60 * 1000)
	if granularity == "day" {
		bucketSize = 24 * 60 * 60 * 1000
	}
	where, args := analyticsWhere(filter)
	query := fmt.Sprintf(`select
	(timestamp_ms / %d) * %d as bucket_ms,
	count(*),
	coalesce(sum(total_tokens), 0),
	sum(case when failed = 0 then 1 else 0 end),
	sum(case when failed = 1 then 1 else 0 end)
from usage_events %s
group by bucket_ms
order by bucket_ms`, bucketSize, bucketSize, where)
	rows, err := r.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	points := make([]TimelinePoint, 0)
	for rows.Next() {
		var point TimelinePoint
		if err := rows.Scan(&point.BucketMS, &point.Calls, &point.Tokens, &point.Success, &point.Failure); err != nil {
			return nil, err
		}
		points = append(points, point)
	}
	return points, rows.Err()
}

func (r *repository) HourlyDistributionWithFilter(ctx context.Context, filter AnalyticsFilter) ([]HourlyPoint, error) {
	where, args := analyticsWhere(filter)
	rows, err := r.db.QueryContext(ctx, `select
	cast(strftime('%H', datetime(timestamp_ms / 1000, 'unixepoch')) as integer) as hour,
	count(*),
	coalesce(sum(total_tokens), 0)
from usage_events `+where+`
group by hour
order by hour`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	points := make([]HourlyPoint, 0)
	for rows.Next() {
		var point HourlyPoint
		if err := rows.Scan(&point.Hour, &point.Calls, &point.Tokens); err != nil {
			return nil, err
		}
		points = append(points, point)
	}
	return points, rows.Err()
}

func (r *repository) ChannelModelStatsWithFilter(ctx context.Context, filter AnalyticsFilter) ([]ChannelModelStat, error) {
	where, args := analyticsWhere(filter)
	rows, err := r.db.QueryContext(ctx, `select
	coalesce(auth_index, ''),
	model,
	coalesce(nullif(resolved_model, ''), model) as billing_model,
	count(*),
	sum(case when failed = 0 then 1 else 0 end),
	sum(case when failed = 1 then 1 else 0 end),
	coalesce(sum(input_tokens), 0),
	coalesce(sum(output_tokens), 0),
	coalesce(sum(case when cached_tokens > cache_tokens then cached_tokens else cache_tokens end), 0),
	coalesce(sum(total_tokens), 0),
	avg(nullif(latency_ms, 0))
from usage_events `+where+`
group by auth_index, model, billing_model
order by count(*) desc`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	stats := make([]ChannelModelStat, 0)
	for rows.Next() {
		var stat ChannelModelStat
		if err := rows.Scan(
			&stat.AuthIndex,
			&stat.Model,
			&stat.BillingModel,
			&stat.Calls,
			&stat.SuccessCalls,
			&stat.FailureCalls,
			&stat.InputTokens,
			&stat.OutputTokens,
			&stat.CachedTokens,
			&stat.TotalTokens,
			&stat.AvgLatencyMS,
		); err != nil {
			return nil, err
		}
		stats = append(stats, stat)
	}
	return stats, rows.Err()
}

func (r *repository) FailureSourcesWithFilter(ctx context.Context, filter AnalyticsFilter) ([]FailureSourceStat, error) {
	where, args := analyticsWhere(filter)
	rows, err := r.db.QueryContext(ctx, `select
	coalesce(source_hash, ''),
	coalesce(auth_index, ''),
	count(*),
	sum(case when failed = 1 then 1 else 0 end),
	max(timestamp_ms),
	avg(nullif(latency_ms, 0))
from usage_events `+where+`
group by source_hash, auth_index
having sum(case when failed = 1 then 1 else 0 end) > 0
order by sum(case when failed = 1 then 1 else 0 end) desc, max(timestamp_ms) desc`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	stats := make([]FailureSourceStat, 0)
	for rows.Next() {
		var stat FailureSourceStat
		if err := rows.Scan(&stat.SourceHash, &stat.AuthIndex, &stat.Calls, &stat.FailureCalls, &stat.LastSeenMS, &stat.AvgLatencyMS); err != nil {
			return nil, err
		}
		stats = append(stats, stat)
	}
	return stats, rows.Err()
}

func (r *repository) TaskBucketsWithFilter(ctx context.Context, filter AnalyticsFilter) ([]TaskBucket, error) {
	where, args := analyticsWhere(filter)
	rows, err := r.db.QueryContext(ctx, `select
	coalesce(timestamp, '') || '|' || coalesce(source_hash, '') || '|' || coalesce(auth_index, '') as bucket_key,
	count(*),
	sum(case when failed = 0 then 1 else 0 end),
	sum(case when failed = 1 then 1 else 0 end),
	min(timestamp_ms),
	max(timestamp_ms),
	coalesce(max(source), ''),
	coalesce(source_hash, ''),
	coalesce(auth_index, ''),
	coalesce(group_concat(distinct model), ''),
	coalesce(group_concat(distinct endpoint), ''),
	coalesce(sum(input_tokens), 0),
	coalesce(sum(output_tokens), 0),
	coalesce(sum(case when cached_tokens > cache_tokens then cached_tokens else cache_tokens end), 0),
	coalesce(sum(total_tokens), 0),
	avg(nullif(latency_ms, 0)),
	max(latency_ms)
from usage_events `+where+`
group by bucket_key, source_hash, auth_index
order by max(timestamp_ms) desc
limit 500`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	buckets := make([]TaskBucket, 0)
	for rows.Next() {
		var bucket TaskBucket
		if err := rows.Scan(
			&bucket.BucketKey,
			&bucket.Total,
			&bucket.Success,
			&bucket.Failure,
			&bucket.FirstMS,
			&bucket.LastMS,
			&bucket.Source,
			&bucket.SourceHash,
			&bucket.AuthIndex,
			&bucket.Models,
			&bucket.Endpoints,
			&bucket.InputTokens,
			&bucket.OutputTokens,
			&bucket.CachedTokens,
			&bucket.TotalTokens,
			&bucket.AvgLatencyMS,
			&bucket.MaxLatencyMS,
		); err != nil {
			return nil, err
		}
		buckets = append(buckets, bucket)
	}
	return buckets, rows.Err()
}

func (r *repository) RecentFailuresWithFilter(ctx context.Context, filter AnalyticsFilter, limit int) ([]RecentFailure, error) {
	if limit <= 0 {
		return nil, nil
	}
	filter.IncludeFailed = true
	where, args := analyticsWhere(filter)
	args = append(args, limit)
	rows, err := r.db.QueryContext(ctx, `select
	timestamp_ms,
	model,
	coalesce(api_key_hash, ''),
	coalesce(source_hash, ''),
	coalesce(auth_index, ''),
	coalesce(endpoint, ''),
	latency_ms
from usage_events `+where+`
and failed = 1
order by timestamp_ms desc, id desc
limit ?`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	failures := make([]RecentFailure, 0, limit)
	for rows.Next() {
		var failure RecentFailure
		if err := rows.Scan(
			&failure.TimestampMS,
			&failure.Model,
			&failure.APIKeyHash,
			&failure.SourceHash,
			&failure.AuthIndex,
			&failure.Endpoint,
			&failure.LatencyMS,
		); err != nil {
			return nil, err
		}
		failures = append(failures, failure)
	}
	return failures, rows.Err()
}

func (r *repository) EventsPageWithFilter(ctx context.Context, filter AnalyticsFilter, beforeMS int64, limit int) (EventsPage, error) {
	if limit <= 0 {
		return EventsPage{}, nil
	}
	queryLimit := limit + 1
	if beforeMS > 0 && beforeMS < filter.ToMS {
		filter.ToMS = beforeMS
	}
	where, args := analyticsWhere(filter)
	args = append(args, queryLimit)
	rows, err := r.db.QueryContext(ctx, `select
	event_hash,
	timestamp_ms,
	timestamp,
	model,
	coalesce(resolved_model, ''),
	coalesce(endpoint, ''),
	coalesce(method, ''),
	coalesce(path, ''),
	coalesce(auth_index, ''),
	coalesce(source, ''),
	coalesce(source_hash, ''),
	coalesce(api_key_hash, ''),
	coalesce(account_snapshot, ''),
	coalesce(auth_label_snapshot, ''),
	coalesce(auth_provider_snapshot, ''),
	coalesce(auth_project_id_snapshot, ''),
	input_tokens,
	output_tokens,
	case when cached_tokens > cache_tokens then cached_tokens else cache_tokens end,
	reasoning_tokens,
	total_tokens,
	latency_ms,
	failed
from usage_events `+where+`
order by timestamp_ms desc, id desc
limit ?`, args...)
	if err != nil {
		return EventsPage{}, err
	}
	defer rows.Close()

	items := make([]EventPageItem, 0, limit)
	for rows.Next() {
		var item EventPageItem
		var failed int
		if err := rows.Scan(
			&item.EventHash,
			&item.TimestampMS,
			&item.Timestamp,
			&item.Model,
			&item.ResolvedModel,
			&item.Endpoint,
			&item.Method,
			&item.Path,
			&item.AuthIndex,
			&item.Source,
			&item.SourceHash,
			&item.APIKeyHash,
			&item.AccountSnapshot,
			&item.AuthLabelSnapshot,
			&item.AuthProviderSnapshot,
			&item.AuthProjectIDSnapshot,
			&item.InputTokens,
			&item.OutputTokens,
			&item.CachedTokens,
			&item.ReasoningTokens,
			&item.TotalTokens,
			&item.LatencyMS,
			&failed,
		); err != nil {
			return EventsPage{}, err
		}
		item.Failed = failed != 0
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		return EventsPage{}, err
	}

	hasMore := len(items) > limit
	if hasMore {
		items = items[:limit]
	}
	nextBeforeMS := int64(0)
	if hasMore && len(items) > 0 {
		nextBeforeMS = items[len(items)-1].TimestampMS
	}
	return EventsPage{Items: items, NextBeforeMS: nextBeforeMS, HasMore: hasMore}, nil
}

func (r *repository) ActiveDaysWithFilter(ctx context.Context, filter AnalyticsFilter) (int64, error) {
	where, args := analyticsWhere(filter)
	var count int64
	if err := r.db.QueryRowContext(ctx, `select count(distinct (timestamp_ms / 86400000)) from usage_events `+where, args...).Scan(&count); err != nil {
		return 0, err
	}
	return count, nil
}

func (r *repository) ZeroTokenModelsWithFilter(ctx context.Context, filter AnalyticsFilter) ([]string, error) {
	where, args := analyticsWhere(filter)
	rows, err := r.db.QueryContext(ctx, `select distinct coalesce(model, '')
from usage_events `+where+`
and total_tokens = 0
and failed = 0
order by model`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	models := make([]string, 0)
	for rows.Next() {
		var model string
		if err := rows.Scan(&model); err != nil {
			return nil, err
		}
		if strings.TrimSpace(model) == "" {
			continue
		}
		models = append(models, model)
	}
	return models, rows.Err()
}

func analyticsWhere(filter AnalyticsFilter) (string, []any) {
	conditions := []string{"timestamp_ms >= ?", "timestamp_ms < ?"}
	args := []any{filter.FromMS, filter.ToMS}

	query := strings.TrimSpace(strings.ToLower(filter.SearchQuery))
	hash := strings.TrimSpace(strings.ToLower(filter.SearchAPIKeyHash))
	if query != "" {
		like := "%" + query + "%"
		if hash != "" {
			conditions = append(conditions, `(lower(coalesce(model, '')) like ? or lower(coalesce(resolved_model, '')) like ? or lower(coalesce(endpoint, '')) like ? or lower(coalesce(source, '')) like ? or lower(coalesce(source_hash, '')) like ? or lower(coalesce(api_key_hash, '')) like ? or lower(coalesce(auth_project_id_snapshot, '')) like ? or lower(coalesce(api_key_hash, '')) = ?)`)
			args = append(args, like, like, like, like, like, like, like, hash)
		} else {
			conditions = append(conditions, `(lower(coalesce(model, '')) like ? or lower(coalesce(resolved_model, '')) like ? or lower(coalesce(endpoint, '')) like ? or lower(coalesce(source, '')) like ? or lower(coalesce(source_hash, '')) like ? or lower(coalesce(api_key_hash, '')) like ? or lower(coalesce(auth_project_id_snapshot, '')) like ?)`)
			args = append(args, like, like, like, like, like, like, like)
		}
	} else if hash != "" {
		conditions = append(conditions, "lower(coalesce(api_key_hash, '')) = ?")
		args = append(args, hash)
	}
	addInCondition := func(column string, values []string) {
		normalized := normalizeFilterValues(values)
		if len(normalized) == 0 {
			return
		}
		placeholders := strings.TrimRight(strings.Repeat("?,", len(normalized)), ",")
		conditions = append(conditions, fmt.Sprintf("coalesce(%s, '') in (%s)", column, placeholders))
		for _, value := range normalized {
			args = append(args, value)
		}
	}
	addInCondition("model", filter.Models)
	addInCondition("auth_index", filter.AuthIndices)
	addInCondition("api_key_hash", filter.APIKeyHashes)
	addInCondition("source_hash", filter.SourceHashes)
	if !filter.IncludeFailed {
		conditions = append(conditions, "failed = 0")
	}
	if filter.FailedOnly {
		conditions = append(conditions, "failed = 1")
	}
	if filter.ExcludeZeroTokens {
		conditions = append(conditions, "total_tokens > 0")
	}

	return "where " + strings.Join(conditions, " and "), args
}

func normalizeFilterValues(values []string) []string {
	seen := map[string]struct{}{}
	result := make([]string, 0, len(values))
	for _, value := range values {
		trimmed := strings.TrimSpace(value)
		if trimmed == "" {
			continue
		}
		if _, ok := seen[trimmed]; ok {
			continue
		}
		seen[trimmed] = struct{}{}
		result = append(result, trimmed)
	}
	return result
}
