import { useCallback, useEffect, useMemo, useRef, useState } from 'react';
import { Button } from '@/components/ui/Button';
import { Modal } from '@/components/ui/Modal';
import { SelectionCheckbox } from '@/components/ui/SelectionCheckbox';
import {
  IconBot,
  IconCrosshair,
  IconFileText,
  IconKey,
  IconModelCluster,
  IconRefreshCw,
  IconShield,
} from '@/components/ui/icons';
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
  formatModelLines,
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

const protocolLabel = (value?: string) => {
  if (value === 'responses') return 'Responses';
  if (value === 'chat_completions') return 'Chat Completions';
  return '自动探测';
};

const authTypeDescription = (authType: AccountAuthType) => {
  if (authType === 'oauth') return '使用官方授权流程，凭证由 Gateway 安全保存并自动刷新。';
  if (authType === 'vertex') return '上传 Google Cloud Service Account JSON 并设置运行地区。';
  return '填写上游地址和 API Key，自动探测协议能力与可用模型。';
};

const platformIcon = (platform: AccountPlatform) => {
  if (platform === 'openai') return <IconBot size={16} />;
  if (platform === 'anthropic') return <IconShield size={16} />;
  if (platform === 'gemini') return <IconModelCluster size={16} />;
  if (platform === 'antigravity') return <IconFileText size={16} />;
  return <IconCrosshair size={16} />;
};

const authTypeIcon = (authType: AccountAuthType) => {
  if (authType === 'oauth') return <IconShield size={20} />;
  if (authType === 'vertex') return <IconFileText size={20} />;
  return <IconKey size={20} />;
};

