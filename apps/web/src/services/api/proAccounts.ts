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
  attributionQuality: 'exact' | 'retained_history' | 'partial' | 'ambiguous' | 'unknown';
  firstSeenAtMs: number;
  lastSeenAtMs: number;
}

export interface ProAccount {
  id: string;
  platform: string;
  authType: string;
  sourceType: string;
  planType?: string;
  name?: string;
  email?: string;
  enabled: boolean;
  healthStatus: string;
  lastError?: string;
  allowedModels: string[];
  modelMapping: Record<string, string>;
  modelRuleVersion?: string;
  lastUsedAtMs?: number;
  lastTestedAtMs?: number;
  expiresAtMs?: number;
  createdAtMs: number;
  updatedAtMs: number;
  version: number;
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
  planType?: string;
  officialWindows: ProAccountUsageWindow[];
  local: ProAccountLocalUsage;
  errorCode?: string;
  errorMessage?: string;
  retryable: boolean;
}

export interface ProAccountOperation {
  operationId: string;
  idempotencyKey: string;
  operationType: string;
  proAccountId?: string;
  state: string;
  version: number;
  retryCount: number;
  cleanupDeadlineMs: number;
  compensationAction?: string;
  errorCode?: string;
  errorSummary?: string;
  createdAtMs: number;
  updatedAtMs: number;
}

export interface ProAccountProtocolResult {
  status: 'supported' | 'unsupported' | 'unknown' | '';
  statusCode?: number;
  errorCode?: string;
  errorMessage?: string;
  retryable: boolean;
}

export interface ProAccountProbeResult {
  platform: string;
  selectedProtocol?: 'responses' | 'chat_completions';
  sourceType?: string;
  testModel?: string;
  models: string[];
  upstreamModels: string[];
  builtInModels: string[];
  manualModels: string[];
  modelsStatus: 'supported' | 'unsupported' | 'unknown';
  warnings: string[];
  responses: ProAccountProtocolResult;
  chatCompletions: ProAccountProtocolResult;
  basicConnectivity: ProAccountProtocolResult;
  errorCode?: string;
  errorMessage?: string;
  retryable: boolean;
}

export interface ProAccountModelCatalog {
  models: string[];
  upstream: string[];
  builtIn: string[];
  manual: string[];
  upstreamStatus: 'supported' | 'unknown';
  warnings: string[];
}

export interface ProAccountConnectivityResult {
  success: boolean;
  statusCode?: number;
  protocol: string;
  model: string;
  mappedModel?: string;
  upstreamModel?: string;
  durationMs?: number;
  responsePreview?: string;
  errorCode?: string;
  errorMessage?: string;
  retryable: boolean;
}

export type ProAccountTestMode = 'default' | 'compact';

export interface ProAccountLifecycleResult {
  account: ProAccount;
  operation: ProAccountOperation;
  probe?: ProAccountProbeResult;
  connectivity?: ProAccountConnectivityResult;
  savedDisabled?: boolean;
}

export interface ProAccountEditable {
  baseUrl?: string;
  proxyUrl?: string;
  headers: Record<string, string>;
  sharedProvider: boolean;
}

export interface ProAccountDetailsResponse {
  item: ProAccount;
  editable: ProAccountEditable;
}

export interface ProAccountCapabilitiesResponse {
  credentialDraft: boolean;
  allowedModels: boolean;
  stores: Record<string, 'supported' | 'unsupported' | 'unknown'>;
  platforms?: Record<string, Record<string, ProAccountAuthCapability | undefined> | undefined>;
  accountActions?: Partial<Record<ProAccountActionCapabilityName, ProAccountAuthCapability>>;
}

export type ProAccountActionCapabilityName = 'reauthorize' | 'refreshToken' | 'scheduledTests';

export interface ProAccountAuthCapability {
  status: 'supported' | 'unsupported' | 'unknown';
  reasonCode?: string;
  pluginId?: string;
  provider?: string;
  version?: string;
}

export interface ProAccountScheduledTestPlan {
  id: number;
  proAccountId: string;
  modelId: string;
  cronExpression: string;
  enabled: boolean;
  maxResults: number;
  autoRecover: boolean;
  lastRunAtMs?: number;
  nextRunAtMs?: number;
  createdAtMs: number;
  updatedAtMs: number;
}

