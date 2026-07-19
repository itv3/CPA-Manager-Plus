import type {
  ProAccountBatchAction,
  ProAccountBatchItemInput,
  ProAccountBatchResult,
} from '@/services/api/proAccounts';

export const PRO_ACCOUNT_BATCH_LIMIT = 100;

type BatchChunkExecutor = (
  items: ProAccountBatchItemInput[],
  chunkIndex: number
) => Promise<ProAccountBatchResult>;

export async function executeAccountBatchChunks(
  action: ProAccountBatchAction,
  items: ProAccountBatchItemInput[],
  executeChunk: BatchChunkExecutor
): Promise<ProAccountBatchResult> {
  const mergedItems: ProAccountBatchResult['items'] = [];

  for (let offset = 0; offset < items.length; offset += PRO_ACCOUNT_BATCH_LIMIT) {
    const chunk = items.slice(offset, offset + PRO_ACCOUNT_BATCH_LIMIT);
    try {
      const result = await executeChunk(chunk, offset / PRO_ACCOUNT_BATCH_LIMIT);
      mergedItems.push(...result.items);
    } catch (error) {
      const message = error instanceof Error ? error.message : String(error);
      mergedItems.push(
        ...chunk.map((item) => ({
          proAccountId: item.account.id,
          success: false,
          code: 'batch_request_failed',
          message,
          retryable: true,
        }))
      );
    }
  }

  const succeeded = mergedItems.filter((item) => item.success).length;
  return {
    action,
    total: items.length,
    succeeded,
    failed: items.length - succeeded,
    items: mergedItems,
  };
}
