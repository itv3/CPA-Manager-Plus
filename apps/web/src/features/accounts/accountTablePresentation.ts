import type {
  ProAccount,
  ProAccountBinding,
  ProAccountActionCapabilityName,
  ProAccountCapabilitiesResponse,
  ProAccountUsageResponse,
  ProAccountUsageWindow,
} from '@/services/api/proAccounts';

export type AccountStatusTone = 'success' | 'muted' | 'warning' | 'danger';

export interface AccountStatusPresentation {
  label: string;
  tone: AccountStatusTone;
}

type ProviderSwitchAccount = {
  sourceType: ProAccount['sourceType'];
  binding?: Pick<NonNullable<ProAccount['binding']>, 'sourceType' | 'sourceLocator'>;
};

const openAICompatibilityLocator = (account: ProviderSwitchAccount) => {
  const sourceType = (account.binding?.sourceType || account.sourceType).trim().toLowerCase();
  if (sourceType !== 'config_openai_compatibility') return null;

  const sourceLocator = account.binding?.sourceLocator?.trim() || '';
  const match = /^provider:(\d+):key:(\d+|none)$/.exec(sourceLocator);
  if (!match) return null;
  return { provider: `provider:${match[1]}`, key: match[2] };
};

export const usesSharedProviderSwitch = (
  account: ProviderSwitchAccount,
  accounts: readonly ProviderSwitchAccount[]
) => {
  const locator = openAICompatibilityLocator(account);
  if (!locator) return false;

  return accounts.some((candidate) => {
    const candidateLocator = openAICompatibilityLocator(candidate);
    return candidateLocator?.provider === locator.provider && candidateLocator.key !== locator.key;
  });
};

export const accountStatusPresentation = (
  account: Pick<ProAccount, 'enabled' | 'healthStatus'>
): AccountStatusPresentation => {
  const healthStatus = account.healthStatus.trim().toLowerCase();
  if (healthStatus === 'reauth_required') {
    return { label: '需要重新授权', tone: 'warning' };
  }
  if (healthStatus === 'error') {
    return { label: '错误', tone: 'danger' };
  }
  if (!account.enabled) {
    return { label: '暂停', tone: 'muted' };
  }
  if (healthStatus === 'healthy') {
    return { label: '正常', tone: 'success' };
  }
  return { label: '未知', tone: 'muted' };
};

export const formatRelativeDate = (value?: number, nowMs = Date.now()) => {
  if (!value || !Number.isFinite(value)) return '-';
  const diffMs = Math.max(0, nowMs - value);
  const minuteMs = 60_000;
  const hourMs = 60 * minuteMs;
  const dayMs = 24 * hourMs;
  if (diffMs < minuteMs) return '刚刚';
  if (diffMs < hourMs) return `${Math.floor(diffMs / minuteMs)} 分钟前`;
  if (diffMs < dayMs) return `${Math.floor(diffMs / hourMs)} 小时前`;
  return `${Math.floor(diffMs / dayMs)} 天前`;
};

export const formatResetCountdown = (
  resetAtMs?: number,
  usedPercent?: number,
  nowMs = Date.now(),
  showNowWhenIdle = false
) => {
  if (!resetAtMs || !Number.isFinite(resetAtMs)) {
    return showNowWhenIdle && (usedPercent ?? 0) <= 0 ? '现在' : '';
  }
  const diffMs = resetAtMs - nowMs;
  if (diffMs <= 0) return (usedPercent ?? 0) > 0 ? '待刷新' : '现在';

  const totalMinutes = Math.max(0, Math.floor(diffMs / 60_000));
  const days = Math.floor(totalMinutes / (24 * 60));
  const hours = Math.floor((totalMinutes % (24 * 60)) / 60);
  const minutes = totalMinutes % 60;
  if (days > 0) return `${days}d ${hours}h`;
  if (hours > 0) return `${hours}h ${minutes}m`;
  return `${minutes}m`;
};

