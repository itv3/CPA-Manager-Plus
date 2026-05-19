package monitoring

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/seakee/cpa-manager-plus/usage-service/internal/service/pricing"
	"github.com/seakee/cpa-manager-plus/usage-service/internal/store"
)

const (
	defaultEventsLimit = 100
	maxEventsLimit     = 50000
	recentWindowMS     = 30 * 60 * 1000
)

type Service struct {
	store *store.Store
}

func New(store *store.Store) *Service {
	return &Service{store: store}
}

type Request struct {
	FromMS           int64   `json:"from_ms"`
	ToMS             int64   `json:"to_ms"`
	NowMS            int64   `json:"now_ms"`
	SearchQuery      string  `json:"search_query"`
	SearchAPIKeyHash string  `json:"search_api_key_hash"`
	Filters          Filters `json:"filters"`
	Include          Include `json:"include"`
}

type Filters struct {
	Models            []string `json:"models"`
	AuthIndices       []string `json:"auth_indices"`
	APIKeyHashes      []string `json:"api_key_hashes"`
	SourceHashes      []string `json:"source_hashes"`
	IncludeFailed     *bool    `json:"include_failed"`
	FailedOnly        bool     `json:"failed_only"`
	ExcludeZeroTokens bool     `json:"exclude_zero_token"`
}

type Include struct {
	Summary            bool        `json:"summary"`
	Timeline           bool        `json:"timeline"`
	HourlyDistribution bool        `json:"hourly_distribution"`
	ModelShare         bool        `json:"model_share"`
	ChannelShare       bool        `json:"channel_share"`
	ModelStats         bool        `json:"model_stats"`
	FailureSources     bool        `json:"failure_sources"`
	TaskBuckets        bool        `json:"task_buckets"`
	RecentFailures     int         `json:"recent_failures"`
	EventsPage         *EventsPage `json:"events_page"`
	Granularity        string      `json:"granularity"`
}

type EventsPage struct {
	Limit    int    `json:"limit"`
	BeforeMS *int64 `json:"before_ms"`
}

type Response struct {
	GeneratedAtMS      int64              `json:"generated_at_ms"`
	Granularity        string             `json:"granularity"`
	Summary            *Summary           `json:"summary,omitempty"`
	Timeline           []TimelinePoint    `json:"timeline,omitempty"`
	HourlyDistribution []HourlyPoint      `json:"hourly_distribution,omitempty"`
	ModelShare         []ModelShareRow    `json:"model_share,omitempty"`
	ModelStats         []ModelStat        `json:"model_stats,omitempty"`
	ChannelShare       []ChannelShareRow  `json:"channel_share,omitempty"`
	FailureSources     []FailureSourceRow `json:"failure_sources,omitempty"`
	TaskBuckets        []TaskBucketRow    `json:"task_buckets,omitempty"`
	RecentFailures     []RecentFailure    `json:"recent_failures,omitempty"`
	Events             *EventsResponse    `json:"events,omitempty"`
}

type Summary struct {
	TotalCalls            int64    `json:"total_calls"`
	SuccessCalls          int64    `json:"success_calls"`
	FailureCalls          int64    `json:"failure_calls"`
	SuccessRate           float64  `json:"success_rate"`
	InputTokens           int64    `json:"input_tokens"`
	OutputTokens          int64    `json:"output_tokens"`
	CachedTokens          int64    `json:"cached_tokens"`
	ReasoningTokens       int64    `json:"reasoning_tokens"`
	TotalTokens           int64    `json:"total_tokens"`
	TotalCost             float64  `json:"total_cost"`
	AverageLatencyMS      *float64 `json:"average_latency_ms"`
	ZeroTokenCalls        int64    `json:"zero_token_calls"`
	RPM30M                float64  `json:"rpm_30m"`
	TPM30M                float64  `json:"tpm_30m"`
	AvgDailyRequests      float64  `json:"avg_daily_requests"`
	AvgDailyTokens        float64  `json:"avg_daily_tokens"`
	ApproxTasks           int64    `json:"approx_tasks"`
	ApproxTaskFailures    int64    `json:"approx_task_failures"`
	ApproxTaskSuccessRate float64  `json:"approx_task_success_rate"`
	ZeroTokenModels       []string `json:"zero_token_models"`
}

