import type { ReactNode } from 'react';
import { act, create, type ReactTestInstance, type ReactTestRenderer } from 'react-test-renderer';
import { beforeEach, describe, expect, it, vi } from 'vitest';
import type { ProAccount } from '@/services/api/proAccounts';

const { apiMocks, notificationMocks } = vi.hoisted(() => ({
  apiMocks: {
    listScheduledTests: vi.fn(),
    modelCatalog: vi.fn(),
    listScheduledTestResults: vi.fn(),
    updateScheduledTest: vi.fn(),
    createScheduledTest: vi.fn(),
    deleteScheduledTest: vi.fn(),
  },
  notificationMocks: {
    showConfirmation: vi.fn(),
    showNotification: vi.fn(),
  },
}));

vi.mock('@/services/api/proAccounts', () => ({ proAccountsApi: apiMocks }));
vi.mock('@/stores', () => ({ useNotificationStore: () => notificationMocks }));
vi.mock('@/components/ui/Modal', () => ({
  Modal: ({
    open,
    title,
    children,
    footer,
  }: {
    open: boolean;
    title: ReactNode;
    children: ReactNode;
    footer?: ReactNode;
  }) =>
    open ? (
      <div>
        <h1>{title}</h1>
        {children}
        {footer}
      </div>
    ) : null,
}));
vi.mock('@/components/ui/Select', () => ({
  Select: ({
    value,
    options,
    onChange,
    ariaLabel,
  }: {
    value: string;
    options: Array<{ value: string; label: string }>;
    onChange: (value: string) => void;
    ariaLabel?: string;
  }) => (
    <select value={value} aria-label={ariaLabel} onChange={(event) => onChange(event.target.value)}>
      {options.map((option) => (
        <option key={option.value} value={option.value}>
          {option.label}
        </option>
      ))}
    </select>
  ),
}));

import { AccountScheduledTestsModal } from './AccountScheduledTestsModal';

const account: ProAccount = {
  id: 'account-1',
  platform: 'openai',
  authType: 'oauth',
  sourceType: 'auth_file',
  name: '主账号',
  enabled: true,
  healthStatus: 'healthy',
  allowedModels: ['gpt-5.5'],
  modelMapping: {},
  createdAtMs: 1,
  updatedAtMs: 2,
  version: 3,
};

const plan = {
  id: 1,
  proAccountId: 'account-1',
  modelId: 'gpt-5.5',
  cronExpression: '*/30 * * * *',
  enabled: true,
  maxResults: 100,
  autoRecover: true,
  nextRunAtMs: Date.now() + 60_000,
  createdAtMs: 1,
  updatedAtMs: 2,
};

const treeText = (node: ReactTestInstance | string): string =>
  typeof node === 'string' ? node : node.children.map(treeText).join('');

const textOf = (value: ReactNode): string => {
  if (typeof value === 'string' || typeof value === 'number') return String(value);
  if (Array.isArray(value)) return value.map(textOf).join('');
  if (value && typeof value === 'object' && 'props' in value) {
    return textOf((value as { props: { children?: ReactNode } }).props.children);
  }
  return '';
};

describe('账号定时测试面板', () => {
  beforeEach(() => {
    Object.values(apiMocks).forEach((mock) => mock.mockReset());
    Object.values(notificationMocks).forEach((mock) => mock.mockReset());
    apiMocks.listScheduledTests.mockResolvedValue([plan]);
    apiMocks.modelCatalog.mockResolvedValue({ models: ['gpt-5.5', 'gpt-5.6'] });
    apiMocks.listScheduledTestResults.mockResolvedValue([
      {
        id: 1,
        planId: 1,
        status: 'success',
        latencyMs: 128,
        responseText: 'ok',
        startedAtMs: Date.now(),
        createdAtMs: Date.now(),
      },
    ]);
    apiMocks.updateScheduledTest.mockImplementation(
      async (
        _base: string,
        _key: string,
        _accountId: string,
        _planId: number,
        input: { enabled?: boolean }
      ) => ({
        ...plan,
        enabled: input.enabled ?? plan.enabled,
      })
    );
  });

  it('加载计划、展开历史结果，并可切换计划状态', async () => {
    let renderer!: ReactTestRenderer;
    await act(async () => {
      renderer = create(
        <AccountScheduledTestsModal
          open
          account={account}
          managerBase="https://manager.example"
          managementKey="manager-key"
          onClose={vi.fn()}
        />
      );
      await Promise.resolve();
      await Promise.resolve();
    });

    expect(apiMocks.listScheduledTests).toHaveBeenCalledWith(
      'https://manager.example',
      'manager-key',
      'account-1'
    );
    expect(treeText(renderer.root)).toContain('gpt-5.5');
    expect(treeText(renderer.root)).not.toContain('自动恢复');

    const planButton = renderer.root
      .findAllByType('button')
      .find((button) => textOf(button.props.children).includes('gpt-5.5'));
    if (!planButton) throw new Error('未找到定时测试计划');
    await act(async () => {
      planButton.props.onClick();
      await Promise.resolve();
    });
    expect(apiMocks.listScheduledTestResults).toHaveBeenCalledWith(
      'https://manager.example',
      'manager-key',
      'account-1',
      1
    );
    expect(treeText(renderer.root)).toContain('128ms');

    const toggle = renderer.root.findByProps({ 'aria-label': '停用定时测试计划' });
    await act(async () => {
      toggle.props.onChange({ target: { checked: false } });
      await Promise.resolve();
    });
    expect(apiMocks.updateScheduledTest).toHaveBeenCalledWith(
      'https://manager.example',
      'manager-key',
      'account-1',
      1,
      { enabled: false }
    );

    renderer.unmount();
  });

  it('添加计划时展示模型、Cron 和保留结果，不暴露未接入的自动恢复开关', async () => {
    apiMocks.listScheduledTests.mockResolvedValue([]);
    let renderer!: ReactTestRenderer;
    await act(async () => {
      renderer = create(
        <AccountScheduledTestsModal
          open
          account={account}
          managerBase="https://manager.example"
          managementKey="manager-key"
          onClose={vi.fn()}
        />
      );
      await Promise.resolve();
      await Promise.resolve();
    });
    const addButton = renderer.root
      .findAllByType('button')
      .find((button) => textOf(button.props.children).includes('添加计划'));
    if (!addButton) throw new Error('未找到添加计划按钮');
    act(() => addButton.props.onClick());

    const text = treeText(renderer.root);
    expect(text).toContain('Cron 表达式');
    expect(text).toContain('最大保留结果');
    expect(text).not.toContain('成功后自动恢复账号');
    expect(renderer.root.findByProps({ 'aria-label': '定时测试模型' })).toBeTruthy();

    renderer.unmount();
  });
});