export interface ProAccountScheduledTestResult {
  id: number;
  planId: number;
  status: 'success' | 'running' | 'failed' | string;
  responseText?: string;
  errorMessage?: string;
  latencyMs?: number;
  startedAtMs: number;
  finishedAtMs?: number;
  createdAtMs: number;
}

export interface ProAccountScheduledTestPlanInput {
  modelId: string;
  cronExpression: string;
  enabled: boolean;
  maxResults: number;
  autoRecover: boolean;
}

export interface ProAccountScheduledTestPlanListResponse {
  items: ProAccountScheduledTestPlan[];
}

export interface ProAccountScheduledTestResultListResponse {
  items: ProAccountScheduledTestResult[];
}

export interface ProAccountOAuthResult {
  operation: ProAccountOperation;
  oauth?: { url: string; state: string };
  status?: 'wait' | 'credential_pending' | 'ok' | 'ambiguous' | 'error' | 'cancelled';
  account?: ProAccount;
}

export type ProAccountBatchAction = 'enable' | 'disable' | 'test' | 'delete';

export interface ProAccountBatchItemInput {
  account: ProAccount;
  model?: string;
}

export interface ProAccountItemResult {
  proAccountId: string;
  success: boolean;
  code?: string;
  message?: string;
  retryable: boolean;
  account?: ProAccount;
  connectivity?: ProAccountConnectivityResult;
}

export interface ProAccountBatchResult {
  action: ProAccountBatchAction;
  total: number;
  succeeded: number;
  failed: number;
  items: ProAccountItemResult[];
}

export interface ProAccountBindingReview {
  id: number;
  discoveryKey: string;
  sourceType: string;
  sourceLocator: string;
  authIndex?: string;
  resolutionStatus: string;
  candidateIds: string[];
  reasonCode: string;
  driftType: 'file_path' | 'api_credential';
  firstSeenAtMs: number;
  lastSeenAtMs: number;
}

export interface ProAccountBindingReviewItem {
  review: ProAccountBindingReview;
  candidates: ProAccount[];
}

export interface ProAccountBindingReviewsResponse {
  items: ProAccountBindingReviewItem[];
  total: number;
}

export interface ProAccountRebindItemInput {
  reviewId: number;
  account: ProAccount;
}

export interface ProAccountRebindItemResult extends ProAccountItemResult {
  reviewId: number;
}

export interface ProAccountRebindResult {
  total: number;
  succeeded: number;
  failed: number;
  items: ProAccountRebindItemResult[];
}

export interface ProAccountResetCredit {
  id?: string;
  expiresAtMs?: number;
}

export interface ProAccountResetCreditsResult {
  capability: 'supported' | 'unsupported' | 'unknown';
  availableCount?: number;
  credits: ProAccountResetCredit[];
  updatedAtMs: number;
  errorCode?: string;
  retryable: boolean;
}

export interface ProAccountResetResult {
  credits: ProAccountResetCreditsResult;
  operation: ProAccountOperation;
}

export interface ProAccountProbeInput {
  operationId: string;
  idempotencyKey: string;
  platform: string;
  baseUrl: string;
  apiKey: string;
  proxyUrl?: string;
  protocolMode: string;
  model?: string;
  allowedModels: string[];
  modelMapping: Record<string, string>;
  headers: Record<string, string>;
}

export interface ProAccountCreateAPIInput extends ProAccountProbeInput {
  name?: string;
  testModel: string;
  saveDisabledOnTestFailure: boolean;
  draftOnly?: boolean;
  skipTest?: boolean;
}

export interface ProAccountCreateVertexInput {
  operationId: string;
  idempotencyKey: string;
  file: File;
  location: string;
  allowedModels: string[];
  modelMapping: Record<string, string>;
  testModel: string;
  saveDisabledOnTestFailure: boolean;
  draftOnly?: boolean;
}

