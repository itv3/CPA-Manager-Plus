package dashboard

import (
	"context"
	"errors"
	"sort"
	"time"

	"github.com/seakee/cpa-manager-plus/usage-service/internal/service/pricing"
	"github.com/seakee/cpa-manager-plus/usage-service/internal/store"
)

const (
	defaultTopModels      = 5
	defaultRecentFailures = 5
	defaultHealthRows     = 5
	rollingWindowMinutes  = 30
	rollingWindowMs       = rollingWindowMinutes * 60 * 1000
	hourWindowMs          = 60 * 60 * 1000
)

type Service struct {
	store *store.Store
}

func New(store *store.Store) *Service {
	return &Service{store: store}
}

type SummaryParams struct {
	TodayStartMS   int64
	NowMS          int64
	TopModels      int
	RecentFailures int
}

type Window struct {
	TodayStartMS      int64 `json:"today_start_ms"`
	NowMS             int64 `json:"now_ms"`
	Rolling30MStartMS int64 `json:"rolling_30m_start_ms"`
}

type TodaySummary struct {
	TotalCalls       int64    `json:"total_calls"`
	SuccessCalls     int64    `json:"success_calls"`
	FailureCalls     int64    `json:"failure_calls"`
	SuccessRate      float64  `json:"success_rate"`
	InputTokens      int64    `json:"input_tokens"`
	OutputTokens     int64    `json:"output_tokens"`
	CachedTokens     int64    `json:"cached_tokens"`
	ReasoningTokens  int64    `json:"reasoning_tokens"`
	TotalTokens      int64    `json:"total_tokens"`
	TotalCost        float64  `json:"total_cost"`
	AverageLatencyMS *float64 `json:"average_latency_ms"`
	ZeroTokenCalls   int64    `json:"zero_token_calls"`
}

type RollingSummary struct {
	RPM         float64 `json:"rpm"`
	TPM         float64 `json:"tpm"`
	TotalCalls  int64   `json:"total_calls"`
	TotalTokens int64   `json:"total_tokens"`
}

type TopModel struct {
	Model       string  `json:"model"`
	Calls       int64   `json:"calls"`
	Tokens      int64   `json:"tokens"`
	Cost        float64 `json:"cost"`
	SuccessRate float64 `json:"success_rate"`
}

type TrafficPoint struct {
	BucketMS    int64   `json:"bucket_ms"`
	Calls       int64   `json:"calls"`
	Tokens      int64   `json:"tokens"`
	Success     int64   `json:"success"`
	Failure     int64   `json:"failure"`
	CallsShare  float64 `json:"calls_share"`
	TokensShare float64 `json:"tokens_share"`
	FailureRate float64 `json:"failure_rate"`
}

type HourlyActivityPoint struct {
	HourIndex int     `json:"hour_index"`
	BucketMS  int64   `json:"bucket_ms"`
	Calls     int64   `json:"calls"`
	Tokens    int64   `json:"tokens"`
	Intensity float64 `json:"intensity"`
}

type TokenMixSegment struct {
	Key    string  `json:"key"`
	Tokens int64   `json:"tokens"`
	Share  float64 `json:"share"`
}

type ModelCostRank struct {
	Model       string  `json:"model"`
	Calls       int64   `json:"calls"`
	Tokens      int64   `json:"tokens"`
	Cost        float64 `json:"cost"`
	SuccessRate float64 `json:"success_rate"`
	CostShare   float64 `json:"cost_share"`
}

type ChannelHealth struct {
	AuthIndex        string   `json:"auth_index"`
	Calls            int64    `json:"calls"`
	Failures         int64    `json:"failures"`
	FailureRate      float64  `json:"failure_rate"`
	SuccessRate      float64  `json:"success_rate"`
	Tokens           int64    `json:"tokens"`
	Cost             float64  `json:"cost"`
	AverageLatencyMS *float64 `json:"average_latency_ms"`
	Tone             string   `json:"tone"`
}

