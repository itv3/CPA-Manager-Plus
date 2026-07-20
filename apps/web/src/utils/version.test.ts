import { describe, expect, it } from 'vitest';
import { compareVersions, getUpstreamBaseVersion, parseVersionSegments } from './version';

describe('version utils', () => {
  it('从 Pro 组件版本中提取上游基线', () => {
    expect(getUpstreamBaseVersion('v7.2.92-1')).toBe('v7.2.92');
    expect(getUpstreamBaseVersion('v1.11.3-12')).toBe('v1.11.3');
  });

  it('解析版本时忽略 Pro 修改序号', () => {
    expect(parseVersionSegments('v7.2.92-3')).toEqual([7, 2, 92]);
  });

  it('同一上游基线的 Pro 修改版视为已是最新', () => {
    expect(compareVersions('v7.2.92', 'v7.2.92-1')).toBe(0);
    expect(compareVersions('v1.11.3', 'v1.11.3-4')).toBe(0);
  });

  it('上游基线升级时提示新版本', () => {
    expect(compareVersions('v7.2.93', 'v7.2.92-9')).toBe(1);
    expect(compareVersions('v1.12.0', 'v1.11.3-2')).toBe(1);
  });
});
