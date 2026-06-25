import type {
  ResponseHeaderQuotaMetadata,
  ResponseHeaderQuotaWindow,
  UsageHeaderSnapshot,
} from '@/services/api/usageService';
import type { AuthFileItem, CodexUsagePayload, CodexUsageWindow } from '@/types';
import { normalizeAuthIndex } from '@/utils/authIndex';

export type UsageHeaderSnapshotLookup = {
  byFileName: Map<string, UsageHeaderSnapshot>;
  byFileAuthIndex: Map<string, UsageHeaderSnapshot>;
  byAuthIndex: Map<string, UsageHeaderSnapshot>;
  byAccount: Map<string, UsageHeaderSnapshot>;
  bySource: Map<string, UsageHeaderSnapshot>;
};

export type UsageHeaderSnapshotMatchConfidence = 'none' | 'low' | 'high';

export type UsageHeaderSnapshotMatch = {
  snapshot?: UsageHeaderSnapshot;
  confidence: UsageHeaderSnapshotMatchConfidence;
};

const readString = (value: unknown): string => {
  if (typeof value === 'string') return value.trim();
  if (typeof value === 'number' || typeof value === 'boolean') return String(value).trim();
  return '';
};

const normalizeKey = (value: unknown) => readString(value).toLowerCase();

const readNumber = (value: unknown): number | null => {
  if (typeof value === 'number' && Number.isFinite(value)) return value;
  if (typeof value === 'string') {
    const trimmed = value.trim();
    if (!trimmed) return null;
    const parsed = Number(trimmed);
    return Number.isFinite(parsed) ? parsed : null;
  }
  return null;
};

const readBoolean = (value: unknown): boolean | null => {
  if (typeof value === 'boolean') return value;
  if (typeof value === 'number' && Number.isFinite(value)) return value !== 0;
  if (typeof value === 'string') {
    const normalized = value.trim().toLowerCase();
    if (['true', '1', 'yes'].includes(normalized)) return true;
    if (['false', '0', 'no'].includes(normalized)) return false;
  }
  return null;
};

const newerSnapshot = (
  current: UsageHeaderSnapshot | undefined,
  next: UsageHeaderSnapshot
): UsageHeaderSnapshot => {
  if (!current) return next;
  return (next.timestamp_ms ?? 0) > (current.timestamp_ms ?? 0) ? next : current;
};

const setNewest = (
  map: Map<string, UsageHeaderSnapshot>,
  key: string,
  snapshot: UsageHeaderSnapshot
) => {
  if (!key) return;
  map.set(key, newerSnapshot(map.get(key), snapshot));
};

const fileAuthKey = (fileName: string, authIndex: string) =>
  fileName && authIndex ? `${normalizeKey(fileName)}::${normalizeKey(authIndex)}` : '';

const confidenceRank: Record<UsageHeaderSnapshotMatchConfidence, number> = {
  none: 0,
  low: 1,
  high: 2,
};

const newerMatch = (
  current: UsageHeaderSnapshotMatch,
  next: UsageHeaderSnapshotMatch
): UsageHeaderSnapshotMatch => {
  if (!next.snapshot) return current;
  if (!current.snapshot) return next;
  const currentRank = confidenceRank[current.confidence];
  const nextRank = confidenceRank[next.confidence];
  if (nextRank !== currentRank) return nextRank > currentRank ? next : current;
  return newerSnapshot(current.snapshot, next.snapshot) === next.snapshot ? next : current;
};

const matchOf = (
  snapshot: UsageHeaderSnapshot | undefined,
  confidence: UsageHeaderSnapshotMatchConfidence
): UsageHeaderSnapshotMatch => (snapshot ? { snapshot, confidence } : { confidence: 'none' });

export const buildUsageHeaderSnapshotLookup = (
  snapshots: UsageHeaderSnapshot[] = []
): UsageHeaderSnapshotLookup => {
  const lookup: UsageHeaderSnapshotLookup = {
    byFileName: new Map(),
    byFileAuthIndex: new Map(),
    byAuthIndex: new Map(),
    byAccount: new Map(),
    bySource: new Map(),
  };

  snapshots.forEach((snapshot) => {
    const fileName = readString(snapshot.auth_file_snapshot);
    const authIndex = normalizeAuthIndex(snapshot.auth_index);
    const account = readString(snapshot.account_snapshot);
    const source = readString(snapshot.source);

    setNewest(lookup.byFileName, normalizeKey(fileName), snapshot);
    setNewest(lookup.byFileAuthIndex, fileAuthKey(fileName, authIndex ?? ''), snapshot);
    setNewest(lookup.byAuthIndex, normalizeKey(authIndex), snapshot);
    setNewest(lookup.byAccount, normalizeKey(account), snapshot);
    setNewest(lookup.bySource, normalizeKey(source), snapshot);
  });

  return lookup;
};