export function AccountWizardModal({
  open,
  managerBase,
  managementKey,
  capabilities,
  onClose,
  onSaved,
}: AccountWizardModalProps) {
  const [step, setStep] = useState<0 | 1>(0);
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
  const [syncingModels, setSyncingModels] = useState(false);
  const [error, setError] = useState('');
  const oauthWindowRef = useRef<Window | null>(null);

  const credentialReady =
    authType === 'api' ? Boolean(probeResult?.sourceType) : Boolean(draftAccount);
  const configurationLocked = busy || oauthWaiting || credentialReady;

  const resetModelState = useCallback(() => {
    setAllowAll(true);
    setDiscoveredModels([]);
    setSelectedModels([]);
    setManualModels('');
    setMappingLines('');
    setTestModel('');
  }, []);

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
    resetModelState();
    setSaveDisabled(false);
    setProbeResult(null);
    setOperationIdentity(createRequestIdentity('account-add'));
    setOAuthWaiting(false);
    setOAuthUrl('');
    setDraftAccount(null);
    setBusy(false);
    setSyncingModels(false);
    setError('');
    oauthWindowRef.current = null;
  }, [resetModelState]);

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

  const clearCredentialResult = useCallback(() => {
    setProbeResult(null);
    setDraftAccount(null);
    setOAuthWaiting(false);
    setOAuthUrl('');
    resetModelState();
    setOperationIdentity(createRequestIdentity('account-add'));
    setError('');
  }, [resetModelState]);

  const selectPlatform = (value: AccountPlatform) => {
    if (configurationLocked) return;
    const option = platformOption(value);
    const nextAuthType = authTypesForPlatform(value, capabilities)[0] ?? 'api';
    setPlatform(value);
    setAuthType(nextAuthType);
    setBaseUrl(option?.defaultBaseUrl ?? '');
    setProtocolMode('auto');
    clearCredentialResult();
  };

  const selectAuthType = (value: AccountAuthType) => {
    if (configurationLocked) return;
    setAuthType(value);
    clearCredentialResult();
  };

  const validateSelection = () => {
    if (!authTypesForPlatform(platform, capabilities).includes(authType)) {
      setError('当前平台不支持所选认证方式');
      return false;
    }
    if (authType === 'oauth' && capabilities && !capabilities.credentialDraft) {
      setError('当前 Gateway 不支持停用草稿凭证，无法安全添加 OAuth 账号');
      return false;
    }
    return true;
  };

  const runAPIProbe = async (preserveSelection = false) => {
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
    if (preserveSelection) setSyncingModels(true);
    else setBusy(true);
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
      const models = result.probe.models ?? [];
      setProbeResult(result.probe);
      setDiscoveredModels(models);
      if (preserveSelection) {
        setSelectedModels((current) => current.filter((model) => models.includes(model)));
      } else {
        setSelectedModels(models);
        setAllowAll(models.length === 0);
      }
      setTestModel((current) => current || result.probe.testModel || models[0] || '');
    } catch (probeError) {
      setError(probeError instanceof Error ? probeError.message : String(probeError));
    } finally {
      if (preserveSelection) setSyncingModels(false);
      else setBusy(false);
    }
  };

  const loadDraftModels = useCallback(
    async (account: ProAccount, preserveSelection = false) => {
      const savedModels = account.allowedModels ?? [];
      const catalog = await proAccountsApi.modelCatalog(managerBase, managementKey, account.id);
      const models = catalog.models ?? [];
      setDiscoveredModels(models);
      if (preserveSelection) {
        setSelectedModels((current) => current.filter((model) => models.includes(model)));
      } else {
        setSelectedModels(savedModels.filter((model) => !model.includes('*')));
        setManualModels(formatModelLines(savedModels.filter((model) => model.includes('*'))));
        setAllowAll(savedModels.length === 0);
      }
      setTestModel((current) =>
        suggestedTestModel(current, savedModels, account.modelMapping, models)
      );
    },
    [managementKey, managerBase]
  );

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
        try {
          await loadDraftModels(result.account);
        } catch {
          const savedModels = result.account.allowedModels ?? [];
          setDiscoveredModels(savedModels.filter((model) => !model.includes('*')));
          setSelectedModels([]);
          setManualModels(formatModelLines(savedModels));
          setTestModel(suggestedTestModel('', savedModels, result.account.modelMapping));
          setAllowAll(savedModels.length === 0);
          setError('模型目录同步失败，可手工添加模型后继续');
        }
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
  }, [loadDraftModels, managementKey, managerBase, oauthWaiting, operationIdentity.operationId]);

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

  const createVertexDraft = async () => {
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
    const identity = createRequestIdentity('account-add');
    setOperationIdentity(identity);
    setBusy(true);
    setError('');
    try {
      const result = await proAccountsApi.createVertex(managerBase, managementKey, {
        ...identity,
        file: vertexFile,
        location: location.trim(),
        allowedModels: [],
        modelMapping: {},
        testModel: '',
        saveDisabledOnTestFailure: false,
        draftOnly: true,
      });
      setDraftAccount(result.account);
      try {
        await loadDraftModels(result.account);
      } catch {
        setDiscoveredModels([]);
        setSelectedModels([]);
        setAllowAll(true);
        setError('模型目录同步失败，可手工添加模型后继续');
      }
    } catch (vertexError) {
      setOperationIdentity(createRequestIdentity('account-add'));
      setError(vertexError instanceof Error ? vertexError.message : String(vertexError));
    } finally {
      setBusy(false);
    }
  };

  const prepareCredential = () => {
    if (!validateSelection()) return;
    setError('');
    if (authType === 'api') {
      void runAPIProbe();
      return;
    }
    if (authType === 'oauth') {
      if (!oauthWaiting) void startOAuth();
      else void checkOAuth();
      return;
    }
    void createVertexDraft();
  };

  const syncModels = async () => {
    if (authType === 'api') {
      await runAPIProbe(true);
      return;
    }
    if (!draftAccount) return;
    setSyncingModels(true);
    setError('');
    try {
      await loadDraftModels(draftAccount, true);
    } catch (syncError) {
      setError(syncError instanceof Error ? syncError.message : String(syncError));
    } finally {
      setSyncingModels(false);
    }
  };

  const restartCredential = async () => {
    if (busy) return;
    setBusy(true);
    setError('');
    try {
      if ((oauthWaiting || draftAccount) && operationIdentity.operationId) {
        await proAccountsApi.cancelDraft(managerBase, managementKey, operationIdentity.operationId);
      }
      oauthWindowRef.current?.close();
      oauthWindowRef.current = null;
      clearCredentialResult();
      if (authType === 'vertex') setVertexFile(null);
    } catch (restartError) {
      setError(restartError instanceof Error ? restartError.message : String(restartError));
    } finally {
      setBusy(false);
    }
  };

  const advanceToReview = () => {
    if (!credentialReady) {
      prepareCredential();
      return;
    }
    try {
      const resolved = rules();
      setTestModel(resolved.testModel);
      setError('');
      setStep(1);
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
      } else {
        if (!draftAccount) throw new Error('账号凭证草稿尚未保存');
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
      setError(submitError instanceof Error ? submitError.message : String(submitError));
      if (authType === 'api') setOperationIdentity(createRequestIdentity('account-add'));
    } finally {
      setBusy(false);
    }
  };

  const close = async () => {
    if (busy) return;
    if ((authType === 'oauth' || authType === 'vertex') && (oauthWaiting || draftAccount)) {
      setBusy(true);
      try {
        await proAccountsApi.cancelDraft(managerBase, managementKey, operationIdentity.operationId);
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

  const primaryLabel = useMemo(() => {
    if (step === 1) return '测试并保存';
    if (credentialReady) return '下一步';
    if (authType === 'api') return '探测账号能力';
    if (authType === 'oauth') return oauthWaiting ? '检查授权结果' : '开始授权';
    return '读取凭证并同步模型';
  }, [authType, credentialReady, oauthWaiting, step]);

  const footer = (
    <div className={styles.footer}>
      {step === 1 ? (
        <Button variant="secondary" onClick={() => setStep(0)} disabled={busy}>
          返回配置
        </Button>
      ) : null}
      <span className={styles.footerSpacer} />
      <Button variant="secondary" onClick={() => void close()} disabled={busy}>
        取消
      </Button>
      <Button
        variant="primary"
        onClick={step === 1 ? () => void submit() : advanceToReview}
        loading={busy}
      >
        {primaryLabel}
      </Button>
    </div>
  );

  return (
    <Modal
      open={open}
      title="添加统一账号"
      onClose={() => void close()}
      footer={footer}
      width={920}
      className={styles.modal}
      closeDisabled={busy}
    >
      <div className={styles.body}>
        <div className={styles.stepper} aria-label="添加账号步骤">
          <div className={`${styles.stepperItem} ${styles.stepperItemActive}`}>
            <span>1</span>
            <div>
              <strong>账号配置</strong>
              <small>平台、认证和模型规则</small>
            </div>
          </div>
          <div className={`${styles.stepperLine} ${step === 1 ? styles.stepperLineActive : ''}`} />
          <div className={`${styles.stepperItem} ${step === 1 ? styles.stepperItemActive : ''}`}>
            <span>2</span>
            <div>
              <strong>测试并保存</strong>
              <small>确认配置和连通性测试</small>
            </div>
          </div>
        </div>

        {step === 0 ? (
          <div className={styles.wizardStack}>
            <section className={styles.formSection}>
              <div className={styles.sectionHeader}>
                <div>
                  <h3 className={styles.sectionTitle}>选择平台</h3>
                  <p className={styles.sectionDescription}>
                    中转站按上游协议归类；OpenAI 格式的其他模型中转站请选择 OpenAI。
                  </p>
                </div>
              </div>
              <div className={styles.platformSegments} role="group" aria-label="账号平台">
                {ACCOUNT_PLATFORMS.map((option) => (
                  <button
                    key={option.id}
                    type="button"
                    className={platform === option.id ? styles.platformSegmentActive : ''}
                    onClick={() => selectPlatform(option.id)}
                    disabled={configurationLocked}
                  >
                    {platformIcon(option.id)}
                    <span>{option.label}</span>
                  </button>
                ))}
              </div>

              <div className={styles.sectionDivider} />

              <div className={styles.sectionHeader}>
                <div>
                  <h3 className={styles.sectionTitle}>认证方式</h3>
                  <p className={styles.sectionDescription}>仅展示当前平台实际支持的添加方式。</p>
                </div>
              </div>
              <div className={styles.authCards}>
                {authTypesForPlatform(platform, capabilities).map((type) => (
                  <button
                    key={type}
                    type="button"
                    className={`${styles.authCard} ${authType === type ? styles.authCardSelected : ''}`}
                    onClick={() => selectAuthType(type)}
                    disabled={configurationLocked}
                  >
                    <span className={styles.authCardIcon}>{authTypeIcon(type)}</span>
                    <span className={styles.authCardText}>
                      <strong>{AUTH_TYPE_LABELS[type]}</strong>
                      <small>{authTypeDescription(type)}</small>
                    </span>
                  </button>
                ))}
              </div>
              {platform === 'gemini' &&
              capabilities?.platforms?.gemini?.oauth?.status !== 'supported' ? (
                <div className={styles.notice}>
                  Gemini OAuth 需要已启用并成功注册的 gemini-cli 插件，当前仅提供 API 和 Vertex。
                </div>
              ) : null}
            </section>

            <section className={styles.formSection}>
              <div className={styles.sectionHeader}>
                <div>
                  <h3 className={styles.sectionTitle}>账号与凭证</h3>
                  <p className={styles.sectionDescription}>
                    完成凭证探测或授权后，模型配置会直接显示在本页下方。
                  </p>
                </div>
                {credentialReady ? (
                  <Button variant="secondary" size="xs" onClick={() => void restartCredential()}>
                    <IconRefreshCw size={14} /> 重新配置凭证
                  </Button>
                ) : null}
              </div>

              <div className={styles.formGrid}>
                <label className={`${styles.field} ${styles.fieldFull}`}>
                  <span className={styles.fieldLabel}>账号名称</span>
                  <input
                    className={styles.input}
                    value={name}
                    onChange={(event) => setName(event.target.value)}
                    placeholder={
                      authType === 'api'
                        ? accountDisplayName(platform, authType)
                        : '授权后自动使用账号名称或邮箱'
                    }
                    disabled={authType !== 'api' || credentialReady}
                  />
                  <span className={styles.fieldHint}>
                    {authType === 'api'
                      ? '可选；留空时使用平台和认证类型生成显示名称。'
                      : 'OAuth 和 Vertex 会从凭证中自动识别，保存后仍可在编辑页面修改。'}
                  </span>
                </label>

                {authType === 'api' ? (
                  <>
                    <label className={styles.field}>
                      <span className={styles.fieldLabel}>Base URL</span>
                      <input
                        className={styles.input}
                        value={baseUrl}
                        onChange={(event) => setBaseUrl(event.target.value)}
                        autoComplete="url"
                        disabled={credentialReady}
                      />
                    </label>
                    {platform === 'openai' ? (
                      <label className={styles.field}>
                        <span className={styles.fieldLabel}>协议模式</span>
                        <select
                          className={styles.select}
                          value={protocolMode}
                          onChange={(event) => setProtocolMode(event.target.value)}
                          disabled={credentialReady}
                        >
                          <option value="auto">自动探测</option>
                          <option value="responses">强制 Responses</option>
                          <option value="chat_completions">强制 Chat Completions</option>
                        </select>
                      </label>
                    ) : null}
                    <label className={`${styles.field} ${styles.fieldFull}`}>
                      <span className={styles.fieldLabel}>API Key</span>
                      <input
                        className={styles.input}
                        type="password"
                        value={apiKey}
                        onChange={(event) => setApiKey(event.target.value)}
                        autoComplete="new-password"
                        disabled={credentialReady}
                        placeholder="输入上游 API Key"
                      />
                    </label>
                    <label className={`${styles.field} ${styles.fieldFull}`}>
                      <span className={styles.fieldLabel}>自定义 Headers</span>
                      <textarea
                        className={styles.textarea}
                        value={headerLines}
                        onChange={(event) => setHeaderLines(event.target.value)}
                        rows={3}
                        placeholder="Header-Name: value"
                        disabled={credentialReady}
                      />
                      <span className={styles.fieldHint}>
                        每行一个 Header，凭证 Header 不允许覆盖。
                      </span>
                    </label>
                  </>
                ) : null}

                {authType === 'vertex' ? (
                  <>
                    <label className={`${styles.field} ${styles.fieldFull}`}>
                      <span className={styles.fieldLabel}>Service Account JSON</span>
                      <input
                        className={styles.fileInput}
                        type="file"
                        accept="application/json,.json"
                        onChange={(event) => setVertexFile(event.target.files?.[0] ?? null)}
                        disabled={credentialReady}
                      />
                      <span className={styles.fieldHint}>文件最大 2 MiB，仅在创建过程中上传。</span>
                    </label>
                    <label className={styles.field}>
                      <span className={styles.fieldLabel}>地区</span>
                      <input
                        className={styles.input}
                        value={location}
                        onChange={(event) => setLocation(event.target.value)}
                        placeholder="us-central1"
                        disabled={credentialReady}
                      />
                    </label>
                  </>
                ) : null}
              </div>

              {authType === 'oauth' ? (
                <div className={styles.oauthPanel}>
                  <span className={styles.oauthPanelIcon}>
                    <IconShield size={22} />
                  </span>
                  <div>
                    <strong>{platformOption(platform)?.label} OAuth</strong>
                    <p>
                      {credentialReady
                        ? '授权已完成，凭证草稿已安全保存。'
                        : oauthWaiting
                          ? '等待浏览器中的授权流程完成，页面会自动检查结果。'
                          : '点击底部“开始授权”，在新窗口完成官方授权。'}
                    </p>
                    {oauthUrl && oauthWaiting ? (
                      <a href={oauthUrl} target="_blank" rel="noreferrer">
                        在新窗口继续授权
                      </a>
                    ) : null}
                  </div>
                </div>
              ) : null}

              {credentialReady ? (
                <div className={styles.credentialSuccess}>
                  <span>✓</span>
                  <div>
                    <strong>凭证准备完成</strong>
                    <p>
                      {authType === 'api'
                        ? `${protocolLabel(probeResult?.selectedProtocol)} · 已同步 ${discoveredModels.length} 个模型`
                        : `凭证草稿已保存 · 已同步 ${discoveredModels.length} 个模型`}
                    </p>
                  </div>
                </div>
              ) : null}

              {probeResult?.warnings?.length ? (
                <div className={styles.notice}>{probeResult.warnings.join('；')}</div>
              ) : null}
            </section>

            {credentialReady ? (
              <section className={styles.formSection}>
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
                  onSyncModels={() => void syncModels()}
                  syncingModels={syncingModels}
                />
              </section>
            ) : null}
          </div>
        ) : (
          <div className={styles.reviewPage}>
            <section className={styles.reviewHero}>
              <span className={styles.reviewHeroIcon}>✓</span>
              <div>
                <h3>配置已准备完成</h3>
                <p>保存时将使用所选模型发起真实请求，测试成功后才会启用账号。</p>
              </div>
            </section>

            <section className={styles.formSection}>
              <div className={styles.sectionHeader}>
                <div>
                  <h3 className={styles.sectionTitle}>配置摘要</h3>
                  <p className={styles.sectionDescription}>请确认平台、认证方式和模型规则。</p>
                </div>
              </div>
              <dl className={styles.review}>
                <dt>平台 / 类型</dt>
                <dd>{accountDisplayName(platform, authType)}</dd>
                {authType === 'api' ? (
                  <>
                    <dt>账号名称</dt>
                    <dd>{name.trim() || accountDisplayName(platform, authType)}</dd>
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
            </section>

            <section className={styles.formSection}>
              <div className={styles.sectionHeader}>
                <div>
                  <h3 className={styles.sectionTitle}>测试失败处理</h3>
                  <p className={styles.sectionDescription}>
                    默认测试失败时回滚本次创建；也可以保留为停用账号，稍后继续排查。
                  </p>
                </div>
              </div>
              <div className={styles.saveDisabledOption}>
                <SelectionCheckbox
                  checked={saveDisabled}
                  onChange={setSaveDisabled}
                  label="连通性测试失败时保留为停用账号"
                  ariaLabel="测试失败时保留停用账号"
                />
              </div>
            </section>
          </div>
        )}

        {error ? (
          <div className={styles.error} role="alert">
            {error}
          </div>
        ) : null}
      </div>
    </Modal>
  );
}
