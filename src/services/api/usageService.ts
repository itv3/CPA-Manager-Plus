import axios from 'axios';
import type { UsagePayload } from '@/features/monitoring/hooks/useUsageData';
import { normalizeApiBase } from '@/utils/connection';
import type { ModelPrice } from '@/utils/usage';

const USAGE_SERVICE_ERROR_CODES = new Set([
  'request_failed',
  'connection_env_managed',
  'cpa_connection_required',
  'cpa_connection_required_for_monitoring',
  'management_api_validation_failed',
  'management_api_config_failed',
  'cpa_usage_retention_invalid',
  'poll_interval_exceeds_retention',
  'enable_cpa_usage_statistics_failed',
  'setup_env_managed',
  'invalid_existing_management_key',
  'invalid_management_key',
  'usage_service_not_configured',
  'prices_required',
  'api_key_aliases_required',
  'api_key_alias_duplicate',
  'model_price_sync_failed',
  'method_not_allowed',
]);

export interface UsageServiceApiError extends Error {
  status?: number;
  code?: string;
  details?: unknown;
  data?: unknown;
}

export interface UsageServiceInfo {
  service?: string;
  mode?: string;
  startedAt?: number;
  configured?: boolean;
}

export interface UsageServiceCollectorStatus {
  collector?: string;
  upstream?: string;
  mode?: string;
  transport?: string;
  queue?: string;
  lastConsumedAt?: number;
  lastInsertedAt?: number;
  totalInserted?: number;
  totalSkipped?: number;
  deadLetters?: number;
  lastError?: string;
}

export interface UsageServiceStatus {
  service?: string;
  dbPath?: string;
  events?: number;
  deadLetters?: number;
  collector?: UsageServiceCollectorStatus;
}

export interface UsageServiceSetupRequest {
  cpaBaseUrl: string;
  managementKey: string;
  collectorMode?: string;
  queue?: string;
  popSide?: string;
  batchSize?: number;
  pollIntervalMs?: number;
  queryLimit?: number;
  tlsSkipVerify?: boolean;
  ensureUsageStatisticsEnabled?: boolean;
  requestMonitoringEnabled?: boolean;
}

export interface ManagerCPAConnectionConfig {
  cpaBaseUrl: string;
  managementKey?: string;
}

export interface ManagerCollectorConfig {
  enabled?: boolean;
  collectorMode: string;
  queue: string;
  popSide: string;
  batchSize: number;
  pollIntervalMs: number;
  queryLimit: number;
  tlsSkipVerify?: boolean;
}

export interface ManagerExternalUsageServiceConfig {
  enabled: boolean;
  serviceBase: string;
}

export interface ManagerConfig {
  cpaConnection: ManagerCPAConnectionConfig;
  collector: ManagerCollectorConfig;
  externalUsageService: ManagerExternalUsageServiceConfig;
  updatedAtMs?: number;
}

export interface CPAUsageConfig {
  usageStatisticsEnabled: boolean;
  redisUsageQueueRetentionSeconds: number;
  retentionSourceDefault?: boolean;
}

export interface ManagerConfigResponse {
  config: ManagerConfig;
  source?: 'env' | 'db' | '';
  cpaUsage?: CPAUsageConfig;
}

export interface ModelPricesResponse {
  prices: Record<string, ModelPrice>;
}

export interface ModelPriceSyncResponse extends ModelPricesResponse {
  source?: string;
  imported: number;
  skipped: number;
}

export interface ApiKeyAlias {
  apiKeyHash: string;
  alias: string;
  updatedAtMs?: number;
}

export interface ApiKeyAliasesResponse {
  items: ApiKeyAlias[];
}

export interface UsageImportResponse {
  format?: string;
  added: number;
  skipped: number;
  total: number;
  failed: number;
  unsupported?: number;
  warnings?: string[];
}

export interface UsageExportResponse {
  blob: Blob;
  filename: string;
}

export interface DashboardSummaryWindow {
  today_start_ms: number;
  now_ms: number;
  rolling_30m_start_ms: number;
}