export interface ProAccountUpdateInput {
  operationId: string;
  idempotencyKey: string;
  expectedVersion: number;
  name?: string;
  baseUrl?: string;
  apiKey?: string;
  proxyUrl?: string;
  protocolMode?: string;
  headers?: Record<string, string>;
  allowedModels: string[];
  modelMapping: Record<string, string>;
  testModel?: string;
}

export class ProAccountsApiError extends Error {
  code: string;
  retryable: boolean;
  details?: unknown;
  status?: number;

  constructor(
    message: string,
    code = 'request_failed',
    retryable = false,
    details?: unknown,
    status?: number
  ) {
    super(message);
    this.name = 'ProAccountsApiError';
    this.code = code;
    this.retryable = retryable;
    this.details = details;
    this.status = status;
  }
}

const buildURL = (base: string, path: string, params?: URLSearchParams) => {
  const normalized = normalizeUsageServiceBase(base);
  return `${normalized}${path}${params && params.toString() ? `?${params.toString()}` : ''}`;
};

const requestConfig = (managementKey: string, idempotencyKey?: string) => ({
  headers: {
    Authorization: `Bearer ${managementKey}`,
    ...(idempotencyKey ? { 'Idempotency-Key': idempotencyKey } : {}),
  },
});

const handleError = (error: unknown): never => {
  if (axios.isAxiosError(error)) {
    const data = error.response?.data as
      | {
          code?: string;
          message?: string;
          error?: string;
          retryable?: boolean;
          details?: unknown;
        }
      | undefined;
    throw new ProAccountsApiError(
      data?.message || data?.error || error.message || '请求失败',
      data?.code,
      Boolean(data?.retryable),
      data?.details,
      error.response?.status
    );
  }
  throw error instanceof Error ? error : new Error(String(error));
};

