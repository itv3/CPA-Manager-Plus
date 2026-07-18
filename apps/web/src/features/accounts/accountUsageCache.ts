import type { ProAccountUsageResponse, ProAccountUsageWindow } from '@/services/api/proAccounts';

interface ActiveOfficialUsageSnapshot {
  windows: ProAccountUsageWindow[];
  planType?: string;
  updatedAtMs: number;
}

export interface AccountUsageCacheEntry {
  value: ProAccountUsageResponse;
  updatedAtMs: number;
  activeOfficial?: ActiveOfficialUsageSnapshot;
}

export const mergeUsageCacheEntry = (
  previous: AccountUsageCacheEntry | undefined,
  next: ProAccountUsageResponse,
  requestSource: 'passive' | 'active',
  nowMs: number,
  officialTtlMs: number
): AccountUsageCacheEntry => {
  const previousActiveOfficial =
    previous?.activeOfficial && nowMs - previous.activeOfficial.updatedAtMs < officialTtlMs
      ? previous.activeOfficial
      : undefined;
  const activeQuerySucceeded =
    requestSource === 'active' &&
    next.source.trim().toLowerCase() === 'official' &&
    !next.errorCode;
  const activeOfficial = activeQuerySucceeded
    ? {
        windows: next.officialWindows,
        planType: next.planType?.trim() || previous?.value.planType,
        updatedAtMs: nowMs,
      }
    : previousActiveOfficial;

  if (activeOfficial) {
    const keepActiveError = requestSource === 'active' && !activeQuerySucceeded;
    return {
      value: {
        ...next,
        source: 'official',
        officialWindows: activeOfficial.windows,
        planType: next.planType?.trim() || activeOfficial.planType || previous?.value.planType,
        errorCode: keepActiveError ? next.errorCode : undefined,
        errorMessage: keepActiveError ? next.errorMessage : undefined,
        retryable: keepActiveError ? next.retryable : false,
      },
      updatedAtMs: nowMs,
      activeOfficial,
    };
  }

  return {
    value: {
      ...next,
      planType: next.planType?.trim() || previous?.value.planType,
    },
    updatedAtMs: nowMs,
  };
};
