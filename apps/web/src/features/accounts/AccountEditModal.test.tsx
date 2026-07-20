import type { ReactNode } from 'react';
import { act, create, type ReactTestInstance, type ReactTestRenderer } from 'react-test-renderer';
import { beforeEach, describe, expect, it, vi } from 'vitest';
import type { ProAccount } from '@/services/api/proAccounts';

const { apiMocks } = vi.hoisted(() => ({
  apiMocks: {
    details: vi.fn(),
    modelCatalog: vi.fn(),
    staticModelCatalog: vi.fn(),
    update: vi.fn(),
  },
}));

vi.mock('@/services/api/proAccounts', async (importOriginal) => {
  const actual = await importOriginal<typeof import('@/services/api/proAccounts')>();
  return { ...actual, proAccountsApi: apiMocks };
});

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

vi.mock('./AccountModelRulesEditor', () => ({
  AccountModelRulesEditor: () => <div>模型规则</div>,
}));

import { AccountEditModal } from './AccountEditModal';

const account: ProAccount = {
  id: 'account-1',
  platform: 'openai',
  authType: 'api',
  sourceType: 'config_codex_api_key',
  name: 'Codex API',
  notes: '生产环境账号',
  enabled: true,
  healthStatus: 'healthy',
  allowedModels: [],
  modelMapping: {},
  createdAtMs: 1,
  updatedAtMs: 2,
  version: 7,
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
    .find((candidate) => textOf(candidate.props.children).trim() === text);
  if (!button) throw new Error(`未找到按钮：${text}`);
  return button;
};

const renderModal = async (editableOverrides: Record<string, unknown> = {}) => {
  apiMocks.details.mockResolvedValue({
    item: account,
    editable: {
      baseUrl: 'https://api.openai.com/v1',
      proxyUrl: '',
      headers: {},
      sharedProvider: false,
      officialClientCompatibilitySupported: true,
      officialClientCompatibility: {
        enabled: false,
        profile: 'codex-desktop-0.145.0-alpha.18-v1',
        tlsProfile: '',
      },
      ...editableOverrides,
    },
  });
  apiMocks.modelCatalog.mockResolvedValue({ models: [] });
  apiMocks.update.mockResolvedValue({ account });
  let renderer!: ReactTestRenderer;
  await act(async () => {
    renderer = create(
      <AccountEditModal
        open
        account={account}
        managerBase="https://manager.example"
        managementKey="manager-key"
        onClose={vi.fn()}
        onSaved={vi.fn()}
      />
    );
    await Promise.resolve();
  });
  return renderer;
};

describe('统一账号官方客户端兼容编辑', () => {
  beforeEach(() => {
    Object.values(apiMocks).forEach((mock) => mock.mockReset());
  });

  it('展示实时 Profile/TLS，单独切换时不要求 API Key', async () => {
    const renderer = await renderModal();
    expect(treeText(renderer.root)).toContain('codex-desktop-0.145.0-alpha.18-v1');
    expect(treeText(renderer.root)).toContain('默认 Transport');

    const compatibility = renderer.root.findByProps({ 'aria-label': '官方客户端兼容' });
    act(() => compatibility.props.onChange({ target: { checked: true } }));
    await act(async () => {
      buttonByText(renderer, '保存').props.onClick();
      await Promise.resolve();
    });

    const input = apiMocks.update.mock.calls[0][3];
    expect(input.notes).toBe('生产环境账号');
    expect(input.apiKey).toBeUndefined();
    expect(input.baseUrl).toBeUndefined();
    expect(input.officialClientCompatibility).toEqual({
      enabled: true,
      profile: 'codex-desktop-0.145.0-alpha.18-v1',
      tlsProfile: '',
    });
  });

  it('只修改名称和备注时不提交任何凭证测试参数', async () => {
    const renderer = await renderModal();
    const nameInput = renderer.root.findByProps({ value: 'Codex API' });
    const notesInput = renderer.root.findByProps({ value: '生产环境账号' });
    act(() => nameInput.props.onChange({ target: { value: '新的账号名称' } }));
    act(() => notesInput.props.onChange({ target: { value: '新的备注' } }));

    await act(async () => {
      buttonByText(renderer, '保存').props.onClick();
      await Promise.resolve();
    });

    const input = apiMocks.update.mock.calls[0][3];
    expect(input.name).toBe('新的账号名称');
    expect(input.notes).toBe('新的备注');
    expect(input.apiKey).toBeUndefined();
    expect(input.baseUrl).toBeUndefined();
    expect(input.headers).toBeUndefined();
    expect(input.testModel).toBeUndefined();
  });

  it('保存失败后保留新密钥，再次保存仍提交同一凭证变更', async () => {
    const renderer = await renderModal();
    apiMocks.update.mockRejectedValueOnce(new Error('该 API Key 已绑定到另一个账号'));
    const keyInput = renderer.root.findByProps({ type: 'password' });
    act(() => keyInput.props.onChange({ target: { value: 'replacement-secret' } }));

    await act(async () => {
      buttonByText(renderer, '保存').props.onClick();
      await Promise.resolve();
      await Promise.resolve();
    });

    expect(treeText(renderer.root)).toContain('该 API Key 已绑定到另一个账号');
    expect(renderer.root.findByProps({ type: 'password' }).props.value).toBe('replacement-secret');
    expect(apiMocks.update.mock.calls[0][3].apiKey).toBe('replacement-secret');

    apiMocks.update.mockResolvedValueOnce({ account });
    await act(async () => {
      buttonByText(renderer, '保存').props.onClick();
      await Promise.resolve();
      await Promise.resolve();
    });

    expect(apiMocks.update).toHaveBeenCalledTimes(2);
    expect(apiMocks.update.mock.calls[1][3].apiKey).toBe('replacement-secret');
  });

  it('Gateway 未声明能力时显示禁用开关且不提交变更', async () => {
    const renderer = await renderModal({ officialClientCompatibilitySupported: false });
    const compatibility = renderer.root.findByProps({ 'aria-label': '官方客户端兼容' });
    expect(compatibility.props.disabled).toBe(true);
    expect(treeText(renderer.root)).toContain('当前 Gateway 不支持');
  });
});
