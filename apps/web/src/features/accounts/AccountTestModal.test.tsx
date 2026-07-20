import type { ReactNode } from 'react';
import { act, create, type ReactTestInstance, type ReactTestRenderer } from 'react-test-renderer';
import { beforeEach, describe, expect, it, vi } from 'vitest';
import type { ProAccount } from '@/services/api/proAccounts';

const { apiMocks } = vi.hoisted(() => ({
  apiMocks: {
    modelCatalog: vi.fn(),
    testAccount: vi.fn(),
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

import { AccountTestModal } from './AccountTestModal';

const account: ProAccount = {
  id: 'account-1',
  platform: 'openai',
  authType: 'api',
  sourceType: 'config_openai_compatibility',
  name: '主账号',
  enabled: true,
  healthStatus: 'healthy',
  allowedModels: ['client-model'],
  modelMapping: { 'client-model': 'upstream-model' },
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

const buttonByText = (renderer: ReactTestRenderer, text: string) => {
  const button = renderer.root
    .findAllByType('button')
    .find((candidate) => textOf(candidate.props.children).includes(text));
  if (!button) throw new Error(`未找到按钮：${text}`);
  return button;
};

const deferred = <T,>() => {
  let resolve!: (value: T) => void;
  const promise = new Promise<T>((resolvePromise) => {
    resolve = resolvePromise;
  });
  return { promise, resolve };
};

const successfulConnectivity = (protocol: string) => ({
  account: { ...account, healthStatus: 'healthy', lastTestedAtMs: 1000 },
  connectivity: {
    success: true,
    statusCode: 200,
    protocol,
    model: 'upstream-model',
    responsePreview: 'OK',
    retryable: false,
  },
});

const renderModal = async (testAccount: ProAccount = account) => {
  let renderer!: ReactTestRenderer;
  await act(async () => {
    renderer = create(
      <AccountTestModal
        open
        account={testAccount}
        managerBase="https://manager.example"
        managementKey="manager-key"
        onClose={vi.fn()}
        onTested={vi.fn()}
      />
    );
    await Promise.resolve();
  });
  return renderer;
};

describe('账号连通性测试弹窗', () => {
  beforeEach(() => {
    Object.values(apiMocks).forEach((mock) => mock.mockReset());
    apiMocks.modelCatalog.mockResolvedValue({
      models: ['client-model', 'other-model'],
    });
    apiMocks.testAccount.mockResolvedValue({
      account: { ...account, healthStatus: 'error', lastTestedAtMs: 1000 },
      connectivity: {
        success: false,
        statusCode: 429,
        protocol: 'responses_compact',
        model: 'upstream-model',
        mappedModel: 'upstream-model',
        upstreamModel: 'upstream-model',
        durationMs: 128,
        responsePreview: '{"error":"rate limit"}',
        errorCode: 'rate_limited',
        errorMessage: '当前上游请求过于频繁',
        retryable: true,
      },
    });
  });

  it('Responses 账号:白名单过滤模型下拉并支持 Compact 模式', async () => {
    const onTested = vi.fn();
    // codex(Responses)账号,白名单只含 client-model
    const codexAccount: ProAccount = {
      ...account,
      sourceType: 'config_codex_api_key',
      binding: {
        id: 1,
        proAccountId: 'account-1',
        sourceType: 'config_codex_api_key',
        sourceLocator: 'index:0',
        bindingStatus: 'bound',
        isCurrent: true,
        validFromMs: 0,
        attributionQuality: 'exact',
        firstSeenAtMs: 0,
        lastSeenAtMs: 0,
      },
    };
    let renderer!: ReactTestRenderer;
    await act(async () => {
      renderer = create(
        <AccountTestModal
          open
          account={codexAccount}
          managerBase="https://manager.example"
          managementKey="manager-key"
          onClose={vi.fn()}
          onTested={onTested}
        />
      );
      await Promise.resolve();
    });

    expect(apiMocks.modelCatalog).toHaveBeenCalledWith(
      'https://manager.example',
      'manager-key',
      'account-1'
    );
    const modelSelect = renderer.root.findByProps({ 'aria-label': '连通性测试模型' });
    expect(modelSelect.type).toBe('select');
    expect(modelSelect.props.value).toBe('client-model');
    // 白名单只有 client-model:目录里的 other-model 不应出现在下拉中
    const optionValues = renderer.root.findAllByType('option').map((option) => option.props.value);
    expect(optionValues).toContain('client-model');
    expect(optionValues).not.toContain('other-model');

    const modeSelect = renderer.root.findByProps({ 'aria-label': '连通性测试模式' });
    act(() => modeSelect.props.onChange({ target: { value: 'compact' } }));
    await act(async () => {
      buttonByText(renderer, '开始测试').props.onClick();
      await Promise.resolve();
    });

    expect(apiMocks.testAccount).toHaveBeenCalledWith(
      'https://manager.example',
      'manager-key',
      codexAccount,
      'client-model',
      'compact',
      expect.any(String),
      expect.any(String)
    );
    expect(renderer.root.findByProps({ 'data-modal-width': 512 })).toBeTruthy();
    expect(treeText(renderer.root)).toContain('开始测试账号: 主账号');
    expect(treeText(renderer.root)).toContain('账号类型: apikey');
    expect(treeText(renderer.root)).toContain('已连接到 API');
    expect(treeText(renderer.root)).toContain('使用模型: upstream-model');
    expect(treeText(renderer.root)).toContain('发送测试消息: "hi"');
    expect(treeText(renderer.root)).toContain('响应:');
    expect(treeText(renderer.root)).toContain('当前上游请求过于频繁');
    expect(treeText(renderer.root)).toContain('{"error":"rate limit"}');
    expect(treeText(renderer.root)).not.toContain('已通过 ');
    expect(treeText(renderer.root)).toContain('重试');
    expect(onTested).toHaveBeenCalledTimes(1);
    expect(onTested).toHaveBeenCalledWith(
      expect.objectContaining({ id: 'account-1', lastTestedAtMs: 1000 })
    );
  });

  it('Chat Completions 账号:显示常规模式但不提供 Compact', async () => {
    let renderer!: ReactTestRenderer;
    await act(async () => {
      renderer = create(
        <AccountTestModal
          open
          account={account}
          managerBase="https://manager.example"
          managementKey="manager-key"
          onClose={vi.fn()}
          onTested={vi.fn()}
        />
      );
      await Promise.resolve();
    });

    // Chat Completions 账号保留同款模式字段，但不能选择不存在的 Compact 端点。
    const modeSelect = renderer.root.findByProps({ 'aria-label': '连通性测试模式' });
    expect(modeSelect.props.value).toBe('default');
    expect(modeSelect.findAllByType('option').map((option) => option.props.value)).toEqual([
      'default',
    ]);
    await act(async () => {
      buttonByText(renderer, '开始测试').props.onClick();
      await Promise.resolve();
    });
    expect(apiMocks.testAccount.mock.calls[0][4]).toBe('default');
  });

  it('Chat Completions 请求中显示测试路径，成功后显示已验证路径', async () => {
    const pending = deferred<ReturnType<typeof successfulConnectivity>>();
    apiMocks.testAccount.mockReturnValue(pending.promise);
    const renderer = await renderModal();

    await act(async () => {
      buttonByText(renderer, '开始测试').props.onClick();
      await Promise.resolve();
    });
    expect(treeText(renderer.root)).toContain('正在通过 /v1/chat/completions 测试连接');

    await act(async () => {
      pending.resolve(successfulConnectivity('chat_completions'));
      await pending.promise;
    });
    expect(treeText(renderer.root)).toContain('已通过 /v1/chat/completions 验证');
  });

  it.each([
    ['responses', 'default', '/v1/responses'],
    ['responses_compact', 'compact', '/v1/responses/compact'],
  ] as const)('%s 成功时显示对应验证路径', async (protocol, testMode, endpointPath) => {
    apiMocks.testAccount.mockResolvedValue(successfulConnectivity(protocol));
    const responsesAccount: ProAccount = {
      ...account,
      sourceType: 'config_codex_api_key',
    };
    const renderer = await renderModal(responsesAccount);
    if (testMode === 'compact') {
      act(() =>
        renderer.root
          .findByProps({ 'aria-label': '连通性测试模式' })
          .props.onChange({ target: { value: 'compact' } })
      );
    }

    await act(async () => {
      buttonByText(renderer, '开始测试').props.onClick();
      await Promise.resolve();
    });
    expect(treeText(renderer.root)).toContain(`已通过 ${endpointPath} 验证`);
  });

  it('未知来源和未知响应协议回退为通用连接文案', async () => {
    const pending = deferred<ReturnType<typeof successfulConnectivity>>();
    apiMocks.testAccount.mockReturnValue(pending.promise);
    const unknownAccount: ProAccount = {
      ...account,
      platform: 'future-platform',
      sourceType: 'future-source',
    };
    const renderer = await renderModal(unknownAccount);

    await act(async () => {
      buttonByText(renderer, '开始测试').props.onClick();
      await Promise.resolve();
    });
    expect(treeText(renderer.root)).toContain('正在连接到 API...');

    await act(async () => {
      pending.resolve(successfulConnectivity('future_protocol'));
      await pending.promise;
    });
    expect(treeText(renderer.root)).toContain('已连接到 API');
    expect(treeText(renderer.root)).not.toContain('已通过 ');
  });

  it('成功时显示实际响应和完成状态', async () => {
    apiMocks.testAccount.mockResolvedValue({
      account: { ...account, healthStatus: 'healthy', lastTestedAtMs: 1000 },
      connectivity: {
        success: true,
        statusCode: 200,
        protocol: 'chat_completions',
        model: 'upstream-model',
        mappedModel: 'upstream-model',
        upstreamModel: 'gpt-5.6-sol',
        durationMs: 321,
        responsePreview: 'Hi! What can I help you with?',
        retryable: false,
      },
    });
    let renderer!: ReactTestRenderer;
    await act(async () => {
      renderer = create(
        <AccountTestModal
          open
          account={account}
          managerBase="https://manager.example"
          managementKey="manager-key"
          onClose={vi.fn()}
          onTested={vi.fn()}
        />
      );
      await Promise.resolve();
    });

    await act(async () => {
      buttonByText(renderer, '开始测试').props.onClick();
      await Promise.resolve();
    });

    expect(treeText(renderer.root)).toContain('Hi! What can I help you with?');
    expect(treeText(renderer.root)).toContain('测试完成!');
    expect(treeText(renderer.root)).toContain('提示词: "hi"');
    expect(treeText(renderer.root)).toContain('active');
    expect(buttonByText(renderer, '重试')).toBeTruthy();
  });

  it('模型目录失败时仍允许手工输入，非 Responses 账号只用常规模式', async () => {
    apiMocks.modelCatalog.mockRejectedValue(new Error('catalog unavailable'));
    const anthropicAccount: ProAccount = {
      ...account,
      id: 'account-2',
      platform: 'anthropic',
      sourceType: 'config_claude_api_key',
      allowedModels: [],
      modelMapping: {},
    };
    let renderer!: ReactTestRenderer;
    await act(async () => {
      renderer = create(
        <AccountTestModal
          open
          account={anthropicAccount}
          managerBase="https://manager.example"
          managementKey="manager-key"
          onClose={vi.fn()}
          onTested={vi.fn()}
        />
      );
      await Promise.resolve();
    });

    expect(treeText(renderer.root)).toContain('模型目录加载失败，可手工输入模型后继续测试');
    expect(renderer.root.findAllByProps({ 'aria-label': '连通性测试模式' })).toHaveLength(0);
    act(() =>
      renderer.root.findByProps({ 'aria-label': '连通性测试模型' }).props.onChange({
        target: { value: 'claude-test' },
      })
    );
    await act(async () => {
      buttonByText(renderer, '开始测试').props.onClick();
      await Promise.resolve();
    });

    expect(apiMocks.testAccount.mock.calls[0][4]).toBe('default');
  });
});
