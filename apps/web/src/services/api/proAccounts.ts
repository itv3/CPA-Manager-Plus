import axios from 'axios';
import { normalizeUsageServiceBase } from './usageService';

export interface ProAccountBinding {
  id: number;
  proAccountId: string;
  authIndex?: string;
  sourceType: string;
  sourceLocator: string;
  sourceFingerprint?: string;
  bindingStatus: string;
  isCurrent: boolean;
  validFromMs: number;
  validToMs?: number;
  firstSeenAtMs: number;
  lastSeenAtMs: number;
}

export interface ProAccount {
  id: string;
  platform: string;
  authType: string;
  sourceType: string;
  name?: string;
  email?: string;
  enabled: boolean;
  healthStatus: string;
  lastError?: string;
  allowedModels: string[];
  modelMapping: Record<string, string>;
  lastUsedAtMs?: number;
  lastTestedAtMs?: number;
  expiresAtMs?: number;
  createdAtMs: number;
  updatedAtMs: number;
  binding?: ProAccountBinding;
}

export interface ProAccountListResponse {
  items: ProAccount[];
  nextCursor?: string;
  total: number;
}

export interface ProAccountSyncItem {
  resolution: string;
  proAccountId?: string;
  sourceLocator: string;
  authIndex?: string;
  candidateIds?: string[];
  reasonCode?: string;
}

export interface ProAccountSyncResponse {
  dryRun: boolean;
  discovered: number;
  created: number;
  updated: number;
  pending: number;
  conflicts: number;
  items: ProAccountSyncItem[];
}

export interface ProAccountListParams {
  cursor?: string;
  limit?: number;
  search?: string;
  platform?: string;
  authType?: string;
  enabled?: boolean;
  healthStatus?: string;
}

export interface ProAccountUsageWindow {
  id: string;
  label: string;
  usedPercent?: number;
  remainingPercent?: number;
  resetAtMs?: number;
  source: string;
}

export interface ProAccountLocalUsage {
  fromMs: number;
  toMs: number;
  requests: number;
  successes: number;
  failures: number;
  inputTokens: number;
  outputTokens: number;
  cachedTokens: number;
  cacheReadTokens: number;
  cacheCreationTokens: number;
  reasoningTokens: number;
  totalTokens: number;
  estimatedCost?: number;
  costKnown: boolean;
  lastActivityAtMs?: number;
}

export interface ProAccountUsageResponse {
  source: string;
  updatedAtMs: number;
  officialWindows: ProAccountUsageWindow[];
  local: ProAccountLocalUsage;
  errorCode?: string;
  errorMessage?: string;
  retryable: boolean;
}

const buildURL = (base: string, path: string, params?: URLSearchParams) => {
  const normalized = normalizeUsageServiceBase(base);
  return `${normalized}${path}${params && params.toString() ? `?${params.toString()}` : ''}`;
};

const requestConfig = (managementKey: string) => ({
  headers: { Authorization: `Bearer ${managementKey}` },
});

const handleError = (error: unknown): never => {
  if (axios.isAxiosError(error)) {
    const data = error.response?.data as { message?: string; error?: string } | undefined;
    throw new Error(data?.message || data?.error || error.message || '请求失败');
  }
  throw error instanceof Error ? error : new Error(String(error));
};

export const proAccountsApi = {
  async list(base: string, managementKey: string, params: ProAccountListParams = {}) {
    const query = new URLSearchParams();
    if (params.cursor) query.set('cursor', params.cursor);
    if (params.limit) query.set('limit', String(params.limit));
    if (params.search?.trim()) query.set('search', params.search.trim());
    if (params.platform) query.set('platform', params.platform);
    if (params.authType) query.set('auth_type', params.authType);
    if (params.enabled !== undefined) query.set('enabled', String(params.enabled));
    if (params.healthStatus) query.set('health_status', params.healthStatus);
    try {
      const response = await axios.get<ProAccountListResponse>(
        buildURL(base, '/v0/pro/accounts', query),
        requestConfig(managementKey)
      );
      return response.data;
    } catch (error) {
      return handleError(error);
    }
  },

  async sync(base: string, managementKey: string, dryRun = false) {
    try {
      const response = await axios.post<ProAccountSyncResponse>(
        buildURL(base, '/v0/pro/accounts/sync'),
        { dry_run: dryRun },
        requestConfig(managementKey)
      );
      return response.data;
    } catch (error) {
      return handleError(error);
    }
  },

  async usage(
    base: string,
    managementKey: string,
    id: string,
    source: 'passive' | 'active' = 'passive',
    force = false
  ) {
    const query = new URLSearchParams({ source, force: String(force) });
    try {
      const response = await axios.get<ProAccountUsageResponse>(
        buildURL(base, `/v0/pro/accounts/${encodeURIComponent(id)}/usage`, query),
        requestConfig(managementKey)
      );
      return response.data;
    } catch (error) {
      return handleError(error);
    }
  },
};
