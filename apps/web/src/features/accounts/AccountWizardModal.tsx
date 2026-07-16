import { useCallback, useEffect, useMemo, useRef, useState } from 'react';
import { Button } from '@/components/ui/Button';
import { Modal } from '@/components/ui/Modal';
import { SelectionCheckbox } from '@/components/ui/SelectionCheckbox';
import type {
  ProAccount,
  ProAccountCapabilitiesResponse,
  ProAccountLifecycleResult,
  ProAccountProbeResult,
} from '@/services/api/proAccounts';
import { proAccountsApi } from '@/services/api/proAccounts';
import { AccountModelRulesEditor } from './AccountModelRulesEditor';
import {
  ACCOUNT_PLATFORMS,
  AUTH_TYPE_LABELS,
  accountDisplayName,
  authTypesForPlatform,
  createRequestIdentity,
  parseHeaderLines,
  parseMappingLines,
  parseModelLines,
  platformOption,
  suggestedTestModel,
  type AccountAuthType,
  type AccountPlatform,
  type RequestIdentity,
} from './accountFormUtils';
import styles from './AccountModals.module.scss';

interface AccountWizardModalProps {
  open: boolean;
  managerBase: string;
  managementKey: string;
  capabilities?: ProAccountCapabilitiesResponse | null;
  onClose: () => void;
  onSaved: (result: ProAccountLifecycleResult) => void;
}

const STEP_LABELS = ['账号类型', '认证凭证', '模型规则', '确认保存'];

const protocolLabel = (value?: string) => {
  if (value === 'responses') return 'Responses';
  if (value === 'chat_completions') return 'Chat Completions';
  return '自动探测';
};