type TimelinePoint struct {
	BucketMS int64  `json:"bucket_ms"`
	Label    string `json:"label"`
	Calls    int64  `json:"calls"`
	Tokens   int64  `json:"tokens"`
	Success  int64  `json:"success"`
	Failure  int64  `json:"failure"`
}

type HourlyPoint struct {
	Hour   int   `json:"hour"`
	Calls  int64 `json:"calls"`
	Tokens int64 `json:"tokens"`
}

type ModelShareRow struct {
	Model  string  `json:"model"`
	Calls  int64   `json:"calls"`
	Tokens int64   `json:"tokens"`
	Cost   float64 `json:"cost"`
}

type ModelStat struct {
	Model        string  `json:"model"`
	Calls        int64   `json:"calls"`
	SuccessCalls int64   `json:"success_calls"`
	FailureCalls int64   `json:"failure_calls"`
	SuccessRate  float64 `json:"success_rate"`
	InputTokens  int64   `json:"input_tokens"`
	OutputTokens int64   `json:"output_tokens"`
	CachedTokens int64   `json:"cached_tokens"`
	TotalTokens  int64   `json:"total_tokens"`
	Cost         float64 `json:"cost"`
}

type ChannelShareRow struct {
	AuthIndex    string   `json:"auth_index"`
	Calls        int64    `json:"calls"`
	Success      int64    `json:"success"`
	Failure      int64    `json:"failure"`
	Tokens       int64    `json:"tokens"`
	Cost         float64  `json:"cost"`
	AvgLatencyMS *float64 `json:"average_latency_ms"`
}

type FailureSourceRow struct {
	SourceHash   string   `json:"source_hash"`
	AuthIndex    string   `json:"auth_index"`
	Calls        int64    `json:"calls"`
	Failure      int64    `json:"failure"`
	LastSeenMS   int64    `json:"last_seen_ms"`
	AvgLatencyMS *float64 `json:"average_latency_ms"`
}

type TaskBucketRow struct {
	BucketKey    string   `json:"bucket_key"`
	Total        int64    `json:"total"`
	Success      int64    `json:"success"`
	Failure      int64    `json:"failure"`
	FirstMS      int64    `json:"first_ms"`
	LastMS       int64    `json:"last_ms"`
	Source       string   `json:"source"`
	SourceHash   string   `json:"source_hash"`
	AuthIndex    string   `json:"auth_index"`
	Models       []string `json:"models"`
	Endpoints    []string `json:"endpoints"`
	InputTokens  int64    `json:"input_tokens"`
	OutputTokens int64    `json:"output_tokens"`
	CachedTokens int64    `json:"cached_tokens"`
	TotalTokens  int64    `json:"total_tokens"`
	AvgLatencyMS *float64 `json:"average_latency_ms"`
	MaxLatencyMS *int64   `json:"max_latency_ms"`
}

type RecentFailure struct {
	TimestampMS int64  `json:"timestamp_ms"`
	Model       string `json:"model"`
	APIKeyHash  string `json:"api_key_hash"`
	SourceHash  string `json:"source_hash"`
	AuthIndex   string `json:"auth_index"`
	Endpoint    string `json:"endpoint"`
	DurationMS  *int64 `json:"duration_ms"`
}

type EventsResponse struct {
	Items        []EventRow `json:"items"`
	NextBeforeMS int64      `json:"next_before_ms"`
	HasMore      bool       `json:"has_more"`
}

