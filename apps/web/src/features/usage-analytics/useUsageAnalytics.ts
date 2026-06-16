import { useCallback, useEffect, useMemo, useRef, useState } from 'react';
import { useMonitoringAnalytics } from '@/features/monitoring/hooks/useMonitoringAnalytics';
import {
  adaptUsageAnalyticsData,
  analyzeUsageBucket,
  buildCredentialQuotaRows,
  buildEntityTrendSeries,
  buildKeyAnomalies,
  buildUsageInsights,
  buildUsageHeatmap,
  buildUsageHeatmapCellDetail,
  buildUsageHeatmapCellDateOptions,
  buildUsageHeatmapHighlights,
  buildUsageHeatmapRangeContext,
  buildUsageMatrix,
  buildUsageSummaryDelta,
  buildUsageAnalyticsFilters,
  buildUsageAnalyticsInclude,
  getUsageRangeBounds,
  resolveUsageGranularity,
  USAGE_ANALYTICS_DEFAULT_FILTERS,
  type UsageMatrixDimension,
  type UsageMatrixMetricKey,
  type UsageTrendMetricKey,
  type UsageAnalyticsFiltersState,
  type UsageAnalyticsTab,
  type UsageSelectedFilterKey,
  type UsageAnomalyAnalysis,
  type UsageHeatmapCellDetail,
  type UsageHeatmapCellSelection,
  type UsageHeatmapDateOption,
  type UsageHeatmapMetricKey,
  type UsageHeatmapScaleMode,
  type UsageTimelinePoint,
} from './usageAnalyticsModel';
import { readUsageAnalyticsUiState, writeUsageAnalyticsUiState } from './usageAnalyticsUiState';

const USAGE_SEARCH_DEBOUNCE_MS = 350;
const USAGE_HEATMAP_ALL_DATES_KEY = 'all';

const getBrowserTimeZone = () => {
  if (typeof Intl === 'undefined') return 'UTC';
  return Intl.DateTimeFormat().resolvedOptions().timeZone || 'UTC';
};

function useDebouncedValue<T>(value: T, delayMs: number): T {
  const [debouncedValue, setDebouncedValue] = useState(value);

  useEffect(() => {
    const timer = setTimeout(() => setDebouncedValue(value), delayMs);
    return () => clearTimeout(timer);
  }, [delayMs, value]);

  return debouncedValue;
}

