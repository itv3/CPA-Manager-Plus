import type { ReactNode } from 'react';
import { act, create, type ReactTestInstance, type ReactTestRenderer } from 'react-test-renderer';
import { beforeEach, describe, expect, it, vi } from 'vitest';

const { apiMocks } = vi.hoisted(() => ({
  apiMocks: {
    probe: vi.fn(),
    createAPI: vi.fn(),
    startOAuth: vi.fn(),
    oauthStatus: vi.fn(),
    submitOAuthCallback: vi.fn(),
    cancelDraft: vi.fn(),
    completeDraft: vi.fn(),
    createVertex: vi.fn(),
    modelCatalog: vi.fn(),
    staticModelCatalog: vi.fn(),
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
  }: {
    open: boolean;
    children: ReactNode;
    footer?: ReactNode;
  }) =>
    open ? (
      <div>
        <div>{children}</div>
        <div>{footer}</div>
      </div>
    ) : null,
}));

import { AccountWizardModal } from './AccountWizardModal';

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
  const buttons = renderer.root.findAllByType('button');
  // 优先精确匹配,避免"保存"命中含相同字样的说明文本
  const button =
    buttons.find((candidate) => textOf(candidate.props.children).trim() === text) ??
    buttons.find((candidate) => textOf(candidate.props.children).includes(text));
  if (!button) throw new Error(`未找到按钮：${text}`);
  return button;
};

const inputBy = (renderer: ReactTestRenderer, predicate: (input: ReactTestInstance) => boolean) => {
  const input = renderer.root.findAllByType('input').find(predicate);
  if (!input) throw new Error('未找到输入框');
  return input;
};

const renderAPIWizard = async (officialClientCompatibility = false) => {
  let renderer!: ReactTestRenderer;
  await act(async () => {
    renderer = create(
      <AccountWizardModal
        open
        managerBase="https://manager.example"
        managementKey="manager-key"
        capabilities={{
          credentialDraft: true,
          allowedModels: true,
          officialClientCompatibility,
          stores: {},
        }}
        onClose={vi.fn()}
        onSaved={vi.fn()}
      />
    );
  });
  act(() => buttonByText(renderer, 'API填写').props.onClick());
  act(() =>
    inputBy(renderer, (input) => input.props.placeholder === 'OpenAI API').props.onChange({
      target: { value: '测试 API 账号' },
    })
  );
  act(() =>
    renderer.root
      .findAllByType('textarea')
      .find((textarea) => textarea.props.placeholder === '可选，用于记录账号用途、归属或其他说明')
      ?.props.onChange({ target: { value: 'API 账号备注' } })
  );
  return renderer;
};

const renderOAuthWizard = async (onSaved = vi.fn(), onClose = vi.fn()) => {
  let renderer!: ReactTestRenderer;
  await act(async () => {
    renderer = create(
      <AccountWizardModal
        open
        managerBase="https://manager.example"
        managementKey="manager-key"
        onClose={onClose}
        onSaved={onSaved}
      />
    );
  });
  act(() =>
    inputBy(renderer, (input) => input.props.placeholder === 'OpenAI OAuth').props.onChange({
      target: { value: '测试 OAuth 账号' },
    })
  );
  act(() =>
    renderer.root
      .findAllByType('textarea')
      .find((textarea) => textarea.props.placeholder === '可选，用于记录账号用途、归属或其他说明')
      ?.props.onChange({ target: { value: 'OAuth 账号备注' } })
  );
  return renderer;
};