export interface DashboardTodaySummary {
  total_calls: number;
  success_calls: number;
  failure_calls: number;
  success_rate: number;
  input_tokens: number;
  output_tokens: number;
  cached_tokens: number;
  reasoning_tokens: number;
  total_tokens: number;
  total_cost: number;
  average_latency_ms: number | null;
  zero_token_calls: number;
}

export interface DashboardRollingSummary {
  rpm: number;
  tpm: number;
  total_calls: number;
  total_tokens: number;
}

export interface DashboardTopModel {
  model: string;
  calls: number;
  tokens: number;
  cost: number;
  success_rate: number;
}

export interface DashboardTrafficPoint {
  bucket_ms: number;
  calls: number;
  tokens: number;
  success: number;
  failure: number;
  calls_share: number;
  tokens_share: number;
  failure_rate: number;
}

export interface DashboardHourlyActivityPoint {
  hour_index: number;
  bucket_ms: number;
  calls: number;
  tokens: number;
  intensity: number;
}

export interface DashboardTokenMixSegment {
  key: 'input' | 'output' | 'reasoning' | 'cached' | string;
  tokens: number;
  share: number;
}

export interface DashboardModelCostRank {
  model: string;
  calls: number;
  tokens: number;
  cost: number;
  success_rate: number;
  cost_share: number;
}

export interface DashboardChannelHealth {
  auth_index: string;
  calls: number;
  failures: number;
  failure_rate: number;
  success_rate: number;
  tokens: number;
  cost: number;
  average_latency_ms: number | null;
  tone: 'good' | 'warn' | 'bad' | string;
}

export interface DashboardFailureSource {
  source_hash: string;
  auth_index: string;
  calls: number;
  failures: number;
  failure_rate: number;
  last_seen_ms: number;
  average_latency_ms: number | null;
  tone: 'good' | 'warn' | 'bad' | string;
}

export interface DashboardRecentFailure {
  timestamp_ms: number;
  model: string;
  api_key_hash: string;
  source_hash: string;
  auth_index: string;
  endpoint: string;
  duration_ms: number | null;
}

export interface DashboardSummaryResponse {
  generated_at_ms: number;
  window: DashboardSummaryWindow;
  today: DashboardTodaySummary;
  rolling_30m: DashboardRollingSummary;
  top_models_today: DashboardTopModel[];
  model_cost_rank?: DashboardModelCostRank[];
  traffic_timeline?: DashboardTrafficPoint[];
  hourly_activity?: DashboardHourlyActivityPoint[];
  token_mix?: DashboardTokenMixSegment[];
  channel_health?: DashboardChannelHealth[];
  failure_sources?: DashboardFailureSource[];
  recent_failures: DashboardRecentFailure[];
}

export interface DashboardSummaryParams {
  todayStartMs: number;
  nowMs?: number;
  topModels?: number;
  recentFailures?: number;
}

export interface MonitoringAnalyticsFilters {
  models?: string[];
  auth_indices?: string[];
  api_key_hashes?: string[];
  source_hashes?: string[];
  include_failed?: boolean;
  failed_only?: boolean;
  exclude_zero_token?: boolean;
}

export interface MonitoringAnalyticsEventsPageRequest {
  limit?: number;
  before_ms?: number | null;
}

export interface MonitoringAnalyticsInclude {
  summary?: boolean;
  timeline?: boolean;
  hourly_distribution?: boolean;
  model_share?: boolean;
  channel_share?: boolean;
  model_stats?: boolean;
  failure_sources?: boolean;
  task_buckets?: boolean;
  recent_failures?: number;
  events_page?: MonitoringAnalyticsEventsPageRequest;
  granularity?: 'hour' | 'day' | string;
}

export interface MonitoringAnalyticsRequest {
  from_ms: number;
  to_ms: number;
  now_ms?: number;
  search_query?: string;
  search_api_key_hash?: string;
  filters?: MonitoringAnalyticsFilters;
  include?: MonitoringAnalyticsInclude;
}

