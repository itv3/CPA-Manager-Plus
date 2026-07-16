import type { ProAccount } from '@/services/api/proAccounts';

export const LEGACY_ACCOUNT_ADVANCED_ROOTS = ['/ai-providers', '/auth-files', '/oauth'] as const;

// 旧页面继续服务高级字段和旧书签，但不再作为一级导航入口。
export const isLegacyAccountAdvancedPath = (path: string) =>
  LEGACY_ACCOUNT_ADVANCED_ROOTS.some((root) => path === root || path.startsWith(`${root}/`));

export const filterLegacyAccountPrimaryNavigation = <T extends { path: string }>(items: T[]) =>
  items.filter((item) => !LEGACY_ACCOUNT_ADVANCED_ROOTS.some((root) => root === item.path));

export const advancedAccountPath = (account: Pick<ProAccount, 'authType'>) =>
  account.authType === 'oauth' || account.authType === 'vertex' ? '/auth-files' : '/ai-providers';
