import { act, create, type ReactTestRenderer } from 'react-test-renderer';
import { beforeEach, describe, expect, it, vi } from 'vitest';
import type { ProAccount } from '@/services/api/proAccounts';
import { OfficialClientCompatibilityPanel } from './OfficialClientCompatibilityPanel';

const apiMocks = vi.hoisted(() => ({
  list: vi.fn(),
  details: vi.fn(),
  update: vi.fn(),
}));

vi.mock('@/services/api/proAccounts', () => ({ proAccountsApi: apiMocks }));

vi.mock('react-i18next', async (importOriginal) => {
  const actual = await importOriginal<typeof import('react-i18next')>();
  return {
    ...actual,
    useTranslation: () => ({
      t: (key: string, values?: { account?: string }) =>
        values?.account ? `${key}:${values.account}` : key,
    }),
  };
});

const account: ProAccount = {
  id: 'account-1',
  platform: 'anthropic',
  authType: 'api',
  sourceType: 'config_claude_api_key',
  name: 'anyrouter',
  enabled: true,
  healthStatus: 'healthy',
  allowedModels: ['claude-opus-4-8'],
  modelMapping: {},
  createdAtMs: 1,
  updatedAtMs: 1,
  version: 3,
};

const openAIAccount: ProAccount = {
  ...account,
  id: 'account-2',
  platform: 'openai',
  sourceType: 'config_codex_api_key',
  name: 'free',
  allowedModels: ['gpt-5.5'],
  createdAtMs: 2,
  updatedAtMs: 2,
};

describe('OfficialClientCompatibilityPanel', () => {
  beforeEach(() => {
    vi.clearAllMocks();
    apiMocks.list.mockImplementation(
      async (_managerBase: string, _managementKey: string, params: { platform?: string }) => ({
        items: params.platform === 'anthropic' ? [account] : [],
        total: params.platform === 'anthropic' ? 1 : 0,
      })
    );
    apiMocks.update.mockResolvedValue({ account: { ...account, version: 4 } });
    apiMocks.details
      .mockResolvedValueOnce({
        item: account,
        editable: {
          headers: {},
          sharedProvider: false,
          officialClientCompatibilitySupported: true,
          officialClientCompatibility: { enabled: false, profile: '', tlsProfile: '' },
        },
      })
      .mockResolvedValueOnce({
        item: { ...account, version: 4 },
        editable: {
          headers: {},
          sharedProvider: false,
          officialClientCompatibilitySupported: true,
          officialClientCompatibility: {
            enabled: true,
            profile: 'claude-desktop-2.1.215-v1',
            tlsProfile: '',
          },
        },
      });
  });

  it('loads API key accounts and verifies the compatibility state after toggling', async () => {
    let renderer!: ReactTestRenderer;
    await act(async () => {
      renderer = create(
        <OfficialClientCompatibilityPanel
          managerBase="http://manager.test"
          managementKey="admin-key"
          available
        />
      );
    });

    const toggle = renderer.root.findByProps({
      'aria-label': 'config_management.visual.sections.pro.compatibility_switch_aria:anyrouter',
    });
    expect(toggle.props.checked).toBe(false);

    await act(async () => {
      toggle.props.onChange({ target: { checked: true } });
    });

    expect(apiMocks.update).toHaveBeenCalledWith(
      'http://manager.test',
      'admin-key',
      'account-1',
      expect.objectContaining({
        expectedVersion: 3,
        officialClientCompatibility: {
          enabled: true,
          profile: 'claude-desktop-2.1.215-v1',
          tlsProfile: '',
        },
      })
    );
    expect(apiMocks.details).toHaveBeenCalledTimes(2);
    expect(apiMocks.list).toHaveBeenCalledWith(
      'http://manager.test',
      'admin-key',
      expect.objectContaining({ platform: 'anthropic', authType: 'api' })
    );
    expect(apiMocks.list).toHaveBeenCalledWith(
      'http://manager.test',
      'admin-key',
      expect.objectContaining({ platform: 'openai', authType: 'api' })
    );

    act(() => {
      renderer.unmount();
    });
  });

  it('lists every supported API key account and sorts enabled accounts first', async () => {
    apiMocks.list.mockImplementation(
      async (_managerBase: string, _managementKey: string, params: { platform?: string }) => ({
        items:
          params.platform === 'anthropic'
            ? [account]
            : [
                openAIAccount,
                {
                  ...openAIAccount,
                  id: 'unsupported-account',
                  sourceType: 'config_openai_compatibility',
                },
              ],
        total: params.platform === 'anthropic' ? 1 : 2,
      })
    );
    apiMocks.details.mockReset();
    apiMocks.details.mockImplementation(
      async (_managerBase: string, _managementKey: string, id: string) => {
        const item = id === openAIAccount.id ? openAIAccount : account;
        return {
          item,
          editable: {
            headers: {},
            sharedProvider: false,
            officialClientCompatibilitySupported: true,
            officialClientCompatibility: {
              enabled: id === openAIAccount.id,
              profile:
                id === openAIAccount.id
                  ? 'codex-desktop-0.145.0-alpha.18-v1'
                  : 'claude-desktop-2.1.215-v1',
              tlsProfile: '',
            },
          },
        };
      }
    );

    let renderer!: ReactTestRenderer;
    await act(async () => {
      renderer = create(
        <OfficialClientCompatibilityPanel
          managerBase="http://manager.test"
          managementKey="admin-key"
          available
        />
      );
    });

    const accountNames = renderer.root
      .findAll((node) => node.props.className?.includes('accountName'))
      .map((node) => node.children.join(''));
    expect(accountNames).toEqual(['free', 'anyrouter']);
    expect(apiMocks.details).toHaveBeenCalledTimes(2);

    act(() => {
      renderer.unmount();
    });
  });
});
