export interface ReconcileAccountsOptions<T> {
  sync: () => Promise<T>;
  load: () => Promise<void>;
  onSyncError?: (error: unknown) => void;
}

export const accountReconcileContextKey = (managerBase: string, managementKey: string) =>
  managerBase && managementKey ? `${managerBase}\u0000${managementKey}` : '';

export const shouldReconcileAccountContext = (previousContext: string, nextContext: string) =>
  Boolean(nextContext && previousContext !== nextContext);

/**
 * 统一账号列表是 Gateway 认证状态的 Manager 投影。
 * 先同步再读取；同步失败时仍读取现有投影，保证页面不会因短暂同步故障完全不可用。
 */
export async function reconcileAccountsThenLoad<T>({
  sync,
  load,
  onSyncError,
}: ReconcileAccountsOptions<T>): Promise<T | undefined> {
  let result: T | undefined;
  try {
    result = await sync();
  } catch (error) {
    onSyncError?.(error);
  }
  await load();
  return result;
}
