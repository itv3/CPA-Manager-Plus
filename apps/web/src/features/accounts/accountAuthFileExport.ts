import type { ProAccount } from '@/services/api/proAccounts';

export interface AccountAuthFileExportPlan {
  fileNames: string[];
  sharedFileNames: string[];
  partialSharedFileNames: string[];
  targetAccountCount: number;
  eligibleAccountCount: number;
  skippedAccountCount: number;
}

const resolveCurrentAuthFileLocator = (account: ProAccount): string | null => {
  const binding = account.binding;
  if (!binding || binding.isCurrent === false) return null;
  if (binding.sourceType.trim().toLowerCase() !== 'auth_file') return null;

  const locator = binding.sourceLocator.trim();
  return locator || null;
};

/**
 * 根据统一账号的当前绑定生成认证文件导出计划。
 *
 * 有选中账号时仅处理选中项；没有选中账号时处理传入的全部账号。
 * 一个物理认证文件可能绑定多个账号，因此文件名按 locator 去重，同时保留账号级统计。
 */
export function buildAccountAuthFileExportPlan(
  accounts: ProAccount[],
  selectedIDs: ReadonlySet<string>
): AccountAuthFileExportPlan {
  const selectionActive = selectedIDs.size > 0;
  const targetAccounts = selectionActive
    ? accounts.filter((account) => selectedIDs.has(account.id))
    : accounts;

  const allAccountIDsByLocator = new Map<string, Set<string>>();
  accounts.forEach((account) => {
    const locator = resolveCurrentAuthFileLocator(account);
    if (!locator) return;

    const accountIDs = allAccountIDsByLocator.get(locator) ?? new Set<string>();
    accountIDs.add(account.id);
    allAccountIDsByLocator.set(locator, accountIDs);
  });

  const fileNames: string[] = [];
  const seenFileNames = new Set<string>();
  const targetAccountIDsByLocator = new Map<string, Set<string>>();
  let eligibleAccountCount = 0;

  targetAccounts.forEach((account) => {
    const locator = resolveCurrentAuthFileLocator(account);
    if (!locator) return;

    eligibleAccountCount += 1;
    if (!seenFileNames.has(locator)) {
      seenFileNames.add(locator);
      fileNames.push(locator);
    }

    const accountIDs = targetAccountIDsByLocator.get(locator) ?? new Set<string>();
    accountIDs.add(account.id);
    targetAccountIDsByLocator.set(locator, accountIDs);
  });

  const sharedFileNames: string[] = [];
  const partialSharedFileNames: string[] = [];
  allAccountIDsByLocator.forEach((allAccountIDs, locator) => {
    if (allAccountIDs.size <= 1) return;
    sharedFileNames.push(locator);

    if (!selectionActive) return;
    const selectedAccountCount = targetAccountIDsByLocator.get(locator)?.size ?? 0;
    if (selectedAccountCount > 0 && selectedAccountCount < allAccountIDs.size) {
      partialSharedFileNames.push(locator);
    }
  });

  return {
    fileNames,
    sharedFileNames,
    partialSharedFileNames,
    targetAccountCount: targetAccounts.length,
    eligibleAccountCount,
    skippedAccountCount: targetAccounts.length - eligibleAccountCount,
  };
}