export const getUsageHeaderSnapshotMatchForIdentity = (
  lookup: UsageHeaderSnapshotLookup | undefined,
  identity: {
    fileName?: unknown;
    authIndex?: unknown;
    account?: unknown;
    source?: unknown;
  }
): UsageHeaderSnapshotMatch => {
  if (!lookup) return { confidence: 'none' };
  const fileName = readString(identity.fileName);
  const authIndex = normalizeAuthIndex(identity.authIndex);
  const account = readString(identity.account);
  const source = readString(identity.source);
  const candidates = [
    matchOf(lookup.byFileAuthIndex.get(fileAuthKey(fileName, authIndex ?? '')), 'high'),
    matchOf(lookup.byFileName.get(normalizeKey(fileName)), 'high'),
    matchOf(lookup.byAccount.get(normalizeKey(account)), 'high'),
    matchOf(lookup.byAuthIndex.get(normalizeKey(authIndex)), 'low'),
    matchOf(lookup.bySource.get(normalizeKey(source)), 'low'),
  ];

  return candidates.reduce<UsageHeaderSnapshotMatch>((current, next) => newerMatch(current, next), {
    confidence: 'none',
  });
};

export const getUsageHeaderSnapshotForIdentity = (
  lookup: UsageHeaderSnapshotLookup | undefined,
  identity: {
    fileName?: unknown;
    authIndex?: unknown;
    account?: unknown;
    source?: unknown;
  }
): UsageHeaderSnapshot | undefined => {
  return getUsageHeaderSnapshotMatchForIdentity(lookup, identity).snapshot;
};

export const getUsageHeaderSnapshotForAuthFile = (
  lookup: UsageHeaderSnapshotLookup | undefined,
  file: AuthFileItem
): UsageHeaderSnapshot | undefined =>
  getUsageHeaderSnapshotForIdentity(lookup, {
    fileName: file.name,
    authIndex: file['auth_index'] ?? file.authIndex,
    account: file.account ?? file.email ?? file.label,
  });

export const getHighConfidenceUsageHeaderSnapshotForAuthFile = (
  lookup: UsageHeaderSnapshotLookup | undefined,
  file: AuthFileItem
): UsageHeaderSnapshot | undefined => {
  const match = getUsageHeaderSnapshotMatchForIdentity(lookup, {
    fileName: file.name,
    authIndex: file['auth_index'] ?? file.authIndex,
    account: file.account ?? file.email ?? file.label,
  });
  return match.confidence === 'high' ? match.snapshot : undefined;
};

export const getHeaderSnapshotPlanType = (
  snapshot: UsageHeaderSnapshot | null | undefined
): string => {
  if (!snapshot) return '';
  return (
    readString(snapshot.header_quota_plan_type) ||
    readString(snapshot.response_metadata?.quota?.plan_type)
  );
};

export const getHeaderSnapshotQuotaMetadata = (
  snapshot: UsageHeaderSnapshot | null | undefined
): ResponseHeaderQuotaMetadata | undefined => snapshot?.response_metadata?.quota;

export const getHeaderSnapshotUsedPercent = (
  snapshot: UsageHeaderSnapshot | null | undefined
): number | null => {
  const value =
    snapshot?.header_quota_used_percent ?? snapshot?.response_metadata?.quota?.used_percent ?? null;
  return typeof value === 'number' && Number.isFinite(value) ? value : null;
};

export const getHeaderSnapshotRecoverAtMs = (
  snapshot: UsageHeaderSnapshot | null | undefined
): number | null => {
  const value =
    snapshot?.header_quota_recover_at_ms ??
    snapshot?.response_metadata?.quota?.recover_at_ms ??
    null;
  return typeof value === 'number' && Number.isFinite(value) && value > 0 ? value : null;
};

export const getHeaderSnapshotErrorKind = (
  snapshot: UsageHeaderSnapshot | null | undefined
): string =>
  readString(snapshot?.header_error_kind) || readString(snapshot?.response_metadata?.errors?.kind);

export const getHeaderSnapshotErrorCode = (
  snapshot: UsageHeaderSnapshot | null | undefined
): string =>
  readString(snapshot?.header_error_code) ||
  readString(snapshot?.response_metadata?.errors?.code) ||
  readString(snapshot?.response_metadata?.errors?.ide_root_error_code) ||
  readString(snapshot?.response_metadata?.errors?.ide_error_code) ||
  readString(snapshot?.response_metadata?.errors?.authorization_error);

export const getHeaderSnapshotTraceId = (
  snapshot: UsageHeaderSnapshot | null | undefined
): string =>
  readString(snapshot?.header_trace_id) ||
  readString(snapshot?.response_metadata?.trace?.primary_trace_id);

export type ObservedCodexHeaderQuota = {
  payload: CodexUsagePayload | null;
  planType: string | null;
  activeLimit: string | null;
  creditsHasCredits: boolean | null;
  creditsUnlimited: boolean | null;
  creditsBalance: string | null;
  rateLimitReachedType: string | null;
  primaryOverSecondaryLimitPercent: number | null;
};

