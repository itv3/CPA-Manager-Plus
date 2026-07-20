import { describe, expect, it } from 'vitest';
import {
  readApiLatestVersion,
  readApiReleaseUrl,
  readManagerLatestTag,
  readManagerReleaseUrl,
} from './versionChecks';

describe('version checks', () => {
  it('读取管理面板上游发布信息', () => {
    const data = {
      tag_name: 'v1.11.3',
      html_url: 'https://github.com/seakee/CPA-Manager-Plus/releases/tag/v1.11.3',
    };

    expect(readManagerLatestTag(data)).toBe('v1.11.3');
    expect(readManagerReleaseUrl(data)).toBe(data.html_url);
  });

  it('读取服务端上游发布信息', () => {
    const data = {
      'latest-version': 'v7.2.92',
      'release-url': 'https://github.com/router-for-me/CLIProxyAPI/releases/tag/v7.2.92',
    };

    expect(readApiLatestVersion(data)).toBe('v7.2.92');
    expect(readApiReleaseUrl(data)).toBe(data['release-url']);
  });
});
