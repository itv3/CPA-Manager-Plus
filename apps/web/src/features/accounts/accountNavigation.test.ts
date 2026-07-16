import { describe, expect, it } from 'vitest';
import {
  advancedAccountPath,
  filterLegacyAccountPrimaryNavigation,
  isLegacyAccountAdvancedPath,
} from './accountNavigation';

describe('统一账号旧入口导航策略', () => {
  it('隐藏旧一级入口但保留其他导航', () => {
    const items = [
      { path: '/accounts' },
      { path: '/ai-providers' },
      { path: '/auth-files' },
      { path: '/oauth' },
      { path: '/quota' },
    ];
    expect(filterLegacyAccountPrimaryNavigation(items).map((item) => item.path)).toEqual([
      '/accounts',
      '/quota',
    ]);
  });

  it('识别旧页面及其子路由', () => {
    expect(isLegacyAccountAdvancedPath('/ai-providers/openai/1')).toBe(true);
    expect(isLegacyAccountAdvancedPath('/auth-files/oauth-model-alias')).toBe(true);
    expect(isLegacyAccountAdvancedPath('/oauth')).toBe(true);
    expect(isLegacyAccountAdvancedPath('/accounts')).toBe(false);
  });

  it('统一账号高级管理仍进入对应旧页面', () => {
    expect(advancedAccountPath({ authType: 'oauth' })).toBe('/auth-files');
    expect(advancedAccountPath({ authType: 'vertex' })).toBe('/auth-files');
    expect(advancedAccountPath({ authType: 'api' })).toBe('/ai-providers');
  });
});
