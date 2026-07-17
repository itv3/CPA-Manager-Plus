import { useEffect, useMemo, useState } from 'react';
import { Button } from '@/components/ui/Button';
import { Modal } from '@/components/ui/Modal';
import type { ProAccount, ProAccountLifecycleResult } from '@/services/api/proAccounts';
import { proAccountsApi } from '@/services/api/proAccounts';
import { AccountModelRulesEditor } from './AccountModelRulesEditor';
import {
  createRequestIdentity,
  formatHeaderLines,
  formatMappingLines,
  formatModelLines,
  parseHeaderLines,
  parseMappingLines,
  parseModelLines,
  suggestedTestModel,
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
  const [baseUrl, setBaseUrl] = useState('');
  const [originalBaseUrl, setOriginalBaseUrl] = useState('');
  const [apiKey, setApiKey] = useState('');
  const [protocolMode, setProtocolMode] = useState('auto');
  const [headerLines, setHeaderLines] = useState('');
  const [originalHeaderLines, setOriginalHeaderLines] = useState('');
  const [allowAll, setAllowAll] = useState(true);
  const [discoveredModels, setDiscoveredModels] = useState<string[]>([]);
  const [selectedModels, setSelectedModels] = useState<string[]>([]);
  const [manualModels, setManualModels] = useState('');
  const [mappingLines, setMappingLines] = useState('');
  const [testModel, setTestModel] = useState('');
  const [sharedProvider, setSharedProvider] = useState(false);
  const [loading, setLoading] = useState(false);
  const [saving, setSaving] = useState(false);
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
        const discovered = catalog?.models ?? [];
        const discoveredKeys = new Set(discovered.map((model) => model.toLowerCase()));
        const selected = result.item.allowedModels.filter(
          (model) => !model.includes('*') && discoveredKeys.has(model.toLowerCase())
        );
        const selectedKeys = new Set(selected.map((model) => model.toLowerCase()));
        const manual = result.item.allowedModels.filter(
          (model) => !selectedKeys.has(model.toLowerCase())
        );
        setCurrent(result.item);
        setName(result.item.name ?? '');
        setBaseUrl(result.editable.baseUrl ?? '');
        setOriginalBaseUrl(result.editable.baseUrl ?? '');
        setHeaderLines(headers);
        setOriginalHeaderLines(headers);
        setAllowAll(result.item.allowedModels.length === 0);
        setDiscoveredModels(discovered);
        setSelectedModels(selected);
        setManualModels(formatModelLines(manual));
        setMappingLines(formatMappingLines(result.item.modelMapping));
        setTestModel(
          suggestedTestModel('', result.item.allowedModels, result.item.modelMapping, discovered)
        );
        setSharedProvider(result.editable.sharedProvider);
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

  const credentialsChanged = useMemo(
    () =>
      apiKey.trim() !== '' ||
      baseUrl.trim() !== originalBaseUrl.trim() ||
      headerLines.trim() !== originalHeaderLines.trim(),
    [apiKey, baseUrl, headerLines, originalBaseUrl, originalHeaderLines]
  );

  const submit = async () => {
    if (!current) return;
    if (credentialsChanged && !apiKey.trim()) {
      setError('修改 API 地址或 Headers 时必须填写新 API Key');
      return;
    }
    try {
      const allowedModels = allowAll
        ? []
        : [...new Set([...selectedModels, ...parseModelLines(manualModels)])];
      const modelMapping = parseMappingLines(mappingLines);
      const headers = parseHeaderLines(headerLines);
      const resolvedTestModel = suggestedTestModel(testModel, allowedModels, modelMapping);
      if (credentialsChanged && !resolvedTestModel) {
        throw new Error('更换凭证时必须填写连通性测试模型');
      }
      const identity = createRequestIdentity('account-edit');
      setSaving(true);
      setError('');
      const result = await proAccountsApi.update(managerBase, managementKey, current.id, {
        ...identity,
        expectedVersion: current.version,
        name: name.trim(),
        baseUrl: credentialsChanged ? baseUrl.trim() : undefined,
        apiKey: credentialsChanged ? apiKey : undefined,
        protocolMode: credentialsChanged ? protocolMode : undefined,
        headers: credentialsChanged ? headers : undefined,
        allowedModels,
        modelMapping,
        testModel: credentialsChanged ? resolvedTestModel : undefined,
      });
      setApiKey('');
      onSaved(result);
      onClose();
    } catch (saveError) {
      setApiKey('');
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
                      Base URL、Headers 和模型目录由同一 Provider 的多个 Key 共享。
                    </div>
                  ) : null}
                </>
              ) : null}
            </div>
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
