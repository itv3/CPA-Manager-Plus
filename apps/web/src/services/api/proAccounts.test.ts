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
    };

    await proAccountsApi.probe('https://manager.example', 'admin-key', input);
    await proAccountsApi.createAPI('https://manager.example', 'admin-key', {
      ...input,
      name: '主账号',
      testModel: 'client-model',
      saveDisabledOnTestFailure: true,
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
    });
    expect(mocks.post.mock.calls[1][2].headers['Idempotency-Key']).toBe('idem-1');
  });

  it('OAuth 分支启动、轮询、完成和取消均使用 Manager 私有路由', async () => {
    mocks.post.mockResolvedValue({ data: { operation: { operationId: 'oauth-1' } } });
    mocks.get.mockResolvedValue({ data: { status: 'wait' } });

    await proAccountsApi.startOAuth('https://manager.example', 'admin-key', {
      operationId: 'oauth-1',
      idempotencyKey: 'oauth-key',
      platform: 'gemini',
    });
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

    expect(mocks.post.mock.calls[0][0]).toContain('/v0/pro/accounts/oauth/start');
    expect(mocks.get.mock.calls[0][0]).toContain(
      '/v0/pro/accounts/oauth/status?operation_id=oauth-1'
    );
    expect(mocks.post.mock.calls[1][0]).toContain('/account%2Fwith%20space/complete');
    expect(mocks.post.mock.calls[2]).toEqual(
      expect.arrayContaining([
        'https://manager.example/v0/pro/accounts/oauth/cancel',
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
      'test-1',
      'test-key'
    );

    expect(mocks.put.mock.calls[0][1]).toMatchObject({ expected_version: 7, enabled: false });
    expect(mocks.delete.mock.calls[0][1].data).toMatchObject({
      expected_version: 7,
      operation_id: 'delete-1',
    });
    expect(mocks.post.mock.calls[0][1]).toMatchObject({
      expected_version: 7,
      model: 'client-model',
    });
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
