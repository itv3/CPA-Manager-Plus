import { describe, expect, it, vi } from 'vitest';
import type { ProAccount, ProAccountBatchItemInput } from '@/services/api/proAccounts';
import { executeAccountBatchChunks, PRO_ACCOUNT_BATCH_LIMIT } from './accountBatchExecution';

const createItem = (index: number): ProAccountBatchItemInput => ({
  account: {
    id: `account-${index}`,
    platform: 'xai',
    authType: 'oauth',
    sourceType: 'auth_file',
    enabled: true,
    healthStatus: 'healthy',
    allowedModels: [],
    modelMapping: {},
    createdAtMs: 1,
    updatedAtMs: 1,
    version: 1,
  } satisfies ProAccount,
});

describe('账号批量操作分批执行', () => {
  it('超过后端上限时按 100 条分批并汇总结果', async () => {
    const items = Array.from({ length: 234 }, (_, index) => createItem(index));
    const executeChunk = vi.fn(async (chunk: ProAccountBatchItemInput[]) => ({
      action: 'delete' as const,
      total: chunk.length,
      succeeded: chunk.length,
      failed: 0,
      items: chunk.map((item) => ({
        proAccountId: item.account.id,
        success: true,
        retryable: false,
      })),
    }));

    const result = await executeAccountBatchChunks('delete', items, executeChunk);

    expect(PRO_ACCOUNT_BATCH_LIMIT).toBe(100);
    expect(executeChunk.mock.calls.map(([chunk]) => chunk.length)).toEqual([100, 100, 34]);
    expect(result).toMatchObject({ total: 234, succeeded: 234, failed: 0 });
    expect(result.items).toHaveLength(234);
  });

  it('单批请求失败时保留其他批次结果并标记失败账号', async () => {
    const items = Array.from({ length: 150 }, (_, index) => createItem(index));
    const result = await executeAccountBatchChunks('delete', items, async (chunk, chunkIndex) => {
      if (chunkIndex === 1) throw new Error('网络中断');
      return {
        action: 'delete',
        total: chunk.length,
        succeeded: chunk.length,
        failed: 0,
        items: chunk.map((item) => ({
          proAccountId: item.account.id,
          success: true,
          retryable: false,
        })),
      };
    });

    expect(result).toMatchObject({ total: 150, succeeded: 100, failed: 50 });
    expect(result.items[100]).toMatchObject({
      proAccountId: 'account-100',
      success: false,
      code: 'batch_request_failed',
      message: '网络中断',
    });
  });
});
