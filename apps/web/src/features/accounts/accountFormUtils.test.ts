import { describe, expect, it, vi } from 'vitest';
import {
  ACCOUNT_PLATFORMS,
  accountSourceLabel,
  authTypesForPlatform,
  createRequestIdentity,
  parseHeaderLines,
  parseMappingLines,
  parseModelLines,
  resolveAccountModelRules,
  resolveMappedModel,
  usageRequestOptions,
} from './accountFormUtils';

describe('统一账号表单规则', () => {
  it('严格限制各平台可用认证方式', () => {
    expect(authTypesForPlatform('openai')).toEqual(['oauth', 'api']);
    expect(authTypesForPlatform('anthropic')).toEqual(['oauth', 'api']);
    expect(authTypesForPlatform('gemini')).toEqual(['api', 'vertex']);
    expect(
      authTypesForPlatform('gemini', {
        credentialDraft: true,
        allowedModels: true,
        stores: {},
        platforms: {
          gemini: {
            oauth: { status: 'supported' },
            api: { status: 'supported' },
            vertex: { status: 'supported' },
          },
        },
      })
    ).toEqual(['oauth', 'api', 'vertex']);
    expect(
      authTypesForPlatform('openai', {
        credentialDraft: false,
        allowedModels: false,
        stores: {},
        platforms: {
          openai: {
            oauth: { status: 'unsupported' },
            api: { status: 'unsupported' },
          },
        },
      })
    ).toEqual([]);
    expect(authTypesForPlatform('antigravity')).toEqual(['oauth']);
    expect(authTypesForPlatform('xai')).toEqual(['oauth']);
    expect(ACCOUNT_PLATFORMS).toHaveLength(5);
  });

  it('将底层来源转换为用户可理解的协议标签', () => {
    expect(accountSourceLabel('auth_file')).toBe('认证文件');
    expect(accountSourceLabel('config_codex_api_key')).toBe('Responses');
    expect(accountSourceLabel('config_openai_compatibility')).toBe('Chat Completions');
    expect(accountSourceLabel('config_gemini_api_key')).toBe('API 配置');
  });

  it('解析并校验模型白名单和映射', () => {
    expect(parseModelLines('gpt-5\ngpt-5\nclaude-*')).toEqual(['gpt-5', 'claude-*']);
    expect(parseMappingLines('fast=gpt-5\nclaude-*=claude-sonnet')).toEqual({
      fast: 'gpt-5',
      'claude-*': 'claude-sonnet',
    });
    expect(() => parseModelLines('bad*model')).toThrow('通配符');
    expect(() => parseMappingLines('alias=target*')).toThrow('目标不允许通配符');
  });

  it('空白名单表示允许全部模型且测试模型可为空', () => {
    expect(
      resolveAccountModelRules({
        models: [],
        mappingLines: '',
      })
    ).toEqual({ allowedModels: [], modelMapping: {}, testModel: '' });
  });

  it('测试模型自动从白名单、映射或目录中选取', () => {
    expect(
      resolveAccountModelRules({
        models: ['custom-*'],
        mappingLines: 'fast=gpt-5',
      })
    ).toEqual({
      allowedModels: ['custom-*'],
      modelMapping: { fast: 'gpt-5' },
      testModel: 'fast',
    });
    expect(
      resolveAccountModelRules({
        models: ['gpt-5.5', 'gpt-5.6-sol'],
        mappingLines: '',
      }).testModel
    ).toBe('gpt-5.5');
    expect(
      resolveAccountModelRules({
        models: [],
        mappingLines: '',
        discoveredModels: ['gpt-first'],
      }).testModel
    ).toBe('gpt-first');
  });

  it('解析连通性测试的精确和通配模型映射', () => {
    expect(resolveMappedModel('fast', { fast: 'gpt-5' })).toBe('gpt-5');
    expect(resolveMappedModel('claude-sonnet', { 'claude-*': 'claude-upstream' })).toBe(
      'claude-upstream'
    );
    expect(resolveMappedModel('unchanged', {})).toBe('unchanged');
  });

  it('拒绝在自定义 Header 中覆盖凭证 Header', () => {
    expect(parseHeaderLines('X-Tenant: tenant-a')).toEqual({ 'X-Tenant': 'tenant-a' });
    expect(() => parseHeaderLines('Authorization: secret')).toThrow('不能在此覆盖');
    expect(() => parseHeaderLines('x-api-key: secret')).toThrow('不能在此覆盖');
  });

  it('为每次写操作生成独立标识', () => {
    vi.stubGlobal('crypto', {
      randomUUID: vi.fn().mockReturnValueOnce('one').mockReturnValueOnce('two'),
    });
    expect(createRequestIdentity('account-add')).toEqual({
      operationId: 'account-add-one',
      idempotencyKey: 'account-add-one',
    });
    expect(createRequestIdentity('account-add').operationId).toBe('account-add-two');
    vi.unstubAllGlobals();
  });

  it('自动刷新只读取 passive 用量，主动查询才使用 force', () => {
    expect(usageRequestOptions('automatic')).toEqual({ source: 'passive', force: false });
    expect(usageRequestOptions('manual-passive')).toEqual({ source: 'passive', force: false });
    expect(usageRequestOptions('manual-active')).toEqual({ source: 'active', force: true });
  });
});