type EventRow struct {
	EventHash            string `json:"event_hash"`
	TimestampMS          int64  `json:"timestamp_ms"`
	Model                string `json:"model"`
	Endpoint             string `json:"endpoint"`
	Method               string `json:"method"`
	Path                 string `json:"path"`
	AuthIndex            string `json:"auth_index"`
	Source               string `json:"source"`
	SourceHash           string `json:"source_hash"`
	APIKeyHash           string `json:"api_key_hash"`
	AccountSnapshot      string `json:"account_snapshot"`
	AuthLabelSnapshot    string `json:"auth_label_snapshot"`
	AuthProviderSnapshot string `json:"auth_provider_snapshot"`
	InputTokens          int64  `json:"input_tokens"`
	OutputTokens         int64  `json:"output_tokens"`
	CachedTokens         int64  `json:"cached_tokens"`
	ReasoningTokens      int64  `json:"reasoning_tokens"`
	TotalTokens          int64  `json:"total_tokens"`
	LatencyMS            *int64 `json:"latency_ms"`
	Failed               bool   `json:"failed"`
}

func (s *Service) Analytics(ctx context.Context, req Request) (Response, error) {
	if req.FromMS <= 0 || req.ToMS <= 0 || req.FromMS >= req.ToMS {
		return Response{}, errors.New("from_ms and to_ms are required and from_ms must be less than to_ms")
	}
	nowMS := req.NowMS
	if nowMS <= 0 {
		nowMS = time.Now().UnixMilli()
	}
	granularity := normalizeGranularity(req.Include.Granularity, req.FromMS, req.ToMS)
	filter := buildFilter(req)
	prices, err := s.store.LoadModelPrices(ctx)
	if err != nil {
		return Response{}, err
	}

	response := Response{
		GeneratedAtMS: time.Now().UnixMilli(),
		Granularity:   granularity,
	}

	var modelStats []store.ModelStat
	needsModelStats := req.Include.Summary || req.Include.ModelShare || req.Include.ModelStats
	if needsModelStats {
		modelStats, err = s.store.ModelStatsWithFilter(ctx, filter, 0)
		if err != nil {
			return Response{}, err
		}
	}

	var taskBuckets []store.TaskBucket
	if req.Include.Summary || req.Include.TaskBuckets {
		taskBuckets, err = s.store.TaskBucketsWithFilter(ctx, filter)
		if err != nil {
			return Response{}, err
		}
	}

	if req.Include.Summary {
		agg, err := s.store.AggregateWithFilter(ctx, filter)
		if err != nil {
			return Response{}, err
		}
		rollingFilter := filter
		rollingFilter.FromMS = nowMS - recentWindowMS
		rollingFilter.ToMS = nowMS
		rollingAgg, err := s.store.AggregateWithFilter(ctx, rollingFilter)
		if err != nil {
			return Response{}, err
		}
		activeDays, err := s.store.ActiveDaysWithFilter(ctx, filter)
		if err != nil {
			return Response{}, err
		}
		zeroTokenModels, err := s.store.ZeroTokenModelsWithFilter(ctx, filter)
		if err != nil {
			return Response{}, err
		}
		response.Summary = buildSummary(agg, rollingAgg, activeDays, modelStats, taskBuckets, prices, zeroTokenModels)
	}
	if req.Include.Timeline {
		points, err := s.store.TimelineWithFilter(ctx, filter, granularity)
		if err != nil {
			return Response{}, err
		}
		response.Timeline = buildTimeline(points, granularity)
	}
	if req.Include.HourlyDistribution {
		points, err := s.store.HourlyDistributionWithFilter(ctx, filter)
		if err != nil {
			return Response{}, err
		}
		response.HourlyDistribution = buildHourly(points)
	}
	if req.Include.ModelShare {
		response.ModelShare = buildModelShare(modelStats, prices)
	}
	if req.Include.ModelStats {
		response.ModelStats = buildModelStats(modelStats, prices)
	}
	if req.Include.ChannelShare {
		stats, err := s.store.ChannelModelStatsWithFilter(ctx, filter)
		if err != nil {
			return Response{}, err
		}
		response.ChannelShare = buildChannelShare(stats, prices)
	}
	if req.Include.FailureSources {
		stats, err := s.store.FailureSourcesWithFilter(ctx, filter)
		if err != nil {
			return Response{}, err
		}
		response.FailureSources = buildFailureSources(stats)
	}
	if req.Include.TaskBuckets {
		response.TaskBuckets = buildTaskBuckets(taskBuckets)
	}
	if req.Include.RecentFailures > 0 {
		failures, err := s.store.RecentFailuresWithFilter(ctx, filter, req.Include.RecentFailures)
		if err != nil {
			return Response{}, err
		}
		response.RecentFailures = buildRecentFailures(failures)
	}
	if req.Include.EventsPage != nil {
		limit := req.Include.EventsPage.Limit
		if limit <= 0 {
			limit = defaultEventsLimit
		}
		if limit > maxEventsLimit {
			limit = maxEventsLimit
		}
		beforeMS := int64(0)
		if req.Include.EventsPage.BeforeMS != nil {
			beforeMS = *req.Include.EventsPage.BeforeMS
		}
		page, err := s.store.EventsPageWithFilter(ctx, filter, beforeMS, limit)
		if err != nil {
			return Response{}, err
		}
		response.Events = buildEvents(page)
	}

	return response, nil
}

