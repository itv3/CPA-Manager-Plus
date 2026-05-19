import { useCallback, useEffect, useMemo, useRef, useState } from 'react';
import {
  useRequestMonitoringAvailability,
  type RequestMonitoringUnavailableReason,
} from '@/hooks/useRequestMonitoringAvailability';
import {
  monitoringAnalyticsApi,
  type MonitoringAnalyticsEventsPageRequest,
  type MonitoringAnalyticsFilters,
  type MonitoringAnalyticsInclude,
  type MonitoringAnalyticsRequest,
  type MonitoringAnalyticsResponse,
} from '@/services/api/usageService';
import { useAuthStore } from '@/stores';

const DEFAULT_REFRESH_THROTTLE_MS = 5_000;

export interface UseMonitoringAnalyticsParams {
  fromMs?: number | null;
  toMs?: number | null;
  nowMs?: number;
  searchQuery?: string;
  searchApiKeyHash?: string;
  filters?: MonitoringAnalyticsFilters;
  include?: MonitoringAnalyticsInclude;
  eventsPage?: MonitoringAnalyticsEventsPageRequest | null;
  throttleMs?: number;
}

export interface MonitoringAnalyticsRefreshOptions {
  force?: boolean;
}

export interface UseMonitoringAnalyticsReturn {
  enabled: boolean;
  loading: boolean;
  error: string;
  data: MonitoringAnalyticsResponse | null;
  lastRefreshedAt: Date | null;
  serviceBase: string;
  unavailableReason: RequestMonitoringUnavailableReason | '';
  refresh: (options?: MonitoringAnalyticsRefreshOptions) => Promise<void>;
}

const isFiniteTimestamp = (value: number | null | undefined): value is number =>
  typeof value === 'number' && Number.isFinite(value);

const stableJson = (value: unknown) => JSON.stringify(value ?? {});

const parseJson = <T>(value: string): T => JSON.parse(value) as T;

export function useMonitoringAnalytics({
  fromMs,
  toMs,
  nowMs,
  searchQuery,
  searchApiKeyHash,
  filters,
  include,
  eventsPage,
  throttleMs = DEFAULT_REFRESH_THROTTLE_MS,
}: UseMonitoringAnalyticsParams): UseMonitoringAnalyticsReturn {
  const managementKey = useAuthStore((state) => state.managementKey);
  const availability = useRequestMonitoringAvailability();
  const [data, setData] = useState<MonitoringAnalyticsResponse | null>(null);
  const [loading, setLoading] = useState(false);
  const [error, setError] = useState('');
  const [lastRefreshedAt, setLastRefreshedAt] = useState<Date | null>(null);
  const requestIdRef = useRef(0);
  const lastStartedAtRef = useRef(0);
  const lastRequestKeyRef = useRef('');

  const filtersKey = useMemo(() => stableJson(filters), [filters]);
  const includeKey = useMemo(() => stableJson(include), [include]);
  const eventsPageKey = useMemo(() => JSON.stringify(eventsPage ?? null), [eventsPage]);

  const request = useMemo<MonitoringAnalyticsRequest | null>(() => {
    if (!isFiniteTimestamp(fromMs) || !isFiniteTimestamp(toMs) || fromMs <= 0 || fromMs >= toMs) {
      return null;
    }

    const nextFilters = parseJson<MonitoringAnalyticsFilters>(filtersKey);
    const nextInclude = parseJson<MonitoringAnalyticsInclude>(includeKey);
    const nextEventsPage = parseJson<MonitoringAnalyticsEventsPageRequest | null>(eventsPageKey);
    if (nextEventsPage) {
      nextInclude.events_page = nextEventsPage;
    }

    const payload: MonitoringAnalyticsRequest = {
      from_ms: fromMs,
      to_ms: toMs,
    };
    if (isFiniteTimestamp(nowMs)) {
      payload.now_ms = nowMs;
    }

    const normalizedSearchQuery = searchQuery?.trim();
    if (normalizedSearchQuery) {
      payload.search_query = normalizedSearchQuery;
    }

    const normalizedApiKeyHash = searchApiKeyHash?.trim();
    if (normalizedApiKeyHash) {
      payload.search_api_key_hash = normalizedApiKeyHash;
    }

    if (Object.keys(nextFilters).length > 0) {
      payload.filters = nextFilters;
    }
    if (Object.keys(nextInclude).length > 0) {
      payload.include = nextInclude;
    }
    return payload;
  }, [eventsPageKey, filtersKey, fromMs, includeKey, nowMs, searchApiKeyHash, searchQuery, toMs]);

  const requestKey = useMemo(() => (request ? stableJson(request) : ''), [request]);
  const serviceBase = availability.serviceBase;
  const enabled = availability.available && Boolean(serviceBase) && Boolean(request);

  const refresh = useCallback(
    async (options: MonitoringAnalyticsRefreshOptions = {}) => {
      if (!enabled || !request || !serviceBase) {
        requestIdRef.current += 1;
        setData(null);
        setLastRefreshedAt(null);
        setLoading(false);
        return;
      }

      const startedAt = Date.now();
      const isSameRequest = lastRequestKeyRef.current === requestKey;
      if (
        !options.force &&
        isSameRequest &&
        startedAt - lastStartedAtRef.current < Math.max(0, throttleMs)
      ) {
        return;
      }

      const requestId = requestIdRef.current + 1;
      requestIdRef.current = requestId;
      lastStartedAtRef.current = startedAt;
      lastRequestKeyRef.current = requestKey;
      setLoading(true);
      setError('');

      try {
        const response = await monitoringAnalyticsApi.getAnalytics(serviceBase, managementKey, request);
        if (requestIdRef.current !== requestId) return;
        setData(response);
        setLastRefreshedAt(new Date());
      } catch (err) {
        if (requestIdRef.current !== requestId) return;
        setData(null);
        setError(err instanceof Error ? err.message : String(err));
      } finally {
        if (requestIdRef.current === requestId) {
          setLoading(false);
        }
      }
    },
    [enabled, managementKey, request, requestKey, serviceBase, throttleMs]
  );

  useEffect(() => {
    if (availability.checking) {
      return;
    }
    void refresh({ force: true });
  }, [availability.checking, refresh]);

  return useMemo(
    () => ({
      enabled,
      loading: availability.checking || loading,
      error,
      data,
      lastRefreshedAt,
      serviceBase,
      unavailableReason: availability.reason,
      refresh,
    }),
    [
      availability.checking,
      availability.reason,
      data,
      enabled,
      error,
      lastRefreshedAt,
      loading,
      refresh,
      serviceBase,
    ]
  );
}