export const usageWindowTone = (label: string, index: number) => {
  const normalized = normalizeCompactValue(label);
  if (
    normalized.includes('7dsonnet') ||
    normalized === '7ds' ||
    normalized.includes('image') ||
    normalized.includes('g31f')
  ) {
    return 'purple';
  }
  if (
    normalized.includes('7dfable') ||
    normalized === '7df' ||
    normalized.includes('claude') ||
    normalized.includes('opus')
  ) {
    return 'amber';
  }
  if (normalized.includes('7d') || normalized.includes('week') || normalized.includes('flash')) {
    return 'emerald';
  }
  if (normalized.includes('5h') || normalized.includes('pro')) return 'indigo';
  return index % 2 === 0 ? 'indigo' : 'emerald';
};

export const usagePercentTone = (usedPercent?: number) => {
  if (usedPercent === undefined) return 'neutral';
  if (usedPercent >= 100) return 'danger';
  if (usedPercent >= 80) return 'warning';
  return 'normal';
};

export const resolveUsageUsedPercent = (
  window: Pick<ProAccountUsageWindow, 'usedPercent' | 'remainingPercent'>
) => {
  const value =
    window.usedPercent !== undefined
      ? window.usedPercent
      : window.remainingPercent !== undefined
        ? 100 - window.remainingPercent
        : undefined;
  if (value === undefined || !Number.isFinite(value)) return undefined;
  return Math.min(100, Math.max(0, value));
};

const normalizeCompactValue = (value?: string) =>
  (value || '')
    .trim()
    .toLowerCase()
    .replace(/[\s_-]+/g, '');

export const shouldShowAccountUsagePlaceholder = (
  account: Pick<ProAccount, 'authType' | 'healthStatus'>,
  loading: boolean
) =>
  loading ||
  normalizeCompactValue(account.healthStatus) === 'reauthrequired' ||
  normalizeCompactValue(account.authType) !== 'api';

export const accountPlanLabel = (planType?: string, platform?: string) => {
  const normalized = normalizeCompactValue(planType);
  if (!normalized || normalized === 'unknown' || normalized === 'none' || normalized === 'na') {
    return '';
  }
  if (normalized === 'pro5x') return 'Pro 5x';
  if (normalized === 'pro20x') return 'Pro 20x';
  if (normalized === 'max' || normalized === 'planmax') return 'Max';
  if (normalized === 'ultralite') return 'Ultra Lite';
  if (normalized === 'plus') return 'Plus';
  if (normalized === 'team' || normalized === 'planteam') return 'Team';
  if (normalized === 'chatgptpro' || normalized === 'pro' || normalized === 'planpro') {
    return 'Pro';
  }
  if (normalized === 'free' || normalized === 'basic' || normalized === 'planfree') {
    return normalizeCompactValue(platform) === 'xai' ? 'Grok Free' : 'Free';
  }
  if (normalized === 'supergrok') return 'SuperGrok';
  if (normalized === 'supergrokheavy') return 'SuperGrok Heavy';
  if (normalized === 'abnormal') return '订阅异常';
  return planType?.trim() || '';
};

export const formatAccountExpiryLabel = (expiresAtMs?: number, planType?: string) => {
  const normalizedPlanType = normalizeCompactValue(planType);
  if (
    !expiresAtMs ||
    !Number.isFinite(expiresAtMs) ||
    !normalizedPlanType ||
    normalizedPlanType === 'free' ||
    normalizedPlanType === 'basic' ||
    normalizedPlanType === 'planfree'
  ) {
    return '';
  }
  const date = new Date(expiresAtMs);
  if (Number.isNaN(date.getTime())) return '';
  const year = date.getFullYear();
  const month = String(date.getMonth() + 1).padStart(2, '0');
  const day = String(date.getDate()).padStart(2, '0');
  return `到期 ${year}-${month}-${day}`;
};