func buildFilter(req Request) store.AnalyticsFilter {
	includeFailed := true
	if req.Filters.IncludeFailed != nil {
		includeFailed = *req.Filters.IncludeFailed
	}
	return store.AnalyticsFilter{
		FromMS:            req.FromMS,
		ToMS:              req.ToMS,
		SearchQuery:       req.SearchQuery,
		SearchAPIKeyHash:  req.SearchAPIKeyHash,
		Models:            req.Filters.Models,
		AuthIndices:       req.Filters.AuthIndices,
		APIKeyHashes:      req.Filters.APIKeyHashes,
		SourceHashes:      req.Filters.SourceHashes,
		IncludeFailed:     includeFailed,
		FailedOnly:        req.Filters.FailedOnly,
		ExcludeZeroTokens: req.Filters.ExcludeZeroTokens,
	}
}

func normalizeGranularity(input string, fromMS int64, toMS int64) string {
	if input == "day" || input == "hour" {
		return input
	}
	if toMS-fromMS <= 24*60*60*1000 {
		return "hour"
	}
	return "day"
}

func buildSummary(agg store.Aggregate, rolling store.Aggregate, activeDays int64, modelStats []store.ModelStat, taskBuckets []store.TaskBucket, prices map[string]store.ModelPrice, zeroTokenModels []string) *Summary {
	dayCount := activeDays
	if dayCount <= 0 {
		dayCount = 1
	}
	taskFailures := int64(0)
	for _, bucket := range taskBuckets {
		if bucket.Failure > 0 {
			taskFailures++
		}
	}
	approxTasks := int64(len(taskBuckets))
	return &Summary{
		TotalCalls:            agg.TotalCalls,
		SuccessCalls:          agg.SuccessCalls,
		FailureCalls:          agg.FailureCalls,
		SuccessRate:           ratio(agg.SuccessCalls, agg.TotalCalls),
		InputTokens:           agg.InputTokens,
		OutputTokens:          agg.OutputTokens,
		CachedTokens:          agg.CachedTokens,
		ReasoningTokens:       agg.ReasoningTokens,
		TotalTokens:           agg.TotalTokens,
		TotalCost:             sumCost(modelStats, prices),
		AverageLatencyMS:      nullableFloat(agg.AvgLatencyMS.Valid, agg.AvgLatencyMS.Float64),
		ZeroTokenCalls:        agg.ZeroTokenCalls,
		RPM30M:                float64(rolling.TotalCalls) / 30,
		TPM30M:                float64(rolling.TotalTokens) / 30,
		AvgDailyRequests:      float64(agg.TotalCalls) / float64(dayCount),
		AvgDailyTokens:        float64(agg.TotalTokens) / float64(dayCount),
		ApproxTasks:           approxTasks,
		ApproxTaskFailures:    taskFailures,
		ApproxTaskSuccessRate: ratio(approxTasks-taskFailures, approxTasks),
		ZeroTokenModels:       zeroTokenModels,
	}
}

func buildTimeline(points []store.TimelinePoint, granularity string) []TimelinePoint {
	result := make([]TimelinePoint, 0, len(points))
	for _, point := range points {
		result = append(result, TimelinePoint{
			BucketMS: point.BucketMS,
			Label:    timelineLabel(point.BucketMS, granularity),
			Calls:    point.Calls,
			Tokens:   point.Tokens,
			Success:  point.Success,
			Failure:  point.Failure,
		})
	}
	return result
}

