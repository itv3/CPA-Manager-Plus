import type { ReactNode } from 'react';
import { act, create, type ReactTestInstance, type ReactTestRenderer } from 'react-test-renderer';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import type { ProAccount } from '@/services/api/proAccounts';

(globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;

const {
  apiMocks,
  authFileMocks,
  downloadMocks,
  navigationMock,
  notificationMocks,
  uploadPreparationMocks,
} = vi.hoisted(() => ({
  apiMocks: {
    bindingReviews: vi.fn(),
    capabilities: vi.fn(),
    list: vi.fn(),
    sync: vi.fn(),
  },
  authFileMocks: {
    downloadBlob: vi.fn(),
    uploadFiles: vi.fn(),
  },
  downloadMocks: {
    downloadBlob: vi.fn(),
  },
  navigationMock: vi.fn(),
  notificationMocks: {
    showConfirmation: vi.fn(),
    showNotification: vi.fn(),
  },
  uploadPreparationMocks: {
    prepareAuthFilesForUpload: vi.fn(),
  },
}));

vi.mock('react-i18next', () => ({
  useTranslation: () => ({
    t: (key: string, options?: { defaultValue?: string }) => options?.defaultValue ?? key,
  }),
}));

vi.mock('react-router-dom', () => ({
  useNavigate: () => navigationMock,
}));

vi.mock('@/hooks/usePanelFeatureAvailability', () => ({
  usePanelFeatureAvailability: () => ({
    checking: false,
    managerServiceBase: 'https://manager.example',
  }),
}));

vi.mock('@/stores', () => ({
  useAuthStore: (selector: (state: { managementKey: string }) => unknown) =>
    selector({ managementKey: 'manager-key' }),
  useNotificationStore: (selector?: (state: typeof notificationMocks) => unknown) =>
    selector ? selector(notificationMocks) : notificationMocks,
}));

vi.mock('@/services/api/proAccounts', () => ({
  proAccountsApi: apiMocks,
}));

vi.mock('@/services/api/authFiles', () => ({
  authFilesApi: authFileMocks,
}));

vi.mock('@/features/authFiles/authFileUpload', () => ({
  prepareAuthFilesForUpload: uploadPreparationMocks.prepareAuthFilesForUpload,
}));

vi.mock('@/utils/download', () => ({
  downloadBlob: downloadMocks.downloadBlob,
}));

vi.mock('@/components/ui/DropdownMenu', () => ({
  DropdownMenu: ({
    ariaLabel,
    disabled,
    items,
  }: {
    ariaLabel: string;
    disabled?: boolean;
    items: Array<{
      key: string;
      label: ReactNode;
      disabled?: boolean;
      onClick: () => void | Promise<void>;
    }>;
  }) => (
    <div aria-label={ariaLabel}>
      {items.map((item) => (
        <button
          key={item.key}
          type="button"
          data-menu-key={item.key}
          disabled={disabled || item.disabled}
          onClick={item.onClick}
        >
          {item.label}
        </button>
      ))}
    </div>
  ),
}));

vi.mock('@/components/ui/Select', () => ({ Select: () => null }));
vi.mock('@/components/ui/ToggleSwitch', () => ({ ToggleSwitch: () => null }));
vi.mock('./AccountBatchModal', () => ({ AccountBatchModal: () => null }));
vi.mock('./AccountBindingReviewModal', () => ({ AccountBindingReviewModal: () => null }));
vi.mock('./AccountEditModal', () => ({ AccountEditModal: () => null }));
vi.mock('./AccountReauthorizeModal', () => ({ AccountReauthorizeModal: () => null }));
vi.mock('./AccountScheduledTestsModal', () => ({ AccountScheduledTestsModal: () => null }));
vi.mock('./AccountStatsModal', () => ({ AccountStatsModal: () => null }));
vi.mock('./AccountTestModal', () => ({
  AccountTestModal: ({
    open,
    account,
    onTested,
  }: {
    open: boolean;
    account: ProAccount | null;
    onTested: (account: ProAccount) => void;
  }) =>
    open && account ? (
      <button
        type="button"
        data-testid="complete-account-test"
        onClick={() =>
          onTested({
            ...account,
            healthStatus: 'healthy',
            lastTestedAtMs: Date.now(),
          })
        }
      >
        完成账号测试
      </button>
    ) : null,
}));
vi.mock('./AccountWizardModal', () => ({ AccountWizardModal: () => null }));

import { AccountsPage } from './AccountsPage';

const treeText = (node: ReactTestInstance | string): string =>
  typeof node === 'string' ? node : node.children.map(treeText).join('');

const createAccount = (
  id: string,
  sourceType: string,
  sourceLocator: string,
  bindingID: number
): ProAccount => ({
  id,
  platform: 'openai',
  authType: 'oauth',
  sourceType,
  name: `账号 ${id}`,
  enabled: true,
  healthStatus: 'healthy',
  allowedModels: [],
  modelMapping: {},
  createdAtMs: 1,
  updatedAtMs: 2,
  version: 1,
  binding: {
    id: bindingID,
    proAccountId: id,
    sourceType,
    sourceLocator,
    bindingStatus: 'bound',
    isCurrent: true,
    validFromMs: 1,
    attributionQuality: 'exact',
    firstSeenAtMs: 1,
    lastSeenAtMs: 2,
  },
});

const accounts: ProAccount[] = [
  createAccount('auth-account-1', 'auth_file', 'shared-auth.json', 1),
  createAccount('auth-account-2', ' AUTH_FILE ', 'shared-auth.json', 2),
  createAccount('config-account', 'config_codex_api_key', 'index:0', 3),
];

type ConfirmationOptions = {
  title?: string;
  message: ReactNode;
  onConfirm: () => void | Promise<void>;
};

let renderer: ReactTestRenderer | null = null;

const renderPage = async () => {
  await act(async () => {
    renderer = create(<AccountsPage />);
  });
  await act(async () => {
    await vi.advanceTimersByTimeAsync(250);
    await Promise.resolve();
  });
  if (!renderer) throw new Error('账号页面未渲染');
  expect(apiMocks.list).toHaveBeenCalled();
  return renderer;
};

const exportButton = (page: ReactTestRenderer) =>
  page.root.findByProps({ 'data-menu-key': 'export-auth-files' });

const latestConfirmation = (): ConfirmationOptions => {
  const calls = notificationMocks.showConfirmation.mock.calls;
  const options = calls[calls.length - 1]?.[0] as ConfirmationOptions | undefined;
  if (!options) throw new Error('未触发导出确认弹窗');
  return options;
};

describe('统一账号页 CPA 认证文件导入导出', () => {
  beforeEach(() => {
    vi.useFakeTimers();
    vi.stubGlobal('window', {
      setTimeout: globalThis.setTimeout,
      clearTimeout: globalThis.clearTimeout,
      setInterval: globalThis.setInterval,
      clearInterval: globalThis.clearInterval,
    });

    Object.values(apiMocks).forEach((mock) => mock.mockReset());
    Object.values(authFileMocks).forEach((mock) => mock.mockReset());
    Object.values(downloadMocks).forEach((mock) => mock.mockReset());
    Object.values(notificationMocks).forEach((mock) => mock.mockReset());
    Object.values(uploadPreparationMocks).forEach((mock) => mock.mockReset());
    navigationMock.mockReset();

    apiMocks.list.mockResolvedValue({ items: accounts, total: accounts.length });
    apiMocks.sync.mockResolvedValue({
      dryRun: false,
      discovered: accounts.length,
      created: 1,
      updated: 0,
      pending: 0,
      conflicts: 0,
      items: [],
    });
    apiMocks.capabilities.mockResolvedValue({
      credentialDraft: true,
      allowedModels: true,
      stores: {},
    });
    apiMocks.bindingReviews.mockResolvedValue({ items: [], total: 0 });
    authFileMocks.uploadFiles.mockResolvedValue({
      status: 'ok',
      uploaded: 1,
      files: ['cpa-account.json'],
      failed: [],
    });
    authFileMocks.downloadBlob.mockResolvedValue(
      new Blob(['{"access_token":"secret"}'], { type: 'application/json' })
    );
    uploadPreparationMocks.prepareAuthFilesForUpload.mockImplementation(async (files: File[]) => ({
      files,
      failures: [],
      convertedSourceCount: 0,
    }));
  });

  afterEach(() => {
    act(() => renderer?.unmount());
    renderer = null;
    vi.clearAllTimers();
    vi.useRealTimers();
    vi.unstubAllGlobals();
  });

  it('导入有效 CPA JSON 后依次上传、同步并重新读取账号列表', async () => {
    const page = await renderPage();
    apiMocks.list.mockClear();
    apiMocks.sync.mockClear();
    apiMocks.bindingReviews.mockClear();

    const file = new File(['{"type":"codex","access_token":"test-token"}'], 'cpa-account.json', {
      type: 'application/json',
    });
    const input = page.root.findAllByType('input').find((node) => node.props.type === 'file');
    if (!input) throw new Error('未找到认证文件输入框');
    const inputTarget = { files: [file], value: 'cpa-account.json' };

    await act(async () => {
      await input.props.onChange({ currentTarget: inputTarget });
    });

    expect(uploadPreparationMocks.prepareAuthFilesForUpload).toHaveBeenCalledWith([file]);
    expect(authFileMocks.uploadFiles).toHaveBeenCalledWith([file]);
    expect(apiMocks.sync).toHaveBeenCalledWith('https://manager.example', 'manager-key');
    expect(apiMocks.list).toHaveBeenCalledTimes(1);
    expect(authFileMocks.uploadFiles.mock.invocationCallOrder[0]).toBeLessThan(
      apiMocks.sync.mock.invocationCallOrder[0]
    );
    expect(apiMocks.sync.mock.invocationCallOrder[0]).toBeLessThan(
      apiMocks.list.mock.invocationCallOrder[0]
    );
  });

  it('无选中账号时确认后按去重文件名导出当前结果并跳过非认证文件账号', async () => {
    const page = await renderPage();
    const rawBlob = new Blob(['\ufeff{"access_token":"secret"}'], {
      type: 'application/json',
    });
    authFileMocks.downloadBlob.mockResolvedValue(rawBlob);

    act(() => exportButton(page).props.onClick());
    const confirmation = latestConfirmation();
    expect(String(confirmation.message)).toContain('1 个非认证文件账号将被跳过');

    await act(async () => {
      await confirmation.onConfirm();
    });

    expect(authFileMocks.downloadBlob).toHaveBeenCalledTimes(1);
    expect(authFileMocks.downloadBlob).toHaveBeenCalledWith('shared-auth.json');
    expect(downloadMocks.downloadBlob).toHaveBeenCalledTimes(1);
    expect(downloadMocks.downloadBlob).toHaveBeenCalledWith({
      filename: 'shared-auth.json',
      blob: rawBlob,
    });
    expect(notificationMocks.showNotification).toHaveBeenCalledWith(
      expect.stringContaining('跳过 1 个非认证文件账号'),
      'success'
    );
  });

  it('导出确认文案明确提示完整 JSON 含未脱敏敏感凭证', async () => {
    const page = await renderPage();

    act(() => exportButton(page).props.onClick());
    const confirmation = latestConfirmation();

    expect(confirmation.title).toBe('导出 CPA 认证文件');
    expect(String(confirmation.message)).toContain('完整 JSON 认证文件');
    expect(String(confirmation.message)).toContain('未脱敏的 Token 等敏感凭证');
    expect(String(confirmation.message)).toContain('请妥善保管');
    expect(String(confirmation.message)).toContain('当前筛选或勾选范围外账号的凭据');
  });

  it('勾选账号后在更多操作中显示批量删除入口', async () => {
    const page = await renderPage();
    const selectAll = page.root.findByProps({ 'aria-label': '选择当前页全部账号' });

    act(() => selectAll.props.onChange({ target: { checked: true } }));

    const deleteButton = page.root.findByProps({ 'data-menu-key': 'delete-selected-accounts' });
    expect(deleteButton.props.children).toBe('批量删除所选账号 (3)');
  });

  it('按调度与最近使用排序，并仅为 OAuth 显示授权账号', async () => {
    const apiAccount = {
      ...createAccount('api-account-id', 'config_codex_api_key', 'index:0', 10),
      authType: 'api',
      name: 'AnyRouter 主账号',
      email: 'api-identity@example.com',
      enabled: true,
      lastUsedAtMs: 1000,
    };
    const oauthAccount = {
      ...createAccount('oauth-account-id', 'auth_file', 'oauth.json', 11),
      name: 'OpenAI 授权账号',
      email: 'oauth-identity@example.com',
      enabled: false,
      lastUsedAtMs: 2000,
    };
    apiMocks.list.mockResolvedValue({ items: [oauthAccount, apiAccount], total: 2 });

    const page = await renderPage();
    const rows = page.root.findAllByType('tbody')[0].findAllByType('tr');
    expect(treeText(rows[0])).toContain('AnyRouter 主账号');
    expect(treeText(rows[0])).not.toContain('api-identity@example.com');
    expect(treeText(rows[0])).not.toContain('api-account-id');
    expect(treeText(rows[1])).toContain('OpenAI 授权账号');
    expect(treeText(rows[1])).toContain('oauth-identity@example.com');
    expect(treeText(rows[1])).not.toContain('oauth-account-id');
  });

  it('测试完成后只更新当前账号，不重新读取整个列表', async () => {
    const page = await renderPage();
    apiMocks.list.mockClear();

    const testButton = page.root.findAllByProps({ 'data-menu-key': 'test' })[0];
    act(() => testButton.props.onClick());
    const completeButton = page.root.findByProps({ 'data-testid': 'complete-account-test' });
    act(() => completeButton.props.onClick());

    expect(apiMocks.list).not.toHaveBeenCalled();
    expect(treeText(page.root.findByType('tbody'))).toContain('测试 刚刚');
  });

  it('上传返回异常状态但已有成功文件时以警告呈现状态', async () => {
    const page = await renderPage();
    authFileMocks.uploadFiles.mockResolvedValue({
      status: 'partial',
      uploaded: 1,
      files: ['cpa-account.json'],
      failed: [],
    });
    const file = new File(['{"type":"codex"}'], 'cpa-account.json', {
      type: 'application/json',
    });
    const input = page.root.findAllByType('input').find((node) => node.props.type === 'file');
    if (!input) throw new Error('未找到认证文件输入框');

    await act(async () => {
      await input.props.onChange({
        currentTarget: { files: [file], value: 'cpa-account.json' },
      });
    });

    expect(notificationMocks.showNotification).toHaveBeenCalledWith(
      expect.stringContaining('CPA 返回异常状态 partial'),
      'warning'
    );
  });
});