type FailureSource struct {
	SourceHash       string   `json:"source_hash"`
	AuthIndex        string   `json:"auth_index"`
	Calls            int64    `json:"calls"`
	Failures         int64    `json:"failures"`
	FailureRate      float64  `json:"failure_rate"`
	LastSeenMS       int64    `json:"last_seen_ms"`
	AverageLatencyMS *float64 `json:"average_latency_ms"`
	Tone             string   `json:"tone"`
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

type SummaryResponse struct {
	GeneratedAtMS   int64                 `json:"generated_at_ms"`
	Window          Window                `json:"window"`
	Today           TodaySummary          `json:"today"`
	Rolling30M      RollingSummary        `json:"rolling_30m"`
	TopModelsToday  []TopModel            `json:"top_models_today"`
	ModelCostRank   []ModelCostRank       `json:"model_cost_rank"`
	TrafficTimeline []TrafficPoint        `json:"traffic_timeline"`
	HourlyActivity  []HourlyActivityPoint `json:"hourly_activity"`
	TokenMix        []TokenMixSegment     `json:"token_mix"`
	ChannelHealth   []ChannelHealth       `json:"channel_health"`
	FailureSources  []FailureSource       `json:"failure_sources"`
	RecentFailures  []RecentFailure       `json:"recent_failures"`
}

func (s *Service) Summary(ctx context.Context, p SummaryParams) (SummaryResponse, error) {
	if p.TodayStartMS <= 0 {
		return SummaryResponse{}, errors.New("today_start_ms is required")
	}

	generatedAt := time.Now().UnixMilli()
	nowMS := p.NowMS
	if nowMS <= 0 {
		nowMS = generatedAt
	}
	if nowMS < p.TodayStartMS {
		return SummaryResponse{}, errors.New("now_ms must be greater than or equal to today_start_ms")
	}

	topLimit := p.TopModels
	if topLimit <= 0 {
		topLimit = defaultTopModels
	}
	recentLimit := p.RecentFailures
	if recentLimit <= 0 {
		recentLimit = defaultRecentFailures
	}

	todayAgg, err := s.store.AggregateBetween(ctx, p.TodayStartMS, nowMS)
	if err != nil {
		return SummaryResponse{}, err
	}
	rollingStartMS := nowMS - rollingWindowMs
	rollingAgg, err := s.store.AggregateBetween(ctx, rollingStartMS, nowMS)
	if err != nil {
		return SummaryResponse{}, err
	}
	modelStats, err := s.store.ModelStatsBetween(ctx, p.TodayStartMS, nowMS)
	if err != nil {
		return SummaryResponse{}, err
	}
	topStats, err := s.store.TopModelsBetween(ctx, p.TodayStartMS, nowMS, topLimit)
	if err != nil {
		return SummaryResponse{}, err
	}
	recentFailures, err := s.store.RecentFailuresBetween(ctx, p.TodayStartMS, nowMS, recentLimit)
	if err != nil {
		return SummaryResponse{}, err
	}
	prices, err := s.store.LoadModelPrices(ctx)
	if err != nil {
		return SummaryResponse{}, err
	}
	filter := store.AnalyticsFilter{
		FromMS:        p.TodayStartMS,
		ToMS:          nowMS,
		IncludeFailed: true,
	}
	timeline, err := s.store.HourlyTimelineBetween(ctx, p.TodayStartMS, nowMS)
	if err != nil {
		return SummaryResponse{}, err
	}
	channelStats, err := s.store.ChannelModelStatsWithFilter(ctx, filter)
	if err != nil {
		return SummaryResponse{}, err
	}
	failureSources, err := s.store.FailureSourcesWithFilter(ctx, filter)
	if err != nil {
		return SummaryResponse{}, err
	}
	today := buildTodaySummary(todayAgg, modelStats, prices)
	trafficTimeline := buildTrafficTimeline(p.TodayStartMS, nowMS, timeline)

	return SummaryResponse{
		GeneratedAtMS: generatedAt,
		Window: Window{
			TodayStartMS:      p.TodayStartMS,
			NowMS:             nowMS,
			Rolling30MStartMS: rollingStartMS,
		},
		Today:           today,
		Rolling30M:      buildRollingSummary(rollingAgg),
		TopModelsToday:  buildTopModels(topStats, prices),
		ModelCostRank:   buildModelCostRank(modelStats, prices, topLimit),
		TrafficTimeline: trafficTimeline,
		HourlyActivity:  buildHourlyActivity(trafficTimeline),
		TokenMix:        buildTokenMix(today),
		ChannelHealth:   buildChannelHealth(channelStats, prices, defaultHealthRows),
		FailureSources:  buildFailureSources(failureSources, defaultHealthRows),
		RecentFailures:  buildRecentFailures(recentFailures),
	}, nil
}

func buildTodaySummary(agg store.Aggregate, modelStats []store.ModelStat, prices map[string]store.ModelPrice) TodaySummary {
	return TodaySummary{
		TotalCalls:       agg.TotalCalls,
		SuccessCalls:     agg.SuccessCalls,
		FailureCalls:     agg.FailureCalls,
		SuccessRate:      rate(agg.SuccessCalls, agg.TotalCalls),
		InputTokens:      agg.InputTokens,
		OutputTokens:     agg.OutputTokens,
		CachedTokens:     agg.CachedTokens,
		ReasoningTokens:  agg.ReasoningTokens,
		TotalTokens:      agg.TotalTokens,
		TotalCost:        totalCost(modelStats, prices),
		AverageLatencyMS: nullableFloat(agg.AvgLatencyMS.Valid, agg.AvgLatencyMS.Float64),
		ZeroTokenCalls:   agg.ZeroTokenCalls,
	}
}

func buildRollingSummary(agg store.Aggregate) RollingSummary {
	return RollingSummary{
		RPM:         float64(agg.TotalCalls) / rollingWindowMinutes,
		TPM:         float64(agg.TotalTokens) / rollingWindowMinutes,
		TotalCalls:  agg.TotalCalls,
		TotalTokens: agg.TotalTokens,
	}
}

func buildTopModels(stats []store.ModelStat, prices map[string]store.ModelPrice) []TopModel {
	result := make([]TopModel, 0, len(stats))
	for _, stat := range stats {
		result = append(result, TopModel{
			Model:       stat.Model,
			Calls:       stat.Calls,
			Tokens:      stat.TotalTokens,
			Cost:        costForStat(stat, prices),
			SuccessRate: rate(stat.SuccessCalls, stat.Calls),
		})
	}
	return result
}

func buildTrafficTimeline(todayStartMS int64, nowMS int64, points []store.TimelinePoint) []TrafficPoint {
	pointByBucket := make(map[int64]store.TimelinePoint, len(points))
	for _, point := range points {
		pointByBucket[point.BucketMS] = point
	}

	hours := 24
	result := make([]TrafficPoint, 0, hours)
	var maxCalls int64
	var maxTokens int64
	for i := 0; i < hours; i++ {
		bucketMS := todayStartMS + int64(i)*hourWindowMs
		point := pointByBucket[bucketMS]
		row := TrafficPoint{
			BucketMS: bucketMS,
			Calls:    point.Calls,
			Tokens:   point.Tokens,
			Success:  point.Success,
			Failure:  point.Failure,
		}
		row.FailureRate = rate(row.Failure, row.Calls)
		if row.Calls > maxCalls {
			maxCalls = row.Calls
		}
		if row.Tokens > maxTokens {
			maxTokens = row.Tokens
		}
		result = append(result, row)
	}

	for index := range result {
		result[index].CallsShare = rate(result[index].Calls, maxCalls)
		result[index].TokensShare = rate(result[index].Tokens, maxTokens)
	}
	return result
}

func buildHourlyActivity(points []TrafficPoint) []HourlyActivityPoint {
	result := make([]HourlyActivityPoint, 0, len(points))
	for index, point := range points {
		intensity := point.CallsShare
		if point.TokensShare > intensity {
			intensity = point.TokensShare
		}
		result = append(result, HourlyActivityPoint{
			HourIndex: index,
			BucketMS:  point.BucketMS,
			Calls:     point.Calls,
			Tokens:    point.Tokens,
			Intensity: intensity,
		})
	}
	return result
}

func buildTokenMix(today TodaySummary) []TokenMixSegment {
	total := today.InputTokens + today.OutputTokens + today.ReasoningTokens + today.CachedTokens
	return []TokenMixSegment{
		{Key: "input", Tokens: today.InputTokens, Share: rate(today.InputTokens, total)},
		{Key: "output", Tokens: today.OutputTokens, Share: rate(today.OutputTokens, total)},
		{Key: "reasoning", Tokens: today.ReasoningTokens, Share: rate(today.ReasoningTokens, total)},
		{Key: "cached", Tokens: today.CachedTokens, Share: rate(today.CachedTokens, total)},
	}
}

func buildModelCostRank(stats []store.ModelStat, prices map[string]store.ModelPrice, limit int) []ModelCostRank {
	rows := make([]ModelCostRank, 0, len(stats))
	var maxCost float64
	for _, stat := range stats {
		cost := costForStat(stat, prices)
		if cost > maxCost {
			maxCost = cost
		}
		rows = append(rows, ModelCostRank{
			Model:       stat.Model,
			Calls:       stat.Calls,
			Tokens:      stat.TotalTokens,
			Cost:        cost,
			SuccessRate: rate(stat.SuccessCalls, stat.Calls),
		})
	}
	for index := range rows {
		rows[index].CostShare = rateFloat(rows[index].Cost, maxCost)
	}
	sort.SliceStable(rows, func(i, j int) bool {
		if rows[i].Cost != rows[j].Cost {
			return rows[i].Cost > rows[j].Cost
		}
		return rows[i].Calls > rows[j].Calls
	})
	if limit > 0 && len(rows) > limit {
		rows = rows[:limit]
	}
	return rows
}

func buildChannelHealth(stats []store.ChannelModelStat, prices map[string]store.ModelPrice, limit int) []ChannelHealth {
	type accumulator struct {
		row        ChannelHealth
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
			entry = &accumulator{row: ChannelHealth{AuthIndex: authIndex}}
			grouped[authIndex] = entry
		}
		entry.row.Calls += stat.Calls
		entry.row.Failures += stat.FailureCalls
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

	rows := make([]ChannelHealth, 0, len(grouped))
	for _, entry := range grouped {
		success := entry.row.Calls - entry.row.Failures
		entry.row.SuccessRate = rate(success, entry.row.Calls)
		entry.row.FailureRate = rate(entry.row.Failures, entry.row.Calls)
		if entry.latencyN > 0 {
			value := entry.latencySum / float64(entry.latencyN)
			entry.row.AverageLatencyMS = &value
		}
		entry.row.Tone = healthTone(entry.row.SuccessRate, entry.row.Failures, entry.row.AverageLatencyMS)
		rows = append(rows, entry.row)
	}
	sort.SliceStable(rows, func(i, j int) bool {
		leftSeverity := toneSeverity(rows[i].Tone)
		rightSeverity := toneSeverity(rows[j].Tone)
		if leftSeverity != rightSeverity {
			return leftSeverity > rightSeverity
		}
		if rows[i].Failures != rows[j].Failures {
			return rows[i].Failures > rows[j].Failures
		}
		return rows[i].Calls > rows[j].Calls
	})
	if limit > 0 && len(rows) > limit {
		rows = rows[:limit]
	}
	return rows
}

func buildFailureSources(stats []store.FailureSourceStat, limit int) []FailureSource {
	rows := make([]FailureSource, 0, len(stats))
	for _, stat := range stats {
		tone := "warn"
		failureRate := rate(stat.FailureCalls, stat.Calls)
		if failureRate >= 0.5 || stat.FailureCalls >= 5 {
			tone = "bad"
		}
		rows = append(rows, FailureSource{
			SourceHash:       stat.SourceHash,
			AuthIndex:        stat.AuthIndex,
			Calls:            stat.Calls,
			Failures:         stat.FailureCalls,
			FailureRate:      failureRate,
			LastSeenMS:       stat.LastSeenMS,
			AverageLatencyMS: nullableFloat(stat.AvgLatencyMS.Valid, stat.AvgLatencyMS.Float64),
			Tone:             tone,
		})
	}
	if limit > 0 && len(rows) > limit {
		rows = rows[:limit]
	}
	return rows
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

func totalCost(stats []store.ModelStat, prices map[string]store.ModelPrice) float64 {
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

func rate(part, total int64) float64 {
	if total <= 0 {
		return 0
	}
	return float64(part) / float64(total)
}

func rateFloat(part, total float64) float64 {
	if total <= 0 {
		return 0
	}
	return part / total
}

func healthTone(successRate float64, failures int64, averageLatencyMS *float64) string {
	latency := 0.0
	if averageLatencyMS != nil {
		latency = *averageLatencyMS
	}
	if successRate < 0.85 || failures >= 5 || latency >= 30000 {
		return "bad"
	}
	if successRate < 0.95 || failures > 0 || latency >= 15000 {
		return "warn"
	}
	return "good"
}

func toneSeverity(tone string) int {
	switch tone {
	case "bad":
		return 3
	case "warn":
		return 2
	default:
		return 1
	}
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
