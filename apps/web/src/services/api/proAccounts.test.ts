import { beforeEach, describe, expect, it, vi } from 'vitest';

const { mocks } = vi.hoisted(() => ({
  mocks: {
    get: vi.fn(),
    post: vi.fn(),
    put: vi.fn(),
    delete: vi.fn(),
    isAxiosError: vi.fn(),
  },
}));

vi.mock('axios', () => ({
  default: {
    get: mocks.get,
    post: mocks.post,
    put: mocks.put,
    delete: mocks.delete,
    isAxiosError: mocks.isAxiosError,
  },
}));

import { ProAccountsApiError, proAccountsApi, type ProAccount } from './proAccounts';

const account: ProAccount = {
  id: 'account-1',
  platform: 'openai',
  authType: 'api',
  sourceType: 'config_codex_api_key',
  enabled: true,
  healthStatus: 'healthy',
  allowedModels: ['client-model'],
  modelMapping: { 'client-model': 'upstream-model' },
  createdAtMs: 1,
  updatedAtMs: 2,
  version: 7,
};

beforeEach(() => {
  Object.values(mocks).forEach((mock) => mock.mockReset());
  mocks.isAxiosError.mockReturnValue(false);
});

describe('proAccountsApi', () => {
  it('序列化列表筛选参数', async () => {
    mocks.get.mockResolvedValue({ data: { items: [], total: 0 } });

    await proAccountsApi.list('https://manager.example/', 'admin-key', {
      limit: 100,
      search: ' alpha ',
      platform: 'openai',
      authType: 'oauth',
      enabled: false,
      healthStatus: 'error',
    });

    const [url, config] = mocks.get.mock.calls[0];
    const parsed = new URL(url);
    expect(parsed.pathname).toBe('/v0/pro/accounts');
    expect(Object.fromEntries(parsed.searchParams)).toEqual({
      limit: '100',
      search: 'alpha',
      platform: 'openai',
      auth_type: 'oauth',
      enabled: 'false',
      health_status: 'error',
    });
    expect(config.headers.Authorization).toBe('Bearer admin-key');
  });

  it('通过 Manager 私有路由读取账号和平台模型目录', async () => {
    mocks.get.mockResolvedValue({ data: { models: [], upstream: [], builtIn: [], manual: [] } });

    await proAccountsApi.modelCatalog('https://manager.example', 'admin-key', 'account/with space');
    await proAccountsApi.staticModelCatalog(
      'https://manager.example',
      'admin-key',
      'gemini',
      'vertex'
    );

    expect(mocks.get.mock.calls[0][0]).toBe(
      'https://manager.example/v0/pro/accounts/account%2Fwith%20space/models'
    );
    expect(mocks.get.mock.calls[1][0]).toBe(
      'https://manager.example/v0/pro/accounts/model-catalog?platform=gemini&auth_type=vertex'
    );
    expect(mocks.get.mock.calls[1][1].headers.Authorization).toBe('Bearer admin-key');
  });

  it('API 分支不会把凭证写入 URL，并复用操作标识完成探测和创建', async () => {
    mocks.post.mockResolvedValue({ data: { probe: { sourceType: 'config_codex_api_key' } } });
    const input = {
      operationId: 'operation-1',
      idempotencyKey: 'idem-1',
      platform: 'openai',
      baseUrl: 'https://api.example',
      apiKey: 'secret-key',
      protocolMode: 'auto',
      allowedModels: ['client-model'],
      modelMapping: { 'client-model': 'upstream-model' },
      headers: { 'X-Tenant': 'tenant-a' },
      officialClientCompatibility: {
        enabled: true,
        profile: 'codex-desktop-0.145.0-alpha.18-v1',
        tlsProfile: '',
      },
    };

    await proAccountsApi.probe('https://manager.example', 'admin-key', input);
    await proAccountsApi.createAPI('https://manager.example', 'admin-key', {
      ...input,
      name: '主账号',
      testModel: 'client-model',
      saveDisabledOnTestFailure: true,
      draftOnly: true,
    });

    expect(mocks.post.mock.calls[0][0]).toBe('https://manager.example/v0/pro/accounts/probe');
    expect(mocks.post.mock.calls[0][0]).not.toContain('secret-key');
    expect(mocks.post.mock.calls[0][1]).toMatchObject({
      operation_id: 'operation-1',
      idempotency_key: 'idem-1',
      auth_type: 'api',
      api_key: 'secret-key',
      allowed_models: ['client-model'],
    });
    expect(mocks.post.mock.calls[1][1]).toMatchObject({
      operation_id: 'operation-1',
      api_key: 'secret-key',
      test_model: 'client-model',
      save_disabled_on_test_failure: true,
      draft_only: true,
      official_client_compatibility: {
        enabled: true,
        profile: 'codex-desktop-0.145.0-alpha.18-v1',
        tls_profile: '',
      },
    });
    expect(mocks.post.mock.calls[1][2].headers['Idempotency-Key']).toBe('idem-1');
  });

  it('编辑请求仅在显式变更时序列化官方客户端兼容结构', async () => {
    mocks.put.mockResolvedValue({ data: { account } });

    await proAccountsApi.update('https://manager.example', 'admin-key', account.id, {
      operationId: 'edit-1',
      idempotencyKey: 'edit-key',
      expectedVersion: account.version,
      allowedModels: account.allowedModels,
      modelMapping: account.modelMapping,
      officialClientCompatibility: {
        enabled: false,
        profile: 'codex-desktop-0.145.0-alpha.18-v1',
        tlsProfile: '',
      },
    });

    expect(mocks.put.mock.calls[0][1]).toMatchObject({
      expected_version: 7,
      api_key: undefined,
      base_url: undefined,
      official_client_compatibility: {
        enabled: false,
        profile: 'codex-desktop-0.145.0-alpha.18-v1',
        tls_profile: '',
      },
    });
  });

  it('OAuth 分支启动、回调、轮询、完成和取消均使用 Manager 私有路由', async () => {
    mocks.post.mockResolvedValue({ data: { operation: { operationId: 'oauth-1' } } });
    mocks.get.mockResolvedValue({ data: { status: 'wait' } });

    await proAccountsApi.startOAuth('https://manager.example', 'admin-key', {
      operationId: 'oauth-1',
      idempotencyKey: 'oauth-key',
      platform: 'gemini',
    });
    await proAccountsApi.submitOAuthCallback(
      'https://manager.example',
      'admin-key',
      'oauth-1',
      'oauth-code',
      'oauth-state'
    );
    await proAccountsApi.oauthStatus('https://manager.example', 'admin-key', 'oauth-1');
    await proAccountsApi.completeDraft(
      'https://manager.example',
      'admin-key',
      'account/with space',
      {
        operationId: 'oauth-1',
        expectedVersion: 3,
        allowedModels: [],
        modelMapping: {},
        testModel: 'gemini-2.5-pro',
        saveDisabledOnTestFailure: false,
      }
    );
    await proAccountsApi.cancelOAuth('https://manager.example', 'admin-key', 'oauth-1');
    await proAccountsApi.cancelDraft('https://manager.example', 'admin-key', 'oauth-1');

    expect(mocks.post.mock.calls[0][0]).toContain('/v0/pro/accounts/oauth/start');
    expect(mocks.post.mock.calls[1]).toEqual(
      expect.arrayContaining([
        'https://manager.example/v0/pro/accounts/oauth/callback',
        {
          operation_id: 'oauth-1',
          callback_input: 'oauth-code',
          callback_state: 'oauth-state',
        },
      ])
    );
    expect(mocks.get.mock.calls[0][0]).toContain(
      '/v0/pro/accounts/oauth/status?operation_id=oauth-1'
    );
    expect(mocks.post.mock.calls[2][0]).toContain('/account%2Fwith%20space/complete');
    expect(mocks.post.mock.calls[3]).toEqual(
      expect.arrayContaining([
        'https://manager.example/v0/pro/accounts/oauth/cancel',
        { operation_id: 'oauth-1' },
      ])
    );
    expect(mocks.post.mock.calls[4]).toEqual(
      expect.arrayContaining([
        'https://manager.example/v0/pro/accounts/drafts/cancel',
        { operation_id: 'oauth-1' },
      ])
    );
  });

  it('Vertex 分支使用 multipart 且保留模型规则', async () => {
    mocks.post.mockResolvedValue({ data: {} });
    const file = new File(['{"type":"service_account"}'], 'service-account.json', {
      type: 'application/json',
    });

    await proAccountsApi.createVertex('https://manager.example', 'admin-key', {
      operationId: 'vertex-1',
      idempotencyKey: 'vertex-key',
      file,
      location: 'us-central1',
      allowedModels: ['gemini-2.5-pro'],
      modelMapping: { gemini: 'gemini-2.5-pro' },
      testModel: 'gemini',
      saveDisabledOnTestFailure: false,
      draftOnly: true,
    });

    const [url, form, config] = mocks.post.mock.calls[0] as [
      string,
      FormData,
      { headers: Record<string, string> },
    ];
    expect(url).toBe('https://manager.example/v0/pro/accounts/vertex');
    expect(form.get('file')).toBe(file);
    expect(form.get('allowed_models')).toBe('["gemini-2.5-pro"]');
    expect(form.get('model_mapping')).toBe('{"gemini":"gemini-2.5-pro"}');
    expect(form.get('draft_only')).toBe('true');
    expect(config.headers['Idempotency-Key']).toBe('vertex-key');
  });

  it('写操作携带当前资源版本和幂等键', async () => {
    mocks.put.mockResolvedValue({ data: { account } });
    mocks.delete.mockResolvedValue({ data: { account } });
    mocks.post.mockResolvedValue({ data: { connectivity: { success: true } } });

    await proAccountsApi.setEnabled(
      'https://manager.example',
      'admin-key',
      account,
      false,
      'toggle-1',
      'toggle-key'
    );
    await proAccountsApi.deleteAccount(
      'https://manager.example',
      'admin-key',
      account,
      'delete-1',
      'delete-key'
    );
    await proAccountsApi.testAccount(
      'https://manager.example',
      'admin-key',
      account,
      'client-model',
      'compact',
      'test-1',
      'test-key'
    );

    expect(mocks.put.mock.calls[0][1]).toMatchObject({ expected_version: 7, enabled: false });
    expect(mocks.delete.mock.calls[0][1].data).toMatchObject({
      expected_version: 7,
      operation_id: 'delete-1',
    });
    expect(mocks.post.mock.calls[0][1]).toMatchObject({
      model: 'client-model',
      mode: 'compact',
    });
    expect(mocks.post.mock.calls[0][1]).toMatchObject({
      expected_version: 7,
      model: 'client-model',
    });
  });

  it('序列化批量操作与批量重绑的逐项资源版本', async () => {
    mocks.post.mockResolvedValue({ data: { total: 1, succeeded: 1, failed: 0, items: [] } });

    await proAccountsApi.batch(
      'https://manager.example',
      'admin-key',
      'test',
      [{ account, model: 'client-model' }],
      'batch-1',
      'batch-key'
    );
    await proAccountsApi.rebind(
      'https://manager.example',
      'admin-key',
      [{ reviewId: 9, account }],
      'rebind-1',
      'rebind-key'
    );

    expect(mocks.post.mock.calls[0][1]).toEqual({
      operation_id: 'batch-1',
      idempotency_key: 'batch-key',
      action: 'test',
      items: [{ pro_account_id: 'account-1', expected_version: 7, model: 'client-model' }],
    });
    expect(mocks.post.mock.calls[1][1]).toEqual({
      operation_id: 'rebind-1',
      idempotency_key: 'rebind-key',
      items: [{ review_id: 9, pro_account_id: 'account-1', expected_version: 7 }],
    });
  });

  it('reset credits 查询无副作用，重置请求显式携带确认和资源版本', async () => {
    mocks.get.mockResolvedValue({ data: { capability: 'supported', availableCount: 2 } });
    mocks.post.mockResolvedValue({ data: { credits: { capability: 'supported' } } });

    await proAccountsApi.resetCredits('https://manager.example', 'admin-key', account.id);
    await proAccountsApi.resetOpenAI(
      'https://manager.example',
      'admin-key',
      account,
      'reset-1',
      'reset-key'
    );

    expect(mocks.get.mock.calls[0][0]).toContain('/account-1/openai-reset-credits');
    expect(mocks.post.mock.calls[0][1]).toMatchObject({
      operation_id: 'reset-1',
      idempotency_key: 'reset-key',
      expected_version: 7,
      confirmed: true,
    });
  });

  it('重新授权与刷新令牌使用账号级 REST 路由', async () => {
    mocks.post.mockResolvedValue({
      data: { operation: { operationId: 'reauth-1' }, oauth: { url: 'https://auth.example' } },
    });
    mocks.get.mockResolvedValue({ data: { status: 'wait' } });

    await proAccountsApi.startReauthorization(
      'https://manager.example',
      'admin-key',
      account,
      'reauth-1',
      'reauth-key'
    );
    await proAccountsApi.reauthorizationStatus(
      'https://manager.example',
      'admin-key',
      'account/with space',
      'reauth-1'
    );
    await proAccountsApi.submitReauthorizationCallback(
      'https://manager.example',
      'admin-key',
      account.id,
      'reauth-1',
      'callback-value',
      'callback-state'
    );
    await proAccountsApi.refreshToken(
      'https://manager.example',
      'admin-key',
      account,
      'refresh-1',
      'refresh-key'
    );

    expect(mocks.post.mock.calls[0][0]).toContain('/account-1/reauthorize/start');
    expect(mocks.post.mock.calls[0][1]).toMatchObject({ expected_version: 7 });
    expect(mocks.get.mock.calls[0][0]).toContain(
      '/account%2Fwith%20space/reauthorize/status?operation_id=reauth-1'
    );
    expect(mocks.post.mock.calls[1][0]).toContain('/account-1/reauthorize/callback');
    expect(mocks.post.mock.calls[1][1]).toMatchObject({
      callback_input: 'callback-value',
      callback_state: 'callback-state',
    });
    expect(mocks.post.mock.calls[2][0]).toContain('/account-1/refresh-token');
    expect(mocks.post.mock.calls[2][2].headers['Idempotency-Key']).toBe('refresh-key');
  });

  it('定时测试计划 API 序列化表单并兼容数组列表响应', async () => {
    mocks.get.mockResolvedValueOnce({ data: [{ id: 1 }] });
    mocks.post.mockResolvedValue({ data: { id: 2 } });
    mocks.put.mockResolvedValue({ data: { id: 1, enabled: false } });
    mocks.delete.mockResolvedValue({ data: {} });

    await expect(
      proAccountsApi.listScheduledTests('https://manager.example', 'admin-key', account.id)
    ).resolves.toEqual([{ id: 1 }]);
    await proAccountsApi.createScheduledTest(
      'https://manager.example',
      'admin-key',
      account,
      {
        modelId: 'gpt-5.5',
        cronExpression: '*/30 * * * *',
        enabled: true,
        maxResults: 100,
        autoRecover: true,
      },
      'schedule-1',
      'schedule-key'
    );
    await proAccountsApi.updateScheduledTest(
      'https://manager.example',
      'admin-key',
      account.id,
      1,
      { enabled: false }
    );
    await proAccountsApi.deleteScheduledTest('https://manager.example', 'admin-key', account.id, 1);

    expect(mocks.post.mock.calls[0][1]).toMatchObject({
      model_id: 'gpt-5.5',
      cron_expression: '*/30 * * * *',
      max_results: 100,
      auto_recover: true,
    });
    expect(mocks.put.mock.calls[0][0]).toContain('/scheduled-test-plans/1');
    expect(mocks.delete.mock.calls[0][0]).toContain('/scheduled-test-plans/1');
  });

  it('保留后端机器可识别错误字段', async () => {
    const failure = {
      message: 'Request failed',
      response: {
        status: 409,
        data: {
          code: 'resource_version_conflict',
          message: '账号版本已变化',
          retryable: true,
          details: { currentVersion: 8 },
        },
      },
    };
    mocks.get.mockRejectedValue(failure);
    mocks.isAxiosError.mockImplementation((value) => value === failure);

    await expect(
      proAccountsApi.details('https://manager.example', 'admin-key', 'account-1')
    ).rejects.toMatchObject({
      name: 'ProAccountsApiError',
      code: 'resource_version_conflict',
      retryable: true,
      status: 409,
      details: { currentVersion: 8 },
    } satisfies Partial<ProAccountsApiError>);
  });
});
