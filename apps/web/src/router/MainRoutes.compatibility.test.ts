import { describe, expect, it } from 'vitest';
import { LEGACY_ACCOUNT_ROUTE_PATHS } from './legacyAccountRoutes';

describe('旧账号管理路由兼容', () => {
  it('保留旧书签和高级管理依赖的页面路由', () => {
    const paths = new Set(Object.values(LEGACY_ACCOUNT_ROUTE_PATHS));
    expect(paths).toContain('/ai-providers');
    expect(paths).toContain('/ai-providers/openai/:index');
    expect(paths).toContain('/auth-files');
    expect(paths).toContain('/auth-files/oauth-excluded');
    expect(paths).toContain('/auth-files/oauth-model-alias');
    expect(paths).toContain('/oauth');
  });
});