describe('API Key 账号添加向导', () => {
  beforeEach(() => {
    Object.values(apiMocks).forEach((mock) => mock.mockReset());
    apiMocks.createAPI.mockResolvedValue({ savedDisabled: false });
  });

  it('API 方式仅提供单个保存入口和双同步动作', async () => {
    const renderer = await renderAPIWizard();

    expect(
      inputBy(
        renderer,
        (input) => input.props.placeholder === '输入自定义模型名称，可使用末尾通配符 *'
      )
    ).toBeDefined();
    expect(buttonByText(renderer, '保存')).toBeDefined();
    expect(buttonByText(renderer, '同步最新支持模型')).toBeDefined();
    expect(buttonByText(renderer, '同步上游支持的模型')).toBeDefined();
    expect(buttonByText(renderer, '清除所有模型')).toBeDefined();
    expect(treeText(renderer.root)).not.toContain('连通性测试模型');
    expect(apiMocks.probe).not.toHaveBeenCalled();
  });

  it('保存无需填写测试模型且每次生成独立操作标识', async () => {
    const renderer = await renderAPIWizard();
    act(() =>
      inputBy(renderer, (input) => input.props.type === 'password').props.onChange({
        target: { value: 'secret-key' },
      })
    );

    await act(async () => {
      buttonByText(renderer, '保存').props.onClick();
      await Promise.resolve();
    });

    expect(apiMocks.createAPI).toHaveBeenCalledTimes(1);
    const firstCall = apiMocks.createAPI.mock.calls[0][2];
    expect(firstCall).toMatchObject({
      name: '测试 API 账号',
      notes: 'API 账号备注',
      apiKey: 'secret-key',
      protocolMode: 'auto',
      allowedModels: [],
      testModel: '',
      saveDisabledOnTestFailure: true,
    });
    expect(firstCall.operationId).toMatch(/^account-save-/);

    // 保存成功后 API Key 会被清空,重新填写再次保存
    act(() =>
      inputBy(renderer, (input) => input.props.type === 'password').props.onChange({
        target: { value: 'secret-key' },
      })
    );
    await act(async () => {
      buttonByText(renderer, '保存').props.onClick();
      await Promise.resolve();
    });
    const secondCall = apiMocks.createAPI.mock.calls[1][2];
    expect(secondCall.operationId).not.toBe(firstCall.operationId);
  });

  it('同步上游支持的模型直接填入白名单列表', async () => {
    apiMocks.probe.mockResolvedValue({
      probe: {
        sourceType: 'config_codex_api_key',
        selectedProtocol: 'responses',
        models: ['synced-a', 'synced-b'],
        upstreamModels: ['synced-a', 'synced-b'],
        modelsStatus: 'supported',
        warnings: [],
      },
    });
    const renderer = await renderAPIWizard();
    act(() =>
      inputBy(renderer, (input) => input.props.type === 'password').props.onChange({
        target: { value: 'secret-key' },
      })
    );

    await act(async () => {
      buttonByText(renderer, '同步上游支持的模型').props.onClick();
      await Promise.resolve();
    });

    const probeInput = apiMocks.probe.mock.calls[0][2];
    expect(probeInput.operationId).toMatch(/^account-probe-/);
    expect(probeInput.model).toBeUndefined();
    expect(treeText(renderer.root)).toContain('synced-a');
    expect(treeText(renderer.root)).toContain('2 个模型');
    expect(renderer.root.findByProps({ 'aria-label': '移除模型 synced-b' })).toBeDefined();
  });

  it('同步最新支持模型使用内置目录且不依赖凭证', async () => {
    apiMocks.staticModelCatalog.mockResolvedValue({
      models: ['gpt-5.5', 'gpt-5.6-sol'],
      builtIn: ['gpt-5.5', 'gpt-5.6-sol'],
      upstream: [],
      manual: [],
    });
    const renderer = await renderAPIWizard();

    await act(async () => {
      buttonByText(renderer, '同步最新支持模型').props.onClick();
      await Promise.resolve();
    });

    expect(apiMocks.staticModelCatalog).toHaveBeenCalledWith(
      'https://manager.example',
      'manager-key',
      'openai',
      'api'
    );
    expect(apiMocks.probe).not.toHaveBeenCalled();
    expect(treeText(renderer.root)).toContain('gpt-5.6-sol');
    expect(treeText(renderer.root)).toContain('2 个模型');
  });

  it('代理 URL 随探测与创建请求一起提交', async () => {
    apiMocks.probe.mockResolvedValue({
      probe: {
        sourceType: 'config_codex_api_key',
        selectedProtocol: 'responses',
        models: ['gpt-test'],
        upstreamModels: ['gpt-test'],
        modelsStatus: 'supported',
        warnings: [],
      },
    });
    const renderer = await renderAPIWizard();
    act(() =>
      inputBy(renderer, (input) => input.props.type === 'password').props.onChange({
        target: { value: 'secret-key' },
      })
    );
    act(() =>
      inputBy(
        renderer,
        (input) => input.props.placeholder === 'http:// 或 socks5://，留空直连'
      ).props.onChange({
        target: { value: 'socks5://127.0.0.1:1080' },
      })
    );

    await act(async () => {
      buttonByText(renderer, '同步上游支持的模型').props.onClick();
      await Promise.resolve();
    });
    expect(apiMocks.probe).toHaveBeenCalledWith(
      'https://manager.example',
      'manager-key',
      expect.objectContaining({ proxyUrl: 'socks5://127.0.0.1:1080' })
    );

    await act(async () => {
      buttonByText(renderer, '保存').props.onClick();
      await Promise.resolve();
    });
    expect(apiMocks.createAPI).toHaveBeenCalledWith(
      'https://manager.example',
      'manager-key',
      expect.objectContaining({
        proxyUrl: 'socks5://127.0.0.1:1080',
        allowedModels: ['gpt-test'],
        testModel: 'gpt-test',
      })
    );
  });

  it('仅在 OpenAI Responses 下提交官方客户端兼容开关', async () => {
    const renderer = await renderAPIWizard(true);
    const protocol = renderer.root
      .findAllByType('select')
      .find((candidate) => candidate.props.value === 'auto');
    if (!protocol) throw new Error('未找到协议模式');
    act(() => protocol.props.onChange({ target: { value: 'responses' } }));

    const compatibility = renderer.root.findByProps({ 'aria-label': '官方客户端兼容' });
    expect(compatibility.props.disabled).toBe(false);
    act(() => compatibility.props.onChange({ target: { checked: true } }));
    act(() =>
      inputBy(renderer, (input) => input.props.type === 'password').props.onChange({
        target: { value: 'secret-key' },
      })
    );

    await act(async () => {
      buttonByText(renderer, '保存').props.onClick();
      await Promise.resolve();
    });

    expect(apiMocks.createAPI.mock.calls[0][2]).toMatchObject({
      protocolMode: 'responses',
      officialClientCompatibility: { enabled: true, profile: '', tlsProfile: '' },
    });
  });

  it('Chat Completions 隐藏兼容开关，旧 Gateway 下开关保持禁用', async () => {
    const renderer = await renderAPIWizard(false);
    const protocol = renderer.root
      .findAllByType('select')
      .find((candidate) => candidate.props.value === 'auto');
    if (!protocol) throw new Error('未找到协议模式');

    act(() => protocol.props.onChange({ target: { value: 'responses' } }));
    expect(renderer.root.findByProps({ 'aria-label': '官方客户端兼容' }).props.disabled).toBe(true);

    act(() => protocol.props.onChange({ target: { value: 'chat_completions' } }));
    expect(renderer.root.findAllByProps({ 'aria-label': '官方客户端兼容' })).toHaveLength(0);
  });
});