export function useUsageAnalytics() {
  const [filters, setFiltersState] = useState<UsageAnalyticsFiltersState>(
    () => readUsageAnalyticsUiState().filters
  );
  const [activeTabState, setActiveTabState] = useState<UsageAnalyticsTab>('overview');
  const [nowMs, setNowMs] = useState(() => Date.now());
  const [selectedBucketMs, setSelectedBucketMs] = useState<number | null>(null);
  const [selectedModelId, setSelectedModelId] = useState('');
  const [selectedApiKeyHash, setSelectedApiKeyHash] = useState('');
  const [selectedCredentialId, setSelectedCredentialId] = useState('');
  const [trendMetric, setTrendMetric] = useState<UsageTrendMetricKey>('requestCount');
  const [matrixDimension, setMatrixDimension] = useState<UsageMatrixDimension>('apiKeyModel');
  const [matrixMetric, setMatrixMetric] = useState<UsageMatrixMetricKey>('requestCount');
  const [heatmapMetric, setHeatmapMetric] = useState<UsageHeatmapMetricKey>('requestCount');
  const [heatmapScaleMode, setHeatmapScaleMode] = useState<UsageHeatmapScaleMode>('absolute');
  const [selectedHeatmapDateKey, setSelectedHeatmapDateKey] = useState(USAGE_HEATMAP_ALL_DATES_KEY);
  const [selectedHeatmapCell, setSelectedHeatmapCell] = useState<UsageHeatmapCellSelection | null>(
    null
  );
  const [activeCredentialsOnly, setActiveCredentialsOnly] = useState(true);
  const browserTimeZone = useMemo(() => getBrowserTimeZone(), []);
  const setActiveTab = useCallback((tab: UsageAnalyticsTab) => {
    setActiveTabState(tab);
  }, []);
  const debouncedSearchQuery = useDebouncedValue(
    filters.searchQuery.trim(),
    USAGE_SEARCH_DEBOUNCE_MS
  );

  const bounds = useMemo(() => getUsageRangeBounds(filters, nowMs), [filters, nowMs]);
  const heatmapRangeContext = useMemo(
    () => buildUsageHeatmapRangeContext(bounds, 'en-US', browserTimeZone),
    [bounds, browserTimeZone]
  );
  const heatmapDateOptions = useMemo(
    () => buildUsageHeatmapCellDateOptions(heatmapRangeContext, selectedHeatmapCell),
    [heatmapRangeContext, selectedHeatmapCell]
  );
  const selectedHeatmapDate = useMemo<UsageHeatmapDateOption | null>(
    () =>
      selectedHeatmapDateKey === USAGE_HEATMAP_ALL_DATES_KEY
        ? null
        : (heatmapDateOptions.find((option) => option.key === selectedHeatmapDateKey) ?? null),
    [heatmapDateOptions, selectedHeatmapDateKey]
  );
  const activeHeatmapDateKey = selectedHeatmapDate
    ? selectedHeatmapDateKey
    : USAGE_HEATMAP_ALL_DATES_KEY;
  const resolvedGranularity = useMemo(
    () => resolveUsageGranularity(filters, nowMs),
    [filters, nowMs]
  );
  const analyticsFilters = useMemo(() => buildUsageAnalyticsFilters(filters), [filters]);
  const drilldownPreview = useMemo(() => {
    if (selectedBucketMs === null) return null;
    return {
      fromMs: selectedBucketMs,
      toMs:
        selectedBucketMs + (resolvedGranularity === 'day' ? 24 * 60 * 60 * 1000 : 60 * 60 * 1000),
      limit: 12,
    };
  }, [resolvedGranularity, selectedBucketMs]);
  const include = useMemo(
    () => buildUsageAnalyticsInclude(resolvedGranularity, drilldownPreview),
    [drilldownPreview, resolvedGranularity]
  );
  const dataScopeKey = useMemo(
    () =>
      JSON.stringify({
        bounds,
        drilldownPreview,
        filters: analyticsFilters,
        granularity: resolvedGranularity,
        searchQuery: debouncedSearchQuery,
      }),
    [analyticsFilters, bounds, debouncedSearchQuery, drilldownPreview, resolvedGranularity]
  );

  const analytics = useMonitoringAnalytics({
    fromMs: bounds?.fromMs,
    toMs: bounds?.toMs,
    nowMs,
    dataScopeKey,
    searchQuery: debouncedSearchQuery,
    filters: analyticsFilters,
    include,
    throttleMs: 0,
  });

  const heatmapDateInclude = useMemo(
    () => ({
      granularity: 'hour' as const,
      heatmap: true,
    }),
    []
  );
  const heatmapDateDataScopeKey = useMemo(
    () =>
      JSON.stringify({
        date: selectedHeatmapDate
          ? {
              fromMs: selectedHeatmapDate.fromMs,
              key: selectedHeatmapDate.key,
              toMs: selectedHeatmapDate.toMs,
            }
          : null,
        filters: analyticsFilters,
        searchQuery: debouncedSearchQuery,
      }),
    [analyticsFilters, debouncedSearchQuery, selectedHeatmapDate]
  );
  const heatmapDateAnalytics = useMonitoringAnalytics({
    fromMs: selectedHeatmapDate?.fromMs,
    toMs: selectedHeatmapDate?.toMs,
    nowMs,
    dataScopeKey: heatmapDateDataScopeKey,
    searchQuery: debouncedSearchQuery,
    filters: analyticsFilters,
    include: heatmapDateInclude,
    throttleMs: 0,
  });

  const analyticsData = analytics.dataStale ? null : analytics.data;
  const adapted = useMemo(
    () => adaptUsageAnalyticsData(analyticsData, resolvedGranularity, filters.apiKeyKeyword),
    [analyticsData, filters.apiKeyKeyword, resolvedGranularity]
  );
  const heatmapDateData = heatmapDateAnalytics.dataStale ? null : heatmapDateAnalytics.data;
  const heatmapDateRows = useMemo(
    () => buildUsageHeatmap(heatmapDateData?.heatmap ?? []),
    [heatmapDateData]
  );
  const heatmapDetailSource = selectedHeatmapDate ? heatmapDateRows : adapted.heatmap;
  const heatmapDateRefreshing = Boolean(
    selectedHeatmapDate &&
      (heatmapDateAnalytics.loading ||
        heatmapDateAnalytics.dataStale ||
        (!heatmapDateAnalytics.data && !heatmapDateAnalytics.error))
  );
  const lastVisibleHeatmapDetailRef = useRef<UsageHeatmapCellDetail | null>(null);

  const summaryDelta = useMemo(
    () => buildUsageSummaryDelta(adapted.summary, adapted.summaryComparison),
    [adapted.summary, adapted.summaryComparison]
  );

  const selectedBucket = useMemo(
    () =>
      selectedBucketMs === null
        ? null
        : (adapted.timeline.find((point) => point.bucketMs === selectedBucketMs) ?? null),
    [adapted.timeline, selectedBucketMs]
  );

  const anomalyAnalysis = useMemo<UsageAnomalyAnalysis | null>(
    () =>
      selectedBucketMs === null ? null : analyzeUsageBucket(adapted.timeline, selectedBucketMs),
    [adapted.timeline, selectedBucketMs]
  );

  const selectedModel =
    adapted.modelRows.find((row) => row.id === selectedModelId) ?? adapted.modelRows[0] ?? null;
  const selectedApiKey =
    adapted.apiKeyRows.find((row) => row.apiKeyHash === selectedApiKeyHash) ??
    adapted.apiKeyRows[0] ??
    null;
  const selectedCredential =
    adapted.credentialRows.find((row) => row.id === selectedCredentialId) ??
    adapted.credentialRows[0] ??
    null;

  const visibleCredentialRows = useMemo(
    () =>
      activeCredentialsOnly
        ? adapted.credentialRows.filter((row) => row.requestCount > 0)
        : adapted.credentialRows,
    [activeCredentialsOnly, adapted.credentialRows]
  );

  const modelTrendSeries = useMemo(
    () => buildEntityTrendSeries(adapted.modelRows, adapted.timeline, trendMetric, 4),
    [adapted.modelRows, adapted.timeline, trendMetric]
  );
  const apiKeyTrendSeries = useMemo(
    () => buildEntityTrendSeries(adapted.apiKeyRows, adapted.timeline, trendMetric, 4),
    [adapted.apiKeyRows, adapted.timeline, trendMetric]
  );
  const credentialTrendSeries = useMemo(
    () => buildEntityTrendSeries(visibleCredentialRows, adapted.timeline, trendMetric, 4),
    [visibleCredentialRows, adapted.timeline, trendMetric]
  );
  const heatmapDetail = useMemo(
    () => buildUsageHeatmapCellDetail(heatmapDetailSource, selectedHeatmapCell, heatmapMetric),
    [heatmapDetailSource, heatmapMetric, selectedHeatmapCell]
  );
  if (heatmapDetail) {
    lastVisibleHeatmapDetailRef.current = heatmapDetail;
  }
  const visibleHeatmapDetail =
    heatmapDetail ?? (heatmapDateRefreshing ? lastVisibleHeatmapDetailRef.current : null);
  const heatmapHighlights = useMemo(
    () => buildUsageHeatmapHighlights(adapted.heatmap),
    [adapted.heatmap]
  );
  const matrix = useMemo(
    () =>
      buildUsageMatrix({
        apiKeyRows: adapted.apiKeyRows,
        credentialRows: adapted.credentialRows,
        dimension: matrixDimension,
        metric: matrixMetric,
      }),
    [adapted.apiKeyRows, adapted.credentialRows, matrixDimension, matrixMetric]
  );
  const keyAnomalies = useMemo(() => buildKeyAnomalies(adapted.apiKeyRows), [adapted.apiKeyRows]);
  const credentialAnomalies = useMemo(
    () => buildKeyAnomalies(adapted.credentialRows),
    [adapted.credentialRows]
  );
  const credentialQuotaRows = useMemo(
    () => buildCredentialQuotaRows(visibleCredentialRows, nowMs),
    [nowMs, visibleCredentialRows]
  );
  const insights = useMemo(
    () =>
      buildUsageInsights({
        apiKeyRows: adapted.apiKeyRows,
        credentialRows: adapted.credentialRows,
        modelRows: adapted.modelRows,
        providerRows: adapted.providerRows,
        summary: adapted.summary,
      }),
    [
      adapted.apiKeyRows,
      adapted.credentialRows,
      adapted.modelRows,
      adapted.providerRows,
      adapted.summary,
    ]
  );
  const setFilters = useCallback((patch: Partial<UsageAnalyticsFiltersState>) => {
    setFiltersState((current) => {
      const next = { ...current, ...patch };
      writeUsageAnalyticsUiState({ filters: next });
      return next;
    });
    setSelectedBucketMs(null);
    setSelectedHeatmapDateKey(USAGE_HEATMAP_ALL_DATES_KEY);
    setSelectedHeatmapCell(null);
  }, []);

  const resetFilters = useCallback(() => {
    setFiltersState(USAGE_ANALYTICS_DEFAULT_FILTERS);
    writeUsageAnalyticsUiState({ filters: USAGE_ANALYTICS_DEFAULT_FILTERS });
    setSelectedBucketMs(null);
    setSelectedHeatmapDateKey(USAGE_HEATMAP_ALL_DATES_KEY);
    setSelectedHeatmapCell(null);
  }, []);

  const clearFilter = useCallback((key: UsageSelectedFilterKey) => {
    setFiltersState((current) => {
      const next = {
        ...current,
        [key]: 'all',
      };
      writeUsageAnalyticsUiState({ filters: next });
      return next;
    });
    setSelectedBucketMs(null);
    setSelectedHeatmapDateKey(USAGE_HEATMAP_ALL_DATES_KEY);
    setSelectedHeatmapCell(null);
  }, []);

  const selectBucket = useCallback((point: UsageTimelinePoint | null) => {
    setSelectedBucketMs(point?.bucketMs ?? null);
  }, []);

  const selectHeatmapCell = useCallback((cell: UsageHeatmapCellSelection | null) => {
    setSelectedHeatmapCell(cell);
    setSelectedHeatmapDateKey(USAGE_HEATMAP_ALL_DATES_KEY);
  }, []);

  const selectHeatmapDate = useCallback((key: string) => {
    setSelectedHeatmapDateKey(key || USAGE_HEATMAP_ALL_DATES_KEY);
  }, []);

  const refresh = useCallback(() => {
    setNowMs(Date.now());
    void analytics.refresh({ force: true });
    if (selectedHeatmapDate) {
      void heatmapDateAnalytics.refresh({ force: true });
    }
  }, [analytics, heatmapDateAnalytics, selectedHeatmapDate]);

  return {
    filters,
    setFilters,
    resetFilters,
    clearFilter,
    activeTab: activeTabState,
    setActiveTab,
    bounds,
    resolvedGranularity,
    loading: analytics.loading,
    error: analytics.error,
    enabled: analytics.enabled,
    unavailableReason: analytics.unavailableReason,
    lastRefreshedAt: analytics.lastRefreshedAt,
    refresh,
    summary: adapted.summary,
    summaryDelta,
    timeline: adapted.timeline,
    modelRows: adapted.modelRows,
    apiKeyRows: adapted.apiKeyRows,
    credentialRows: visibleCredentialRows,
    allCredentialRows: adapted.credentialRows,
    providerRows: adapted.providerRows,
    heatmap: adapted.heatmap,
    heatmapMetric,
    setHeatmapMetric,
    heatmapScaleMode,
    setHeatmapScaleMode,
    heatmapDateOptions,
    selectedHeatmapDateKey: activeHeatmapDateKey,
    selectHeatmapDate,
    heatmapDateLoading: heatmapDateRefreshing,
    heatmapDateError: selectedHeatmapDate ? heatmapDateAnalytics.error : '',
    selectedHeatmapCell,
    selectHeatmapCell,
    heatmapDetail: visibleHeatmapDetail,
    heatmapHighlights,
    browserTimeZone,
    matrix,
    matrixDimension,
    setMatrixDimension,
    matrixMetric,
    setMatrixMetric,
    trendMetric,
    setTrendMetric,
    modelTrendSeries,
    apiKeyTrendSeries,
    credentialTrendSeries,
    keyAnomalies,
    credentialAnomalies,
    credentialQuotaRows,
    activeCredentialsOnly,
    setActiveCredentialsOnly,
    insights,
    anomalyPoints: adapted.anomalyPoints,
    drilldownPreview: adapted.drilldownPreview,
    filterOptions: adapted.filterOptions,
    selectedBucket,
    selectBucket,
    anomalyAnalysis,
    selectedModel,
    setSelectedModelId,
    selectedApiKey,
    setSelectedApiKeyHash,
    selectedCredential,
    setSelectedCredentialId,
  };
}
