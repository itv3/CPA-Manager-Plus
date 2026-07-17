import type { ProAccount, ProAccountListResponse } from '@/services/api/proAccounts';

interface PageRequest {
  cursor?: string;
}

type PageLoader = (request: PageRequest) => Promise<ProAccountListResponse>;

export interface AccountLoadSequence {
  begin: () => number;
  isLatest: (requestID: number) => boolean;
}

// 筛选条件变化时只允许最后一次请求更新页面，避免较慢的旧响应覆盖新筛选结果。
export function createAccountLoadSequence(): AccountLoadSequence {
  let latestRequestID = 0;
  return {
    begin: () => {
      latestRequestID += 1;
      return latestRequestID;
    },
    isLatest: (requestID) => requestID === latestRequestID,
  };
}

// 页面上的全选和批量操作需要覆盖当前筛选条件下的全部账号，因此这里完整消费游标分页。
export async function loadAllAccountPages(loadPage: PageLoader): Promise<ProAccount[]> {
  const items: ProAccount[] = [];
  const seenAccountIDs = new Set<string>();
  const seenCursors = new Set<string>();
  let cursor: string | undefined;

  do {
    const page = await loadPage({ cursor });
    page.items.forEach((item) => {
      if (seenAccountIDs.has(item.id)) return;
      seenAccountIDs.add(item.id);
      items.push(item);
    });

    const nextCursor = page.nextCursor?.trim() || undefined;
    if (nextCursor && seenCursors.has(nextCursor)) {
      throw new Error('账号列表返回了重复游标，已停止加载以避免死循环');
    }
    if (nextCursor) seenCursors.add(nextCursor);
    cursor = nextCursor;
  } while (cursor);

  return items;
}