const mutationBody = (operationId: string, idempotencyKey: string, expectedVersion: number) => ({
  operation_id: operationId,
  idempotency_key: idempotencyKey,
  expected_version: expectedVersion,
});

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

  async capabilities(base: string, managementKey: string) {
    try {
      const response = await axios.get<ProAccountCapabilitiesResponse>(
        buildURL(base, '/v0/pro/accounts/capabilities'),
        requestConfig(managementKey)
      );
      return response.data;
    } catch (error) {
      return handleError(error);
    }
  },

  async details(base: string, managementKey: string, id: string) {
    try {
      const response = await axios.get<ProAccountDetailsResponse>(
        buildURL(base, `/v0/pro/accounts/${encodeURIComponent(id)}`),
        requestConfig(managementKey)
      );
      return response.data;
    } catch (error) {
      return handleError(error);
    }
  },

  async modelCatalog(base: string, managementKey: string, id: string) {
    try {
      const response = await axios.get<ProAccountModelCatalog>(
        buildURL(base, `/v0/pro/accounts/${encodeURIComponent(id)}/models`),
        requestConfig(managementKey)
      );
      return response.data;
    } catch (error) {
      return handleError(error);
    }
  },

  async staticModelCatalog(
    base: string,
    managementKey: string,
    platform: string,
    authType: string
  ) {
    const query = new URLSearchParams({ platform, auth_type: authType });
    try {
      const response = await axios.get<ProAccountModelCatalog>(
        buildURL(base, '/v0/pro/accounts/model-catalog', query),
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

  async probe(base: string, managementKey: string, input: ProAccountProbeInput) {
    try {
      const response = await axios.post<{
        probe: ProAccountProbeResult;
        operation: ProAccountOperation;
      }>(
        buildURL(base, '/v0/pro/accounts/probe'),
        {
          operation_id: input.operationId,
          idempotency_key: input.idempotencyKey,
          platform: input.platform,
          auth_type: 'api',
          base_url: input.baseUrl,
          api_key: input.apiKey,
          proxy_url: input.proxyUrl,
          protocol_mode: input.protocolMode,
          model: input.model,
          allowed_models: input.allowedModels,
          model_mapping: input.modelMapping,
          headers: input.headers,
        },
        requestConfig(managementKey, input.idempotencyKey)
      );
      return response.data;
    } catch (error) {
      return handleError(error);
    }
  },

  async createAPI(base: string, managementKey: string, input: ProAccountCreateAPIInput) {
    try {
      const response = await axios.post<ProAccountLifecycleResult>(
        buildURL(base, '/v0/pro/accounts'),
        {
          operation_id: input.operationId,
          idempotency_key: input.idempotencyKey,
          platform: input.platform,
          auth_type: 'api',
          name: input.name,
          base_url: input.baseUrl,
          api_key: input.apiKey,
          proxy_url: input.proxyUrl,
          protocol_mode: input.protocolMode,
          headers: input.headers,
          allowed_models: input.allowedModels,
          model_mapping: input.modelMapping,
          test_model: input.testModel,
          save_disabled_on_test_failure: input.saveDisabledOnTestFailure,
          draft_only: Boolean(input.draftOnly),
          skip_test: Boolean(input.skipTest),
        },
        requestConfig(managementKey, input.idempotencyKey)
      );
      return response.data;
    } catch (error) {
      return handleError(error);
    }
  },

  async createVertex(base: string, managementKey: string, input: ProAccountCreateVertexInput) {
    const form = new FormData();
    form.append('operation_id', input.operationId);
    form.append('idempotency_key', input.idempotencyKey);
    form.append('file', input.file);
    form.append('location', input.location);
    form.append('allowed_models', JSON.stringify(input.allowedModels));
    form.append('model_mapping', JSON.stringify(input.modelMapping));
    form.append('test_model', input.testModel);
    form.append('save_disabled_on_test_failure', String(input.saveDisabledOnTestFailure));
    form.append('draft_only', String(Boolean(input.draftOnly)));
    try {
      const response = await axios.post<ProAccountLifecycleResult>(
        buildURL(base, '/v0/pro/accounts/vertex'),
        form,
        requestConfig(managementKey, input.idempotencyKey)
      );
      return response.data;
    } catch (error) {
      return handleError(error);
    }
  },

  async startOAuth(
    base: string,
    managementKey: string,
    input: { operationId: string; idempotencyKey: string; platform: string }
  ) {
    try {
      const response = await axios.post<ProAccountOAuthResult>(
        buildURL(base, '/v0/pro/accounts/oauth/start'),
        {
          operation_id: input.operationId,
          idempotency_key: input.idempotencyKey,
          platform: input.platform,
        },
        requestConfig(managementKey, input.idempotencyKey)
      );
      return response.data;
    } catch (error) {
      return handleError(error);
    }
  },

  async oauthStatus(base: string, managementKey: string, operationId: string) {
    const query = new URLSearchParams({ operation_id: operationId });
    try {
      const response = await axios.get<ProAccountOAuthResult>(
        buildURL(base, '/v0/pro/accounts/oauth/status', query),
        requestConfig(managementKey)
      );
      return response.data;
    } catch (error) {
      return handleError(error);
    }
  },

  async submitOAuthCallback(
    base: string,
    managementKey: string,
    operationId: string,
    callbackInput: string,
    callbackState = ''
  ) {
    try {
      const response = await axios.post<ProAccountOAuthResult>(
        buildURL(base, '/v0/pro/accounts/oauth/callback'),
        {
          operation_id: operationId,
          callback_input: callbackInput,
          callback_state: callbackState,
        },
        requestConfig(managementKey)
      );
      return response.data;
    } catch (error) {
      return handleError(error);
    }
  },

  async cancelOAuth(base: string, managementKey: string, operationId: string) {
    try {
      const response = await axios.post<ProAccountOAuthResult>(
        buildURL(base, '/v0/pro/accounts/oauth/cancel'),
        { operation_id: operationId },
        requestConfig(managementKey)
      );
      return response.data;
    } catch (error) {
      return handleError(error);
    }
  },

  async cancelDraft(base: string, managementKey: string, operationId: string) {
    try {
      const response = await axios.post<ProAccountOAuthResult>(
        buildURL(base, '/v0/pro/accounts/drafts/cancel'),
        { operation_id: operationId },
        requestConfig(managementKey)
      );
      return response.data;
    } catch (error) {
      return handleError(error);
    }
  },

  async completeDraft(
    base: string,
    managementKey: string,
    id: string,
    input: {
      operationId: string;
      expectedVersion: number;
      allowedModels: string[];
      modelMapping: Record<string, string>;
      testModel: string;
      saveDisabledOnTestFailure: boolean;
    }
  ) {
    try {
      const response = await axios.post<ProAccountLifecycleResult>(
        buildURL(base, `/v0/pro/accounts/${encodeURIComponent(id)}/complete`),
        {
          operation_id: input.operationId,
          expected_version: input.expectedVersion,
          allowed_models: input.allowedModels,
          model_mapping: input.modelMapping,
          test_model: input.testModel,
          save_disabled_on_test_failure: input.saveDisabledOnTestFailure,
        },
        requestConfig(managementKey)
      );
      return response.data;
    } catch (error) {
      return handleError(error);
    }
  },

  async batch(
    base: string,
    managementKey: string,
    action: ProAccountBatchAction,
    items: ProAccountBatchItemInput[],
    operationId: string,
    idempotencyKey: string
  ) {
    try {
      const response = await axios.post<ProAccountBatchResult>(
        buildURL(base, '/v0/pro/accounts/batch'),
        {
          operation_id: operationId,
          idempotency_key: idempotencyKey,
          action,
          items: items.map((item) => ({
            pro_account_id: item.account.id,
            expected_version: item.account.version,
            model: item.model,
          })),
        },
        requestConfig(managementKey, idempotencyKey)
      );
      return response.data;
    } catch (error) {
      return handleError(error);
    }
  },

  async bindingReviews(base: string, managementKey: string, limit = 100) {
    const query = new URLSearchParams({ limit: String(limit) });
    try {
      const response = await axios.get<ProAccountBindingReviewsResponse>(
        buildURL(base, '/v0/pro/accounts/binding-reviews', query),
        requestConfig(managementKey)
      );
      return response.data;
    } catch (error) {
      return handleError(error);
    }
  },

  async rebind(
    base: string,
    managementKey: string,
    items: ProAccountRebindItemInput[],
    operationId: string,
    idempotencyKey: string
  ) {
    try {
      const response = await axios.post<ProAccountRebindResult>(
        buildURL(base, '/v0/pro/accounts/rebind'),
        {
          operation_id: operationId,
          idempotency_key: idempotencyKey,
          items: items.map((item) => ({
            review_id: item.reviewId,
            pro_account_id: item.account.id,
            expected_version: item.account.version,
          })),
        },
        requestConfig(managementKey, idempotencyKey)
      );
      return response.data;
    } catch (error) {
      return handleError(error);
    }
  },

  async resetCredits(base: string, managementKey: string, accountId: string) {
    try {
      const response = await axios.get<ProAccountResetCreditsResult>(
        buildURL(base, `/v0/pro/accounts/${encodeURIComponent(accountId)}/openai-reset-credits`),
        requestConfig(managementKey)
      );
      return response.data;
    } catch (error) {
      return handleError(error);
    }
  },

  async resetOpenAI(
    base: string,
    managementKey: string,
    account: ProAccount,
    operationId: string,
    idempotencyKey: string
  ) {
    try {
      const response = await axios.post<ProAccountResetResult>(
        buildURL(base, `/v0/pro/accounts/${encodeURIComponent(account.id)}/openai-reset`),
        {
          ...mutationBody(operationId, idempotencyKey, account.version),
          confirmed: true,
        },
        requestConfig(managementKey, idempotencyKey)
      );
      return response.data;
    } catch (error) {
      return handleError(error);
    }
  },

  async startReauthorization(
    base: string,
    managementKey: string,
    account: ProAccount,
    operationId: string,
    idempotencyKey: string
  ) {
    try {
      const response = await axios.post<ProAccountOAuthResult>(
        buildURL(base, `/v0/pro/accounts/${encodeURIComponent(account.id)}/reauthorize/start`),
        mutationBody(operationId, idempotencyKey, account.version),
        requestConfig(managementKey, idempotencyKey)
      );
      return response.data;
    } catch (error) {
      return handleError(error);
    }
  },

  async reauthorizationStatus(
    base: string,
    managementKey: string,
    accountId: string,
    operationId: string
  ) {
    const query = new URLSearchParams({ operation_id: operationId });
    try {
      const response = await axios.get<ProAccountOAuthResult>(
        buildURL(
          base,
          `/v0/pro/accounts/${encodeURIComponent(accountId)}/reauthorize/status`,
          query
        ),
        requestConfig(managementKey)
      );
      return response.data;
    } catch (error) {
      return handleError(error);
    }
  },

  async submitReauthorizationCallback(
    base: string,
    managementKey: string,
    accountId: string,
    operationId: string,
    callbackInput: string,
    callbackState = ''
  ) {
    try {
      const response = await axios.post<ProAccountOAuthResult>(
        buildURL(base, `/v0/pro/accounts/${encodeURIComponent(accountId)}/reauthorize/callback`),
        {
          operation_id: operationId,
          callback_input: callbackInput,
          callback_state: callbackState,
        },
        requestConfig(managementKey)
      );
      return response.data;
    } catch (error) {
      return handleError(error);
    }
  },

  async cancelReauthorization(
    base: string,
    managementKey: string,
    accountId: string,
    operationId: string
  ) {
    try {
      const response = await axios.post<ProAccountOAuthResult>(
        buildURL(base, `/v0/pro/accounts/${encodeURIComponent(accountId)}/reauthorize/cancel`),
        { operation_id: operationId },
        requestConfig(managementKey)
      );
      return response.data;
    } catch (error) {
      return handleError(error);
    }
  },

  async refreshToken(
    base: string,
    managementKey: string,
    account: ProAccount,
    operationId: string,
    idempotencyKey: string
  ) {
    try {
      const response = await axios.post<ProAccountLifecycleResult>(
        buildURL(base, `/v0/pro/accounts/${encodeURIComponent(account.id)}/refresh-token`),
        mutationBody(operationId, idempotencyKey, account.version),
        requestConfig(managementKey, idempotencyKey)
      );
      return response.data;
    } catch (error) {
      return handleError(error);
    }
  },

  async listScheduledTests(base: string, managementKey: string, accountId: string) {
    try {
      const response = await axios.get<
        ProAccountScheduledTestPlanListResponse | ProAccountScheduledTestPlan[]
      >(
        buildURL(base, `/v0/pro/accounts/${encodeURIComponent(accountId)}/scheduled-test-plans`),
        requestConfig(managementKey)
      );
      return Array.isArray(response.data) ? response.data : (response.data.items ?? []);
    } catch (error) {
      return handleError(error);
    }
  },

  async createScheduledTest(
    base: string,
    managementKey: string,
    account: ProAccount,
    input: ProAccountScheduledTestPlanInput,
    operationId: string,
    idempotencyKey: string
  ) {
    try {
      const response = await axios.post<ProAccountScheduledTestPlan>(
        buildURL(base, `/v0/pro/accounts/${encodeURIComponent(account.id)}/scheduled-test-plans`),
        {
          ...mutationBody(operationId, idempotencyKey, account.version),
          model_id: input.modelId,
          cron_expression: input.cronExpression,
          enabled: input.enabled,
          max_results: input.maxResults,
          auto_recover: input.autoRecover,
        },
        requestConfig(managementKey, idempotencyKey)
      );
      return response.data;
    } catch (error) {
      return handleError(error);
    }
  },

  async updateScheduledTest(
    base: string,
    managementKey: string,
    accountId: string,
    planId: number,
    input: Partial<ProAccountScheduledTestPlanInput>
  ) {
    try {
      const response = await axios.put<ProAccountScheduledTestPlan>(
        buildURL(
          base,
          `/v0/pro/accounts/${encodeURIComponent(accountId)}/scheduled-test-plans/${encodeURIComponent(planId)}`
        ),
        {
          model_id: input.modelId,
          cron_expression: input.cronExpression,
          enabled: input.enabled,
          max_results: input.maxResults,
          auto_recover: input.autoRecover,
        },
        requestConfig(managementKey)
      );
      return response.data;
    } catch (error) {
      return handleError(error);
    }
  },

  async deleteScheduledTest(
    base: string,
    managementKey: string,
    accountId: string,
    planId: number
  ) {
    try {
      await axios.delete(
        buildURL(
          base,
          `/v0/pro/accounts/${encodeURIComponent(accountId)}/scheduled-test-plans/${encodeURIComponent(planId)}`
        ),
        requestConfig(managementKey)
      );
    } catch (error) {
      return handleError(error);
    }
  },

  async listScheduledTestResults(
    base: string,
    managementKey: string,
    accountId: string,
    planId: number,
    limit = 20
  ) {
    const query = new URLSearchParams({ limit: String(limit) });
    try {
      const response = await axios.get<
        ProAccountScheduledTestResultListResponse | ProAccountScheduledTestResult[]
      >(
        buildURL(
          base,
          `/v0/pro/accounts/${encodeURIComponent(accountId)}/scheduled-test-plans/${encodeURIComponent(planId)}/results`,
          query
        ),
        requestConfig(managementKey)
      );
      return Array.isArray(response.data) ? response.data : (response.data.items ?? []);
    } catch (error) {
      return handleError(error);
    }
  },

  async update(base: string, managementKey: string, id: string, input: ProAccountUpdateInput) {
    try {
      const response = await axios.put<ProAccountLifecycleResult>(
        buildURL(base, `/v0/pro/accounts/${encodeURIComponent(id)}`),
        {
          ...mutationBody(input.operationId, input.idempotencyKey, input.expectedVersion),
          name: input.name,
          base_url: input.baseUrl,
          api_key: input.apiKey,
          proxy_url: input.proxyUrl,
          protocol_mode: input.protocolMode,
          headers: input.headers,
          allowed_models: input.allowedModels,
          model_mapping: input.modelMapping,
          test_model: input.testModel,
        },
        requestConfig(managementKey, input.idempotencyKey)
      );
      return response.data;
    } catch (error) {
      return handleError(error);
    }
  },

  async setEnabled(
    base: string,
    managementKey: string,
    account: ProAccount,
    enabled: boolean,
    operationId: string,
    idempotencyKey: string
  ) {
    try {
      const response = await axios.put<ProAccountLifecycleResult>(
        buildURL(base, `/v0/pro/accounts/${encodeURIComponent(account.id)}`),
        { ...mutationBody(operationId, idempotencyKey, account.version), enabled },
        requestConfig(managementKey, idempotencyKey)
      );
      return response.data;
    } catch (error) {
      return handleError(error);
    }
  },

  async deleteAccount(
    base: string,
    managementKey: string,
    account: ProAccount,
    operationId: string,
    idempotencyKey: string
  ) {
    try {
      const response = await axios.delete<ProAccountLifecycleResult>(
        buildURL(base, `/v0/pro/accounts/${encodeURIComponent(account.id)}`),
        {
          ...requestConfig(managementKey, idempotencyKey),
          data: mutationBody(operationId, idempotencyKey, account.version),
        }
      );
      return response.data;
    } catch (error) {
      return handleError(error);
    }
  },

  async testAccount(
    base: string,
    managementKey: string,
    account: ProAccount,
    model: string,
    mode: ProAccountTestMode,
    operationId: string,
    idempotencyKey: string
  ) {
    try {
      const response = await axios.post<{
        connectivity: ProAccountConnectivityResult;
        operation: ProAccountOperation;
      }>(
        buildURL(base, `/v0/pro/accounts/${encodeURIComponent(account.id)}/test`),
        {
          ...mutationBody(operationId, idempotencyKey, account.version),
          model,
          mode,
        },
        requestConfig(managementKey, idempotencyKey)
      );
      return response.data;
    } catch (error) {
      return handleError(error);
    }
  },

  async updateModels(
    base: string,
    managementKey: string,
    account: ProAccount,
    allowedModels: string[],
    modelMapping: Record<string, string>,
    operationId: string,
    idempotencyKey: string
  ) {
    try {
      const response = await axios.put<{ account: ProAccount; operation: ProAccountOperation }>(
        buildURL(base, `/v0/pro/accounts/${encodeURIComponent(account.id)}/models`),
        {
          ...mutationBody(operationId, idempotencyKey, account.version),
          allowed_models: allowedModels,
          model_mapping: modelMapping,
        },
        requestConfig(managementKey, idempotencyKey)
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
