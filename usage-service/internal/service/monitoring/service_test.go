package monitoring

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/seakee/cpa-manager-plus/usage-service/internal/store"
	"github.com/seakee/cpa-manager-plus/usage-service/internal/usage"
)

func TestAnalyticsBuildsIncludedSections(t *testing.T) {
	db := newMonitoringTestStore(t)
	ctx := context.Background()
	fromMS := int64(1_778_000_000_000)
	toMS := fromMS + 2*60*60*1000
	latency := int64(250)

	if err := db.SaveModelPrices(ctx, map[string]store.ModelPrice{
		"gpt-a": {Prompt: 1, Completion: 2, Cache: 0.5},
	}); err != nil {
		t.Fatalf("save model prices: %v", err)
	}
	_, err := db.InsertEvents(ctx, []usage.Event{
		monitoringEvent("analytics-a", fromMS+1_000, "gpt-a", "auth-1", "source-a", false, 1_000_000, 500_000, 0, 100, 1_500_100, &latency),
		monitoringEvent("analytics-b", fromMS+2_000, "gpt-b", "auth-2", "source-b", true, 10, 20, 0, 0, 30, nil),
		monitoringEvent("analytics-outside", toMS, "gpt-a", "auth-1", "source-a", false, 1, 1, 0, 0, 2, nil),
	})
	if err != nil {
		t.Fatalf("insert events: %v", err)
	}

	includeFailed := true
	resp, err := New(db).Analytics(ctx, Request{
		FromMS: fromMS,
		ToMS:   toMS,
		NowMS:  toMS,
		Filters: Filters{
			IncludeFailed: &includeFailed,
		},
		Include: Include{
			Summary:            true,
			Timeline:           true,
			HourlyDistribution: true,
			ModelShare:         true,
			ChannelShare:       true,
			ModelStats:         true,
			FailureSources:     true,
			TaskBuckets:        true,
			RecentFailures:     5,
			EventsPage:         &EventsPage{Limit: 1},
			Granularity:        "hour",
		},
	})
	if err != nil {
		t.Fatalf("analytics: %v", err)
	}

	if resp.Summary == nil || resp.Summary.TotalCalls != 2 || resp.Summary.FailureCalls != 1 {
		t.Fatalf("summary = %#v", resp.Summary)
	}
	if resp.Summary.TotalCost <= 0 {
		t.Fatalf("summary cost = %v", resp.Summary.TotalCost)
	}
	if len(resp.Timeline) == 0 || len(resp.HourlyDistribution) == 0 {
		t.Fatalf("timeline = %#v hourly = %#v", resp.Timeline, resp.HourlyDistribution)
	}
	if len(resp.ModelStats) != 2 || len(resp.ModelShare) != 2 {
		t.Fatalf("model stats/share = %#v %#v", resp.ModelStats, resp.ModelShare)
	}
	if len(resp.ChannelShare) != 2 {
		t.Fatalf("channel share = %#v", resp.ChannelShare)
	}
	if len(resp.FailureSources) != 1 || resp.FailureSources[0].SourceHash == "" {
		t.Fatalf("failure sources = %#v", resp.FailureSources)
	}
	if len(resp.TaskBuckets) != 2 {
		t.Fatalf("task buckets = %#v", resp.TaskBuckets)
	}
	if len(resp.RecentFailures) != 1 || resp.RecentFailures[0].Model != "gpt-b" {
		t.Fatalf("recent failures = %#v", resp.RecentFailures)
	}
	if resp.Events == nil || len(resp.Events.Items) != 1 || !resp.Events.HasMore {
		t.Fatalf("events page = %#v", resp.Events)
	}
}

func TestAnalyticsAppliesFilters(t *testing.T) {
	db := newMonitoringTestStore(t)
	ctx := context.Background()
	fromMS := int64(1_778_000_000_000)
	toMS := fromMS + 60*60*1000
	includeFailed := false

	_, err := db.InsertEvents(ctx, []usage.Event{
		monitoringEvent("filter-a", fromMS+1_000, "gpt-a", "auth-1", "source-a", false, 1, 1, 0, 0, 2, nil),
		monitoringEvent("filter-b", fromMS+2_000, "gpt-a", "auth-1", "source-a", true, 1, 1, 0, 0, 2, nil),
		monitoringEvent("filter-c", fromMS+3_000, "gpt-b", "auth-2", "source-b", false, 1, 1, 0, 0, 2, nil),
	})
	if err != nil {
		t.Fatalf("insert events: %v", err)
	}

	resp, err := New(db).Analytics(ctx, Request{
		FromMS: fromMS,
		ToMS:   toMS,
		Filters: Filters{
			Models:        []string{"gpt-a"},
			AuthIndices:   []string{"auth-1"},
			IncludeFailed: &includeFailed,
		},
		Include: Include{Summary: true, EventsPage: &EventsPage{Limit: 10}},
	})
	if err != nil {
		t.Fatalf("analytics: %v", err)
	}
	if resp.Summary == nil || resp.Summary.TotalCalls != 1 || resp.Summary.FailureCalls != 0 {
		t.Fatalf("filtered summary = %#v", resp.Summary)
	}
	if resp.Events == nil || len(resp.Events.Items) != 1 || resp.Events.Items[0].EventHash != "filter-a" {
		t.Fatalf("filtered events = %#v", resp.Events)
	}

	includeFailed = true
	resp, err = New(db).Analytics(ctx, Request{
		FromMS:           fromMS,
		ToMS:             toMS,
		SearchQuery:      "raw-api-key",
		SearchAPIKeyHash: "api-key-auth-2",
		Filters: Filters{
			IncludeFailed: &includeFailed,
		},
		Include: Include{Summary: true, EventsPage: &EventsPage{Limit: 10}},
	})
	if err != nil {
		t.Fatalf("analytics api key hash search: %v", err)
	}
	if resp.Events == nil || len(resp.Events.Items) != 1 || resp.Events.Items[0].EventHash != "filter-c" {
		t.Fatalf("api key hash search events = %#v", resp.Events)
	}
}

