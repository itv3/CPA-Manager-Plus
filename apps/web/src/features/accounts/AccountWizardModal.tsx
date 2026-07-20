import { useCallback, useEffect, useMemo, useRef, useState } from 'react';
import { Button } from '@/components/ui/Button';
import { Modal } from '@/components/ui/Modal';
import { SelectionCheckbox } from '@/components/ui/SelectionCheckbox';
import { ToggleSwitch } from '@/components/ui/ToggleSwitch';
import {
  IconBot,
  IconCheck,
  IconCopy,
  IconCrosshair,
  IconExternalLink,
  IconFileText,
  IconInfo,
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
import { copyToClipboard } from '@/utils/clipboard';
import { AccountModelRulesEditor } from './AccountModelRulesEditor';
import {
  ACCOUNT_PLATFORMS,
  AUTH_TYPE_LABELS,
  accountDisplayName,
  authTypesForPlatform,
  createRequestIdentity,
  parseHeaderLines,
  parseMappingLines,
  platformOption,
  resolveAccountModelRules,
  type AccountAuthType,
  type AccountPlatform,
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

const mergeUnique = (current: string[], additions: string[]) => {
  const seen = new Set(current.map((item) => item.toLowerCase()));
  const merged = [...current];
  additions.forEach((item) => {
    const normalized = item.trim();
    const key = normalized.toLowerCase();
    if (!normalized || seen.has(key)) return;
    seen.add(key);
    merged.push(normalized);
  });
  return merged;
};

// 与 sub2api 一致：粘贴完整回调地址时，仅在输入框保留授权 Code，state 单独保存。
const normalizeOAuthCallbackInput = (value: string) => {
  const trimmed = value.trim();
  if (!trimmed.includes('code=')) {
    return { code: value, state: '' };
  }
  try {
    const callbackUrl = trimmed.includes('?')
      ? new URL(trimmed)
      : new URL(`http://localhost/callback?${trimmed.replace(/^\?/, '')}`);
    const code = callbackUrl.searchParams.get('code');
    const state = callbackUrl.searchParams.get('state') ?? '';
    if (code && code !== trimmed) {
      return { code, state };
    }
  } catch {
    const codeMatch = trimmed.match(/[?&]code=([^&]+)/);
    const stateMatch = trimmed.match(/[?&]state=([^&]+)/);
    if (codeMatch?.[1] && codeMatch[1] !== trimmed) {
      return { code: codeMatch[1], state: stateMatch?.[1] ?? '' };
    }
  }
  return { code: value, state: '' };
};

const authTypeDescription = (authType: AccountAuthType) => {
  if (authType === 'oauth') return '使用官方授权流程，凭证由 Gateway 安全保存并自动刷新。';
  if (authType === 'vertex') return '上传 Google Cloud Service Account JSON 并设置运行地区。';
  return '填写上游地址和 API Key，直接保存为中转账号。';
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
  const [notes, setNotes] = useState('');
  const [baseUrl, setBaseUrl] = useState('https://api.openai.com');
  const [apiKey, setApiKey] = useState('');
  const [proxyUrl, setProxyUrl] = useState('');
  const [protocolMode, setProtocolMode] = useState('auto');
  const [headerLines, setHeaderLines] = useState('');
  const [vertexFile, setVertexFile] = useState<File | null>(null);
  const [location, setLocation] = useState('us-central1');
  const [whitelistModels, setWhitelistModels] = useState<string[]>([]);
  const [catalogModels, setCatalogModels] = useState<string[]>([]);
  const [mappingLines, setMappingLines] = useState('');
  const [saveDisabled, setSaveDisabled] = useState(false);
  const [officialClientCompatibilityEnabled, setOfficialClientCompatibilityEnabled] =
    useState(false);
  const [probeResult, setProbeResult] = useState<ProAccountProbeResult | null>(null);
  const [oauthOperationId, setOAuthOperationId] = useState('');
  const [oauthWaiting, setOAuthWaiting] = useState(false);
  const [oauthUrl, setOAuthUrl] = useState('');
  const [oauthCallbackInput, setOAuthCallbackInput] = useState('');
  const [oauthCallbackState, setOAuthCallbackState] = useState('');
  const [oauthCallbackSubmitted, setOAuthCallbackSubmitted] = useState(false);
  const [oauthFinalizeRequested, setOAuthFinalizeRequested] = useState(false);
  const [oauthLinkCopied, setOAuthLinkCopied] = useState(false);
  const [draftAccount, setDraftAccount] = useState<ProAccount | null>(null);
  const [busy, setBusy] = useState(false);
  const [syncingBuiltIn, setSyncingBuiltIn] = useState(false);
  const [syncingUpstream, setSyncingUpstream] = useState(false);
  const [error, setError] = useState('');
  const oauthCopyTimerRef = useRef<number | null>(null);
  const oauthDraftAcceptedRef = useRef(false);

  const syncingModels = syncingBuiltIn || syncingUpstream;
  const configurationLocked =
    busy || syncingModels || oauthWaiting || (authType !== 'api' && Boolean(draftAccount));

  const resetModelState = useCallback(() => {
    setWhitelistModels([]);
    setCatalogModels([]);
    setMappingLines('');
  }, []);

  const reset = useCallback(() => {
    setStep(0);
    setPlatform('openai');
    setAuthType('oauth');
    setName('');
    setNotes('');
    setBaseUrl('https://api.openai.com');
    setApiKey('');
    setProxyUrl('');
    setProtocolMode('auto');
    setHeaderLines('');
    setVertexFile(null);
    setLocation('us-central1');
    resetModelState();
    setSaveDisabled(false);
    setOfficialClientCompatibilityEnabled(false);
    setProbeResult(null);
    setOAuthOperationId('');
    setOAuthWaiting(false);
    setOAuthUrl('');
    setOAuthCallbackInput('');
    setOAuthCallbackState('');
    setOAuthCallbackSubmitted(false);
    setOAuthFinalizeRequested(false);
    setOAuthLinkCopied(false);
    setDraftAccount(null);
    setBusy(false);
    setSyncingBuiltIn(false);
    setSyncingUpstream(false);
    setError('');
    oauthDraftAcceptedRef.current = false;
    if (oauthCopyTimerRef.current !== null) {
      window.clearTimeout(oauthCopyTimerRef.current);
      oauthCopyTimerRef.current = null;
    }
  }, [resetModelState]);

  useEffect(() => {
    if (open) reset();
  }, [open, reset]);

  useEffect(
    () => () => {
      if (oauthCopyTimerRef.current !== null) {
        window.clearTimeout(oauthCopyTimerRef.current);
      }
    },
    []
  );

  const clearCredentialResult = useCallback(() => {
    setProbeResult(null);
    setDraftAccount(null);
    setOAuthOperationId('');
    setOAuthWaiting(false);
    setOAuthUrl('');
    setOAuthCallbackInput('');
    setOAuthCallbackState('');
    setOAuthCallbackSubmitted(false);
    setOAuthFinalizeRequested(false);
    setOAuthLinkCopied(false);
    resetModelState();
    setError('');
  }, [resetModelState]);

  const officialClientCompatibilityEligible =
    authType === 'api' &&
    (platform === 'anthropic' ||
      (platform === 'openai' &&
        (protocolMode === 'responses' || probeResult?.sourceType === 'config_codex_api_key')));
  const officialClientCompatibilitySupported = Boolean(capabilities?.officialClientCompatibility);

  useEffect(() => {
    if (!officialClientCompatibilityEligible) {
      setOfficialClientCompatibilityEnabled(false);
    }
  }, [officialClientCompatibilityEligible]);

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
    if (!name.trim()) {
      setError('请输入账号名称');
      return false;
    }
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

  // 同步上游支持的模型(API 方式):独立的候选凭证探测,结果直接填入白名单
  const runAPIProbe = async () => {
    if (!baseUrl.trim() || !apiKey.trim()) {
      setError('请先填写 Base URL 和 API Key，再同步上游支持的模型');
      return;
    }
    let headers: Record<string, string>;
    try {
      headers = parseHeaderLines(headerLines);
    } catch (parseError) {
      setError(parseError instanceof Error ? parseError.message : String(parseError));
      return;
    }
    const identity = createRequestIdentity('account-probe');
    setSyncingUpstream(true);
    setError('');
    try {
      const result = await proAccountsApi.probe(managerBase, managementKey, {
        ...identity,
        platform,
        baseUrl: baseUrl.trim(),
        apiKey,
        proxyUrl: proxyUrl.trim() || undefined,
        protocolMode,
        allowedModels: [],
        modelMapping: {},
        headers,
      });
      const upstream = result.probe?.upstreamModels ?? [];
      const models = result.probe?.models ?? [];
      setProbeResult(result.probe ?? null);
      setCatalogModels((current) => mergeUnique(current, models));
      if (upstream.length > 0) {
        setWhitelistModels((current) => mergeUnique(current, upstream));
      } else {
        setError('上游未提供模型列表，可同步最新支持模型或直接填入自定义模型名称');
      }
    } catch (probeError) {
      setError(probeError instanceof Error ? probeError.message : String(probeError));
    } finally {
      setSyncingUpstream(false);
    }
  };

  const loadDraftModels = useCallback(
    async (account: ProAccount, mergeSelection = false) => {
      const savedModels = account.allowedModels ?? [];
      const catalog = await proAccountsApi.modelCatalog(managerBase, managementKey, account.id);
      const models = catalog.models ?? [];
      setCatalogModels((current) => (mergeSelection ? mergeUnique(current, models) : models));
      if (mergeSelection) {
        const upstream = catalog.upstream?.length ? catalog.upstream : models;
        setWhitelistModels((current) => mergeUnique(current, upstream));
      } else {
        setWhitelistModels(savedModels);
      }
    },
    [managementKey, managerBase]
  );

  // OAuth 第二步沿用第一页已选择的白名单，只补充凭证可见的模型目录。
  const loadDraftCatalog = useCallback(
    async (account: ProAccount) => {
      const catalog = await proAccountsApi.modelCatalog(managerBase, managementKey, account.id);
      const models = catalog.models ?? [];
      setCatalogModels((current) => mergeUnique(current, models));
      return models;
    },
    [managementKey, managerBase]
  );

  const finalizeOAuthDraft = useCallback(
    async (account: ProAccount, discoveredModels = catalogModels) => {
      let resolvedRules: ReturnType<typeof resolveAccountModelRules>;
      try {
        resolvedRules = resolveAccountModelRules({
          models: whitelistModels,
          mappingLines,
          discoveredModels,
        });
      } catch (rulesError) {
        setError(rulesError instanceof Error ? rulesError.message : String(rulesError));
        return;
      }
      setBusy(true);
      setError('');
      try {
        const result = await proAccountsApi.completeDraft(managerBase, managementKey, account.id, {
          operationId: oauthOperationId,
          expectedVersion: account.version,
          ...resolvedRules,
          saveDisabledOnTestFailure: saveDisabled,
        });
        onSaved(result);
        onClose();
      } catch (completeError) {
        setError(completeError instanceof Error ? completeError.message : String(completeError));
      } finally {
        setBusy(false);
        setOAuthFinalizeRequested(false);
      }
    },
    [
      catalogModels,
      managementKey,
      managerBase,
      mappingLines,
      onClose,
      onSaved,
      oauthOperationId,
      saveDisabled,
      whitelistModels,
    ]
  );

  const acceptOAuthDraft = useCallback(
    async (account: ProAccount, finalize: boolean) => {
      setDraftAccount(account);
      setOAuthWaiting(false);
      let discoveredModels = catalogModels;
      try {
        const accountModels = await loadDraftCatalog(account);
        discoveredModels = mergeUnique(catalogModels, accountModels);
      } catch {
        setError('模型目录同步失败，将使用第一页配置的模型规则继续');
      }
      if (finalize) {
        await finalizeOAuthDraft(account, discoveredModels);
      }
    },
    [catalogModels, finalizeOAuthDraft, loadDraftCatalog]
  );

  // 同步最新支持模型:项目内置目录,不依赖账号凭证,结果直接填入白名单
  const syncBuiltInModels = async () => {
    setSyncingBuiltIn(true);
    setError('');
    try {
      const catalog = await proAccountsApi.staticModelCatalog(
        managerBase,
        managementKey,
        platform,
        authType
      );
      const builtIn = catalog.builtIn?.length ? catalog.builtIn : (catalog.models ?? []);
      if (builtIn.length === 0) {
        setError('项目内置目录暂无该平台模型，可同步上游支持的模型或填入自定义模型名称');
        return;
      }
      setCatalogModels((current) => mergeUnique(current, builtIn));
      setWhitelistModels((current) => mergeUnique(current, builtIn));
    } catch (syncError) {
      setError(syncError instanceof Error ? syncError.message : String(syncError));
    } finally {
      setSyncingBuiltIn(false);
    }
  };

  const syncUpstreamModels = async () => {
    if (authType === 'api') {
      await runAPIProbe();
      return;
    }
    if (!draftAccount) {
      setError('请先完成授权或读取凭证，再同步上游支持的模型');
      return;
    }
    setSyncingUpstream(true);
    setError('');
    try {
      await loadDraftModels(draftAccount, true);
    } catch (syncError) {
      setError(syncError instanceof Error ? syncError.message : String(syncError));
    } finally {
      setSyncingUpstream(false);
    }
  };

  const checkOAuth = useCallback(async () => {
    if (!oauthWaiting || !oauthOperationId) return;
    try {
      const result = await proAccountsApi.oauthStatus(managerBase, managementKey, oauthOperationId);
      if (result.account && result.status === 'ok') {
        if (oauthDraftAcceptedRef.current) return;
        oauthDraftAcceptedRef.current = true;
        await acceptOAuthDraft(result.account, oauthFinalizeRequested);
        return;
      }
      if (result.status === 'ambiguous' || result.status === 'error') {
        setOAuthWaiting(false);
        setOAuthFinalizeRequested(false);
        setError(result.operation.errorSummary || 'OAuth 授权未完成');
      }
    } catch (statusError) {
      setError(statusError instanceof Error ? statusError.message : String(statusError));
    }
  }, [
    acceptOAuthDraft,
    managementKey,
    managerBase,
    oauthFinalizeRequested,
    oauthOperationId,
    oauthWaiting,
  ]);

  useEffect(() => {
    if (!open || !oauthWaiting || typeof window === 'undefined') return;
    const timer = window.setInterval(() => void checkOAuth(), 2000);
    return () => window.clearInterval(timer);
  }, [checkOAuth, oauthWaiting, open]);

  const startOAuth = async () => {
    const identity = createRequestIdentity('account-oauth');
    setOAuthOperationId(identity.operationId);
    setBusy(true);
    setError('');
    oauthDraftAcceptedRef.current = false;
    try {
      const result = await proAccountsApi.startOAuth(managerBase, managementKey, {
        ...identity,
        platform,
        name: name.trim(),
        notes: notes.trim(),
      });
      const url = result.oauth?.url ?? '';
      if (!url) throw new Error('Gateway 未返回授权地址');
      setOAuthUrl(url);
      setOAuthCallbackState(result.oauth?.state ?? '');
      setOAuthWaiting(true);
    } catch (oauthError) {
      setError(oauthError instanceof Error ? oauthError.message : String(oauthError));
    } finally {
      setBusy(false);
    }
  };

  const copyOAuthLink = async () => {
    if (!oauthUrl) return;
    const copied = await copyToClipboard(oauthUrl);
    if (!copied) {
      setError('复制授权链接失败，请手工选择并复制');
      return;
    }
    setOAuthLinkCopied(true);
    if (oauthCopyTimerRef.current !== null) {
      window.clearTimeout(oauthCopyTimerRef.current);
    }
    oauthCopyTimerRef.current = window.setTimeout(() => {
      setOAuthLinkCopied(false);
      oauthCopyTimerRef.current = null;
    }, 1800);
  };

  const openOAuthLink = () => {
    if (!oauthUrl) return;
    window.open(oauthUrl, '_blank', 'noopener,noreferrer');
  };

  const clearOAuthSessionState = () => {
    setOAuthOperationId('');
    setOAuthWaiting(false);
    setOAuthUrl('');
    setOAuthCallbackInput('');
    setOAuthCallbackState('');
    setOAuthCallbackSubmitted(false);
    setOAuthFinalizeRequested(false);
    setOAuthLinkCopied(false);
    setDraftAccount(null);
    oauthDraftAcceptedRef.current = false;
  };

  const regenerateOAuthLink = async () => {
    if (busy) return;
    setBusy(true);
    setError('');
    try {
      if (oauthOperationId) {
        await proAccountsApi.cancelDraft(managerBase, managementKey, oauthOperationId);
      }
      clearOAuthSessionState();
    } catch (restartError) {
      setError(restartError instanceof Error ? restartError.message : String(restartError));
      setBusy(false);
      return;
    }
    setBusy(false);
    await startOAuth();
  };

  const backToOAuthConfiguration = async () => {
    if (busy) return;
    setBusy(true);
    setError('');
    try {
      if (oauthOperationId) {
        await proAccountsApi.cancelDraft(managerBase, managementKey, oauthOperationId);
      }
      clearOAuthSessionState();
      setStep(0);
    } catch (backError) {
      setError(backError instanceof Error ? backError.message : String(backError));
    } finally {
      setBusy(false);
    }
  };

  const completeOAuthAuthorization = async () => {
    if (draftAccount) {
      await finalizeOAuthDraft(draftAccount);
      return;
    }
    if (!oauthOperationId || !oauthUrl) {
      setError('请先生成授权链接');
      return;
    }
    if (!oauthCallbackInput.trim()) {
      setError('请输入完整回调地址、回调参数或授权 Code');
      return;
    }
    setBusy(true);
    setError('');
    try {
      await proAccountsApi.submitOAuthCallback(
        managerBase,
        managementKey,
        oauthOperationId,
        oauthCallbackInput.trim(),
        oauthCallbackState
      );
      setOAuthCallbackSubmitted(true);
      setOAuthFinalizeRequested(true);
      setOAuthWaiting(true);
      const result = await proAccountsApi.oauthStatus(managerBase, managementKey, oauthOperationId);
      if (result.account && result.status === 'ok') {
        if (oauthDraftAcceptedRef.current) return;
        oauthDraftAcceptedRef.current = true;
        setBusy(false);
        await acceptOAuthDraft(result.account, true);
        return;
      }
      if (result.status === 'ambiguous' || result.status === 'error') {
        setOAuthWaiting(false);
        setOAuthFinalizeRequested(false);
        setError(result.operation.errorSummary || 'OAuth 授权未完成');
      }
    } catch (callbackError) {
      setOAuthCallbackSubmitted(false);
      setOAuthFinalizeRequested(false);
      setError(callbackError instanceof Error ? callbackError.message : String(callbackError));
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
    const identity = createRequestIdentity('account-vertex');
    setOAuthOperationId(identity.operationId);
    setBusy(true);
    setError('');
    try {
      const result = await proAccountsApi.createVertex(managerBase, managementKey, {
        ...identity,
        name: name.trim(),
        notes: notes.trim(),
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
        setWhitelistModels([]);
        setError('模型目录同步失败，可手工填入模型后继续');
      }
    } catch (vertexError) {
      setError(vertexError instanceof Error ? vertexError.message : String(vertexError));
    } finally {
      setBusy(false);
    }
  };

  const prepareCredential = () => {
    if (!validateSelection()) return;
    setError('');
    if (authType === 'oauth') return;
    void createVertexDraft();
  };

  const restartCredential = async () => {
    if (busy) return;
    setBusy(true);
    setError('');
    try {
      if ((oauthWaiting || draftAccount) && oauthOperationId) {
        await proAccountsApi.cancelDraft(managerBase, managementKey, oauthOperationId);
      }
      clearCredentialResult();
      if (authType === 'vertex') setVertexFile(null);
    } catch (restartError) {
      setError(restartError instanceof Error ? restartError.message : String(restartError));
    } finally {
      setBusy(false);
    }
  };

  const advanceToReview = () => {
    if (authType === 'oauth') {
      if (!validateSelection()) return;
      try {
        resolveAccountModelRules({
          models: whitelistModels,
          mappingLines,
          discoveredModels: catalogModels,
        });
        setError('');
        setStep(1);
      } catch (rulesError) {
        setError(rulesError instanceof Error ? rulesError.message : String(rulesError));
      }
      return;
    }
    if (!draftAccount) {
      prepareCredential();
      return;
    }
    try {
      resolveAccountModelRules({
        models: whitelistModels,
        mappingLines,
        discoveredModels: catalogModels,
      });
      setError('');
      setStep(1);
    } catch (rulesError) {
      setError(rulesError instanceof Error ? rulesError.message : String(rulesError));
    }
  };

  const submit = async () => {
    if (!validateSelection()) return;
    let resolvedRules: ReturnType<typeof resolveAccountModelRules>;
    let headers: Record<string, string> = {};
    try {
      resolvedRules = resolveAccountModelRules({
        models: whitelistModels,
        mappingLines,
        discoveredModels: catalogModels,
      });
      if (authType === 'api') {
        if (!baseUrl.trim() || !apiKey.trim()) {
          throw new Error('请输入 Base URL 和 API Key');
        }
        headers = parseHeaderLines(headerLines);
      }
    } catch (submitError) {
      setError(submitError instanceof Error ? submitError.message : String(submitError));
      return;
    }
    // 每次保存使用独立操作 ID,与探测操作互不影响
    const identity = createRequestIdentity('account-save');
    setBusy(true);
    setError('');
    try {
      let result: ProAccountLifecycleResult;
      if (authType === 'api') {
        result = await proAccountsApi.createAPI(managerBase, managementKey, {
          ...identity,
          platform,
          name: name.trim(),
          notes: notes.trim(),
          baseUrl: baseUrl.trim(),
          apiKey,
          proxyUrl: proxyUrl.trim() || undefined,
          protocolMode,
          headers,
          ...resolvedRules,
          saveDisabledOnTestFailure: true,
          officialClientCompatibility: officialClientCompatibilityEnabled
            ? { enabled: true, profile: '', tlsProfile: '' }
            : undefined,
          // 与 sub2api 一致:保存即启用,连通性由列表中的"测试"入口单独验证
          skipTest: true,
        });
      } else {
        if (!draftAccount) throw new Error('账号凭证草稿尚未保存');
        result = await proAccountsApi.completeDraft(managerBase, managementKey, draftAccount.id, {
          operationId: oauthOperationId,
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
    } finally {
      setBusy(false);
    }
  };

  const close = async () => {
    if (busy) return;
    if ((authType === 'oauth' || authType === 'vertex') && (oauthWaiting || draftAccount)) {
      setBusy(true);
      try {
        await proAccountsApi.cancelDraft(managerBase, managementKey, oauthOperationId);
      } catch (cancelError) {
        setError(cancelError instanceof Error ? cancelError.message : String(cancelError));
        setBusy(false);
        return;
      }
    }
    setApiKey('');
    onClose();
  };

  const primaryLabel = useMemo(() => {
    if (step === 1 && authType === 'oauth') {
      if (oauthFinalizeRequested) return '等待授权结果';
      if (draftAccount) return '保存账号';
      return '完成授权';
    }
    if (step === 1) return '保存';
    if (draftAccount) return '下一步';
    if (authType === 'oauth') return '下一步';
    return '读取凭证并同步模型';
  }, [authType, draftAccount, oauthFinalizeRequested, step]);

  const apiFooter = (
    <div className={styles.footer}>
      <Button variant="secondary" onClick={() => void close()} disabled={busy || syncingModels}>
        取消
      </Button>
      <span className={styles.footerSpacer} />
      <Button
        variant="primary"
        onClick={() => void submit()}
        loading={busy}
        disabled={busy || syncingModels}
      >
        保存
      </Button>
    </div>
  );

  const stagedFooter = (
    <div className={styles.footer}>
      {step === 1 ? (
        <Button
          variant="secondary"
          onClick={authType === 'oauth' ? () => void backToOAuthConfiguration() : () => setStep(0)}
          disabled={busy || oauthFinalizeRequested}
        >
          {authType === 'oauth' ? '返回' : '返回配置'}
        </Button>
      ) : null}
      <span className={styles.footerSpacer} />
      {step === 0 || authType !== 'oauth' ? (
        <Button variant="secondary" onClick={() => void close()} disabled={busy}>
          取消
        </Button>
      ) : null}
      <Button
        variant="primary"
        onClick={
          step === 1
            ? authType === 'oauth'
              ? () => void completeOAuthAuthorization()
              : () => void submit()
            : advanceToReview
        }
        loading={busy}
        disabled={
          busy ||
          oauthFinalizeRequested ||
          (step === 1 &&
            authType === 'oauth' &&
            !draftAccount &&
            (!oauthUrl || !oauthCallbackInput.trim()))
        }
      >
        {primaryLabel}
      </Button>
    </div>
  );

  const footer = authType === 'api' ? apiFooter : stagedFooter;

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
        {authType !== 'api' ? (
          <div className={styles.stepper} aria-label="添加账号步骤">
            <div className={`${styles.stepperItem} ${styles.stepperItemActive}`}>
              <span>1</span>
              <div>
                <strong>{authType === 'oauth' ? '授权方式' : '账号配置'}</strong>
                <small>{authType === 'oauth' ? '平台、认证和模型规则' : '凭证与模型规则'}</small>
              </div>
            </div>
            <div
              className={`${styles.stepperLine} ${step === 1 ? styles.stepperLineActive : ''}`}
            />
            <div className={`${styles.stepperItem} ${step === 1 ? styles.stepperItemActive : ''}`}>
              <span>2</span>
              <div>
                <strong>
                  {authType === 'oauth'
                    ? `${platformOption(platform)?.label ?? platform} 账户授权`
                    : '确认并保存'}
                </strong>
                <small>
                  {authType === 'oauth' ? '生成链接并提交授权结果' : '确认配置和连通性测试'}
                </small>
              </div>
            </div>
          </div>
        ) : null}

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
                    {authType === 'api'
                      ? '填写上游地址和 API Key，点击保存即完成添加。'
                      : authType === 'oauth'
                        ? '配置模型规则后点击下一步，再生成授权链接并完成官方授权。'
                        : '读取凭证后，可同步账号的模型目录。'}
                  </p>
                </div>
                {draftAccount && authType !== 'api' ? (
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
                    placeholder={accountDisplayName(platform, authType)}
                    disabled={configurationLocked}
                  />
                  <span className={styles.fieldHint}>
                    必填；仅用于账号管理页面显示，不会修改上游授权账号。
                  </span>
                </label>

                <label className={`${styles.field} ${styles.fieldFull}`}>
                  <span className={styles.fieldLabel}>备注</span>
                  <textarea
                    className={styles.textarea}
                    value={notes}
                    onChange={(event) => setNotes(event.target.value)}
                    placeholder="可选，用于记录账号用途、归属或其他说明"
                    rows={3}
                    disabled={configurationLocked}
                  />
                </label>

                {authType === 'api' ? (
                  <>
                    <label className={styles.field}>
                      <span className={styles.fieldLabel}>Base URL</span>
                      <input
                        className={styles.input}
                        value={baseUrl}
                        onChange={(event) => {
                          setBaseUrl(event.target.value);
                          setProbeResult(null);
                        }}
                        autoComplete="url"
                        disabled={busy || syncingModels}
                      />
                    </label>
                    <label className={styles.field}>
                      <span className={styles.fieldLabel}>代理 URL</span>
                      <input
                        className={styles.input}
                        value={proxyUrl}
                        onChange={(event) => {
                          setProxyUrl(event.target.value);
                          setProbeResult(null);
                        }}
                        placeholder="http:// 或 socks5://，留空直连"
                        autoComplete="off"
                        disabled={busy || syncingModels}
                      />
                      <span className={styles.fieldHint}>
                        可选；仅此账号的上游请求经该代理转发。
                      </span>
                    </label>
                    <label className={`${styles.field} ${styles.fieldFull}`}>
                      <span className={styles.fieldLabel}>API Key</span>
                      <input
                        className={styles.input}
                        type="password"
                        value={apiKey}
                        onChange={(event) => {
                          setApiKey(event.target.value);
                          setProbeResult(null);
                        }}
                        autoComplete="new-password"
                        disabled={busy || syncingModels}
                        placeholder="输入上游 API Key"
                      />
                    </label>
                    <label className={`${styles.field} ${styles.fieldFull}`}>
                      <span className={styles.fieldLabel}>自定义 Headers</span>
                      <textarea
                        className={styles.textarea}
                        value={headerLines}
                        onChange={(event) => {
                          setHeaderLines(event.target.value);
                          setProbeResult(null);
                        }}
                        rows={3}
                        placeholder="Header-Name: value"
                        disabled={busy || syncingModels}
                      />
                      <span className={styles.fieldHint}>
                        每行一个 Header，凭证 Header 不允许覆盖。
                      </span>
                    </label>
                    {platform === 'openai' ? (
                      <details className={`${styles.advancedSettings} ${styles.fieldFull}`}>
                        <summary>
                          <span>高级设置</span>
                          <small>协议模式：{protocolLabel(protocolMode)}</small>
                        </summary>
                        <div className={styles.advancedSettingsBody}>
                          <label className={styles.field}>
                            <span className={styles.fieldLabel}>协议模式</span>
                            <select
                              className={styles.select}
                              value={protocolMode}
                              onChange={(event) => {
                                setProtocolMode(event.target.value);
                                setProbeResult(null);
                              }}
                              disabled={busy || syncingModels}
                            >
                              <option value="auto">自动探测</option>
                              <option value="responses">强制 Responses</option>
                              <option value="chat_completions">强制 Chat Completions</option>
                            </select>
                            <span className={styles.fieldHint}>
                              默认按上游能力自动选择；仅在已明确上游协议时强制指定。
                            </span>
                          </label>
                        </div>
                      </details>
                    ) : null}
                    {officialClientCompatibilityEligible ? (
                      <div
                        className={`${styles.compatibilitySection} ${styles.fieldFull}`}
                        data-testid="official-client-compatibility"
                      >
                        <div className={styles.compatibilityHeader}>
                          <div>
                            <span className={styles.fieldLabel}>官方客户端兼容</span>
                            <span className={styles.fieldHint}>
                              兼容 Profile 由 Gateway 自动选择并保存。
                            </span>
                          </div>
                          <ToggleSwitch
                            checked={officialClientCompatibilityEnabled}
                            onChange={setOfficialClientCompatibilityEnabled}
                            ariaLabel="官方客户端兼容"
                            disabled={
                              busy || syncingModels || !officialClientCompatibilitySupported
                            }
                          />
                        </div>
                        {!officialClientCompatibilitySupported ? (
                          <span className={styles.fieldHint}>
                            当前 Gateway 不支持 API Key 官方客户端兼容。
                          </span>
                        ) : null}
                      </div>
                    ) : null}
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
                        disabled={Boolean(draftAccount)}
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
                        disabled={Boolean(draftAccount)}
                      />
                    </label>
                  </>
                ) : null}
              </div>

              {authType === 'api' && probeResult?.sourceType ? (
                <div className={styles.credentialSuccess}>
                  <span>✓</span>
                  <div>
                    <strong>已同步上游能力</strong>
                    <p>
                      {protocolLabel(probeResult.selectedProtocol)} · 已同步{' '}
                      {probeResult.models?.length ?? 0} 个模型
                    </p>
                  </div>
                </div>
              ) : null}

              {authType !== 'api' && draftAccount ? (
                <div className={styles.credentialSuccess}>
                  <span>✓</span>
                  <div>
                    <strong>凭证准备完成</strong>
                    <p>凭证草稿已保存 · 白名单 {whitelistModels.length} 个模型</p>
                  </div>
                </div>
              ) : null}

              {probeResult?.warnings?.length && !error ? (
                <div className={styles.notice}>{probeResult.warnings.join('；')}</div>
              ) : null}
            </section>

            <section className={styles.formSection}>
              <AccountModelRulesEditor
                models={whitelistModels}
                onModelsChange={setWhitelistModels}
                mappingLines={mappingLines}
                onMappingLinesChange={setMappingLines}
                onSyncBuiltInModels={() => void syncBuiltInModels()}
                onSyncUpstreamModels={
                  authType === 'api' || draftAccount ? () => void syncUpstreamModels() : undefined
                }
                syncingBuiltIn={syncingBuiltIn}
                syncingUpstream={syncingUpstream}
              />
            </section>
          </div>
        ) : authType === 'oauth' ? (
          <div className={styles.oauthAuthorizationPage}>
            <section className={styles.oauthAuthorizationCard}>
              <div className={styles.oauthAuthorizationLayout}>
                <span className={styles.oauthAuthorizationIcon}>
                  <IconExternalLink size={22} />
                </span>
                <div className={styles.oauthAuthorizationContent}>
                  <h3>{platformOption(platform)?.label ?? platform} 账户授权</h3>

                  <div className={styles.oauthMethod}>
                    <span>Authorization Method</span>
                    <label>
                      <input type="radio" checked readOnly />
                      <span>手动授权</span>
                    </label>
                  </div>

                  <p className={styles.oauthAuthorizationIntro}>
                    请按照以下步骤完成 {platformOption(platform)?.label ?? platform} 账户的授权：
                  </p>

                  <div className={styles.oauthInstructionList}>
                    <section className={styles.oauthInstructionCard}>
                      <span className={styles.oauthInstructionNumber}>1</span>
                      <div className={styles.oauthInstructionContent}>
                        <h4>点击下方按钮生成授权链接</h4>
                        {!oauthUrl ? (
                          <Button
                            type="button"
                            onClick={() => void startOAuth()}
                            loading={busy}
                            disabled={busy}
                          >
                            <IconExternalLink size={15} /> 生成授权链接
                          </Button>
                        ) : (
                          <div className={styles.oauthGeneratedLink}>
                            <div className={styles.oauthLinkRow}>
                              <input
                                className={styles.input}
                                value={oauthUrl}
                                readOnly
                                aria-label="OAuth 授权链接"
                              />
                              <Button
                                type="button"
                                variant="secondary"
                                onClick={() => void copyOAuthLink()}
                              >
                                <IconCopy size={14} /> {oauthLinkCopied ? '已复制' : '复制'}
                              </Button>
                            </div>
                            <div className={styles.oauthLinkActions}>
                              <Button type="button" size="sm" onClick={openOAuthLink}>
                                <IconExternalLink size={14} /> 打开授权链接
                              </Button>
                              <Button
                                type="button"
                                variant="secondary"
                                size="sm"
                                onClick={() => void regenerateOAuthLink()}
                                disabled={busy || oauthFinalizeRequested}
                              >
                                <IconRefreshCw size={14} /> 重新生成
                              </Button>
                            </div>
                          </div>
                        )}
                      </div>
                    </section>

                    <section className={styles.oauthInstructionCard}>
                      <span className={styles.oauthInstructionNumber}>2</span>
                      <div className={styles.oauthInstructionContent}>
                        <h4>在浏览器中打开链接并完成授权</h4>
                        <p>请在新标签页中登录账号并确认官方授权。</p>
                        <div className={styles.oauthImportantNotice}>
                          重要提示：授权后页面可能加载较长时间。当浏览器地址栏变为
                          http://localhost... 开头时，即可复制当前完整地址返回本页面。
                        </div>
                      </div>
                    </section>

                    <section className={styles.oauthInstructionCard}>
                      <span className={styles.oauthInstructionNumber}>3</span>
                      <div className={styles.oauthInstructionContent}>
                        <h4>输入授权链接或 Code</h4>
                        <p>
                          授权完成后，粘贴浏览器中的完整回调地址、回调参数，或仅粘贴 code 参数值。
                        </p>
                        <label className={styles.oauthCallbackField}>
                          <span>
                            <IconCheck size={15} /> 授权链接或 Code
                          </span>
                          <textarea
                            value={oauthCallbackInput}
                            onChange={(event) => {
                              const normalized = normalizeOAuthCallbackInput(event.target.value);
                              setOAuthCallbackInput(normalized.code);
                              if (normalized.state) {
                                setOAuthCallbackState(normalized.state);
                              }
                              setOAuthCallbackSubmitted(false);
                              setError('');
                            }}
                            rows={3}
                            placeholder={
                              '方式1：复制完整回调地址\n(http://localhost:xxx/auth/callback?code=...)\n方式2：仅复制 code 参数值'
                            }
                            disabled={!oauthUrl || busy || Boolean(draftAccount)}
                          />
                        </label>
                        <div className={styles.oauthCallbackHint}>
                          <IconInfo size={13} />
                          系统会使用当前授权会话自动识别并校验参数。
                        </div>
                        {oauthCallbackSubmitted && !draftAccount ? (
                          <div className={styles.oauthWaitingStatus}>
                            回调已提交，正在等待凭证保存...
                          </div>
                        ) : null}
                        {draftAccount ? (
                          <div className={styles.oauthSuccessStatus}>
                            授权成功，凭证草稿已安全保存，可以保存账号。
                          </div>
                        ) : null}
                      </div>
                    </section>
                  </div>
                </div>
              </div>
            </section>
          </div>
        ) : (
          <div className={styles.reviewPage}>
            <section className={styles.reviewHero}>
              <span className={styles.reviewHeroIcon}>✓</span>
              <div>
                <h3>配置已准备完成</h3>
                <p>保存时会自动发起连通性测试，测试成功后启用账号。</p>
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
                <dt>账号名称</dt>
                <dd>{name.trim()}</dd>
                <dt>平台 / 类型</dt>
                <dd>{accountDisplayName(platform, authType)}</dd>
                <dt>允许模型</dt>
                <dd>
                  {whitelistModels.length === 0 ? '全部模型' : `${whitelistModels.length} 个模型`}
                </dd>
                <dt>模型映射</dt>
                <dd>{Object.keys(parseMappingLines(mappingLines)).length} 条</dd>
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
