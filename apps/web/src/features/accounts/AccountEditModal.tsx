import { useEffect, useMemo, useState } from 'react';
import { Button } from '@/components/ui/Button';
import { Modal } from '@/components/ui/Modal';
import { ToggleSwitch } from '@/components/ui/ToggleSwitch';
import type {
  ProAccount,
  ProAccountLifecycleResult,
  ProAccountOfficialClientCompatibility,
} from '@/services/api/proAccounts';
import { proAccountsApi } from '@/services/api/proAccounts';
import { AccountModelRulesEditor } from './AccountModelRulesEditor';
import {
  createRequestIdentity,
  formatHeaderLines,
  formatMappingLines,
  parseHeaderLines,
  resolveAccountModelRules,
} from './accountFormUtils';
import styles from './AccountModals.module.scss';

interface AccountEditModalProps {
  open: boolean;
  account: ProAccount | null;
  managerBase: string;
  managementKey: string;
  onClose: () => void;
  onSaved: (result: ProAccountLifecycleResult) => void;
}

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

export function AccountEditModal({
  open,
  account,
  managerBase,
  managementKey,
  onClose,
  onSaved,
}: AccountEditModalProps) {
  const [current, setCurrent] = useState<ProAccount | null>(null);
  const [name, setName] = useState('');
  const [notes, setNotes] = useState('');
  const [baseUrl, setBaseUrl] = useState('');
  const [originalBaseUrl, setOriginalBaseUrl] = useState('');
  const [proxyUrl, setProxyUrl] = useState('');
  const [originalProxyUrl, setOriginalProxyUrl] = useState('');
  const [apiKey, setApiKey] = useState('');
  const [protocolMode, setProtocolMode] = useState('auto');
  const [headerLines, setHeaderLines] = useState('');
  const [originalHeaderLines, setOriginalHeaderLines] = useState('');
  const [whitelistModels, setWhitelistModels] = useState<string[]>([]);
  const [catalogModels, setCatalogModels] = useState<string[]>([]);
  const [mappingLines, setMappingLines] = useState('');
  const [sharedProvider, setSharedProvider] = useState(false);
  const [compatibilitySupported, setCompatibilitySupported] = useState(false);
  const [compatibility, setCompatibility] = useState<ProAccountOfficialClientCompatibility | null>(
    null
  );
  const [originalCompatibility, setOriginalCompatibility] =
    useState<ProAccountOfficialClientCompatibility | null>(null);
  const [loading, setLoading] = useState(false);
  const [saving, setSaving] = useState(false);
  const [syncingBuiltIn, setSyncingBuiltIn] = useState(false);
  const [syncingUpstream, setSyncingUpstream] = useState(false);
  const [error, setError] = useState('');

  useEffect(() => {
    if (!open || !account) return;
    let cancelled = false;
    setLoading(true);
    setError('');
    setApiKey('');
    void Promise.all([
      proAccountsApi.details(managerBase, managementKey, account.id),
      proAccountsApi.modelCatalog(managerBase, managementKey, account.id).catch(() => null),
    ])
      .then(([result, catalog]) => {
        if (cancelled) return;
        const headers = formatHeaderLines(result.editable.headers ?? {});
        setCurrent(result.item);
        setName(result.item.name ?? '');
        setNotes(result.item.notes ?? '');
        setBaseUrl(result.editable.baseUrl ?? '');
        setOriginalBaseUrl(result.editable.baseUrl ?? '');
        setProxyUrl(result.editable.proxyUrl ?? '');
        setOriginalProxyUrl(result.editable.proxyUrl ?? '');
        setHeaderLines(headers);
        setOriginalHeaderLines(headers);
        setWhitelistModels(result.item.allowedModels);
        setCatalogModels(catalog?.models ?? []);
        setMappingLines(formatMappingLines(result.item.modelMapping));
        setSharedProvider(result.editable.sharedProvider);
        const liveCompatibility = result.editable.officialClientCompatibility
          ? { ...result.editable.officialClientCompatibility }
          : { enabled: false, profile: '', tlsProfile: '' };
        setCompatibilitySupported(Boolean(result.editable.officialClientCompatibilitySupported));
        setCompatibility(liveCompatibility);
        setOriginalCompatibility({ ...liveCompatibility });
      })
      .catch((loadError) => {
        if (!cancelled)
          setError(loadError instanceof Error ? loadError.message : String(loadError));
      })
      .finally(() => {
        if (!cancelled) setLoading(false);
      });
    return () => {
      cancelled = true;
    };
  }, [account, managementKey, managerBase, open]);

  // 需要重建凭证的变更:换 Key、改 Base URL 或 Headers;代理变更走热更新,不强制换 Key
  const credentialsChanged = useMemo(
    () =>
      apiKey.trim() !== '' ||
      baseUrl.trim() !== originalBaseUrl.trim() ||
      headerLines.trim() !== originalHeaderLines.trim(),
    [apiKey, baseUrl, headerLines, originalBaseUrl, originalHeaderLines]
  );
  const proxyChanged = useMemo(
    () => proxyUrl.trim() !== originalProxyUrl.trim(),
    [proxyUrl, originalProxyUrl]
  );
  const compatibilityChanged = useMemo(
    () =>
      compatibility !== null &&
      originalCompatibility !== null &&
      (compatibility.enabled !== originalCompatibility.enabled ||
        compatibility.profile !== originalCompatibility.profile ||
        compatibility.tlsProfile !== originalCompatibility.tlsProfile),
    [compatibility, originalCompatibility]
  );
  const compatibilityEligible =
    current?.authType === 'api' &&
    (current.sourceType === 'config_claude_api_key' ||
      current.sourceType === 'config_codex_api_key');

  const syncBuiltInModels = async () => {
    if (!current) return;
    setSyncingBuiltIn(true);
    setError('');
    try {
      const catalog = await proAccountsApi.staticModelCatalog(
        managerBase,
        managementKey,
        current.platform,
        current.authType
      );
      const builtIn = catalog.builtIn?.length ? catalog.builtIn : (catalog.models ?? []);
      if (builtIn.length === 0) {
        setError('项目内置目录暂无该平台模型，可同步上游支持的模型或填入自定义模型名称');
        return;
      }
      setCatalogModels((value) => mergeUnique(value, builtIn));
      setWhitelistModels((value) => mergeUnique(value, builtIn));
    } catch (syncError) {
      setError(syncError instanceof Error ? syncError.message : String(syncError));
    } finally {
      setSyncingBuiltIn(false);
    }
  };

  const syncUpstreamModels = async () => {
    if (!current) return;
    setSyncingUpstream(true);
    setError('');
    try {
      const catalog = await proAccountsApi.modelCatalog(managerBase, managementKey, current.id);
      const models = catalog.models ?? [];
      const upstream = catalog.upstream?.length ? catalog.upstream : models;
      if (models.length === 0) {
        setError('上游未提供模型列表，可同步最新支持模型或填入自定义模型名称');
        return;
      }
      setCatalogModels((value) => mergeUnique(value, models));
      setWhitelistModels((value) => mergeUnique(value, upstream));
    } catch (syncError) {
      setError(syncError instanceof Error ? syncError.message : String(syncError));
    } finally {
      setSyncingUpstream(false);
    }
  };

  const submit = async () => {
    if (!current) return;
    if (!name.trim()) {
      setError('请输入账号名称');
      return;
    }
    if (credentialsChanged && !apiKey.trim()) {
      setError('修改 API 地址或 Headers 时必须填写新 API Key');
      return;
    }
    try {
      const resolved = resolveAccountModelRules({
        models: whitelistModels,
        mappingLines,
        discoveredModels: catalogModels,
      });
      const headers = parseHeaderLines(headerLines);
      const identity = createRequestIdentity('account-edit');
      setSaving(true);
      setError('');
      const result = await proAccountsApi.update(managerBase, managementKey, current.id, {
        ...identity,
        expectedVersion: current.version,
        name: name.trim(),
        notes: notes.trim(),
        baseUrl: credentialsChanged ? baseUrl.trim() : undefined,
        apiKey: credentialsChanged ? apiKey : undefined,
        proxyUrl: credentialsChanged || proxyChanged ? proxyUrl.trim() : undefined,
        protocolMode: credentialsChanged ? protocolMode : undefined,
        headers: credentialsChanged ? headers : undefined,
        allowedModels: resolved.allowedModels,
        modelMapping: resolved.modelMapping,
        officialClientCompatibility:
          compatibilityChanged && compatibility ? compatibility : undefined,
      });
      setApiKey('');
      onSaved(result);
      onClose();
    } catch (saveError) {
      // 保存失败时保留用户刚输入的密钥，避免再次点击保存退化为仅更新名称和备注。
      setError(saveError instanceof Error ? saveError.message : String(saveError));
    } finally {
      setSaving(false);
    }
  };

  const footer = (
    <div className={styles.footer}>
      <Button variant="secondary" size="sm" onClick={onClose} disabled={saving}>
        取消
      </Button>
      <span className={styles.footerSpacer} />
      <Button
        variant="primary"
        size="sm"
        onClick={() => void submit()}
        loading={saving}
        disabled={loading || !current}
      >
        保存
      </Button>
    </div>
  );

  return (
    <Modal
      open={open}
      title="编辑统一账号"
      onClose={onClose}
      footer={footer}
      width={760}
      className={styles.modal}
      closeDisabled={saving}
    >
      <div className={styles.body}>
        {loading ? <div className={styles.notice}>加载中...</div> : null}
        {current ? (
          <div className={styles.formStack}>
            <div className={styles.formGrid}>
              <label className={styles.field}>
                <span className={styles.fieldLabel}>账号名称</span>
                <input
                  className={styles.input}
                  value={name}
                  onChange={(event) => setName(event.target.value)}
                />
              </label>
              <label className={styles.field}>
                <span className={styles.fieldLabel}>平台 / 类型</span>
                <input
                  className={styles.input}
                  value={`${current.platform} / ${current.authType}`}
                  disabled
                />
              </label>
              <label className={`${styles.field} ${styles.fieldFull}`}>
                <span className={styles.fieldLabel}>备注</span>
                <textarea
                  className={styles.textarea}
                  value={notes}
                  onChange={(event) => setNotes(event.target.value)}
                  placeholder="可选，用于记录账号用途、归属或其他说明"
                  rows={3}
                />
              </label>
              {current.authType === 'api' ? (
                <>
                  <label className={styles.field}>
                    <span className={styles.fieldLabel}>Base URL</span>
                    <input
                      className={styles.input}
                      value={baseUrl}
                      onChange={(event) => setBaseUrl(event.target.value)}
                    />
                  </label>
                  <label className={styles.field}>
                    <span className={styles.fieldLabel}>协议模式</span>
                    <select
                      className={styles.select}
                      value={protocolMode}
                      onChange={(event) => setProtocolMode(event.target.value)}
                      disabled={current.platform !== 'openai'}
                    >
                      <option value="auto">自动探测</option>
                      <option value="responses">强制 Responses</option>
                      <option value="chat_completions">强制 Chat Completions</option>
                    </select>
                  </label>
                  <label className={`${styles.field} ${styles.fieldFull}`}>
                    <span className={styles.fieldLabel}>代理 URL</span>
                    <input
                      className={styles.input}
                      value={proxyUrl}
                      onChange={(event) => setProxyUrl(event.target.value)}
                      placeholder="http:// 或 socks5://，留空直连"
                      autoComplete="off"
                    />
                    <span className={styles.fieldHint}>
                      可选；仅此账号的上游请求经该代理转发，修改代理无需重新填写 API Key。
                    </span>
                  </label>
                  <label className={`${styles.field} ${styles.fieldFull}`}>
                    <span className={styles.fieldLabel}>新 API Key</span>
                    <input
                      className={styles.input}
                      type="password"
                      value={apiKey}
                      onChange={(event) => setApiKey(event.target.value)}
                      autoComplete="new-password"
                      placeholder="仅更换凭证时填写"
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
                    />
                  </label>
                  {sharedProvider ? (
                    <div className={`${styles.sharedWarning} ${styles.fieldFull}`}>
                      Base URL、Headers 和模型目录由同一 Provider 的多个 Key 共享；代理 URL
                      仅作用于当前 Key。
                    </div>
                  ) : null}
                  {compatibilityEligible && compatibility ? (
                    <div
                      className={`${styles.compatibilitySection} ${styles.fieldFull}`}
                      data-testid="official-client-compatibility"
                    >
                      <div className={styles.compatibilityHeader}>
                        <div>
                          <span className={styles.fieldLabel}>官方客户端兼容</span>
                          <span className={styles.fieldHint}>
                            配置从 Gateway 实时读取，切换开关无需重新填写 API Key。
                          </span>
                        </div>
                        <ToggleSwitch
                          checked={compatibility.enabled}
                          onChange={(enabled) =>
                            setCompatibility((value) => (value ? { ...value, enabled } : value))
                          }
                          ariaLabel="官方客户端兼容"
                          disabled={saving || !compatibilitySupported}
                        />
                      </div>
                      <div className={styles.compatibilityDetails}>
                        <div>
                          <span>Profile ID</span>
                          <code>{compatibility.profile || '启用后由 Gateway 分配'}</code>
                        </div>
                        <div>
                          <span>TLS 策略</span>
                          <code>{compatibility.tlsProfile || '默认 Transport'}</code>
                        </div>
                      </div>
                      {!compatibilitySupported ? (
                        <span className={styles.fieldHint}>
                          当前 Gateway 不支持 API Key 官方客户端兼容。
                        </span>
                      ) : null}
                    </div>
                  ) : null}
                </>
              ) : (
                <label className={`${styles.field} ${styles.fieldFull}`}>
                  <span className={styles.fieldLabel}>代理 URL</span>
                  <input
                    className={styles.input}
                    value={proxyUrl}
                    onChange={(event) => setProxyUrl(event.target.value)}
                    placeholder="http:// 或 socks5://，留空直连"
                    autoComplete="off"
                  />
                  <span className={styles.fieldHint}>可选；仅此账号的上游请求经该代理转发。</span>
                </label>
              )}
            </div>
            <AccountModelRulesEditor
              models={whitelistModels}
              onModelsChange={setWhitelistModels}
              mappingLines={mappingLines}
              onMappingLinesChange={setMappingLines}
              onSyncBuiltInModels={() => void syncBuiltInModels()}
              onSyncUpstreamModels={() => void syncUpstreamModels()}
              syncingBuiltIn={syncingBuiltIn}
              syncingUpstream={syncingUpstream}
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
