import type { ReactNode } from 'react';
import { act, create, type ReactTestRenderer } from 'react-test-renderer';
import { beforeEach, describe, expect, it, vi } from 'vitest';
import type { ProAccount, ProAccountResetCreditsResult } from '@/services/api/proAccounts';

(globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;

const { apiMocks, notificationMocks, identityMock } = vi.hoisted(() => ({
  apiMocks: {
    resetCredits: vi.fn(),
    resetOpenAI: vi.fn(),
  },
  notificationMocks: {
    showConfirmation: vi.fn(),
  },
  identityMock: vi.fn(() => ({ operationId: 'reset-operation', idempotencyKey: 'reset-key' })),
}));

vi.mock('@/services/api/proAccounts', () => ({ proAccountsApi: apiMocks }));
vi.mock('@/stores', () => ({ useNotificationStore: () => notificationMocks }));
vi.mock('./accountFormUtils', () => ({ createRequestIdentity: identityMock }));

import { OpenAIQuotaResetControls } from './OpenAIQuotaResetControls';

const account: ProAccount = {
  id: 'account-openai',
  platform: 'openai',
  authType: 'oauth',
  sourceType: 'auth_file',
  name: 'OpenAI 主账号',
  enabled: true,
  healthStatus: 'healthy',
  allowedModels: [],
  modelMapping: {},
  createdAtMs: 1,
  updatedAtMs: 2,
  version: 3,
};

const creditsResult = (
  availableCount: number,
  credits: ProAccountResetCreditsResult['credits'] = []
): ProAccountResetCreditsResult => ({
  capability: 'supported',
  availableCount,
  credits,
  updatedAtMs: 1,
  retryable: false,
});

const textOf = (value: ReactNode): string => {
  if (typeof value === 'string' || typeof value === 'number') return String(value);
  if (Array.isArray(value)) return value.map(textOf).join('');
  if (value && typeof value === 'object' && 'props' in value) {
    return textOf((value as { props: { children?: ReactNode } }).props.children);
  }
  return '';
};

const buttonByText = (renderer: ReactTestRenderer, text: string) => {
  const button = renderer.root
    .findAllByType('button')
    .find((candidate) => textOf(candidate.props.children).includes(text));
  if (!button) throw new Error(`未找到按钮：${text}`);
  return button;
};

const renderControls = async (onQueryUsage = vi.fn(), onResetCompleted = vi.fn()) => {
  let renderer!: ReactTestRenderer;
  await act(async () => {
    renderer = create(
      <OpenAIQuotaResetControls
        account={account}
        managerBase="https://manager.example"
        managementKey="manager-key"
        usageSource="passive"
        usageLoading={false}
        onQueryUsage={onQueryUsage}
        onResetCompleted={onResetCompleted}
      />
    );
  });
  return renderer;
};

describe('OpenAI 配额次数与重置控制', () => {
  beforeEach(() => {
    apiMocks.resetCredits.mockReset();
    apiMocks.resetOpenAI.mockReset();
    notificationMocks.showConfirmation.mockReset();
    identityMock.mockClear();
  });

  it('固定显示查询、次数和重置，未查询次数时禁用重置', async () => {
    const renderer = await renderControls();

    expect(buttonByText(renderer, '查询')).toBeTruthy();
    expect(buttonByText(renderer, '次数').props.title).toBe('点击查询剩余重置次数');
    expect(buttonByText(renderer, '重置').props.disabled).toBe(true);
    expect(buttonByText(renderer, '重置').props.title).toContain('先点击');

    renderer.unmount();
  });

  it('点击次数只查询重置次数，零次时仍保留禁用的重置按钮', async () => {
    const onQueryUsage = vi.fn();
    apiMocks.resetCredits.mockResolvedValue(creditsResult(0));
    const renderer = await renderControls(onQueryUsage);

    await act(async () => {
      buttonByText(renderer, '次数').props.onClick();
      await Promise.resolve();
    });

    expect(apiMocks.resetCredits).toHaveBeenCalledWith(
      'https://manager.example',
      'manager-key',
      'account-openai'
    );
    expect(onQueryUsage).not.toHaveBeenCalled();
    expect(textOf(buttonByText(renderer, '次数').props.children)).toContain('次数 0');
    expect(buttonByText(renderer, '重置').props.disabled).toBe(true);

    renderer.unmount();
  });

  it('展示最早到期时间并支持展开完整到期明细', async () => {
    apiMocks.resetCredits.mockResolvedValue(
      creditsResult(3, [
        { expiresAtMs: Date.UTC(2026, 6, 5, 4, 5) },
        { expiresAtMs: Date.UTC(2026, 6, 3, 4, 5) },
        { expiresAtMs: Date.UTC(2026, 6, 7, 4, 5) },
      ])
    );
    const renderer = await renderControls();

    await act(async () => {
      buttonByText(renderer, '次数').props.onClick();
      await Promise.resolve();
    });

    const toggle = renderer.root.findByProps({ 'data-testid': 'reset-credit-expiry-toggle' });
    expect(textOf(toggle.props.children)).toBe('+2');
    expect(toggle.props['aria-expanded']).toBe(false);
    await act(async () => toggle.props.onClick());
    expect(
      renderer.root.findByProps({ 'data-testid': 'reset-credit-expiry-details' })
    ).toBeTruthy();

    renderer.unmount();
  });

  it('查询到可用次数后启用重置，确认后刷新次数和用量', async () => {
    const onResetCompleted = vi.fn();
    apiMocks.resetCredits
      .mockResolvedValueOnce(creditsResult(2))
      .mockResolvedValueOnce(creditsResult(1));
    apiMocks.resetOpenAI.mockResolvedValue({
      credits: creditsResult(1),
      operation: { operationId: 'reset-operation' },
    });
    const renderer = await renderControls(vi.fn(), onResetCompleted);

    await act(async () => {
      buttonByText(renderer, '次数').props.onClick();
      await Promise.resolve();
    });
    expect(buttonByText(renderer, '重置').props.disabled).toBe(false);

    act(() => buttonByText(renderer, '重置').props.onClick());
    const confirmation = notificationMocks.showConfirmation.mock.calls[0][0];
    expect(confirmation.title).toBe('确认重置周限');
    expect(confirmation.message).toContain('剩余 2 次');

    await act(async () => {
      await confirmation.onConfirm();
      await Promise.resolve();
    });

    expect(apiMocks.resetOpenAI).toHaveBeenCalledWith(
      'https://manager.example',
      'manager-key',
      account,
      'reset-operation',
      'reset-key'
    );
    expect(apiMocks.resetCredits).toHaveBeenCalledTimes(2);
    expect(onResetCompleted).toHaveBeenCalledTimes(1);
    expect(textOf(buttonByText(renderer, '次数').props.children)).toContain('次数 1');

    renderer.unmount();
  });
});
