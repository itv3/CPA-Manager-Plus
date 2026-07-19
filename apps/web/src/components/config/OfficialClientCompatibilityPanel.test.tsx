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

describe('OfficialClientCompatibilityPanel', () => {
  beforeEach(() => {
    vi.clearAllMocks();
    apiMocks.list.mockResolvedValue({ items: [account], total: 1 });
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

    act(() => {
      renderer.unmount();
    });
  });
});
