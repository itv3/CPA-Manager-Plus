import { describe, expect, it } from 'vitest';
import type { ProAccount, ProAccountBinding } from '@/services/api/proAccounts';
import { buildAccountAuthFileExportPlan } from './accountAuthFileExport';

type AccountOptions = {
  sourceType?: string;
  sourceLocator?: string;
  isCurrent?: boolean;
  withBinding?: boolean;
};

const createAccount = (id: string, options: AccountOptions = {}): ProAccount => {
  const withBinding = options.withBinding !== false;
  const binding = withBinding
    ? ({
        id: Number(id.replace(/\D/g, '')) || 1,
        proAccountId: id,
        sourceType: options.sourceType ?? 'auth_file',
        sourceLocator: options.sourceLocator ?? `${id}.json`,
        bindingStatus: 'current',
        ...(options.isCurrent === undefined ? {} : { isCurrent: options.isCurrent }),
        validFromMs: 1,
        attributionQuality: 'exact',
        firstSeenAtMs: 1,
        lastSeenAtMs: 1,
      } as ProAccountBinding)
    : undefined;

  return {
    id,
    platform: 'openai',
    authType: 'oauth',
    sourceType: options.sourceType ?? 'auth_file',
    enabled: true,
    healthStatus: 'healthy',
    allowedModels: [],
    modelMapping: {},
    createdAtMs: 1,
    updatedAtMs: 1,
    version: 1,
    binding,
  };
};

describe('buildAccountAuthFileExportPlan', () => {
  it('没有选中账号时处理全部传入账号并统计不可导出项', () => {
    const accounts = [
      createAccount('account-1', { sourceLocator: 'first.json' }),
      createAccount('account-2', {
        sourceType: 'config_codex_api_key',
        sourceLocator: 'index:0',
      }),
      createAccount('account-3', { withBinding: false }),
    ];

    expect(buildAccountAuthFileExportPlan(accounts, new Set())).toEqual({
      fileNames: ['first.json'],
      sharedFileNames: [],
      partialSharedFileNames: [],
      targetAccountCount: 3,
      eligibleAccountCount: 1,
      skippedAccountCount: 2,
    });
  });

  it('有选中账号时只处理选中的账号', () => {
    const accounts = [
      createAccount('account-1', { sourceLocator: 'first.json' }),
      createAccount('account-2', { sourceLocator: 'second.json' }),
      createAccount('account-3', {
        sourceType: 'config_openai_compatibility',
        sourceLocator: 'provider:0:key:0',
      }),
    ];

    expect(buildAccountAuthFileExportPlan(accounts, new Set(['account-2', 'account-3']))).toEqual({
      fileNames: ['second.json'],
      sharedFileNames: [],
      partialSharedFileNames: [],
      targetAccountCount: 2,
      eligibleAccountCount: 1,
      skippedAccountCount: 1,
    });
  });

  it('仅接受未明确失效的 auth_file 当前绑定和非空 locator', () => {
    const accounts = [
      createAccount('account-1', { sourceType: ' AUTH_FILE ', sourceLocator: ' valid.json ' }),
      createAccount('account-2', { sourceLocator: 'stale.json', isCurrent: false }),
      createAccount('account-3', { sourceLocator: '   ' }),
    ];

    expect(buildAccountAuthFileExportPlan(accounts, new Set())).toMatchObject({
      fileNames: ['valid.json'],
      targetAccountCount: 3,
      eligibleAccountCount: 1,
      skippedAccountCount: 2,
    });
  });

  it('同一 locator 对应多个账号时只导出一个物理文件并标记共享', () => {
    const accounts = [
      createAccount('account-1', { sourceLocator: 'shared.json' }),
      createAccount('account-2', { sourceLocator: 'shared.json' }),
      createAccount('account-3', { sourceLocator: 'other.json' }),
    ];

    expect(buildAccountAuthFileExportPlan(accounts, new Set())).toEqual({
      fileNames: ['shared.json', 'other.json'],
      sharedFileNames: ['shared.json'],
      partialSharedFileNames: [],
      targetAccountCount: 3,
      eligibleAccountCount: 3,
      skippedAccountCount: 0,
    });
  });

  it('只选中共享文件的部分账号时标记 partialSharedFileNames', () => {
    const accounts = [
      createAccount('account-1', { sourceLocator: 'shared.json' }),
      createAccount('account-2', { sourceLocator: 'shared.json' }),
      createAccount('account-3', { sourceLocator: 'other.json' }),
    ];

    expect(buildAccountAuthFileExportPlan(accounts, new Set(['account-1', 'account-3']))).toEqual({
      fileNames: ['shared.json', 'other.json'],
      sharedFileNames: ['shared.json'],
      partialSharedFileNames: ['shared.json'],
      targetAccountCount: 2,
      eligibleAccountCount: 2,
      skippedAccountCount: 0,
    });
  });

  it('选中共享文件的全部账号时不标记为部分选择', () => {
    const accounts = [
      createAccount('account-1', { sourceLocator: 'shared.json' }),
      createAccount('account-2', { sourceLocator: 'shared.json' }),
      createAccount('account-3', { sourceLocator: 'other.json' }),
    ];

    expect(
      buildAccountAuthFileExportPlan(accounts, new Set(['account-1', 'account-2']))
        .partialSharedFileNames
    ).toEqual([]);
  });
});