func TestAnalyticsReportsZeroTokenModels(t *testing.T) {
	db := newMonitoringTestStore(t)
	ctx := context.Background()
	fromMS := int64(1_778_000_000_000)
	toMS := fromMS + 60*60*1000

	_, err := db.InsertEvents(ctx, []usage.Event{
		monitoringEvent("zero-a", fromMS+1_000, "gpt-zero", "auth-1", "source-a", false, 0, 0, 0, 0, 0, nil),
		monitoringEvent("zero-b", fromMS+2_000, "gpt-failed-zero", "auth-1", "source-a", true, 0, 0, 0, 0, 0, nil),
		monitoringEvent("zero-c", fromMS+3_000, "gpt-nonzero", "auth-1", "source-a", false, 1, 1, 0, 0, 2, nil),
	})
	if err != nil {
		t.Fatalf("insert events: %v", err)
	}

	resp, err := New(db).Analytics(ctx, Request{
		FromMS:  fromMS,
		ToMS:    toMS,
		Include: Include{Summary: true},
	})
	if err != nil {
		t.Fatalf("analytics: %v", err)
	}
	if resp.Summary == nil || len(resp.Summary.ZeroTokenModels) != 1 || resp.Summary.ZeroTokenModels[0] != "gpt-zero" {
		t.Fatalf("zero token models = %#v", resp.Summary)
	}

	resp, err = New(db).Analytics(ctx, Request{
		FromMS:  fromMS,
		ToMS:    toMS,
		Filters: Filters{ExcludeZeroTokens: true},
		Include: Include{Summary: true},
	})
	if err != nil {
		t.Fatalf("analytics with zero-token filter: %v", err)
	}
	if resp.Summary == nil || resp.Summary.ZeroTokenCalls != 0 || len(resp.Summary.ZeroTokenModels) != 0 {
		t.Fatalf("filtered zero token summary = %#v", resp.Summary)
	}
}

func TestAnalyticsAppliesFailedOnlyFilter(t *testing.T) {
	db := newMonitoringTestStore(t)
	ctx := context.Background()
	fromMS := int64(1_778_100_000_000)
	toMS := fromMS + 60*60*1000

	_, err := db.InsertEvents(ctx, []usage.Event{
		monitoringEvent("status-a", fromMS+1_000, "gpt-ok", "auth-1", "source-a", false, 10, 5, 0, 0, 15, nil),
		monitoringEvent("status-b", fromMS+2_000, "gpt-failed", "auth-1", "source-a", true, 1, 1, 0, 0, 2, nil),
	})
	if err != nil {
		t.Fatalf("insert events: %v", err)
	}

	resp, err := New(db).Analytics(ctx, Request{
		FromMS:  fromMS,
		ToMS:    toMS,
		Filters: Filters{FailedOnly: true},
		Include: Include{Summary: true, EventsPage: &EventsPage{Limit: 10}},
	})
	if err != nil {
		t.Fatalf("analytics: %v", err)
	}
	if resp.Summary == nil || resp.Summary.TotalCalls != 1 || resp.Summary.FailureCalls != 1 {
		t.Fatalf("summary = %#v", resp.Summary)
	}
	if resp.Events == nil || len(resp.Events.Items) != 1 || !resp.Events.Items[0].Failed {
		t.Fatalf("events = %#v", resp.Events)
	}
}

func newMonitoringTestStore(t *testing.T) *store.Store {
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

func monitoringEvent(
	hash string,
	timestampMS int64,
	model string,
	authIndex string,
	sourceHash string,
	failed bool,
	inputTokens int64,
	outputTokens int64,
	reasoningTokens int64,
	cachedTokens int64,
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
		AuthIndex:       authIndex,
		Source:          "user@example.com",
		SourceHash:      sourceHash,
		APIKeyHash:      "api-key-" + authIndex,
		InputTokens:     inputTokens,
		OutputTokens:    outputTokens,
		ReasoningTokens: reasoningTokens,
		CachedTokens:    cachedTokens,
		TotalTokens:     totalTokens,
		LatencyMS:       latencyMS,
		Failed:          failed,
		CreatedAtMS:     timestampMS,
	}
}
