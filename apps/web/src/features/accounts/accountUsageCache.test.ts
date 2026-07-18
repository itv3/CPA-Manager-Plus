import { describe, expect, it } from 'vitest';
import type { ProAccountUsageResponse } from '@/services/api/proAccounts';
import { mergeUsageCacheEntry, type AccountUsageCacheEntry } from './accountUsageCache';

const usage = (
  source: string,
  officialWindows: ProAccountUsageResponse['officialWindows'],
  errorCode?: string,
  planType?: string
) =>
  ({
    source,
    updatedAtMs: 1,
    officialWindows,
    local: {
      fromMs: 1,
      toMs: 2,
      requests: 1,
      successes: 1,
      failures: 0,
      inputTokens: 1,
      outputTokens: 1,
      cachedTokens: 0,
      cacheReadTokens: 0,
      cacheCreationTokens: 0,
      reasoningTokens: 0,
      totalTokens: 2,
      costKnown: false,
    },
    errorCode,
    planType,
    errorMessage: errorCode ? '查询失败' : undefined,
    retryable: Boolean(errorCode),
  }) satisfies ProAccountUsageResponse;

const activeWindow = {
  id: 'active-five-hour',
  label: '5h',
  usedPercent: 20,
  source: 'official',
};

const passiveWindow = {
  id: 'passive-five-hour',
  label: '5h 快照',
  usedPercent: 60,
  source: 'passive',
};

const activeEntry = (): AccountUsageCacheEntry => ({
  value: usage('official', [activeWindow]),
  updatedAtMs: 1_000,
  activeOfficial: {
    windows: [activeWindow],
    updatedAtMs: 1_000,
  },
});

describe('账号用量缓存合并', () => {
  it('主动官方成功结果建立最高优先级快照', () => {
    const result = mergeUsageCacheEntry(
      undefined,
      usage('official', [activeWindow]),
      'active',
      1_000,
      5_000
    );

    expect(result.activeOfficial?.windows).toEqual([activeWindow]);
    expect(result.value.officialWindows).toEqual([activeWindow]);
  });

  it('有效期内的被动响应头窗口不能覆盖主动官方窗口', () => {
    const result = mergeUsageCacheEntry(
      activeEntry(),
      usage('passive', [passiveWindow]),
      'passive',
      2_000,
      5_000
    );

    expect(result.value.officialWindows).toEqual([activeWindow]);
    expect(result.value.source).toBe('official');
  });

  it('主动查询失败时保留有效官方窗口并附带错误摘要', () => {
    const result = mergeUsageCacheEntry(
      activeEntry(),
      usage('local', [], 'official_usage_unknown'),
      'active',
      2_000,
      5_000
    );

    expect(result.value.officialWindows).toEqual([activeWindow]);
    expect(result.value.errorCode).toBe('official_usage_unknown');
    expect(result.value.errorMessage).toBe('查询失败');
  });

  it('主动官方快照过期后采用最新被动窗口', () => {
    const result = mergeUsageCacheEntry(
      activeEntry(),
      usage('passive', [passiveWindow]),
      'passive',
      7_000,
      5_000
    );

    expect(result.value.officialWindows).toEqual([passiveWindow]);
    expect(result.activeOfficial).toBeUndefined();
  });

  it('被动响应缺少套餐时保留主动查询发现的套餐', () => {
    const active = mergeUsageCacheEntry(
      undefined,
      usage('official', [activeWindow], undefined, 'pro'),
      'active',
      1_000,
      5_000
    );
    const result = mergeUsageCacheEntry(
      active,
      usage('passive', [passiveWindow]),
      'passive',
      2_000,
      5_000
    );

    expect(result.value.planType).toBe('pro');
    expect(result.activeOfficial?.planType).toBe('pro');
  });
});
