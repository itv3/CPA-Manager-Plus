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
    expect(input.apiKey).toBeUndefined();
    expect(input.baseUrl).toBeUndefined();
    expect(input.officialClientCompatibility).toEqual({
      enabled: true,
      profile: 'codex-desktop-0.145.0-alpha.18-v1',
      tlsProfile: '',
    });
  });

  it('Gateway 未声明能力时显示禁用开关且不提交变更', async () => {
    const renderer = await renderModal({ officialClientCompatibilitySupported: false });
    const compatibility = renderer.root.findByProps({ 'aria-label': '官方客户端兼容' });
    expect(compatibility.props.disabled).toBe(true);
    expect(treeText(renderer.root)).toContain('当前 Gateway 不支持');
  });
});