export const accountActionAvailable = (
  account: Pick<ProAccount, 'platform' | 'authType' | 'sourceType'> & {
    binding?: Pick<ProAccountBinding, 'sourceType' | 'authIndex'>;
  },
  capabilities: ProAccountCapabilitiesResponse | null,
  action: ProAccountActionCapabilityName
) => {
  const actionCapability = capabilities?.accountActions?.[action];
  const capability = actionCapability?.status;
  if (capability === 'unsupported') return false;
  if (action === 'scheduledTests') return true;
  if (capability !== 'supported' || normalizeCompactValue(account.authType) !== 'oauth') {
    return false;
  }
  const bindingSource = normalizeCompactValue(account.binding?.sourceType || account.sourceType);
  if (bindingSource !== 'authfile' || !account.binding?.authIndex?.trim()) return false;
  if (action === 'reauthorize') {
    return (
      normalizeCompactValue(account.platform) === 'openai' &&
      normalizeCompactValue(actionCapability?.provider) === 'codex'
    );
  }
  return true;
};

export type AccountUsageWindowRow = ProAccountUsageWindow & {
  localPlaceholder?: boolean;
};

const usageWindowKind = (window: Pick<ProAccountUsageWindow, 'id' | 'label'>) => {
  const value = normalizeCompactValue(`${window.id} ${window.label}`);
  if (value.includes('5h') || value.includes('fivehour')) return '5h';
  if (value.includes('7dsonnet') || value.includes('sevendaysonnet')) return '7d-sonnet';
  if (value.includes('7dfable') || value.includes('sevendayfable')) return '7d-fable';
  if (value.includes('7d') || value.includes('sevenday') || value.includes('weekly')) return '7d';
  if (value.includes('30d') || value.includes('monthly')) return '30d';
  return value;
};

const usageWindowRank = (window: Pick<ProAccountUsageWindow, 'id' | 'label'>) => {
  const kind = usageWindowKind(window);
  if (kind === '5h') return 0;
  if (kind === '7d') return 1;
  if (kind === '7d-sonnet') return 2;
  if (kind === '7d-fable') return 3;
  if (kind === '30d') return 4;
  return 10;
};

export const isLocalUsageWindowSource = (source?: string) => {
  const normalized = normalizeCompactValue(source);
  return (
    normalized === 'local' || normalized === 'localestimate' || normalized === 'localplaceholder'
  );
};

export const usageWindowSourceTitle = (source?: string) => {
  if (isLocalUsageWindowSource(source)) {
    return '本地统计占位窗口，仅用于保持滚动窗口布局，不代表官方配额';
  }
  if (!source) return '官方配额窗口';
  return `官方配额窗口 · ${source}`;
};

export const buildAccountUsageWindowRows = (
  account: Pick<ProAccount, 'platform' | 'authType'>,
  usage: Pick<ProAccountUsageResponse, 'officialWindows'>
): AccountUsageWindowRow[] => {
  const windows: AccountUsageWindowRow[] = usage.officialWindows.map((window) => {
    const localPlaceholder = isLocalUsageWindowSource(window.source);
    return localPlaceholder
      ? {
          ...window,
          usedPercent: undefined,
          remainingPercent: undefined,
          localPlaceholder: true,
        }
      : { ...window };
  });
  const kinds = new Set(windows.map(usageWindowKind));
  const isOpenAIOAuth =
    normalizeCompactValue(account.platform) === 'openai' &&
    normalizeCompactValue(account.authType) === 'oauth';

  // sub2api 在官方接口未返回滚动窗口时仍保留 5h/7d 本地统计行；
  // source 明确标为 local_placeholder，避免把占位数据误解为官方配额。
  if (isOpenAIOAuth && !kinds.has('5h')) {
    windows.push({
      id: 'local-placeholder-5h',
      label: '5h',
      source: 'local_placeholder',
      localPlaceholder: true,
    });
  }
  if (isOpenAIOAuth && !kinds.has('7d')) {
    windows.push({
      id: 'local-placeholder-7d',
      label: '7d',
      source: 'local_placeholder',
      localPlaceholder: true,
    });
  }

  const sorted = windows
    .map((window, index) => ({ window, index }))
    .sort(
      (left, right) =>
        usageWindowRank(left.window) - usageWindowRank(right.window) || left.index - right.index
    )
    .map(({ window }) => window);
  if (!isOpenAIOAuth) return sorted;
  return sorted.filter((window) => {
    const kind = usageWindowKind(window);
    return kind === '5h' || kind === '7d';
  });
};
