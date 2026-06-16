import { act } from 'react';
import { create, type ReactTestInstance, type ReactTestRenderer } from 'react-test-renderer';
import { beforeEach, describe, expect, it, vi } from 'vitest';
import { Button } from '@/components/ui/Button';
import { Select } from '@/components/ui/Select';
import {
  USAGE_ANALYTICS_DEFAULT_FILTERS,
  type UsageRankRow,
  type UsageTimelinePoint,
} from './usageAnalyticsModel';
import { UsageAnalyticsPage } from './UsageAnalyticsPage';

const { mocks } = vi.hoisted(() => ({
  mocks: {
    navigate: vi.fn(),
    usageState: null as unknown,
  },
}));

vi.mock('react-router-dom', () => ({
  useNavigate: () => mocks.navigate,
}));

vi.mock('react-i18next', () => ({
  initReactI18next: { type: '3rdParty', init: () => {} },
  useTranslation: () => ({
    i18n: { language: 'en' },
    t: (key: string, options?: Record<string, unknown>) => {
      if (!options) return key;
      return Object.entries(options).reduce(
        (value, [name, replacement]) => value.replace(`{{${name}}}`, String(replacement)),
        key
      );
    },
  }),
}));

vi.mock('./useUsageAnalytics', () => ({
  useUsageAnalytics: () => mocks.usageState,
}));

const getText = (node: ReactTestInstance): string =>
  node.children
    .map((child) => {
      if (typeof child === 'string' || typeof child === 'number') return String(child);
      return getText(child);
    })
    .join('');

const renderPage = () => {
  let renderer!: ReactTestRenderer;
  act(() => {
    renderer = create(<UsageAnalyticsPage />);
  });
  return renderer;
};

const findHostButtonByText = (renderer: ReactTestRenderer, text: string) => {
  const button = renderer.root.findAllByType('button').find((node) => getText(node).includes(text));
  if (!button) throw new Error(`Button not found: ${text}`);
  return button;
};

const clickHostButton = (button: ReactTestInstance) => {
  const onClick = button.props.onClick as (() => void) | undefined;
  if (!onClick) throw new Error('Button onClick not found');
  act(() => {
    onClick();
  });
};

const createTimelinePoint = (overrides: Partial<UsageTimelinePoint> = {}): UsageTimelinePoint => ({
  bucketMs: 1_780_000_000_000,
  bucketEndMs: 1_780_003_600_000,
  label: '06/04 12:00',
  requestCount: 12,
  totalTokens: 1200,
  inputTokens: 700,
  outputTokens: 400,
  cachedTokens: 100,
  cacheReadTokens: 80,
  cacheCreationTokens: 20,
  reasoningTokens: 0,
  estimatedCost: 1.25,
  successCount: 11,
  failureCount: 1,
  successRate: 11 / 12,
  failureRate: 1 / 12,
  averageLatencyMs: 250,
  p95LatencyMs: 420,
  p95TtftMs: 180,
  cacheHitRate: 0.1,
  averageTokensPerRequest: 100,
  ...overrides,
});

const createRankRow = (overrides: Partial<UsageRankRow> = {}): UsageRankRow => ({
  id: 'gpt-4o',
  label: 'gpt-4o',
  model: 'gpt-4o',
  requestCount: 12,
  successCount: 11,
  failureCount: 1,
  successRate: 11 / 12,
  totalTokens: 1200,
  inputTokens: 700,
  outputTokens: 400,
  cachedTokens: 100,
  cacheReadTokens: 80,
  cacheCreationTokens: 20,
  estimatedCost: 1.25,
  averageLatencyMs: null,
  share: 1,
  ...overrides,
});