describe('OAuth 账号添加向导', () => {
  beforeEach(() => {
    Object.values(apiMocks).forEach((mock) => mock.mockReset());
    apiMocks.startOAuth.mockResolvedValue({
      oauth: { url: 'https://login.example/authorize', state: 'oauth-state' },
      status: 'wait',
    });
    apiMocks.submitOAuthCallback.mockResolvedValue({ status: 'wait' });
    apiMocks.oauthStatus.mockResolvedValue({
      status: 'ok',
      account: { id: 'oauth-account', version: 3, allowedModels: [] },
    });
    apiMocks.modelCatalog.mockResolvedValue({ models: ['gpt-test'], upstream: ['gpt-test'] });
    apiMocks.completeDraft.mockResolvedValue({
      account: { id: 'oauth-account' },
      operation: { state: 'enabled' },
    });
  });

  it('第一步只进入授权页，不会提前生成链接或打开网页', async () => {
    const renderer = await renderOAuthWizard();

    expect(treeText(renderer.root)).not.toContain('点击底部“开始授权”');
    await act(async () => {
      buttonByText(renderer, '下一步').props.onClick();
    });

    expect(treeText(renderer.root)).toContain('OpenAI 账户授权');
    expect(treeText(renderer.root)).toContain('生成授权链接');
    expect(apiMocks.startOAuth).not.toHaveBeenCalled();
  });

  it('粘贴完整回调地址后仅保留 Code，并自动完成草稿账号', async () => {
    const onSaved = vi.fn();
    const onClose = vi.fn();
    const renderer = await renderOAuthWizard(onSaved, onClose);
    act(() => buttonByText(renderer, '下一步').props.onClick());

    await act(async () => {
      buttonByText(renderer, '生成授权链接').props.onClick();
      await Promise.resolve();
    });
    expect(apiMocks.startOAuth).toHaveBeenCalledTimes(1);
    expect(apiMocks.startOAuth.mock.calls[0][2]).toMatchObject({
      name: '测试 OAuth 账号',
      notes: 'OAuth 账号备注',
    });
    expect(treeText(renderer.root)).toContain('打开授权链接');

    const callbackInput = renderer.root
      .findAllByType('textarea')
      .find((textarea) => String(textarea.props.placeholder).includes('完整回调地址'));
    if (!callbackInput) throw new Error('未找到 OAuth 回调输入框');
    act(() =>
      callbackInput.props.onChange({
        target: {
          value:
            'http://localhost:1455/auth/callback?code=authorization-code&scope=openid+email&state=oauth-state',
        },
      })
    );
    expect(callbackInput.props.value).toBe('authorization-code');

    await act(async () => {
      buttonByText(renderer, '完成授权').props.onClick();
      await Promise.resolve();
    });
    await vi.waitFor(() => expect(apiMocks.completeDraft).toHaveBeenCalledTimes(1));

    expect(apiMocks.submitOAuthCallback).toHaveBeenCalledWith(
      'https://manager.example',
      'manager-key',
      expect.stringMatching(/^account-oauth-/),
      'authorization-code',
      'oauth-state'
    );
    expect(apiMocks.completeDraft).toHaveBeenCalledWith(
      'https://manager.example',
      'manager-key',
      'oauth-account',
      expect.objectContaining({
        operationId: expect.stringMatching(/^account-oauth-/),
        expectedVersion: 3,
        testModel: 'gpt-test',
      })
    );
    expect(onSaved).toHaveBeenCalledTimes(1);
    expect(onClose).toHaveBeenCalledTimes(1);
  });

  it('返回配置会取消已生成的 OAuth 会话', async () => {
    apiMocks.cancelDraft.mockResolvedValue({ status: 'cancelled' });
    const renderer = await renderOAuthWizard();
    act(() => buttonByText(renderer, '下一步').props.onClick());
    await act(async () => {
      buttonByText(renderer, '生成授权链接').props.onClick();
      await Promise.resolve();
    });

    await act(async () => {
      buttonByText(renderer, '返回').props.onClick();
      await Promise.resolve();
    });

    expect(apiMocks.cancelDraft).toHaveBeenCalledWith(
      'https://manager.example',
      'manager-key',
      expect.stringMatching(/^account-oauth-/)
    );
    expect(treeText(renderer.root)).toContain('选择平台');
  });
});
