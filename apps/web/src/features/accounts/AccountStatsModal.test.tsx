import type { ReactNode } from 'react';
import { act, create, type ReactTestInstance, type ReactTestRenderer } from 'react-test-renderer';
import { beforeEach, describe, expect, it, vi } from 'vitest';
import type { ProAccount, ProAccountUsageResponse } from '@/services/api/proAccounts';

const { apiMocks } = vi.hoisted(() => ({
  apiMocks: {
    usage: vi.fn(),
  },
}));

vi.mock('@/services/api/proAccounts', () => ({
  proAccountsApi: apiMocks,
}));

vi.mock('@/components/ui/Modal', () => ({
  Modal: ({
    open,
    children,
    footer,
    title,
    width,
  }: {
    open: boolean;
    children: ReactNode;
    footer?: ReactNode;
    title?: ReactNode;
    width?: number | string;
  }) =>
    open ? (
      <div data-modal-width={width}>
        <h1>{title}</h1>
        {children}
        {footer}
      </div>
    ) : null,
}));

import { AccountStatsModal } from './AccountStatsModal';

const account: ProAccount = {
  id: 'account-1',
  platform: 'openai',
  authType: 'oauth',
  sourceType: 'auth_file',
  name: '主账号',
  email: 'owner@example.com',
  enabled: true,
  healthStatus: 'healthy',
  allowedModels: [],
  modelMapping: {},
  createdAtMs: 1,
  updatedAtMs: 2,
  version: 3,
};

const nowMs = Date.now();
const usageResponse: ProAccountUsageResponse = {
  source: 'passive',
  updatedAtMs: nowMs,
  officialWindows: [
    {
      id: 'five-hour',
      label: '5 小时',
      usedPercent: 25,
      remainingPercent: 75,
      resetAtMs: nowMs + 2 * 60 * 60 * 1_000,
      source: 'official',
    },
  ],
  local: {
    fromMs: nowMs - 60 * 60 * 1_000,
    toMs: nowMs,
    requests: 100,
    successes: 96,
    failures: 4,
    inputTokens: 1_200,
    outputTokens: 600,
    cachedTokens: 300,
    cacheReadTokens: 240,
    cacheCreationTokens: 60,
    reasoningTokens: 150,
    totalTokens: 2_250,
    estimatedCost: 12.345,
    costKnown: true,
    lastActivityAtMs: nowMs - 1_000,
  },
  retryable: false,
};

const textOf = (value: ReactNode): string => {
  if (typeof value === 'string' || typeof value === 'number') return String(value);
  if (Array.isArray(value)) return value.map(textOf).join('');
  if (value && typeof value === 'object' && 'props' in value) {
    return textOf((value as { props: { children?: ReactNode } }).props.children);
  }
  return '';
};

const treeText = (node: ReactTestInstance | string): string =>
  typeof node === 'string' ? node : node.children.map(treeText).join('');

const buttonByText = (renderer: ReactTestRenderer, text: string) => {
  const button = renderer.root
    .findAllByType('button')
    .find((candidate) => textOf(candidate.props.children).includes(text));
  if (!button) throw new Error(`未找到按钮：${text}`);
  return button;
};

const renderModal = async (onUsageLoaded = vi.fn()) => {
  let renderer!: ReactTestRenderer;
  await act(async () => {
    renderer = create(
      <AccountStatsModal
        open
        account={account}
        managerBase="https://manager.example"
        managementKey="manager-key"
        onClose={vi.fn()}
        onUsageLoaded={onUsageLoaded}
      />
    );
    await Promise.resolve();
    await Promise.resolve();
  });
  return renderer;
};

describe('账号用量统计弹窗', () => {
  beforeEach(() => {
    apiMocks.usage.mockReset();
    apiMocks.usage.mockResolvedValue(usageResponse);
  });

  it('打开时被动加载并展示官方窗口、本地统计与单一成本', async () => {
    const onUsageLoaded = vi.fn();
    const renderer = await renderModal(onUsageLoaded);

    expect(apiMocks.usage).toHaveBeenCalledWith(
      'https://manager.example',
      'manager-key',
      'account-1',
      'passive',
      false
    );
    expect(onUsageLoaded).toHaveBeenCalledWith(usageResponse, 'passive');
    expect(renderer.root.findByProps({ 'data-modal-width': 900 })).toBeTruthy();
    const text = treeText(renderer.root);
    expect(text).toContain('账号用量统计');
    expect(text).toContain('主账号');
    expect(text).toContain('官方用量窗口');
    expect(text).toContain('已用 25%');
    expect(text).toContain('成功请求');
    expect(text).toContain('输入 Token');
    expect(text).toContain('缓存 Token');
    expect(text).toContain('推理 Token');
    expect(text).toContain('总 Token');
    expect(text).toContain('$12.35');
    expect(text).toContain('上游响应记录');
    expect(text).not.toContain('用户计费');
    expect(text).not.toContain('标准成本');

    renderer.unmount();
  });

  it('分别使用主动官方查询和被动本地刷新参数', async () => {
    const renderer = await renderModal();
    apiMocks.usage.mockClear();

    await act(async () => {
      buttonByText(renderer, '查询官方配额').props.onClick();
      await Promise.resolve();
      await Promise.resolve();
    });
    expect(apiMocks.usage).toHaveBeenLastCalledWith(
      'https://manager.example',
      'manager-key',
      'account-1',
      'active',
      true
    );

    await act(async () => {
      buttonByText(renderer, '刷新本地统计').props.onClick();
      await Promise.resolve();
      await Promise.resolve();
    });
    expect(apiMocks.usage).toHaveBeenLastCalledWith(
      'https://manager.example',
      'manager-key',
      'account-1',
      'passive',
      false
    );

    renderer.unmount();
  });

  it('保留本地统计并显示官方查询错误摘要', async () => {
    apiMocks.usage.mockResolvedValue({
      ...usageResponse,
      officialWindows: [],
      errorCode: 'official_usage_unsupported',
      errorMessage: '该账号类型暂不支持官方主动用量查询',
      retryable: false,
    });
    const renderer = await renderModal();
    const text = treeText(renderer.root);

    expect(text).toContain('暂无官方配额窗口');
    expect(text).toContain('官方配额查询未完成');
    expect(text).toContain('该账号类型暂不支持官方主动用量查询');
    expect(text).toContain('请求总数');

    renderer.unmount();
  });

  it('展示父页面按优先级合并后的官方窗口', async () => {
    const resolvedUsage: ProAccountUsageResponse = {
      ...usageResponse,
      source: 'official',
      officialWindows: [
        {
          id: 'active-official',
          label: '主动官方窗口',
          usedPercent: 40,
          source: 'official',
        },
      ],
    };
    const renderer = await renderModal(vi.fn(() => resolvedUsage));

    expect(treeText(renderer.root)).toContain('主动官方窗口');
    renderer.unmount();
  });
});