export interface MonitoringAnalyticsSummary {
  total_calls: number;
  success_calls: number;
  failure_calls: number;
  success_rate: number;
  input_tokens: number;
  output_tokens: number;
  cached_tokens: number;
  reasoning_tokens: number;
  total_tokens: number;
  total_cost: number;
  average_latency_ms: number | null;
  zero_token_calls: number;
  rpm_30m: number;
  tpm_30m: number;
  avg_daily_requests: number;
  avg_daily_tokens: number;
  approx_tasks: number;
  approx_task_failures: number;
  approx_task_success_rate: number;
  zero_token_models: string[];
}

export interface MonitoringAnalyticsTimelinePoint {
  bucket_ms: number;
  label: string;
  calls: number;
  tokens: number;
  success: number;
  failure: number;
}

export interface MonitoringAnalyticsHourlyPoint {
  hour: number;
  calls: number;
  tokens: number;
}

export interface MonitoringAnalyticsModelShareRow {
  model: string;
  calls: number;
  tokens: number;
  cost: number;
}

export interface MonitoringAnalyticsModelStat {
  model: string;
  calls: number;
  success_calls: number;
  failure_calls: number;
  success_rate: number;
  input_tokens: number;
  output_tokens: number;
  cached_tokens: number;
  total_tokens: number;
  cost: number;
}

export interface MonitoringAnalyticsChannelShareRow {
  auth_index: string;
  calls: number;
  success: number;
  failure: number;
  tokens: number;
  cost: number;
  average_latency_ms: number | null;
}

export interface MonitoringAnalyticsFailureSourceRow {
  source_hash: string;
  auth_index: string;
  calls: number;
  failure: number;
  last_seen_ms: number;
  average_latency_ms: number | null;
}

export interface MonitoringAnalyticsTaskBucketRow {
  bucket_key: string;
  total: number;
  success: number;
  failure: number;
  first_ms: number;
  last_ms: number;
  source: string;
  source_hash: string;
  auth_index: string;
  models: string[];
  endpoints: string[];
  input_tokens: number;
  output_tokens: number;
  cached_tokens: number;
  total_tokens: number;
  average_latency_ms: number | null;
  max_latency_ms: number | null;
}

export interface MonitoringAnalyticsRecentFailure {
  timestamp_ms: number;
  model: string;
  api_key_hash: string;
  source_hash: string;
  auth_index: string;
  endpoint: string;
  duration_ms: number | null;
}

export interface MonitoringAnalyticsEventRow {
  event_hash: string;
  timestamp_ms: number;
  model: string;
  endpoint: string;
  method: string;
  path: string;
  auth_index: string;
  source: string;
  source_hash: string;
  api_key_hash: string;
  account_snapshot: string;
  auth_label_snapshot: string;
  auth_provider_snapshot: string;
  input_tokens: number;
  output_tokens: number;
  cached_tokens: number;
  reasoning_tokens: number;
  total_tokens: number;
  latency_ms: number | null;
  failed: boolean;
}

export interface MonitoringAnalyticsEventsResponse {
  items: MonitoringAnalyticsEventRow[];
  next_before_ms: number;
  has_more: boolean;
}

export interface MonitoringAnalyticsResponse {
  generated_at_ms: number;
  granularity: 'hour' | 'day' | string;
  summary?: MonitoringAnalyticsSummary;
  timeline?: MonitoringAnalyticsTimelinePoint[];
  hourly_distribution?: MonitoringAnalyticsHourlyPoint[];
  model_share?: MonitoringAnalyticsModelShareRow[];
  model_stats?: MonitoringAnalyticsModelStat[];
  channel_share?: MonitoringAnalyticsChannelShareRow[];
  failure_sources?: MonitoringAnalyticsFailureSourceRow[];
  task_buckets?: MonitoringAnalyticsTaskBucketRow[];
  recent_failures?: MonitoringAnalyticsRecentFailure[];
  events?: MonitoringAnalyticsEventsResponse;
}