const createUsageState = (overrides: Record<string, unknown> = {}) => {
  const point = createTimelinePoint();
  const modelRow = createRankRow();
  const apiKeyRow = createRankRow({
    id: 'abcdef1234567890',
    label: 'sk-****7890',
    apiKeyHash: 'abcdef1234567890',
    model: undefined,
    models: [
      createRankRow({
        id: 'gpt-4o',
        label: 'gpt-4o',
        model: 'gpt-4o',
        share: 1,
      }),
    ],
  });
  const credentialRow = createRankRow({
    id: 'credential-prod',
    label: 'prod-auth',
    model: undefined,
    provider: 'openai',
    authFile: 'auth.json',
    projectId: 'project-a',
    models: [
      createRankRow({
        id: 'gpt-4o',
        label: 'gpt-4o',
        model: 'gpt-4o',
        share: 1,
      }),
    ],
  });

  return {
    filters: USAGE_ANALYTICS_DEFAULT_FILTERS,
    setFilters: vi.fn(),
    resetFilters: vi.fn(),
    clearFilter: vi.fn(),
    activeTab: 'overview',
    setActiveTab: vi.fn(),
    bounds: { fromMs: point.bucketMs, toMs: point.bucketEndMs },
    resolvedGranularity: 'hour',
    loading: false,
    error: '',
    enabled: true,
    unavailableReason: '',
    lastRefreshedAt: null,
    refresh: vi.fn(),
    summary: {
      requestCount: 12,
      totalTokens: 1200,
      inputTokens: 700,
      outputTokens: 400,
      cachedTokens: 100,
      cacheReadTokens: 80,
      cacheCreationTokens: 20,
      estimatedCost: 1.25,
      averageCostPerCall: 1.25 / 12,
      successRate: 11 / 12,
      failureCount: 1,
      averageLatencyMs: 250,
      p95LatencyMs: 420,
      p95TtftMs: 180,
      rpm30m: 0,
      tpm30m: 0,
    },
    summaryDelta: { hasComparison: false, requestCount: 0, totalTokens: 0, estimatedCost: 0 },
    timeline: [point],
    modelRows: [modelRow],
    apiKeyRows: [apiKeyRow],
    credentialRows: [credentialRow],
    allCredentialRows: [credentialRow],
    providerRows: [
      {
        id: 'openai',
        label: 'openai',
        requestCount: 12,
        successCount: 11,
        failureCount: 1,
        successRate: 11 / 12,
        cacheRate: 0.1,
        totalTokens: 1200,
        estimatedCost: 1.25,
        averageLatencyMs: 250,
        share: 1,
        models: [modelRow],
      },
    ],
    heatmap: [
      {
        weekday: 1,
        hour: 9,
        requestCount: 12,
        successCount: 11,
        failureCount: 1,
        totalTokens: 1200,
        estimatedCost: 1.25,
        failureRate: 1 / 12,
      },
    ],
    heatmapMetric: 'requestCount',
    setHeatmapMetric: vi.fn(),
    heatmapScaleMode: 'absolute',
    setHeatmapScaleMode: vi.fn(),
    heatmapDateOptions: [
      {
        key: '2026-06-08',
        label: '06/08',
        fromMs: Date.UTC(2026, 5, 8, 0, 0, 0),
        toMs: Date.UTC(2026, 5, 9, 0, 0, 0),
        sampleWindowCount: 24,
      },
    ],
    selectedHeatmapDateKey: 'all',
    selectHeatmapDate: vi.fn(),
    heatmapDateLoading: false,
    heatmapDateError: '',
    selectedHeatmapCell: null,
    selectHeatmapCell: vi.fn(),
    heatmapDetail: null,
    heatmapHighlights: {
      requestPeaks: [
        {
          id: 'requestCount-1-9',
          metric: 'requestCount',
          value: 12,
          point: {
            weekday: 1,
            hour: 9,
            requestCount: 12,
            successCount: 11,
            failureCount: 1,
            totalTokens: 1200,
            estimatedCost: 1.25,
            failureRate: 1 / 12,
          },
        },
      ],
      costPeaks: [
        {
          id: 'estimatedCost-1-9',
          metric: 'estimatedCost',
          value: 1.25,
          point: {
            weekday: 1,
            hour: 9,
            requestCount: 12,
            successCount: 11,
            failureCount: 1,
            totalTokens: 1200,
            estimatedCost: 1.25,
            failureRate: 1 / 12,
          },
        },
      ],
      failureRisks: [
        {
          id: 'failureRate-1-9',
          metric: 'failureRate',
          value: 1 / 12,
          point: {
            weekday: 1,
            hour: 9,
            requestCount: 12,
            successCount: 11,
            failureCount: 1,
            totalTokens: 1200,
            estimatedCost: 1.25,
            failureRate: 1 / 12,
          },
        },
      ],
    },
    browserTimeZone: 'UTC',
    matrix: {
      dimension: 'apiKeyModel',
      metric: 'requestCount',
      rowLabels: ['sk-****7890'],
      columnLabels: ['gpt-4o'],
      cells: [
        {
          rowId: 'sk-****7890',
          rowLabel: 'sk-****7890',
          columnId: 'gpt-4o',
          columnLabel: 'gpt-4o',
          requestCount: 12,
          successCount: 11,
          failureCount: 1,
          totalTokens: 1200,
          estimatedCost: 1.25,
          failureRate: 1 / 12,
          value: 12,
          share: 1,
        },
      ],
      maxValue: 12,
      totalValue: 12,
    },
    matrixDimension: 'apiKeyModel',
    setMatrixDimension: vi.fn(),
    matrixMetric: 'requestCount',
    setMatrixMetric: vi.fn(),
    trendMetric: 'requestCount',
    setTrendMetric: vi.fn(),
    modelTrendSeries: [
      {
        id: 'gpt-4o',
        label: 'gpt-4o',
        color: '#2563eb',
        points: [{ bucketMs: point.bucketMs, label: point.label, value: 12 }],
      },
    ],
    apiKeyTrendSeries: [
      {
        id: 'abcdef1234567890',
        label: 'sk-****7890',
        color: '#2563eb',
        points: [{ bucketMs: point.bucketMs, label: point.label, value: 12 }],
      },
    ],
    credentialTrendSeries: [
      {
        id: 'credential-prod',
        label: 'prod-auth',
        color: '#2563eb',
        points: [{ bucketMs: point.bucketMs, label: point.label, value: 8 }],
      },
    ],
    keyAnomalies: [
      {
        id: 'abcdef1234567890',
        label: 'sk-****7890',
        severity: 'medium',
        reasonKey: 'usage_analytics.anomaly_reason_error_rate',
        triggeredAtMs: point.bucketMs,
        row: apiKeyRow,
      },
    ],
    credentialAnomalies: [
      {
        id: 'credential-prod',
        label: 'prod-auth',
        severity: 'low',
        reasonKey: 'usage_analytics.anomaly_reason_usage_skew',
        triggeredAtMs: point.bucketMs,
        row: credentialRow,
      },
    ],
    credentialQuotaRows: [
      {
        id: 'credential-prod',
        label: 'prod-auth',
        plan: 'Pay as You Go',
        used: 1.25,
        limit: 50,
        remaining: 48.75,
        usedRate: 0.025,
        resetAtMs: point.bucketEndMs,
        status: 'normal',
        refreshedAtMs: point.bucketMs,
      },
    ],
    activeCredentialsOnly: true,
    setActiveCredentialsOnly: vi.fn(),
    insights: [
      {
        id: 'cache-room',
        tone: 'info',
        titleKey: 'usage_analytics.insight_cache_room',
        bodyKey: 'usage_analytics.insight_cache_room_body',
        actionTab: 'trends',
      },
    ],
    anomalyPoints: [
      {
        bucketMs: point.bucketMs,
        bucketEndMs: point.bucketEndMs,
        label: point.label,
        severity: 'medium',
        metricKeys: ['request_spike'],
        requestCount: 12,
        totalTokens: 1200,
        estimatedCost: 1.25,
        failureRate: 1 / 12,
        requestChange: 0,
        costChange: 0,
        tokensPerRequestChange: 0,
        cacheHitRateChange: 0,
        failureRateChange: 0,
        latencyP95Change: 0,
      },
    ],
    drilldownPreview: [],
    filterOptions: {
      models: ['gpt-4o'],
      api_key_hashes: ['abcdef1234567890'],
      providers: ['openai'],
      auth_files: ['auth.json'],
    },
    selectedBucket: point,
    selectBucket: vi.fn(),
    anomalyAnalysis: {
      point,
      previousPoint: null,
      anomalies: [],
      changes: {
        requestCount: 0,
        totalTokens: 0,
        inputTokens: 0,
        outputTokens: 0,
        cachedTokens: 0,
        cacheCreationTokens: 0,
        estimatedCost: 0,
        cacheHitRate: 0,
        averageTokensPerRequest: 0,
        failureRate: 0,
        averageLatencyMs: 0,
      },
      causeKeys: ['usage_analytics.cause_no_clear_anomaly'],
    },
    selectedModel: modelRow,
    setSelectedModelId: vi.fn(),
    selectedApiKey: apiKeyRow,
    setSelectedApiKeyHash: vi.fn(),
    selectedCredential: credentialRow,
    setSelectedCredentialId: vi.fn(),
    ...overrides,
  };
};

