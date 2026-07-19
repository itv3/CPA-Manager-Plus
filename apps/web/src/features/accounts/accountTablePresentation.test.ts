import { describe, expect, it } from 'vitest';
import {
  accountActionAvailable,
  accountPlanLabel,
  accountStatusPresentation,
  buildAccountUsageWindowRows,
  formatAccountExpiryLabel,
  formatRelativeDate,
  formatResetCountdown,
  resolveUsageUsedPercent,
  shouldShowAccountUsagePlaceholder,
  usagePercentTone,
  usageWindowTone,
  usageWindowSourceTitle,
  usesSharedProviderSwitch,
} from './accountTablePresentation';

describe('accountTablePresentation', () => {
  it('按错误、停用和健康状态确定展示优先级', () => {
    expect(accountStatusPresentation({ enabled: false, healthStatus: 'reauth_required' })).toEqual({
      label: '需要重新授权',
      tone: 'warning',
    });
    expect(accountStatusPresentation({ enabled: false, healthStatus: 'healthy' })).toEqual({
      label: '暂停',
      tone: 'muted',
    });
    expect(accountStatusPresentation({ enabled: true, healthStatus: 'healthy' })).toEqual({
      label: '正常',
      tone: 'success',
    });
  });

  it('将活动时间格式化为紧凑相对时间', () => {
    const now = Date.UTC(2026, 6, 18, 12, 0, 0);
    expect(formatRelativeDate(now - 2 * 60_000, now)).toBe('2 分钟前');
    expect(formatRelativeDate(now - 7 * 60 * 60_000, now)).toBe('7 小时前');
    expect(formatRelativeDate(now - 2 * 24 * 60 * 60_000, now)).toBe('2 天前');
  });

  it('将重置时间格式化为倒计时并区分过期快照', () => {
    const now = Date.UTC(2026, 6, 18, 12, 0, 0);
    expect(formatResetCountdown(now + 29 * 24 * 60 * 60_000 + 23 * 60 * 60_000, 1, now)).toBe(
      '29d 23h'
    );
    expect(formatResetCountdown(now - 1, 12, now)).toBe('待刷新');
    expect(formatResetCountdown(now - 1, 0, now)).toBe('现在');
    expect(formatResetCountdown(now + 30_000, 0, now)).toBe('0m');
    expect(formatResetCountdown(undefined, 0, now, true)).toBe('现在');
  });

  it('按窗口名称和用量阈值选择颜色', () => {
    expect(usageWindowTone('7d', 0)).toBe('emerald');
    expect(usageWindowTone('7d S', 0)).toBe('purple');
    expect(usageWindowTone('7d F', 0)).toBe('amber');
    expect(usageWindowTone('Gemini 3 Flash', 0)).toBe('emerald');
    expect(usageWindowTone('G31F', 0)).toBe('purple');
    expect(usageWindowTone('Claude', 0)).toBe('amber');
    expect(usageWindowTone('7d Opus', 0)).toBe('amber');
    expect(usagePercentTone(100)).toBe('danger');
    expect(usagePercentTone(82)).toBe('warning');
    expect(usagePercentTone(20)).toBe('normal');
    expect(resolveUsageUsedPercent({ remainingPercent: 70 })).toBe(30);
    expect(resolveUsageUsedPercent({ usedPercent: 120 })).toBe(100);
  });

  it('仅将同一 Provider 下的多个 Key 识别为共享开关', () => {
    const openCode = {
      sourceType: 'config_openai_compatibility',
      binding: {
        sourceType: 'config_openai_compatibility',
        sourceLocator: 'provider:0:key:0',
      },
    };
    const nvidia = {
      sourceType: 'config_openai_compatibility',
      binding: {
        sourceType: 'config_openai_compatibility',
        sourceLocator: 'provider:1:key:0',
      },
    };
    const openCodeSecondKey = {
      sourceType: 'config_openai_compatibility',
      binding: {
        sourceType: 'config_openai_compatibility',
        sourceLocator: 'provider:0:key:1',
      },
    };

    expect(usesSharedProviderSwitch(openCode, [openCode, nvidia])).toBe(false);
    expect(usesSharedProviderSwitch(nvidia, [openCode, nvidia])).toBe(false);
    expect(usesSharedProviderSwitch(openCode, [openCode, openCodeSecondKey, nvidia])).toBe(true);
    expect(
      usesSharedProviderSwitch(
        {
          sourceType: 'config_openai_compatibility',
        },
        []
      )
    ).toBe(false);
    expect(
      usesSharedProviderSwitch(
        {
          sourceType: 'auth_file',
        },
        []
      )
    ).toBe(false);
  });

  it('将套餐类型格式化为 sub2api 徽标文案，未知套餐不显示', () => {
    expect(accountPlanLabel('free', 'openai')).toBe('Free');
    expect(accountPlanLabel('chatgpt_pro', 'openai')).toBe('Pro');
    expect(accountPlanLabel('basic', 'xai')).toBe('Grok Free');
    expect(accountPlanLabel('pro_5x', 'openai')).toBe('Pro 5x');
    expect(accountPlanLabel('pro20x', 'openai')).toBe('Pro 20x');
    expect(accountPlanLabel('plan_max', 'anthropic')).toBe('Max');
    expect(accountPlanLabel('plan_pro', 'anthropic')).toBe('Pro');
    expect(accountPlanLabel('plan_team', 'anthropic')).toBe('Team');
    expect(accountPlanLabel('plan_free', 'anthropic')).toBe('Free');
    expect(accountPlanLabel('ultra-lite', 'antigravity')).toBe('Ultra Lite');
    expect(accountPlanLabel('unknown', 'openai')).toBe('');
    expect(accountPlanLabel()).toBe('');
  });

  it('仅为有有效到期时间的付费套餐生成到期标签', () => {
    const expiresAt = new Date(2026, 7, 17).getTime();
    expect(formatAccountExpiryLabel(expiresAt, 'pro')).toBe('到期 2026-08-17');
    expect(formatAccountExpiryLabel(expiresAt, 'free')).toBe('');
    expect(formatAccountExpiryLabel(expiresAt, 'basic')).toBe('');
    expect(formatAccountExpiryLabel(undefined, 'pro')).toBe('');
  });

  it('按能力和账号认证方式显示更多操作', () => {
    const oauthAccount = {
      platform: 'openai',
      authType: 'oauth',
      sourceType: 'auth_file',
      binding: { sourceType: 'auth_file', authIndex: 'auth-openai' },
    };
    const capabilities = {
      credentialDraft: true,
      allowedModels: true,
      stores: {},
      accountActions: {
        reauthorize: { status: 'supported' as const, provider: 'codex' },
        refreshToken: { status: 'supported' as const },
        scheduledTests: { status: 'supported' as const },
      },
    };
    expect(accountActionAvailable(oauthAccount, capabilities, 'reauthorize')).toBe(true);
    expect(accountActionAvailable(oauthAccount, capabilities, 'refreshToken')).toBe(true);
    expect(accountActionAvailable(oauthAccount, null, 'reauthorize')).toBe(false);
    expect(
      accountActionAvailable(
        { ...oauthAccount, platform: 'anthropic' },
        capabilities,
        'reauthorize'
      )
    ).toBe(false);
    expect(
      accountActionAvailable({ ...oauthAccount, binding: undefined }, capabilities, 'refreshToken')
    ).toBe(false);
    expect(
      accountActionAvailable(
        { platform: 'openai', authType: 'api', sourceType: 'auth_file' },
        null,
        'scheduledTests'
      )
    ).toBe(true);
    expect(
      accountActionAvailable(
        oauthAccount,
        {
          credentialDraft: true,
          allowedModels: true,
          stores: {},
          accountActions: { refreshToken: { status: 'unsupported' } },
        },
        'refreshToken'
      )
    ).toBe(false);
  });

  it('OpenAI 表格按 sub2api 仅展示 5h/7d，并将缺失窗口标为待采样', () => {
    const rows = buildAccountUsageWindowRows(
      { platform: 'openai', authType: 'oauth' },
      {
        officialWindows: [{ id: 'monthly', label: '30d', usedPercent: 4, source: 'openai_wham' }],
      }
    );

    expect(rows.map((row) => row.label)).toEqual(['5h', '7d']);
    expect(rows.every((row) => row.source === 'local_placeholder')).toBe(true);
    expect(rows.every((row) => row.usedPercent === undefined)).toBe(true);
    expect(usageWindowSourceTitle(rows[0].source)).toContain('本地统计占位');
  });

  it('保留官方窗口且不会为非 OpenAI 账号伪造 5h', () => {
    const rows = buildAccountUsageWindowRows(
      { platform: 'xai', authType: 'oauth' },
      {
        officialWindows: [
          { id: 'weekly', label: '7d', source: 'xai_official' },
          { id: 'monthly', label: '30d', source: 'xai_official' },
        ],
      }
    );
    expect(rows.map((row) => row.label)).toEqual(['7d', '30d']);
  });

  it('API 账号只隐藏无意义的官方配额空提示', () => {
    const apiAccount = { authType: 'api', healthStatus: 'unknown' };

    expect(shouldShowAccountUsagePlaceholder(apiAccount, false)).toBe(false);
    expect(shouldShowAccountUsagePlaceholder(apiAccount, true)).toBe(true);
    expect(
      shouldShowAccountUsagePlaceholder({ authType: 'API', healthStatus: 'reauth_required' }, false)
    ).toBe(true);
    expect(
      shouldShowAccountUsagePlaceholder({ authType: 'oauth', healthStatus: 'unknown' }, false)
    ).toBe(true);
  });
});
