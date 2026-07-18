import { describe, expect, it, vi } from 'vitest';
import {
  accountReconcileContextKey,
  reconcileAccountsThenLoad,
  shouldReconcileAccountContext,
} from './accountRefresh';

describe('reconcileAccountsThenLoad', () => {
  it('只按 Manager 连接上下文判定首次同步，筛选条件不参与上下文', () => {
    const context = accountReconcileContextKey('https://manager.example', 'admin-key');
    expect(shouldReconcileAccountContext('', context)).toBe(true);
    expect(shouldReconcileAccountContext(context, context)).toBe(false);
    expect(
      shouldReconcileAccountContext(
        context,
        accountReconcileContextKey('https://manager-2.example', 'admin-key')
      )
    ).toBe(true);
  });

  it('严格按 sync→list 顺序刷新统一账号投影', async () => {
    const calls: string[] = [];
    const result = await reconcileAccountsThenLoad({
      sync: async () => {
        calls.push('sync');
        return { updated: 2 };
      },
      load: async () => {
        calls.push('list');
      },
    });

    expect(calls).toEqual(['sync', 'list']);
    expect(result).toEqual({ updated: 2 });
  });

  it('同步失败时仍读取现有账号列表并回报同步错误', async () => {
    const load = vi.fn().mockResolvedValue(undefined);
    const onSyncError = vi.fn();

    const result = await reconcileAccountsThenLoad({
      sync: vi.fn().mockRejectedValue(new Error('gateway unavailable')),
      load,
      onSyncError,
    });

    expect(result).toBeUndefined();
    expect(onSyncError).toHaveBeenCalledWith(
      expect.objectContaining({ message: 'gateway unavailable' })
    );
    expect(load).toHaveBeenCalledTimes(1);
  });
});