func buildHourly(points []store.HourlyPoint) []HourlyPoint {
	result := make([]HourlyPoint, 0, len(points))
	for _, point := range points {
		result = append(result, HourlyPoint(point))
	}
	return result
}

func buildModelShare(stats []store.ModelStat, prices map[string]store.ModelPrice) []ModelShareRow {
	result := make([]ModelShareRow, 0, len(stats))
	for _, stat := range stats {
		result = append(result, ModelShareRow{
			Model:  stat.Model,
			Calls:  stat.Calls,
			Tokens: stat.TotalTokens,
			Cost:   costForStat(stat, prices),
		})
	}
	return result
}

func buildModelStats(stats []store.ModelStat, prices map[string]store.ModelPrice) []ModelStat {
	result := make([]ModelStat, 0, len(stats))
	for _, stat := range stats {
		result = append(result, ModelStat{
			Model:        stat.Model,
			Calls:        stat.Calls,
			SuccessCalls: stat.SuccessCalls,
			FailureCalls: stat.Calls - stat.SuccessCalls,
			SuccessRate:  ratio(stat.SuccessCalls, stat.Calls),
			InputTokens:  stat.InputTokens,
			OutputTokens: stat.OutputTokens,
			CachedTokens: stat.CachedTokens,
			TotalTokens:  stat.TotalTokens,
			Cost:         costForStat(stat, prices),
		})
	}
	return result
}

func buildChannelShare(stats []store.ChannelModelStat, prices map[string]store.ModelPrice) []ChannelShareRow {
	type accumulator struct {
		row        ChannelShareRow
		latencySum float64
		latencyN   int64
	}
	grouped := map[string]*accumulator{}
	for _, stat := range stats {
		authIndex := stat.AuthIndex
		if authIndex == "" {
			authIndex = "-"
		}
		entry := grouped[authIndex]
		if entry == nil {
			entry = &accumulator{row: ChannelShareRow{AuthIndex: authIndex}}
			grouped[authIndex] = entry
		}
		entry.row.Calls += stat.Calls
		entry.row.Success += stat.SuccessCalls
		entry.row.Failure += stat.FailureCalls
		entry.row.Tokens += stat.TotalTokens
		entry.row.Cost += pricing.CostForModel(stat.Model, pricing.ModelTokens{
			InputTokens:  stat.InputTokens,
			OutputTokens: stat.OutputTokens,
			CachedTokens: stat.CachedTokens,
		}, prices)
		if stat.AvgLatencyMS.Valid {
			entry.latencySum += stat.AvgLatencyMS.Float64
			entry.latencyN++
		}
	}
	result := make([]ChannelShareRow, 0, len(grouped))
	for _, entry := range grouped {
		if entry.latencyN > 0 {
			value := entry.latencySum / float64(entry.latencyN)
			entry.row.AvgLatencyMS = &value
		}
		result = append(result, entry.row)
	}
	return result
}

func buildFailureSources(stats []store.FailureSourceStat) []FailureSourceRow {
	result := make([]FailureSourceRow, 0, len(stats))
	for _, stat := range stats {
		result = append(result, FailureSourceRow{
			SourceHash:   stat.SourceHash,
			AuthIndex:    stat.AuthIndex,
			Calls:        stat.Calls,
			Failure:      stat.FailureCalls,
			LastSeenMS:   stat.LastSeenMS,
			AvgLatencyMS: nullableFloat(stat.AvgLatencyMS.Valid, stat.AvgLatencyMS.Float64),
		})
	}
	return result
}