const USAGE_SERVICE_TIMEOUT_MS = 15 * 1000;
const USAGE_SERVICE_TRANSFER_TIMEOUT_MS = 60 * 1000;
export const USAGE_SERVICE_ID = 'cpa-manager-plus';
export const LEGACY_USAGE_SERVICE_ID = 'cpa-manager';
export const LEGACY_USAGE_SERVICE_IDS = [LEGACY_USAGE_SERVICE_ID, 'cpa-usage-service'] as const;
export const USAGE_SERVICE_LAST_CPA_BASE_KEY = 'cpa-manager-plus:last-cpa-base';
export const LEGACY_USAGE_SERVICE_LAST_CPA_BASE_KEY = 'cpa-manager:last-cpa-base';
export const LEGACY_USAGE_SERVICE_LAST_CPA_BASE_KEYS = [
  LEGACY_USAGE_SERVICE_LAST_CPA_BASE_KEY,
  'cpa-usage-service:last-cpa-base',
] as const;

export const isUsageServiceId = (service?: string): boolean =>
  service === USAGE_SERVICE_ID ||
  (typeof service === 'string' &&
    (LEGACY_USAGE_SERVICE_IDS as readonly string[]).includes(service));

export const normalizeUsageServiceBase = (input: string): string => normalizeApiBase(input);

const buildUrl = (base: string, path: string): string => {
  const normalized = normalizeUsageServiceBase(base).replace(/\/+$/, '');
  return `${normalized}${path}`;
};

const authHeaders = (managementKey?: string) =>
  managementKey ? { Authorization: `Bearer ${managementKey}` } : undefined;

const isRecord = (value: unknown): value is Record<string, unknown> =>
  value !== null && typeof value === 'object';

const readUsageServiceErrorCode = (value: unknown): string => {
  if (!isRecord(value) || typeof value.code !== 'string') return '';
  return USAGE_SERVICE_ERROR_CODES.has(value.code) ? value.code : '';
};

const fallbackUsageServiceCodeByStatus = (status?: number): string => {
  switch (status) {
    case 401:
      return 'invalid_management_key';
    case 405:
      return 'method_not_allowed';
    case 412:
      return 'usage_service_not_configured';
    default:
      return '';
  }
};

export const getUsageServiceErrorCode = (error: unknown): string => {
  if (axios.isAxiosError(error)) {
    return (
      readUsageServiceErrorCode(error.response?.data) ||
      fallbackUsageServiceCodeByStatus(error.response?.status)
    );
  }

  if (!isRecord(error)) return '';
  const code = typeof error.code === 'string' ? error.code : '';
  if (USAGE_SERVICE_ERROR_CODES.has(code)) return code;
  return readUsageServiceErrorCode(error.data) || readUsageServiceErrorCode(error.details);
};

const readUsageServiceErrorMessage = (value: unknown): string => {
  if (!isRecord(value)) return '';
  if (typeof value.error === 'string') return value.error;
  if (typeof value.message === 'string') return value.message;
  return '';
};

const toUsageServiceApiError = (error: unknown): UsageServiceApiError => {
  if (axios.isAxiosError(error)) {
    const data = error.response?.data;
    const message =
      readUsageServiceErrorMessage(data) || error.message || 'Usage Service request failed';
    const apiError = new Error(message) as UsageServiceApiError;
    apiError.name = 'UsageServiceApiError';
    apiError.status = error.response?.status;
    apiError.code = getUsageServiceErrorCode(error) || error.code;
    apiError.details = data;
    apiError.data = data;
    return apiError;
  }

  if (error instanceof Error) return error as UsageServiceApiError;
  const fallback = new Error(
    typeof error === 'string' ? error : 'Usage Service request failed'
  ) as UsageServiceApiError;
  fallback.name = 'UsageServiceApiError';
  return fallback;
};

const withUsageServiceError = async <T>(operation: () => Promise<T>): Promise<T> => {
  try {
    return await operation();
  } catch (error) {
    throw toUsageServiceApiError(error);
  }
};

const readHeader = (headers: unknown, name: string): string => {
  if (!headers || typeof headers !== 'object') return '';
  const getter = (headers as { get?: (key: string) => unknown }).get;
  if (typeof getter === 'function') {
    const value = getter.call(headers, name);
    return value === undefined || value === null ? '' : String(value);
  }
  const target = name.toLowerCase();
  const entries = Object.entries(headers as Record<string, unknown>);
  const match = entries.find(([key]) => key.toLowerCase() === target);
  return match?.[1] === undefined || match?.[1] === null ? '' : String(match[1]);
};