export function AccountWizardModal({
  open,
  managerBase,
  managementKey,
  capabilities,
  onClose,
  onSaved,
}: AccountWizardModalProps) {
  const [step, setStep] = useState(0);
  const [platform, setPlatform] = useState<AccountPlatform>('openai');
  const [authType, setAuthType] = useState<AccountAuthType>('oauth');
  const [name, setName] = useState('');
  const [baseUrl, setBaseUrl] = useState('https://api.openai.com');
  const [apiKey, setApiKey] = useState('');
  const [protocolMode, setProtocolMode] = useState('auto');
  const [headerLines, setHeaderLines] = useState('');
  const [vertexFile, setVertexFile] = useState<File | null>(null);
  const [location, setLocation] = useState('us-central1');
  const [allowAll, setAllowAll] = useState(true);
  const [discoveredModels, setDiscoveredModels] = useState<string[]>([]);
  const [selectedModels, setSelectedModels] = useState<string[]>([]);
  const [manualModels, setManualModels] = useState('');
  const [mappingLines, setMappingLines] = useState('');
  const [testModel, setTestModel] = useState('');
  const [saveDisabled, setSaveDisabled] = useState(false);
  const [probeResult, setProbeResult] = useState<ProAccountProbeResult | null>(null);
  const [operationIdentity, setOperationIdentity] = useState<RequestIdentity>(() =>
    createRequestIdentity('account-add')
  );
  const [oauthWaiting, setOAuthWaiting] = useState(false);
  const [oauthUrl, setOAuthUrl] = useState('');
  const [draftAccount, setDraftAccount] = useState<ProAccount | null>(null);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState('');
  const oauthWindowRef = useRef<Window | null>(null);

  const reset = useCallback(() => {
    setStep(0);
    setPlatform('openai');
    setAuthType('oauth');
    setName('');
    setBaseUrl('https://api.openai.com');
    setApiKey('');
    setProtocolMode('auto');
    setHeaderLines('');
    setVertexFile(null);
    setLocation('us-central1');
    setAllowAll(true);
    setDiscoveredModels([]);
    setSelectedModels([]);
    setManualModels('');
    setMappingLines('');
    setTestModel('');
    setSaveDisabled(false);
    setProbeResult(null);
    setOperationIdentity(createRequestIdentity('account-add'));
    setOAuthWaiting(false);
    setOAuthUrl('');
    setDraftAccount(null);
    setBusy(false);
    setError('');
    oauthWindowRef.current = null;
  }, []);

  useEffect(() => {
    if (open) reset();
  }, [open, reset]);

  const rules = useCallback(() => {
    const manual = parseModelLines(manualModels);
    const allowedModels = allowAll ? [] : [...new Set([...selectedModels, ...manual])];
    const modelMapping = parseMappingLines(mappingLines);
    const resolvedTestModel = suggestedTestModel(
      testModel,
      allowedModels,
      modelMapping,
      discoveredModels
    );
    if (!resolvedTestModel) {
      throw new Error('请输入连通性测试模型');
    }
    return { allowedModels, modelMapping, testModel: resolvedTestModel };
  }, [allowAll, discoveredModels, manualModels, mappingLines, selectedModels, testModel]);

  const selectPlatform = (value: AccountPlatform) => {
    const option = platformOption(value);
    const nextAuthType = option?.authTypes[0] ?? 'oauth';
    setPlatform(value);
    setAuthType(nextAuthType);
    setBaseUrl(option?.defaultBaseUrl ?? '');
    setProtocolMode('auto');
    setProbeResult(null);
    setDiscoveredModels([]);
    setSelectedModels([]);
    setTestModel('');
    setError('');
  };

  const advanceFromType = () => {
    if (!authTypesForPlatform(platform).includes(authType)) {
      setError('当前平台不支持所选认证方式');
      return;
    }
    if (authType === 'oauth' && capabilities && !capabilities.credentialDraft) {
      setError('当前 Gateway 不支持停用草稿凭证，无法安全添加 OAuth 账号');
      return;
    }
    setError('');
    setStep(1);
  };

  const probeAPI = async () => {
    if (!baseUrl.trim() || !apiKey.trim()) {
      setError('请输入 Base URL 和 API Key');
      return;
    }
    let headers: Record<string, string>;
    try {
      headers = parseHeaderLines(headerLines);
    } catch (parseError) {
      setError(parseError instanceof Error ? parseError.message : String(parseError));
      return;
    }
    const identity = createRequestIdentity('account-add');
    setOperationIdentity(identity);
    setBusy(true);
    setError('');
    try {
      const result = await proAccountsApi.probe(managerBase, managementKey, {
        ...identity,
        platform,
        baseUrl: baseUrl.trim(),
        apiKey,
        protocolMode,
        allowedModels: [],
        modelMapping: {},
        headers,
      });
      if (!result.probe?.sourceType) {
        throw new Error(result.probe?.errorCode || '未能确定可用协议，请检查凭证后重试');
      }
      setProbeResult(result.probe);
      setDiscoveredModels(result.probe.models ?? []);
      setSelectedModels(result.probe.models ?? []);
      setTestModel(result.probe.testModel ?? result.probe.models?.[0] ?? '');
      setStep(2);
    } catch (probeError) {
      setError(probeError instanceof Error ? probeError.message : String(probeError));
    } finally {
      setBusy(false);
    }
  };

  const checkOAuth = useCallback(async () => {
    if (!oauthWaiting || !operationIdentity.operationId) return;
    try {
      const result = await proAccountsApi.oauthStatus(
        managerBase,
        managementKey,
        operationIdentity.operationId
      );
      if (result.account && result.status === 'ok') {
        setDraftAccount(result.account);
        setOAuthWaiting(false);
        setDiscoveredModels(result.account.allowedModels ?? []);
        setSelectedModels(result.account.allowedModels ?? []);
        setAllowAll((result.account.allowedModels?.length ?? 0) === 0);
        setTestModel(result.account.allowedModels?.[0] ?? '');
        setStep(2);
        oauthWindowRef.current?.close();
        oauthWindowRef.current = null;
        return;
      }
      if (result.status === 'ambiguous' || result.status === 'error') {
        setOAuthWaiting(false);
        setError(result.operation.errorSummary || 'OAuth 授权未完成');
      }
    } catch (statusError) {
      setError(statusError instanceof Error ? statusError.message : String(statusError));
    }
  }, [managementKey, managerBase, oauthWaiting, operationIdentity.operationId]);

  useEffect(() => {
    if (!open || !oauthWaiting) return;
    const timer = window.setInterval(() => void checkOAuth(), 2000);
    return () => window.clearInterval(timer);
  }, [checkOAuth, oauthWaiting, open]);

  const startOAuth = async () => {
    const identity = createRequestIdentity('account-oauth');
    setOperationIdentity(identity);
    setBusy(true);
    setError('');
    const popup =
      typeof window === 'undefined'
        ? null
        : window.open('about:blank', 'pro-account-oauth', 'width=720,height=760');
    oauthWindowRef.current = popup;
    if (popup) popup.opener = null;
    try {
      const result = await proAccountsApi.startOAuth(managerBase, managementKey, {
        ...identity,
        platform,
      });
      const url = result.oauth?.url ?? '';
      if (!url) throw new Error('Gateway 未返回授权地址');
      setOAuthUrl(url);
      setOAuthWaiting(true);
      if (popup) popup.location.href = url;
    } catch (oauthError) {
      popup?.close();
      oauthWindowRef.current = null;
      setError(oauthError instanceof Error ? oauthError.message : String(oauthError));
    } finally {
      setBusy(false);
    }
  };

  const advanceFromCredentials = () => {
    if (authType === 'api') {
      void probeAPI();
      return;
    }
    if (authType === 'oauth') {
      if (!oauthWaiting) void startOAuth();
      else void checkOAuth();
      return;
    }
    if (!vertexFile) {
      setError('请选择 Service Account JSON 文件');
      return;
    }
    if (vertexFile.size > 2 * 1024 * 1024) {
      setError('Service Account JSON 文件不能超过 2 MiB');
      return;
    }
    if (!location.trim()) {
      setError('请输入 Vertex 地区');
      return;
    }
    setError('');
    setStep(2);
  };

  const advanceFromModels = () => {
    try {
      const resolved = rules();
      setTestModel(resolved.testModel);
      setError('');
      setStep(3);
    } catch (rulesError) {
      setError(rulesError instanceof Error ? rulesError.message : String(rulesError));
    }
  };

  const submit = async () => {
    let resolvedRules: ReturnType<typeof rules>;
    let headers: Record<string, string> = {};
    try {
      resolvedRules = rules();
      if (authType === 'api') headers = parseHeaderLines(headerLines);
    } catch (submitError) {
      setError(submitError instanceof Error ? submitError.message : String(submitError));
      return;
    }
    setBusy(true);
    setError('');
    try {
      let result: ProAccountLifecycleResult;
      if (authType === 'api') {
        result = await proAccountsApi.createAPI(managerBase, managementKey, {
          ...operationIdentity,
          platform,
          name: name.trim(),
          baseUrl: baseUrl.trim(),
          apiKey,
          protocolMode,
          headers,
          ...resolvedRules,
          saveDisabledOnTestFailure: saveDisabled,
        });
      } else if (authType === 'vertex') {
        if (!vertexFile) throw new Error('请选择 Service Account JSON 文件');
        result = await proAccountsApi.createVertex(managerBase, managementKey, {
          ...operationIdentity,
          file: vertexFile,
          location: location.trim(),
          ...resolvedRules,
          saveDisabledOnTestFailure: saveDisabled,
        });
      } else {
        if (!draftAccount) throw new Error('OAuth 凭证尚未保存');
        result = await proAccountsApi.completeDraft(managerBase, managementKey, draftAccount.id, {
          operationId: operationIdentity.operationId,
          expectedVersion: draftAccount.version,
          ...resolvedRules,
          saveDisabledOnTestFailure: saveDisabled,
        });
      }
      setApiKey('');
      onSaved(result);
      onClose();
    } catch (submitError) {
      setApiKey('');
      setError(submitError instanceof Error ? submitError.message : String(submitError));
      if (authType !== 'oauth') setOperationIdentity(createRequestIdentity('account-add'));
    } finally {
      setBusy(false);
    }
  };

  const close = async () => {
    if (busy) return;
    if (authType === 'oauth' && (oauthWaiting || draftAccount)) {
      setBusy(true);
      try {
        await proAccountsApi.cancelOAuth(managerBase, managementKey, operationIdentity.operationId);
      } catch (cancelError) {
        setError(cancelError instanceof Error ? cancelError.message : String(cancelError));
        setBusy(false);
        return;
      }
    }
    oauthWindowRef.current?.close();
    setApiKey('');
    onClose();
  };

  const nextLabel = useMemo(() => {
    if (step === 0) return '下一步';
    if (step === 1 && authType === 'api') return '探测并继续';
    if (step === 1 && authType === 'oauth') return oauthWaiting ? '检查授权结果' : '开始授权';
    if (step === 1) return '下一步';
    if (step === 2) return '确认配置';
    return '测试并保存';
  }, [authType, oauthWaiting, step]);

  const handleNext = () => {
    if (step === 0) advanceFromType();
    else if (step === 1) advanceFromCredentials();
    else if (step === 2) advanceFromModels();
    else void submit();
  };

  const footer = (
    <div className={styles.footer}>
      {step > 0 ? (
        <Button
          variant="secondary"
          size="sm"
          onClick={() => setStep((value) => Math.max(0, value - 1))}
          disabled={busy || oauthWaiting}
        >
          上一步
        </Button>
      ) : null}
      <Button variant="secondary" size="sm" onClick={() => void close()} disabled={busy}>
        取消
      </Button>
      <Button variant="primary" size="sm" onClick={handleNext} loading={busy}>
        {nextLabel}
      </Button>
    </div>
  );

  return (
    <Modal
      open={open}
      title="添加统一账号"
      onClose={() => void close()}
      footer={footer}
      width={760}
      className={styles.modal}
      closeDisabled={busy}
    >
      <div className={styles.body}>
        <div className={styles.steps} aria-label="添加步骤">
          {STEP_LABELS.map((label, index) => (
            <div
              key={label}
              className={`${styles.step} ${index === step ? styles.stepActive : ''}`}
            >
              {label}
            </div>
          ))}
        </div>

        {step === 0 ? (
          <div className={styles.formStack}>
            <div className={styles.field}>
              <h3 className={styles.sectionTitle}>平台</h3>
              <div className={styles.platforms}>
                {ACCOUNT_PLATFORMS.map((option) => (
                  <button
                    key={option.id}
                    type="button"
                    className={`${styles.choice} ${platform === option.id ? styles.choiceSelected : ''}`}
                    onClick={() => selectPlatform(option.id)}
                  >
                    {option.label}
                  </button>
                ))}
              </div>
            </div>
            <div className={styles.field}>
              <h3 className={styles.sectionTitle}>认证方式</h3>
              <div className={styles.authTypes}>
                {authTypesForPlatform(platform).map((type) => (
                  <button
                    key={type}
                    type="button"
                    className={`${styles.choice} ${authType === type ? styles.choiceSelected : ''}`}
                    onClick={() => setAuthType(type)}
                  >
                    {AUTH_TYPE_LABELS[type]}
                  </button>
                ))}
              </div>
            </div>
          </div>
        ) : null}

        {step === 1 && authType === 'api' ? (
          <div className={styles.formGrid}>
            <label className={styles.field}>
              <span className={styles.fieldLabel}>账号名称</span>
              <input
                className={styles.input}
                value={name}
                onChange={(event) => setName(event.target.value)}
                placeholder={accountDisplayName(platform, authType)}
              />
            </label>
            <label className={styles.field}>
              <span className={styles.fieldLabel}>Base URL</span>
              <input
                className={styles.input}
                value={baseUrl}
                onChange={(event) => setBaseUrl(event.target.value)}
                autoComplete="url"
              />
            </label>
            <label className={`${styles.field} ${styles.fieldFull}`}>
              <span className={styles.fieldLabel}>API Key</span>
              <input
                className={styles.input}
                type="password"
                value={apiKey}
                onChange={(event) => setApiKey(event.target.value)}
                autoComplete="new-password"
              />
            </label>
            {platform === 'openai' ? (
              <label className={styles.field}>
                <span className={styles.fieldLabel}>协议模式</span>
                <select
                  className={styles.select}
                  value={protocolMode}
                  onChange={(event) => setProtocolMode(event.target.value)}
                >
                  <option value="auto">自动探测</option>
                  <option value="responses">强制 Responses</option>
                  <option value="chat_completions">强制 Chat Completions</option>
                </select>
              </label>
            ) : null}
            <label className={`${styles.field} ${platform === 'openai' ? '' : styles.fieldFull}`}>
              <span className={styles.fieldLabel}>自定义 Headers</span>
              <textarea
                className={styles.textarea}
                value={headerLines}
                onChange={(event) => setHeaderLines(event.target.value)}
                rows={3}
                placeholder="Header-Name: value"
              />
            </label>
          </div>
        ) : null}

        {step === 1 && authType === 'oauth' ? (
          <div className={styles.formStack}>
            <div className={styles.oauthStatus}>
              <strong>{platformOption(platform)?.label} OAuth</strong>
              <span>{oauthWaiting ? '等待授权完成' : '尚未开始'}</span>
            </div>
            {oauthUrl ? (
              <a href={oauthUrl} target="_blank" rel="noreferrer" className={styles.notice}>
                在新窗口继续授权
              </a>
            ) : null}
          </div>
        ) : null}

        {step === 1 && authType === 'vertex' ? (
          <div className={styles.formGrid}>
            <label className={`${styles.field} ${styles.fieldFull}`}>
              <span className={styles.fieldLabel}>Service Account JSON</span>
              <input
                className={styles.input}
                type="file"
                accept="application/json,.json"
                onChange={(event) => setVertexFile(event.target.files?.[0] ?? null)}
              />
            </label>
            <label className={styles.field}>
              <span className={styles.fieldLabel}>地区</span>
              <input
                className={styles.input}
                value={location}
                onChange={(event) => setLocation(event.target.value)}
                placeholder="us-central1"
              />
            </label>
          </div>
        ) : null}

        {step === 2 ? (
          <AccountModelRulesEditor
            allowAll={allowAll}
            onAllowAllChange={setAllowAll}
            discoveredModels={discoveredModels}
            selectedModels={selectedModels}
            onSelectedModelsChange={setSelectedModels}
            manualModels={manualModels}
            onManualModelsChange={setManualModels}
            mappingLines={mappingLines}
            onMappingLinesChange={setMappingLines}
            testModel={testModel}
            onTestModelChange={setTestModel}
          />
        ) : null}

        {step === 3 ? (
          <div className={styles.formStack}>
            <dl className={styles.review}>
              <dt>平台 / 类型</dt>
              <dd>{accountDisplayName(platform, authType)}</dd>
              {authType === 'api' ? (
                <>
                  <dt>Base URL</dt>
                  <dd>{baseUrl}</dd>
                </>
              ) : null}
              {platform === 'openai' && authType === 'api' ? (
                <>
                  <dt>协议</dt>
                  <dd>{protocolLabel(probeResult?.selectedProtocol)}</dd>
                </>
              ) : null}
              <dt>允许模型</dt>
              <dd>
                {allowAll
                  ? '全部模型'
                  : `${selectedModels.length + parseModelLines(manualModels).length} 个模型`}
              </dd>
              <dt>模型映射</dt>
              <dd>{Object.keys(parseMappingLines(mappingLines)).length} 条</dd>
              <dt>测试模型</dt>
              <dd>{testModel}</dd>
            </dl>
            <SelectionCheckbox
              checked={saveDisabled}
              onChange={setSaveDisabled}
              label="连通性测试失败时保留为停用账号"
              ariaLabel="测试失败时保留停用账号"
            />
          </div>
        ) : null}

        {error ? (
          <div className={styles.error} role="alert">
            {error}
          </div>
        ) : null}
      </div>
    </Modal>
  );
}
