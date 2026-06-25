import { describe, expect, it } from 'vitest';
import type { AuthFileItem } from '@/types';
import type { UsageHeaderSnapshot } from '@/services/api/usageService';
import { buildCodexQuotaWindowInfos } from './quota/codexQuota';
import {
  buildObservedCodexQuotaFromHeaderSnapshot,
  buildUsageHeaderSnapshotLookup,
  getHeaderSnapshotPlanType,
  getHighConfidenceUsageHeaderSnapshotForAuthFile,
  getUsageHeaderSnapshotForAuthFile,
  hasUsageHeaderDiagnosticSignal,
  hasUsageHeaderQuotaSignal,
} from './usageHeaderSnapshots';

describe('buildObservedCodexQuotaFromHeaderSnapshot', () => {
  it('normalizes Codex header quota metadata into usage quota windows', () => {
    const snapshot: UsageHeaderSnapshot = {
      event_hash: 'event-test',
      timestamp_ms: 1_700_000_000_000,
      response_metadata: {
        quota: {
          plan_type: 'free',
          active_limit: 'premium',
          credits_has_credits: false,
          credits_unlimited: false,
          rate_limit_reached_type: 'workspace_member_credits_depleted',
          primary_over_secondary_limit_percent: 20,
          primary: {
            used_percent: 20,
            reset_at_ms: 1_784_805_897_000,
            window_minutes: 43_200,
          },
          secondary: {
            used_percent: 0,
            window_minutes: 0,
          },
        },
      },
    };

    const observed = buildObservedCodexQuotaFromHeaderSnapshot(snapshot);

    expect(observed).toMatchObject({
      planType: 'free',
      activeLimit: 'premium',
      creditsHasCredits: false,
      creditsUnlimited: false,
      rateLimitReachedType: 'workspace_member_credits_depleted',
      primaryOverSecondaryLimitPercent: 20,
    });
    expect(observed?.payload?.rate_limit?.primary_window).toMatchObject({
      used_percent: 20,
      reset_at: 1_784_805_897,
      limit_window_seconds: 2_592_000,
    });
    expect(observed?.payload?.rate_limit?.secondary_window).toBeUndefined();

    const windows = buildCodexQuotaWindowInfos(observed?.payload ?? {});
    expect(windows).toMatchObject([
      {
        id: 'monthly',
        labelKey: 'codex_quota.monthly_window',
        usedPercent: 20,
        limitWindowSeconds: 2_592_000,
      },
    ]);
  });

  it('does not treat trace-only metadata as quota evidence', () => {
    const snapshot: UsageHeaderSnapshot = {
      event_hash: 'trace-only',
      timestamp_ms: 1_700_000_000_000,
      response_metadata: {
        trace: {
          primary_trace_id: 'req-trace-only',
        },
      },
      header_trace_id: 'req-trace-only',
    };

    expect(hasUsageHeaderDiagnosticSignal(snapshot)).toBe(true);
    expect(hasUsageHeaderQuotaSignal(snapshot)).toBe(false);
    expect(buildObservedCodexQuotaFromHeaderSnapshot(snapshot)).toBeNull();
  });

  it('requires high-confidence identity matches for auth-file quota fallback', () => {
    const lookup = buildUsageHeaderSnapshotLookup([
      {
        event_hash: 'auth-index-only',
        timestamp_ms: 1_700_000_000_200,
        auth_index: '7',
        response_metadata: {
          quota: {
            plan_type: 'team',
          },
        },
      },
      {
        event_hash: 'file-and-auth-index',
        timestamp_ms: 1_700_000_000_100,
        auth_file_snapshot: 'codex-account.json',
        auth_index: '7',
        response_metadata: {
          quota: {
            plan_type: 'plus',
          },
        },
      },
    ]);
    const file = {
      name: 'codex-account.json',
      provider: 'codex',
      authIndex: '7',
    } as AuthFileItem;

    expect(getUsageHeaderSnapshotForAuthFile(lookup, file)?.event_hash).toBe('file-and-auth-index');
    expect(getHighConfidenceUsageHeaderSnapshotForAuthFile(lookup, file)?.event_hash).toBe(
      'file-and-auth-index'
    );

    const unmatchedFile = {
      name: 'other-codex.json',
      provider: 'codex',
      authIndex: '7',
    } as AuthFileItem;
    expect(getUsageHeaderSnapshotForAuthFile(lookup, unmatchedFile)?.event_hash).toBe(
      'auth-index-only'
    );
    expect(getHighConfidenceUsageHeaderSnapshotForAuthFile(lookup, unmatchedFile)).toBeUndefined();
  });

  it('does not use active limit as the plan type', () => {
    const snapshot: UsageHeaderSnapshot = {
      event_hash: 'active-limit-only',
      timestamp_ms: 1_700_000_000_000,
      response_metadata: {
        quota: {
          active_limit: 'premium',
        },
      },
    };

    expect(getHeaderSnapshotPlanType(snapshot)).toBe('');
    expect(hasUsageHeaderQuotaSignal(snapshot)).toBe(true);
  });
});
