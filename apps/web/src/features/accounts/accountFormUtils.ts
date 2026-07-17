import type { ProAccountCapabilitiesResponse } from '@/services/api/proAccounts';

export type AccountPlatform = 'openai' | 'anthropic' | 'gemini' | 'antigravity' | 'xai';
export type AccountAuthType = 'oauth' | 'api' | 'vertex';

export interface AccountPlatformOption {
  id: AccountPlatform;
  label: string;
  authTypes: AccountAuthType[];
  defaultBaseUrl?: string;
}

export const ACCOUNT_PLATFORMS: AccountPlatformOption[] = [
  {
    id: 'openai',
    label: 'OpenAI',
    authTypes: ['oauth', 'api'],
    defaultBaseUrl: 'https://api.openai.com',
  },
  {
    id: 'anthropic',
    label: 'Anthropic',
    authTypes: ['oauth', 'api'],
    defaultBaseUrl: 'https://api.anthropic.com',
  },
  {
    id: 'gemini',
    label: 'Gemini',
    authTypes: ['oauth', 'api', 'vertex'],
    defaultBaseUrl: 'https://generativelanguage.googleapis.com',
  },
  { id: 'antigravity', label: 'Antigravity', authTypes: ['oauth'] },
  { id: 'xai', label: 'Grok / xAI', authTypes: ['oauth'] },
];

export const AUTH_TYPE_LABELS: Record<AccountAuthType, string> = {
  oauth: 'OAuth',
  api: 'API',
  vertex: 'Vertex',
};

export interface RequestIdentity {
  operationId: string;
  idempotencyKey: string;
}

const randomID = () => {
  if (typeof crypto !== 'undefined' && typeof crypto.randomUUID === 'function') {
    return crypto.randomUUID();
  }
  return `${Date.now().toString(36)}-${Math.random().toString(36).slice(2)}-${Math.random().toString(36).slice(2)}`;
};

export const createRequestIdentity = (prefix: string): RequestIdentity => {
  const value = randomID();
  return {
    operationId: `${prefix}-${value}`,
    idempotencyKey: `${prefix}-${value}`,
  };
};

export const platformOption = (platform: string) =>
  ACCOUNT_PLATFORMS.find((item) => item.id === platform);

export const authTypesForPlatform = (
  platform: string,
  capabilities?: ProAccountCapabilitiesResponse | null
): AccountAuthType[] => {
  const authTypes = platformOption(platform)?.authTypes ?? [];
  const platformCapabilities = capabilities?.platforms?.[platform];
  if (platformCapabilities) {
    return authTypes.filter((authType) => platformCapabilities[authType]?.status === 'supported');
  }
  if (platform !== 'gemini') return authTypes;
  return authTypes.filter((authType) => authType !== 'oauth');
};

const uniqueValues = (values: string[]) => {
  const seen = new Set<string>();
  const result: string[] = [];
  values.forEach((value) => {
    const normalized = value.trim();
    const key = normalized.toLowerCase();
    if (!normalized || seen.has(key)) return;
    seen.add(key);
    result.push(normalized);
  });
  return result;
};

const isValidWildcard = (value: string) => {
  const count = value.split('*').length - 1;
  return count === 0 || (count === 1 && value.endsWith('*'));
};

export const parseModelLines = (value: string) => {
  const models = uniqueValues(value.split(/[\n,]/));
  const invalid = models.find((model) => !isValidWildcard(model));
  if (invalid) {
    throw new Error(`模型“${invalid}”的通配符只能在末尾出现一次`);
  }
  return models;
};

export const formatModelLines = (models: string[]) => models.join('\n');

export const parseMappingLines = (value: string) => {
  const mapping: Record<string, string> = {};
  value.split('\n').forEach((raw, index) => {
    const line = raw.trim();
    if (!line) return;
    const separator = line.indexOf('=');
    const alias = line.slice(0, separator).trim();
    const upstream = line.slice(separator + 1).trim();
    if (separator <= 0 || !alias || !upstream) {
      throw new Error(`第 ${index + 1} 行映射应使用“别名=上游模型”格式`);
    }
    if (!isValidWildcard(alias) || upstream.includes('*')) {
      throw new Error(`第 ${index + 1} 行映射的别名仅允许末尾通配符，目标不允许通配符`);
    }
    mapping[alias] = upstream;
  });
  return mapping;
};

export const formatMappingLines = (mapping: Record<string, string>) =>
  Object.entries(mapping)
    .sort(([left], [right]) => left.localeCompare(right))
    .map(([alias, upstream]) => `${alias}=${upstream}`)
    .join('\n');

const PROTECTED_HEADERS = new Set([
  'authorization',
  'x-api-key',
  'x-goog-api-key',
  'proxy-authorization',
]);

export const parseHeaderLines = (value: string) => {
  const headers: Record<string, string> = {};
  value.split('\n').forEach((raw, index) => {
    const line = raw.trim();
    if (!line) return;
    const colon = line.indexOf(':');
    const name = line.slice(0, colon).trim();
    const headerValue = line.slice(colon + 1).trim();
    if (colon <= 0 || !name || !headerValue) {
      throw new Error(`第 ${index + 1} 行 Header 应使用“名称: 值”格式`);
    }
    if (PROTECTED_HEADERS.has(name.toLowerCase())) {
      throw new Error(`Header“${name}”由账号凭证管理，不能在此覆盖`);
    }
    headers[name] = headerValue;
  });
  return headers;
};

export const formatHeaderLines = (headers: Record<string, string>) =>
  Object.entries(headers)
    .sort(([left], [right]) => left.localeCompare(right))
    .map(([name, value]) => `${name}: ${value}`)
    .join('\n');

export const suggestedTestModel = (
  requested: string,
  allowedModels: string[],
  mapping: Record<string, string>,
  discoveredModels: string[] = []
) => {
  if (requested.trim()) return requested.trim();
  const concreteAllowed = allowedModels.find((model) => !model.includes('*'));
  if (concreteAllowed) return concreteAllowed;
  const concreteAlias = Object.keys(mapping).find((alias) => !alias.includes('*'));
  if (concreteAlias) return concreteAlias;
  return discoveredModels[0] ?? '';
};

export const accountDisplayName = (platform: string, authType: string) => {
  const platformLabel = platformOption(platform)?.label ?? platform;
  const authLabel = AUTH_TYPE_LABELS[authType as AccountAuthType] ?? authType;
  return `${platformLabel} ${authLabel}`;
};

export const accountSourceLabel = (sourceType: string) => {
  if (sourceType === 'auth_file') return '认证文件';
  if (sourceType === 'config_codex_api_key') return 'Responses';
  if (sourceType === 'config_openai_compatibility') return 'Chat Completions';
  if (sourceType === 'config_vertex_api_key') return 'Vertex 配置';
  if (sourceType.startsWith('config_')) return 'API 配置';
  return '兼容配置';
};

export type UsageRefreshTrigger = 'initial' | 'automatic' | 'manual-passive' | 'manual-active';

export const usageRequestOptions = (trigger: UsageRefreshTrigger) => ({
  source: trigger === 'manual-active' ? ('active' as const) : ('passive' as const),
  force: trigger === 'manual-active',
});