func buildTaskBuckets(buckets []store.TaskBucket) []TaskBucketRow {
	result := make([]TaskBucketRow, 0, len(buckets))
	for _, bucket := range buckets {
		result = append(result, TaskBucketRow{
			BucketKey:    bucket.BucketKey,
			Total:        bucket.Total,
			Success:      bucket.Success,
			Failure:      bucket.Failure,
			FirstMS:      bucket.FirstMS,
			LastMS:       bucket.LastMS,
			Source:       bucket.Source,
			SourceHash:   bucket.SourceHash,
			AuthIndex:    bucket.AuthIndex,
			Models:       splitCSV(bucket.Models),
			Endpoints:    splitCSV(bucket.Endpoints),
			InputTokens:  bucket.InputTokens,
			OutputTokens: bucket.OutputTokens,
			CachedTokens: bucket.CachedTokens,
			TotalTokens:  bucket.TotalTokens,
			AvgLatencyMS: nullableFloat(bucket.AvgLatencyMS.Valid, bucket.AvgLatencyMS.Float64),
			MaxLatencyMS: nullableInt(bucket.MaxLatencyMS.Valid, bucket.MaxLatencyMS.Int64),
		})
	}
	return result
}

func buildRecentFailures(failures []store.RecentFailure) []RecentFailure {
	result := make([]RecentFailure, 0, len(failures))
	for _, failure := range failures {
		result = append(result, RecentFailure{
			TimestampMS: failure.TimestampMS,
			Model:       failure.Model,
			APIKeyHash:  failure.APIKeyHash,
			SourceHash:  failure.SourceHash,
			AuthIndex:   failure.AuthIndex,
			Endpoint:    failure.Endpoint,
			DurationMS:  nullableInt(failure.LatencyMS.Valid, failure.LatencyMS.Int64),
		})
	}
	return result
}

func buildEvents(page store.EventsPage) *EventsResponse {
	items := make([]EventRow, 0, len(page.Items))
	for _, item := range page.Items {
		items = append(items, EventRow{
			EventHash:            item.EventHash,
			TimestampMS:          item.TimestampMS,
			Model:                item.Model,
			Endpoint:             item.Endpoint,
			Method:               item.Method,
			Path:                 item.Path,
			AuthIndex:            item.AuthIndex,
			Source:               item.Source,
			SourceHash:           item.SourceHash,
			APIKeyHash:           item.APIKeyHash,
			AccountSnapshot:      item.AccountSnapshot,
			AuthLabelSnapshot:    item.AuthLabelSnapshot,
			AuthProviderSnapshot: item.AuthProviderSnapshot,
			InputTokens:          item.InputTokens,
			OutputTokens:         item.OutputTokens,
			CachedTokens:         item.CachedTokens,
			ReasoningTokens:      item.ReasoningTokens,
			TotalTokens:          item.TotalTokens,
			LatencyMS:            nullableInt(item.LatencyMS.Valid, item.LatencyMS.Int64),
			Failed:               item.Failed,
		})
	}
	return &EventsResponse{Items: items, NextBeforeMS: page.NextBeforeMS, HasMore: page.HasMore}
}

func sumCost(stats []store.ModelStat, prices map[string]store.ModelPrice) float64 {
	total := 0.0
	for _, stat := range stats {
		total += costForStat(stat, prices)
	}
	return total
}

func costForStat(stat store.ModelStat, prices map[string]store.ModelPrice) float64 {
	return pricing.CostForModel(stat.Model, pricing.ModelTokens{
		InputTokens:  stat.InputTokens,
		OutputTokens: stat.OutputTokens,
		CachedTokens: stat.CachedTokens,
	}, prices)
}

func ratio(part int64, total int64) float64 {
	if total <= 0 {
		return 0
	}
	return float64(part) / float64(total)
}

func nullableFloat(valid bool, value float64) *float64 {
	if !valid {
		return nil
	}
	return &value
}

func nullableInt(valid bool, value int64) *int64 {
	if !valid {
		return nil
	}
	return &value
}

func splitCSV(value string) []string {
	parts := strings.Split(value, ",")
	result := make([]string, 0, len(parts))
	for _, part := range parts {
		trimmed := strings.TrimSpace(part)
		if trimmed != "" {
			result = append(result, trimmed)
		}
	}
	return result
}

func timelineLabel(bucketMS int64, granularity string) string {
	tm := time.UnixMilli(bucketMS).UTC()
	if granularity == "day" {
		return tm.Format("01/02")
	}
	return tm.Format("15:04")
}
