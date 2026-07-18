import type { ReactNode } from 'react';
import { act, create, type ReactTestInstance, type ReactTestRenderer } from 'react-test-renderer';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import type { ProAccount } from '@/services/api/proAccounts';

(globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;

const { apiMocks, notificationMocks } = vi.hoisted(() => ({
  apiMocks: {
    startReauthorization: vi.fn(),
    reauthorizationStatus: vi.fn(),
    submitReauthorizationCallback: vi.fn(),
    cancelReauthorization: vi.fn(),
  },
  notificationMocks: {
    showNotification: vi.fn(),
  },
}));

vi.mock('@/services/api/proAccounts', () => ({ proAccountsApi: apiMocks }));
vi.mock('@/stores', () => ({ useNotificationStore: () => notificationMocks }));
vi.mock('@/utils/clipboard', () => ({ copyToClipboard: vi.fn().mockResolvedValue(true) }));
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

import { AccountReauthorizeModal } from './AccountReauthorizeModal';

const account: ProAccount = {
  id: 'account-openai',
  platform: 'openai',
  authType: 'oauth',
  sourceType: 'auth_file',
  name: 'OpenAI 主账号',
  enabled: true,
  healthStatus: 'healthy',
  allowedModels: ['gpt-5'],
  modelMapping: {},
  createdAtMs: 1,
  updatedAtMs: 2,
  version: 3,
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

describe('账号重新授权弹窗', () => {
  beforeEach(() => {
    vi.useFakeTimers();
    vi.stubGlobal('window', {
      setTimeout: globalThis.setTimeout,
      clearTimeout: globalThis.clearTimeout,
      open: vi.fn(),
    });
    Object.values(apiMocks).forEach((mock) => mock.mockReset());
    notificationMocks.showNotification.mockReset();
    apiMocks.startReauthorization.mockResolvedValue({
      operation: { operationId: 'reauthorize-operation' },
      oauth: { url: 'https://auth.example/authorize', state: 'oauth-state' },
      status: 'wait',
    });
    apiMocks.reauthorizationStatus.mockResolvedValue({ status: 'wait' });
    apiMocks.submitReauthorizationCallback.mockResolvedValue({ status: 'wait' });
    apiMocks.cancelReauthorization.mockResolvedValue({ status: 'cancelled' });
  });

  afterEach(() => {
    vi.clearAllTimers();
    vi.useRealTimers();
    vi.unstubAllGlobals();
  });

  it('提交回调后保持等待并继续轮询，不会把 wait 误判为成功', async () => {
    const onCompleted = vi.fn();
    let renderer!: ReactTestRenderer;
    await act(async () => {
      renderer = create(
        <AccountReauthorizeModal
          open
          account={account}
          managerBase="https://manager.example"
          managementKey="manager-key"
          onClose={vi.fn()}
          onCompleted={onCompleted}
        />
      );
    });
    await act(async () => {
      await vi.advanceTimersByTimeAsync(0);
      await Promise.resolve();
    });

    const callbackInput = renderer.root.findByProps({
      placeholder: '粘贴浏览器最终跳转地址，或直接粘贴授权码',
    });
    act(() =>
      callbackInput.props.onChange({ target: { value: 'https://localhost/callback?code=ok' } })
    );
    const submitButton = renderer.root
      .findAllByType('button')
      .find((button) => textOf(button.props.children).includes('提交授权结果'));
    if (!submitButton) throw new Error('未找到提交授权结果按钮');

    await act(async () => {
      submitButton.props.onClick();
      await Promise.resolve();
      await Promise.resolve();
    });

    expect(apiMocks.submitReauthorizationCallback).toHaveBeenCalled();
    expect(onCompleted).not.toHaveBeenCalled();
    expect(notificationMocks.showNotification).not.toHaveBeenCalledWith(
      '账号重新授权成功',
      'success'
    );
    expect(treeText(renderer.root)).toContain('等待同一账号完成授权');

    await act(async () => {
      await vi.advanceTimersByTimeAsync(2_000);
    });
    expect(apiMocks.reauthorizationStatus).toHaveBeenCalledTimes(1);

    await act(async () => {
      renderer.unmount();
    });
  });

  it('上一轮状态请求未返回时不会发起重叠轮询', async () => {
    let resolveStatus!: (value: { status: string }) => void;
    apiMocks.reauthorizationStatus.mockImplementation(
      () =>
        new Promise((resolve) => {
          resolveStatus = resolve;
        })
    );
    let renderer!: ReactTestRenderer;
    await act(async () => {
      renderer = create(
        <AccountReauthorizeModal
          open
          account={account}
          managerBase="https://manager.example"
          managementKey="manager-key"
          onClose={vi.fn()}
          onCompleted={vi.fn()}
        />
      );
    });
    await act(async () => {
      await vi.advanceTimersByTimeAsync(0);
      await vi.advanceTimersByTimeAsync(2_000);
    });
    expect(apiMocks.reauthorizationStatus).toHaveBeenCalledTimes(1);

    await act(async () => {
      await vi.advanceTimersByTimeAsync(6_000);
    });
    expect(apiMocks.reauthorizationStatus).toHaveBeenCalledTimes(1);

    await act(async () => {
      resolveStatus({ status: 'wait' });
      await Promise.resolve();
      await vi.advanceTimersByTimeAsync(2_000);
    });
    expect(apiMocks.reauthorizationStatus).toHaveBeenCalledTimes(2);

    await act(async () => {
      renderer.unmount();
    });
  });

  it('父组件更新账号对象和回调时不会重新启动或取消当前会话', async () => {
    let renderer!: ReactTestRenderer;
    await act(async () => {
      renderer = create(
        <AccountReauthorizeModal
          open
          account={account}
          managerBase="https://manager.example"
          managementKey="manager-key"
          onClose={vi.fn()}
          onCompleted={vi.fn()}
        />
      );
    });
    await act(async () => {
      await vi.advanceTimersByTimeAsync(0);
    });
    expect(apiMocks.startReauthorization).toHaveBeenCalledTimes(1);

    const updatedAccount = { ...account, version: 4, updatedAtMs: 10 };
    await act(async () => {
      renderer.update(
        <AccountReauthorizeModal
          open
          account={updatedAccount}
          managerBase="https://manager.example"
          managementKey="manager-key"
          onClose={vi.fn()}
          onCompleted={vi.fn()}
        />
      );
      await vi.advanceTimersByTimeAsync(10_000);
    });

    expect(apiMocks.startReauthorization).toHaveBeenCalledTimes(1);
    expect(apiMocks.cancelReauthorization).not.toHaveBeenCalled();
    expect(apiMocks.reauthorizationStatus).toHaveBeenCalled();

    await act(async () => {
      renderer.unmount();
    });
  });

  it('明确重新生成时取消旧会话，关闭时只取消新会话', async () => {
    apiMocks.startReauthorization
      .mockResolvedValueOnce({
        operation: { operationId: 'reauthorize-operation-1' },
        oauth: { url: 'https://auth.example/authorize-1', state: 'oauth-state-1' },
        status: 'wait',
      })
      .mockResolvedValueOnce({
        operation: { operationId: 'reauthorize-operation-2' },
        oauth: { url: 'https://auth.example/authorize-2', state: 'oauth-state-2' },
        status: 'wait',
      });
    const onClose = vi.fn();
    let renderer!: ReactTestRenderer;
    await act(async () => {
      renderer = create(
        <AccountReauthorizeModal
          open
          account={account}
          managerBase="https://manager.example"
          managementKey="manager-key"
          onClose={onClose}
          onCompleted={vi.fn()}
        />
      );
    });
    await act(async () => {
      await vi.advanceTimersByTimeAsync(0);
    });

    const regenerateButton = renderer.root
      .findAllByType('button')
      .find((button) => textOf(button.props.children).includes('重新生成'));
    if (!regenerateButton) throw new Error('未找到重新生成按钮');
    await act(async () => {
      regenerateButton.props.onClick();
      await Promise.resolve();
      await Promise.resolve();
    });

    expect(apiMocks.startReauthorization).toHaveBeenCalledTimes(2);
    expect(apiMocks.cancelReauthorization).toHaveBeenCalledTimes(1);
    expect(apiMocks.cancelReauthorization).toHaveBeenNthCalledWith(
      1,
      'https://manager.example',
      'manager-key',
      account.id,
      'reauthorize-operation-1'
    );

    const closeButton = renderer.root
      .findAllByType('button')
      .find((button) => textOf(button.props.children).trim() === '关闭');
    if (!closeButton) throw new Error('未找到关闭按钮');
    await act(async () => {
      closeButton.props.onClick();
      await Promise.resolve();
    });

    expect(apiMocks.cancelReauthorization).toHaveBeenCalledTimes(2);
    expect(apiMocks.cancelReauthorization).toHaveBeenNthCalledWith(
      2,
      'https://manager.example',
      'manager-key',
      account.id,
      'reauthorize-operation-2'
    );
    expect(onClose).toHaveBeenCalledTimes(1);

    await act(async () => {
      renderer.unmount();
    });
  });
});