beforeEach(() => {
  mocks.navigate.mockReset();
  mocks.usageState = createUsageState();
});

describe('UsageAnalyticsPage', () => {
  it('renders overview as the default tab with risk, trend, and contribution panels', () => {
    const renderer = renderPage();
    const text = getText(renderer.root);

    expect(text).toContain('usage_analytics.tab_overview');
    expect(text).toContain('usage_analytics.anomaly_points_title');
    expect(text).toContain('usage_analytics.insights_title');
    expect(text).toContain('usage_analytics.overview_trend_title');
    expect(text).toContain('usage_analytics.health_timeline_title');
    expect(text).toContain('usage_analytics.model_overview_title');
    expect(text).toContain('usage_analytics.api_key_overview_title');
    expect(text).toContain('usage_analytics.provider_overview_title');
    expect(text).not.toContain('usage_analytics.analysis_entry_trends');
    expect(text).not.toContain('usage_analytics.favorite_views_title');
    expect(text).not.toContain('usage_analytics.recent_views_title');
    expect(text).not.toContain('usage_analytics.model_rank_title');
  });

  it('renders the overview request health timeline without click actions', () => {
    const usageState = createUsageState();
    mocks.usageState = usageState;
    const renderer = renderPage();
    const timelineCells = renderer.root.findAll((node) =>
      String(node.props.title ?? '').includes('usage_analytics.health_timeline_status')
    );
    const timelineButton = renderer.root
      .findAllByType('button')
      .find((node) =>
        String(node.props['aria-label'] ?? '').includes('usage_analytics.health_timeline_status')
      );

    expect(timelineCells.length).toBeGreaterThan(0);
    expect(timelineButton).toBeUndefined();
    expect(usageState.selectBucket).not.toHaveBeenCalled();
  });

  it('compacts long hourly health timelines into bounded day cells', () => {
    const hourMs = 60 * 60 * 1000;
    const dayMs = 24 * hourMs;
    const startDate = new Date(1_780_000_000_000);
    startDate.setHours(0, 0, 0, 0);
    const fromMs = startDate.getTime();
    const timeline = Array.from({ length: 30 }, (_, index) =>
      createTimelinePoint({
        bucketMs: fromMs + index * dayMs + 12 * hourMs,
        bucketEndMs: fromMs + index * dayMs + 13 * hourMs,
        failureCount: index % 5 === 0 ? 1 : 0,
        failureRate: index % 5 === 0 ? 0.1 : 0,
        label: `day-${index + 1}`,
        requestCount: 10,
        successCount: index % 5 === 0 ? 9 : 10,
        successRate: index % 5 === 0 ? 0.9 : 1,
      })
    );
    mocks.usageState = createUsageState({
      bounds: { fromMs, toMs: fromMs + 30 * dayMs },
      resolvedGranularity: 'hour',
      timeline,
    });
    const renderer = renderPage();
    const timelineCells = renderer.root.findAll((node) =>
      String(node.props.title ?? '').includes('usage_analytics.health_timeline_status')
    );

    expect(timelineCells).toHaveLength(30);
  });

  it('renders trends as a focused time-series workspace', () => {
    mocks.usageState = createUsageState({
      activeTab: 'trends',
      anomalyAnalysis: null,
      selectedBucket: null,
    });
    const renderer = renderPage();
    const text = getText(renderer.root);

    expect(text).toContain('usage_analytics.trend_peak_request_bucket');
    expect(text).toContain('usage_analytics.trend_average_bucket_requests');
    expect(text).toContain('usage_analytics.trend_metric_requestCount');
    expect(text).toContain('usage_analytics.trend_entity_compare_title');
    expect(text).toContain('usage_analytics.model_compare_title');
    expect(text).toContain('usage_analytics.api_key_compare_title');
    expect(text).toContain('usage_analytics.health_trend_title');
    expect(text).toContain('usage_analytics.token_structure_title');
    expect(text).toContain('usage_analytics.anomaly_points_title');
    expect(text).not.toContain('usage_analytics.api_key_warning_title');
    expect(text).not.toContain('usage_analytics.model_overview_title');
    expect(text).not.toContain('usage_analytics.api_key_overview_title');
    expect(text).not.toContain('usage_analytics.drilldown_preview_title');
  });

  it('shows empty and error states from the analytics hook', () => {
    mocks.usageState = createUsageState({
      summary: {
        requestCount: 0,
        totalTokens: 0,
        inputTokens: 0,
        outputTokens: 0,
        cachedTokens: 0,
        cacheReadTokens: 0,
        cacheCreationTokens: 0,
        estimatedCost: 0,
        averageCostPerCall: 0,
        successRate: 0,
        failureCount: 0,
        averageLatencyMs: null,
        p95LatencyMs: null,
        p95TtftMs: null,
        rpm30m: 0,
        tpm30m: 0,
      },
      timeline: [],
      selectedBucket: null,
      anomalyAnalysis: null,
    });
    let renderer = renderPage();
    expect(getText(renderer.root)).toContain('usage_analytics.empty_title');
    renderer.unmount();

    mocks.usageState = createUsageState({
      error: 'analytics failed',
    });
    renderer = renderPage();
    expect(getText(renderer.root)).toContain('usage_analytics.error_title');
    expect(getText(renderer.root)).toContain('analytics failed');
  });

  it('navigates to request monitoring details for a selected anomaly bucket', () => {
    const renderer = renderPage();
    clickHostButton(findHostButtonByText(renderer, 'usage_analytics.view_monitoring_details'));

    expect(mocks.navigate).toHaveBeenCalledWith(
      '/monitoring?from_ms=1780000000000&to_ms=1780003600000'
    );
  });

  it('renders available advanced filters without unavailable placeholders', () => {
    const renderer = renderPage();

    clickHostButton(findHostButtonByText(renderer, 'usage_analytics.show_advanced_filters'));

    const selects = renderer.root.findAllByType(Select);
    const selectLabels = selects.map((node) => node.props.ariaLabel);
    const cacheStatusSelect = selects.find(
      (node) => node.props.ariaLabel === 'usage_analytics.filter_cache_status'
    );
    const text = getText(renderer.root);
    expect(selectLabels).toEqual(
      expect.arrayContaining([
        'usage_analytics.filter_auth_file',
        'usage_analytics.filter_latency',
        'usage_analytics.filter_cache_status',
      ])
    );
    expect(
      cacheStatusSelect?.props.options.map((option: { value: string }) => option.value)
    ).toEqual(['all', 'hit', 'miss']);
    expect(text).not.toContain('usage_analytics.filter_auth_file');
    expect(text).not.toContain('usage_analytics.filter_latency');
    expect(text).not.toContain('usage_analytics.filter_cache_status');
    expect(text).not.toContain('usage_analytics.filter_exclude_zero_token');
    expect(text).not.toContain('usage_analytics.filter_request_type');
    expect(text).not.toContain('usage_analytics.filter_project_team');
    expect(text).not.toContain('usage_analytics.common_views_title');
  });

  it('does not render selected filter chips for active filters', () => {
    mocks.usageState = createUsageState({
      filters: {
        ...USAGE_ANALYTICS_DEFAULT_FILTERS,
        searchQuery: 'req-42',
        cacheStatus: 'hit',
        minLatencyMs: '10000',
      },
    });
    const renderer = renderPage();

    const text = getText(renderer.root);
    expect(text).not.toContain('usage_analytics.selected_filters');
    expect(text).not.toContain('usage_analytics.filter_search: req-42');
    expect(text).not.toContain(
      'usage_analytics.filter_cache_status: usage_analytics.cache_status_hit'
    );
    expect(text).not.toContain(
      'usage_analytics.filter_latency: usage_analytics.latency_over_10000'
    );
  });

  it('keeps API key values masked in the API Key tab', () => {
    mocks.usageState = createUsageState({ activeTab: 'apiKeys' });
    const renderer = renderPage();
    const text = getText(renderer.root);

    expect(text).toContain('sk-****7890');
    expect(text).not.toContain('abcdef1234567890');
    expect(text).not.toContain('usage_analytics.trend_pending_data');
  });

  it('renders the API Key tab with key-dimension cards, unit-economics columns, and anomaly drilldown', () => {
    mocks.usageState = createUsageState({ activeTab: 'apiKeys' });
    const renderer = renderPage();
    const text = getText(renderer.root);

    // Key-dimension summary cards replace the global totals.
    expect(text).toContain('usage_analytics.active_api_keys');
    expect(text).toContain('usage_analytics.api_key_top_cost_share');
    expect(text).toContain('usage_analytics.api_key_lowest_success');
    expect(text).toContain('usage_analytics.metric_average_cost_per_call');
    expect(text).toContain('usage_analytics.anomaly_keys');

    // Rank table gains the model-tab unit-economics columns.
    expect(text).toContain('usage_analytics.cache_read_rate');
    expect(text).toContain('usage_analytics.metric_failure_count');

    // Detail panel stays unit-economics only, plus the related model distribution.
    expect(text).toContain('usage_analytics.api_key_detail_title');
    expect(text).toContain('usage_analytics.average_tokens_per_request');
    expect(text).toContain('usage_analytics.related_model_distribution');

    // Anomaly rows drill down into monitoring scoped to the key.
    const drilldownButtons = renderer.root
      .findAllByType('button')
      .filter((node) => getText(node) === 'usage_analytics.view_request_details');
    expect(drilldownButtons.length).toBeGreaterThan(0);
    clickHostButton(drilldownButtons[drilldownButtons.length - 1]);
    expect(mocks.navigate).toHaveBeenCalledWith('/monitoring?api_key_hash=abcdef1234567890');
  });

  it('renders the models tab with unit-economics columns and model-scoped insights only', () => {
    mocks.usageState = createUsageState({
      activeTab: 'models',
      insights: [
        {
          id: 'model-cost-share',
          tone: 'warning',
          titleKey: 'usage_analytics.insight_model_cost_high',
          bodyKey: 'usage_analytics.insight_model_cost_high_body',
          actionTab: 'models',
        },
        {
          id: 'credential-health',
          tone: 'danger',
          titleKey: 'usage_analytics.insight_credential_success_drop',
          bodyKey: 'usage_analytics.insight_credential_success_drop_body',
          actionTab: 'credentials',
        },
      ],
    });
    const renderer = renderPage();
    const text = getText(renderer.root);

    expect(text).toContain('usage_analytics.model_rank_title');
    expect(text).toContain('usage_analytics.cache_read_rate');
    expect(text).toContain('usage_analytics.metric_average_cost_per_call');
    expect(text).toContain('usage_analytics.metric_failure_count');
    expect(text).toContain('usage_analytics.cost_share');
    expect(text).toContain('usage_analytics.model_top_cost_share');
    expect(text).toContain('usage_analytics.model_caller_distribution');
    expect(text).toContain('usage_analytics.view_request_details');
    expect(text).toContain('usage_analytics.insight_model_cost_high');
    expect(text).not.toContain('usage_analytics.insight_credential_success_drop');
    // Only one model row, so the show-all toggle stays hidden.
    expect(text).not.toContain('usage_analytics.rank_show_all');
  });

  it('renders the heatmap tab as a focused time-window workspace', () => {
    const usageState = createUsageState({ activeTab: 'heatmap' });
    mocks.usageState = usageState;
    const renderer = renderPage();
    const text = getText(renderer.root);

    expect(text).toContain('usage_analytics.heatmap_title');
    expect(text).toContain('usage_analytics.heatmap_metric_requestCount');
    expect(text).toContain('usage_analytics.heatmap_scale_absolute');
    expect(text).not.toContain('usage_analytics.heatmap_date_all');
    expect(text).not.toContain('06/08');
    expect(text).not.toContain('usage_analytics.heatmap_range_label');
    expect(text).toContain('usage_analytics.heatmap_focus_title');
    expect(text).toContain('usage_analytics.heatmap_peak_requests');
    expect(text).not.toContain('usage_analytics.insights_title');
    expect(text).not.toContain('usage_analytics.insight_heatmap_failure_window');
    expect(text).not.toContain('usage_analytics.heatmap_matrix_title');
    expect(text).not.toContain('usage_analytics.hot_combinations_title');

    expect(
      findHostButtonByText(renderer, 'usage_analytics.heatmap_metric_requestCount').props[
        'aria-pressed'
      ]
    ).toBe(true);
    expect(
      findHostButtonByText(renderer, 'usage_analytics.heatmap_metric_totalTokens').props[
        'aria-pressed'
      ]
    ).toBe(false);

    clickHostButton(findHostButtonByText(renderer, 'usage_analytics.heatmap_metric_totalTokens'));
    expect(usageState.setHeatmapMetric).toHaveBeenCalledWith('totalTokens');
  });

  it('renders selected heatmap contributors with masked API keys', () => {
    const point = {
      weekday: 1,
      hour: 9,
      requestCount: 12,
      successCount: 11,
      failureCount: 1,
      totalTokens: 1200,
      estimatedCost: 1.25,
      failureRate: 1 / 12,
      modelContributors: [
        {
          key: 'gpt-4o',
          label: 'gpt-4o',
          requestCount: 8,
          successCount: 8,
          failureCount: 0,
          totalTokens: 900,
          estimatedCost: 1,
          failureRate: 0,
          share: 8 / 12,
        },
      ],
      apiKeyContributors: [
        {
          key: 'abcdef1234567890',
          label: 'abcdef1234567890',
          requestCount: 8,
          successCount: 8,
          failureCount: 0,
          totalTokens: 900,
          estimatedCost: 1,
          failureRate: 0,
          share: 8 / 12,
        },
      ],
      providerContributors: [
        {
          key: 'openai',
          label: 'openai',
          requestCount: 8,
          successCount: 8,
          failureCount: 0,
          totalTokens: 900,
          estimatedCost: 1,
          failureRate: 0,
          share: 8 / 12,
        },
      ],
    };
    const usageState = createUsageState({
      activeTab: 'heatmap',
      bounds: {
        fromMs: Date.UTC(2026, 5, 8, 0, 0, 0),
        toMs: Date.UTC(2026, 5, 16, 0, 0, 0),
      },
      browserTimeZone: 'UTC',
      heatmap: [point],
      selectedHeatmapCell: { weekday: 1, hour: 9 },
      heatmapDetail: {
        point,
        metricValue: 12,
        overallBaseline: 12,
        weekdayBaseline: 12,
        hourBaseline: 12,
        overallDelta: 0,
        weekdayDelta: 0,
        hourDelta: 0,
        rank: 1,
        totalCells: 1,
      },
    });
    mocks.usageState = usageState;
    const renderer = renderPage();
    const text = getText(renderer.root);

    expect(text).toContain('usage_analytics.heatmap_date_all');
    expect(text).toContain('06/08');
    expect(text).toContain('usage_analytics.heatmap_contributors_title');
    expect(text).toContain('gpt-4o');
    expect(text).toContain('sk-****7890');
    expect(text).toContain('openai');
    expect(text).not.toContain('abcdef1234567890');

    clickHostButton(findHostButtonByText(renderer, '06/08'));
    expect(usageState.selectHeatmapDate).toHaveBeenCalledWith('2026-06-08');
  });

  it('keeps date-specific empty heatmap details inside the selected window panel', () => {
    mocks.usageState = createUsageState({
      activeTab: 'heatmap',
      heatmapDetail: null,
      selectedHeatmapCell: { weekday: 1, hour: 9 },
      selectedHeatmapDateKey: '2026-06-08',
    });
    const renderer = renderPage();
    const text = getText(renderer.root);

    expect(text).toContain('usage_analytics.heatmap_detail_title');
    expect(text).toContain('usage_analytics.heatmap_date_all');
    expect(text).toContain('usage_analytics.heatmap_date_empty');
    expect(text).not.toContain('usage_analytics.heatmap_focus_title');
  });

  it('keeps heatmap detail content mounted while a date tab refreshes', () => {
    const point = {
      weekday: 1,
      hour: 9,
      requestCount: 12,
      successCount: 11,
      failureCount: 1,
      totalTokens: 1200,
      estimatedCost: 1.25,
      failureRate: 1 / 12,
    };
    mocks.usageState = createUsageState({
      activeTab: 'heatmap',
      heatmapDateLoading: true,
      heatmapDetail: {
        point,
        metricValue: 12,
        overallBaseline: 12,
        weekdayBaseline: 12,
        hourBaseline: 12,
        overallDelta: 0,
        weekdayDelta: 0,
        hourDelta: 0,
        rank: 1,
        totalCells: 1,
      },
      selectedHeatmapCell: { weekday: 1, hour: 9 },
      selectedHeatmapDateKey: '2026-06-08',
    });
    const renderer = renderPage();
    const text = getText(renderer.root);

    expect(text).toContain('common.loading');
    expect(text).toContain('usage_analytics.metric_request_count');
    expect(text).toContain('usage_analytics.metric_total_tokens');
    expect(text).not.toContain('usage_analytics.heatmap_date_empty');
  });

  it('offers time range and status controls that update usage filters', () => {
    const usageState = createUsageState();
    mocks.usageState = usageState;
    const renderer = renderPage();

    clickHostButton(findHostButtonByText(renderer, 'usage_analytics.range_24h'));

    expect(usageState.setFilters).toHaveBeenCalledWith({ timeRange: '24h' });
  });

  it('keeps the removed page header and export action out of the page shell', () => {
    const renderer = renderPage();
    const text = getText(renderer.root);

    expect(text).not.toContain('usage_analytics.title');
    expect(text).not.toContain('usage_analytics.subtitle');
    expect(text).not.toContain('usage_analytics.export');
    expect(
      renderer.root
        .findAllByType(Button)
        .some((button) => getText(button).includes('common.refresh'))
    ).toBe(true);
  });
});