const buildCodexWindowFromHeaderQuota = (
  window: ResponseHeaderQuotaWindow | null | undefined
): CodexUsageWindow | null => {
  if (!window) return null;
  const usedPercent = readNumber(window.used_percent);
  const resetAtMs = readNumber(window.reset_at_ms);
  const resetAfterSeconds = readNumber(window.reset_after_seconds);
  const windowMinutes = readNumber(window.window_minutes);
  const hasPositiveWindow = windowMinutes !== null && windowMinutes > 0;
  const hasResetSignal =
    (resetAtMs !== null && resetAtMs > 0) || (resetAfterSeconds !== null && resetAfterSeconds > 0);

  if (windowMinutes === 0 && !hasResetSignal) return null;
  if (usedPercent === null && !hasResetSignal && !hasPositiveWindow) return null;

  return {
    ...(usedPercent !== null ? { used_percent: usedPercent } : {}),
    ...(resetAtMs !== null && resetAtMs > 0 ? { reset_at: Math.floor(resetAtMs / 1000) } : {}),
    ...(resetAfterSeconds !== null && resetAfterSeconds > 0
      ? { reset_after_seconds: resetAfterSeconds }
      : {}),
    ...(hasPositiveWindow ? { limit_window_seconds: windowMinutes * 60 } : {}),
  };
};

export const buildObservedCodexQuotaFromHeaderSnapshot = (
  snapshot: UsageHeaderSnapshot | null | undefined
): ObservedCodexHeaderQuota | null => {
  const quota = getHeaderSnapshotQuotaMetadata(snapshot);
  if (!quota) return null;

  const planType = readString(quota.plan_type) || null;
  const activeLimit = readString(quota.active_limit) || null;
  const creditsBalance = readString(quota.credits_balance) || null;
  const creditsHasCredits = readBoolean(quota.credits_has_credits);
  const creditsUnlimited = readBoolean(quota.credits_unlimited);
  const rateLimitReachedType = readString(quota.rate_limit_reached_type) || null;
  const primaryOverSecondaryLimitPercent = readNumber(quota.primary_over_secondary_limit_percent);
  const primaryWindow = buildCodexWindowFromHeaderQuota(quota.primary);
  const secondaryWindow = buildCodexWindowFromHeaderQuota(quota.secondary);
  const hasRateLimitPayload = Boolean(primaryWindow || secondaryWindow || rateLimitReachedType);
  const hasCreditsPayload = Boolean(
    creditsHasCredits !== null || creditsUnlimited !== null || creditsBalance
  );
  const payload: CodexUsagePayload | null =
    planType || hasRateLimitPayload || hasCreditsPayload
      ? {
          ...(planType ? { plan_type: planType } : {}),
          ...(hasRateLimitPayload
            ? {
                rate_limit: {
                  ...(rateLimitReachedType ? { limit_reached: true } : {}),
                  ...(primaryWindow ? { primary_window: primaryWindow } : {}),
                  ...(secondaryWindow ? { secondary_window: secondaryWindow } : {}),
                },
              }
            : {}),
          ...(hasCreditsPayload
            ? {
                credits: {
                  ...(creditsHasCredits !== null ? { has_credits: creditsHasCredits } : {}),
                  ...(creditsUnlimited !== null ? { unlimited: creditsUnlimited } : {}),
                  ...(creditsBalance ? { balance: creditsBalance } : {}),
                },
              }
            : {}),
          ...(rateLimitReachedType ? { rate_limit_reached_type: rateLimitReachedType } : {}),
        }
      : null;

  if (
    !payload &&
    !activeLimit &&
    primaryOverSecondaryLimitPercent === null &&
    !rateLimitReachedType
  ) {
    return null;
  }

  return {
    payload,
    planType,
    activeLimit,
    creditsHasCredits,
    creditsUnlimited,
    creditsBalance,
    rateLimitReachedType,
    primaryOverSecondaryLimitPercent,
  };
};

export const hasUsageHeaderQuotaSignal = (
  snapshot: UsageHeaderSnapshot | null | undefined
): boolean =>
  Boolean(
    getHeaderSnapshotPlanType(snapshot) ||
    buildObservedCodexQuotaFromHeaderSnapshot(snapshot) ||
    getHeaderSnapshotUsedPercent(snapshot) !== null ||
    getHeaderSnapshotRecoverAtMs(snapshot) !== null
  );

export const hasUsageHeaderDiagnosticSignal = (
  snapshot: UsageHeaderSnapshot | null | undefined
): boolean =>
  Boolean(
    hasUsageHeaderQuotaSignal(snapshot) ||
    getHeaderSnapshotErrorKind(snapshot) ||
    getHeaderSnapshotErrorCode(snapshot) ||
    getHeaderSnapshotTraceId(snapshot)
  );

export const hasUsageHeaderSnapshotSignal = hasUsageHeaderDiagnosticSignal;