const parseContentDispositionFilename = (value: string): string => {
  const utf8Match = value.match(/filename\*=UTF-8''([^;]+)/i);
  if (utf8Match?.[1]) {
    try {
      return decodeURIComponent(utf8Match[1].trim());
    } catch {
      return utf8Match[1].trim();
    }
  }
  const quotedMatch = value.match(/filename="([^"]+)"/i);
  if (quotedMatch?.[1]) return quotedMatch[1].trim();
  const plainMatch = value.match(/filename=([^;]+)/i);
  return plainMatch?.[1]?.trim() || '';
};

export const usageServiceApi = {
  getInfo: async (base: string): Promise<UsageServiceInfo> => {
    return withUsageServiceError(async () => {
      const response = await axios.get<UsageServiceInfo>(buildUrl(base, '/usage-service/info'), {
        timeout: USAGE_SERVICE_TIMEOUT_MS,
      });
      return response.data;
    });
  },

  setup: async (base: string, payload: UsageServiceSetupRequest): Promise<void> => {
    await withUsageServiceError(async () => {
      await axios.post(buildUrl(base, '/setup'), payload, {
        timeout: USAGE_SERVICE_TIMEOUT_MS,
      });
    });
  },

  getManagerConfig: async (
    base: string,
    managementKey?: string
  ): Promise<ManagerConfigResponse> => {
    return withUsageServiceError(async () => {
      const response = await axios.get<ManagerConfigResponse>(
        buildUrl(base, '/usage-service/config'),
        {
          timeout: USAGE_SERVICE_TIMEOUT_MS,
          headers: authHeaders(managementKey),
        }
      );
      return response.data;
    });
  },

  saveManagerConfig: async (
    base: string,
    config: ManagerConfig,
    managementKey?: string
  ): Promise<ManagerConfigResponse> => {
    return withUsageServiceError(async () => {
      const response = await axios.put<ManagerConfigResponse>(
        buildUrl(base, '/usage-service/config'),
        { config },
        {
          timeout: USAGE_SERVICE_TIMEOUT_MS,
          headers: authHeaders(managementKey),
        }
      );
      return response.data;
    });
  },

  getStatus: async (base: string, managementKey?: string): Promise<UsageServiceStatus> => {
    return withUsageServiceError(async () => {
      const response = await axios.get<UsageServiceStatus>(buildUrl(base, '/status'), {
        timeout: USAGE_SERVICE_TIMEOUT_MS,
        headers: authHeaders(managementKey),
      });
      return response.data;
    });
  },

  getUsage: async (base: string, managementKey?: string): Promise<UsagePayload> => {
    return withUsageServiceError(async () => {
      const response = await axios.get<UsagePayload>(buildUrl(base, '/v0/management/usage'), {
        timeout: USAGE_SERVICE_TIMEOUT_MS,
        headers: authHeaders(managementKey),
      });
      return response.data;
    });
  },

  getModelPrices: async (base: string, managementKey?: string): Promise<ModelPricesResponse> => {
    return withUsageServiceError(async () => {
      const response = await axios.get<ModelPricesResponse>(
        buildUrl(base, '/v0/management/model-prices'),
        {
          timeout: USAGE_SERVICE_TIMEOUT_MS,
          headers: authHeaders(managementKey),
        }
      );
      return response.data;
    });
  },

  saveModelPrices: async (
    base: string,
    prices: Record<string, ModelPrice>,
    managementKey?: string
  ): Promise<ModelPricesResponse> => {
    return withUsageServiceError(async () => {
      const response = await axios.put<ModelPricesResponse>(
        buildUrl(base, '/v0/management/model-prices'),
        { prices },
        {
          timeout: USAGE_SERVICE_TIMEOUT_MS,
          headers: authHeaders(managementKey),
        }
      );
      return response.data;
    });
  },

  getApiKeyAliases: async (
    base: string,
    managementKey?: string
  ): Promise<ApiKeyAliasesResponse> => {
    return withUsageServiceError(async () => {
      const response = await axios.get<ApiKeyAliasesResponse>(
        buildUrl(base, '/v0/management/api-key-aliases'),
        {
          timeout: USAGE_SERVICE_TIMEOUT_MS,
          headers: authHeaders(managementKey),
        }
      );
      return response.data;
    });
  },

  saveApiKeyAliases: async (
    base: string,
    items: ApiKeyAlias[],
    managementKey?: string
  ): Promise<ApiKeyAliasesResponse> => {
    return withUsageServiceError(async () => {
      const response = await axios.put<ApiKeyAliasesResponse>(
        buildUrl(base, '/v0/management/api-key-aliases'),
        { items },
        {
          timeout: USAGE_SERVICE_TIMEOUT_MS,
          headers: authHeaders(managementKey),
        }
      );
      return response.data;
    });
  },

  deleteApiKeyAlias: async (
    base: string,
    apiKeyHash: string,
    managementKey?: string
  ): Promise<void> => {
    await withUsageServiceError(async () => {
      await axios.delete(
        buildUrl(base, `/v0/management/api-key-aliases/${encodeURIComponent(apiKeyHash)}`),
        {
          timeout: USAGE_SERVICE_TIMEOUT_MS,
          headers: authHeaders(managementKey),
        }
      );
    });
  },

  syncModelPrices: async (
    base: string,
    managementKey?: string,
    models?: string[]
  ): Promise<ModelPriceSyncResponse> => {
    return withUsageServiceError(async () => {
      const response = await axios.post<ModelPriceSyncResponse>(
        buildUrl(base, '/v0/management/model-prices/sync'),
        models ? { models } : {},
        {
          timeout: 30 * 1000,
          headers: authHeaders(managementKey),
        }
      );
      return response.data;
    });
  },

  exportUsage: async (base: string, managementKey?: string): Promise<UsageExportResponse> => {
    return withUsageServiceError(async () => {
      const response = await axios.get<Blob>(buildUrl(base, '/v0/management/usage/export'), {
        timeout: USAGE_SERVICE_TRANSFER_TIMEOUT_MS,
        headers: authHeaders(managementKey),
        responseType: 'blob',
      });
      const contentDisposition = readHeader(response.headers, 'content-disposition');
      return {
        blob: response.data,
        filename: parseContentDispositionFilename(contentDisposition) || 'usage-events.jsonl',
      };
    });
  },

  importUsage: async (
    base: string,
    payload: Blob | string,
    managementKey?: string
  ): Promise<UsageImportResponse> => {
    return withUsageServiceError(async () => {
      const response = await axios.post<UsageImportResponse>(
        buildUrl(base, '/v0/management/usage/import'),
        payload,
        {
          timeout: USAGE_SERVICE_TRANSFER_TIMEOUT_MS,
          headers: authHeaders(managementKey),
        }
      );
      return response.data;
    });
  },
};

