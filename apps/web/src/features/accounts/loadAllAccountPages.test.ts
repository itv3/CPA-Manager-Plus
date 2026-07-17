import { describe, expect, it, vi } from 'vitest';
import type { ProAccount, ProAccountListResponse } from '@/services/api/proAccounts';
import { createAccountLoadSequence, loadAllAccountPages } from './loadAllAccountPages';

const account = (id: string): ProAccount => ({
  id,
  platform: 'openai',
  authType: 'oauth',
  sourceType: 'auth_file',
  enabled: true,
  healthStatus: 'healthy',
  allowedModels: [],
  modelMapping: {},
  createdAtMs: 1,
  updatedAtMs: 1,
  version: 1,
});

describe('loadAllAccountPages', () => {
  it('按游标顺序加载全部页面并去除重复账号', async () => {
    const loader = vi.fn(async ({ cursor }: { cursor?: string }) => {
      const page: ProAccountListResponse = cursor
        ? { items: [account('b'), account('c')], total: 3 }
        : { items: [account('a'), account('b')], nextCursor: 'page-2', total: 3 };
      return page;
    });

    await expect(loadAllAccountPages(loader)).resolves.toEqual([
      account('a'),
      account('b'),
      account('c'),
    ]);
    expect(loader).toHaveBeenNthCalledWith(1, { cursor: undefined });
    expect(loader).toHaveBeenNthCalledWith(2, { cursor: 'page-2' });
  });

  it('检测重复游标并中止，避免无限循环', async () => {
    const loader = vi.fn(async () => ({
      items: [account('a')],
      nextCursor: 'same-cursor',
      total: 2,
    }));

    await expect(loadAllAccountPages(loader)).rejects.toThrow('重复游标');
    expect(loader).toHaveBeenCalledTimes(2);
  });

  it('筛选切换后只接受最新请求', () => {
    const sequence = createAccountLoadSequence();
    const oldFilterRequest = sequence.begin();
    const newFilterRequest = sequence.begin();

    expect(sequence.isLatest(oldFilterRequest)).toBe(false);
    expect(sequence.isLatest(newFilterRequest)).toBe(true);
  });
});
