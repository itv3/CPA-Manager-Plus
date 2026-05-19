package dashboard

import (
	"context"
	"math"
	"path/filepath"
	"testing"
	"time"

	"github.com/seakee/cpa-manager-plus/usage-service/internal/store"
	"github.com/seakee/cpa-manager-plus/usage-service/internal/usage"
)

func TestSummaryEmptyStore(t *testing.T) {
	db := newDashboardTestStore(t)
	service := New(db)

	resp, err := service.Summary(context.Background(), SummaryParams{
		TodayStartMS: 1_778_000_000_000,
		NowMS:        1_778_000_060_000,
	})
	if err != nil {
		t.Fatalf("summary: %v", err)
	}
	if resp.Today.TotalCalls != 0 || resp.Today.SuccessRate != 0 ||
		resp.Today.AverageLatencyMS != nil || resp.Rolling30M.TotalCalls != 0 {
		t.Fatalf("empty response = %#v", resp)
	}
	if len(resp.TopModelsToday) != 0 || len(resp.RecentFailures) != 0 {
		t.Fatalf("empty lists = %#v %#v", resp.TopModelsToday, resp.RecentFailures)
	}
}

func TestSummaryAggregatesCostsAndWindows(t *testing.T) {
	db := newDashboardTestStore(t)
	ctx := context.Background()
	todayStart := int64(1_778_000_000_000)
	nowMS := todayStart + 60*60*1000
	latency100 := int64(100)
	latency200 := int64(200)

	if err := db.SaveModelPrices(ctx, map[string]store.ModelPrice{
		"gpt-a": {Prompt: 2, Completion: 4, Cache: 1},
	}); err != nil {
		t.Fatalf("save prices: %v", err)
	}
	_, err := db.InsertEvents(ctx, []usage.Event{
		dashboardEvent("event-a-1", todayStart+10*60*1000, "gpt-a", false, 1_000_000, 500_000, 0, 250_000, 0, 1_750_000, &latency100),
		dashboardEvent("event-b-1", todayStart+50*60*1000, "gpt-b", true, 0, 100, 0, 0, 0, 100, &latency200),
		dashboardEvent("event-a-2", todayStart+55*60*1000, "gpt-a", false, 0, 0, 0, 0, 0, 0, nil),
		dashboardEvent("event-outside", nowMS, "gpt-a", false, 10, 10, 0, 0, 0, 20, nil),
	})
	if err != nil {
		t.Fatalf("insert events: %v", err)
	}

	resp, err := New(db).Summary(ctx, SummaryParams{
		TodayStartMS:   todayStart,
		NowMS:          nowMS,
		TopModels:      1,
		RecentFailures: 2,
	})
	if err != nil {
		t.Fatalf("summary: %v", err)
	}

	if resp.Today.TotalCalls != 3 || resp.Today.SuccessCalls != 2 || resp.Today.FailureCalls != 1 {
		t.Fatalf("today counts = %#v", resp.Today)
	}
	if resp.Today.TotalTokens != 1_750_100 || resp.Today.ZeroTokenCalls != 1 {
		t.Fatalf("today tokens = %#v", resp.Today)
	}
	if math.Abs(resp.Today.SuccessRate-(2.0/3.0)) > 0.000001 {
		t.Fatalf("success rate = %v", resp.Today.SuccessRate)
	}
	if resp.Today.AverageLatencyMS == nil || *resp.Today.AverageLatencyMS != 150 {
		t.Fatalf("average latency = %#v", resp.Today.AverageLatencyMS)
	}
	if math.Abs(resp.Today.TotalCost-4.25) > 0.000001 {
		t.Fatalf("total cost = %v", resp.Today.TotalCost)
	}
	if resp.Rolling30M.TotalCalls != 2 || resp.Rolling30M.TotalTokens != 100 {
		t.Fatalf("rolling = %#v", resp.Rolling30M)
	}
	if len(resp.TopModelsToday) != 1 || resp.TopModelsToday[0].Model != "gpt-a" ||
		resp.TopModelsToday[0].Calls != 2 || math.Abs(resp.TopModelsToday[0].Cost-4.25) > 0.000001 {
		t.Fatalf("top models = %#v", resp.TopModelsToday)
	}
	if len(resp.RecentFailures) != 1 || resp.RecentFailures[0].Model != "gpt-b" ||
		resp.RecentFailures[0].DurationMS == nil || *resp.RecentFailures[0].DurationMS != 200 {
		t.Fatalf("recent failures = %#v", resp.RecentFailures)
	}
	if len(resp.TrafficTimeline) != 24 || resp.TrafficTimeline[0].Calls != 3 ||
		resp.TrafficTimeline[0].Tokens != 1_750_100 ||
		math.Abs(resp.TrafficTimeline[0].FailureRate-(1.0/3.0)) > 0.000001 {
		t.Fatalf("traffic timeline = %#v", resp.TrafficTimeline)
	}
	if len(resp.HourlyActivity) != 24 || resp.HourlyActivity[0].Intensity != 1 {
		t.Fatalf("hourly activity = %#v", resp.HourlyActivity)
	}
	if len(resp.TokenMix) != 4 || resp.TokenMix[0].Key != "input" ||
		resp.TokenMix[0].Tokens != 1_000_000 {
		t.Fatalf("token mix = %#v", resp.TokenMix)
	}
	if len(resp.ModelCostRank) != 1 || resp.ModelCostRank[0].Model != "gpt-a" ||
		resp.ModelCostRank[0].CostShare != 1 {
		t.Fatalf("model cost rank = %#v", resp.ModelCostRank)
	}
	if len(resp.ChannelHealth) != 1 || resp.ChannelHealth[0].AuthIndex != "auth-1" ||
		resp.ChannelHealth[0].Failures != 1 || resp.ChannelHealth[0].Tone != "bad" {
		t.Fatalf("channel health = %#v", resp.ChannelHealth)
	}
	if len(resp.FailureSources) != 1 || resp.FailureSources[0].SourceHash != "source-hash" ||
		resp.FailureSources[0].Failures != 1 {
		t.Fatalf("failure sources = %#v", resp.FailureSources)
	}
}

func newDashboardTestStore(t *testing.T) *store.Store {
	t.Helper()
	db, err := store.Open(filepath.Join(t.TempDir(), "usage.sqlite"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() {
		_ = db.Close()
	})
	return db
}

func dashboardEvent(
	hash string,
	timestampMS int64,
	model string,
	failed bool,
	inputTokens int64,
	outputTokens int64,
	reasoningTokens int64,
	cachedTokens int64,
	cacheTokens int64,
	totalTokens int64,
	latencyMS *int64,
) usage.Event {
	return usage.Event{
		EventHash:       hash,
		TimestampMS:     timestampMS,
		Timestamp:       time.UnixMilli(timestampMS).UTC().Format(time.RFC3339Nano),
		Model:           model,
		Endpoint:        "POST /v1/chat/completions",
		Method:          "POST",
		Path:            "/v1/chat/completions",
		AuthIndex:       "auth-1",
		Source:          "user@example.com",
		SourceHash:      "source-hash",
		APIKeyHash:      "api-key-hash",
		InputTokens:     inputTokens,
		OutputTokens:    outputTokens,
		ReasoningTokens: reasoningTokens,
		CachedTokens:    cachedTokens,
		CacheTokens:     cacheTokens,
		TotalTokens:     totalTokens,
		LatencyMS:       latencyMS,
		Failed:          failed,
		CreatedAtMS:     timestampMS,
	}
}