export const dashboardApi = {
  getSummary: async (
    base: string,
    managementKey: string | undefined,
    params: DashboardSummaryParams
  ): Promise<DashboardSummaryResponse> => {
    return withUsageServiceError(async () => {
      const query: Record<string, number> = {
        today_start_ms: params.todayStartMs,
      };
      if (params.nowMs !== undefined) query.now_ms = params.nowMs;
      if (params.topModels !== undefined) query.top_models = params.topModels;
      if (params.recentFailures !== undefined) query.recent_failures = params.recentFailures;

      const response = await axios.get<DashboardSummaryResponse>(
        buildUrl(base, '/v0/management/dashboard/summary'),
        {
          timeout: USAGE_SERVICE_TIMEOUT_MS,
          headers: authHeaders(managementKey),
          params: query,
        }
      );
      return response.data;
    });
  },
};

export const monitoringAnalyticsApi = {
  getAnalytics: async (
    base: string,
    managementKey: string | undefined,
    request: MonitoringAnalyticsRequest
  ): Promise<MonitoringAnalyticsResponse> => {
    return withUsageServiceError(async () => {
      const response = await axios.post<MonitoringAnalyticsResponse>(
        buildUrl(base, '/v0/management/monitoring/analytics'),
        request,
        {
          timeout: USAGE_SERVICE_TIMEOUT_MS,
          headers: authHeaders(managementKey),
        }
      );
      return response.data;
    });
  },
};
