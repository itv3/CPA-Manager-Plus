import { useEffect, useMemo, useRef, useState } from 'react';
import { Button } from '@/components/ui/Button';
import {
  IconBot,
  IconCheck,
  IconCopy,
  IconModelCluster,
  IconRefreshCw,
  IconX,
} from '@/components/ui/icons';
import { Modal } from '@/components/ui/Modal';
import type {
  ProAccount,
  ProAccountConnectivityResult,
  ProAccountTestMode,
} from '@/services/api/proAccounts';
import { proAccountsApi } from '@/services/api/proAccounts';
import { createRequestIdentity, resolveMappedModel, suggestedTestModel } from './accountFormUtils';
import styles from './AccountModals.module.scss';

interface AccountTestModalProps {
  open: boolean;
  account: ProAccount | null;
  managerBase: string;
  managementKey: string;
  onClose: () => void;
  onTested: () => void;
}

type TestStatus = 'idle' | 'connecting' | 'success' | 'error';
type TerminalTone = 'default' | 'blue' | 'green' | 'cyan' | 'yellow' | 'red' | 'muted';

interface TerminalLine {
  text: string;
  tone: TerminalTone;
}

const TEST_PROMPT = 'hi';

const ERROR_MESSAGES: Record<string, string> = {
  authentication_failed: '认证失败，请检查账号凭证',
  model_not_allowed: '测试模型不在账号有效白名单内',
  model_unavailable: '模型不可用或当前账号无权访问',
  protocol_not_supported: '上游不支持当前账号协议',
  rate_limited: '上游限流，请稍后重试',
  quota_exhausted: '上游配额已用尽',
  network_error: '连接上游失败，请检查网络或代理',
  tls_error: 'TLS 握手失败，请检查证书与代理配置',
  upstream_unavailable: '上游服务暂时不可用',
  connectivity_request_failed: '无法完成连通性请求',
  connectivity_test_failed: '账号连通性测试失败',
};

const connectivityErrorMessage = (code?: string) =>
  code ? (ERROR_MESSAGES[code] ?? `未识别的错误：${code}`) : '未知错误';

const accountTypeLabel = (authType?: string) => {
  const normalized = (authType || '').trim().toLowerCase();
  if (normalized === 'api' || normalized === 'apikey') return 'apikey';
  return normalized || 'account';
};

const initialOutputLines = (account: ProAccount): TerminalLine[] => [
  {
    text: `开始测试账号: ${account.name || account.email || account.id}`,
    tone: 'blue',
  },
  { text: `账号类型: ${accountTypeLabel(account.authType)}`, tone: 'muted' },
  { text: '', tone: 'default' },
];

export function AccountTestModal({
  open,
  account,
  managerBase,
  managementKey,
  onClose,
  onTested,
}: AccountTestModalProps) {
  const terminalRef = useRef<HTMLDivElement | null>(null);
  const [model, setModel] = useState('');
  const [models, setModels] = useState<string[]>([]);
  const [mode, setMode] = useState<ProAccountTestMode>('default');
  const [catalogLoading, setCatalogLoading] = useState(false);
  const [catalogError, setCatalogError] = useState('');
  const [testing, setTesting] = useState(false);
  const [outputLines, setOutputLines] = useState<TerminalLine[]>([]);
  const [result, setResult] = useState<ProAccountConnectivityResult | null>(null);
  const [error, setError] = useState('');

  const sourceType = account?.binding?.sourceType || account?.sourceType || '';
  const isOpenAIAccount = account?.platform === 'openai';
  // Compact 仅供 Responses 账号使用，Chat Completions 账号仍展示常规测试模式。
  const supportsCompact = isOpenAIAccount && sourceType === 'config_codex_api_key';
  const upstreamModel = useMemo(
    () => resolveMappedModel(model, account?.modelMapping ?? {}),
    [account?.modelMapping, model]
  );
  const modelOptions = useMemo(() => {
    const allowed = (account?.allowedModels ?? []).map((item) => item.trim()).filter(Boolean);
    const aliases = Object.keys(account?.modelMapping ?? {}).map((item) => item.trim());
    const concreteAllowed = allowed.filter((item) => item && !item.includes('*'));
    const concreteAliases = aliases.filter((item) => item && !item.includes('*'));
    // 白名单非空时，测试模型必须来自白名单或映射别名。
    if (concreteAllowed.length > 0 || concreteAliases.length > 0) {
      return [...new Set([...concreteAllowed, ...concreteAliases])];
    }
    // 白名单为空表示允许全部，使用同步到的模型目录。
    return [...new Set([...models, ...aliases])]
      .map((item) => item.trim())
      .filter((item) => item && !item.includes('*'));
  }, [account?.allowedModels, account?.modelMapping, models]);

  const status: TestStatus = testing
    ? 'connecting'
    : result
      ? result.success
        ? 'success'
        : 'error'
      : error
        ? 'error'
        : 'idle';
  const resultErrorMessage =
    result?.errorMessage ||
    (result ? connectivityErrorMessage(result.errorCode) : error) ||
    '账号连通性测试失败';
  const resultErrorDetails =
    result?.responsePreview && result.responsePreview !== resultErrorMessage
      ? result.responsePreview
      : '';

  useEffect(() => {
    if (!open || !account) return;
    let cancelled = false;
    setModel(suggestedTestModel('', account.allowedModels, account.modelMapping));
    setModels([]);
    setMode('default');
    setOutputLines([]);
    setResult(null);
    setError('');
    setCatalogError('');
    setCatalogLoading(true);
    void proAccountsApi
      .modelCatalog(managerBase, managementKey, account.id)
      .then((catalog) => {
        if (cancelled) return;
        const nextModels = catalog.models ?? [];
        setModels(nextModels);
        setModel((current) =>
          suggestedTestModel(current, account.allowedModels, account.modelMapping, nextModels)
        );
      })
      .catch(() => {
        if (!cancelled) setCatalogError('模型目录加载失败，可手工输入模型后继续测试');
      })
      .finally(() => {
        if (!cancelled) setCatalogLoading(false);
      });
    return () => {
      cancelled = true;
    };
  }, [account, managementKey, managerBase, open]);

  useEffect(() => {
    if (!terminalRef.current) return;
    terminalRef.current.scrollTop = terminalRef.current.scrollHeight;
  }, [error, outputLines, result, testing]);

  const resetResult = () => {
    setOutputLines([]);
    setResult(null);
    setError('');
  };

  const test = async () => {
    if (!account || !model.trim()) {
      setError('请选择连通性测试模型');
      return;
    }
    const startingLines = initialOutputLines(account);
    setTesting(true);
    setOutputLines(startingLines);
    setResult(null);
    setError('');
    try {
      const identity = createRequestIdentity('account-test');
      const response = await proAccountsApi.testAccount(
        managerBase,
        managementKey,
        account,
        model.trim(),
        mode,
        identity.operationId,
        identity.idempotencyKey
      );
      const connectivity = response.connectivity;
      const resolvedModel = connectivity.mappedModel || upstreamModel || model.trim();
      const nextLines: TerminalLine[] = [
        ...startingLines,
        { text: '已连接到 API', tone: 'green' },
        { text: `使用模型: ${resolvedModel}`, tone: 'cyan' },
        { text: `发送测试消息: "${TEST_PROMPT}"`, tone: 'muted' },
        { text: '', tone: 'default' },
        { text: '响应:', tone: 'yellow' },
      ];
      if (connectivity.success) {
        nextLines.push({
          text:
            connectivity.responsePreview ||
            (mode === 'compact' ? 'Compact probe succeeded' : 'API 请求成功'),
          tone: 'green',
        });
      }
      setOutputLines(nextLines);
      setResult(connectivity);
      onTested();
    } catch (testError) {
      setError(testError instanceof Error ? testError.message : String(testError));
    } finally {
      setTesting(false);
    }
  };

  const copyOutput = () => {
    const terminalText = [
      ...outputLines.map((line) => line.text),
      status === 'connecting' ? '正在连接到 API...' : '',
      status === 'success' ? '✓ 测试完成!' : '',
      status === 'error' ? `✕ ${resultErrorMessage}` : '',
      resultErrorDetails,
    ]
      .filter(Boolean)
      .join('\n');
    if (terminalText && navigator.clipboard) {
      void navigator.clipboard.writeText(terminalText);
    }
  };

  const footer = (
    <div className={styles.testFooter}>
      <Button variant="secondary" size="sm" onClick={onClose} disabled={testing}>
        关闭
      </Button>
      <Button
        variant="primary"
        size="sm"
        className={`${styles.testActionButton} ${
          status === 'success'
            ? styles.testActionSuccess
            : status === 'error'
              ? styles.testActionError
              : ''
        }`}
        onClick={() => void test()}
        loading={testing}
        disabled={!model.trim()}
      >
        {!testing && status !== 'idle' ? <IconRefreshCw size={15} /> : null}
        {!testing && status === 'idle' ? <span className={styles.testButtonPlay} /> : null}
        {testing ? '测试中' : status === 'idle' ? '开始测试' : '重试'}
      </Button>
    </div>
  );

  return (
    <Modal
      open={open}
      title="测试账号连接"
      onClose={onClose}
      footer={footer}
      width={512}
      className={styles.testModal}
      closeDisabled={testing}
    >
      <div className={styles.testBody}>
        {account ? (
          <div className={styles.testAccountCard}>
            <div className={styles.testAccountIdentity}>
              <span className={styles.testAccountPlay} aria-hidden="true" />
              <div>
                <strong>{account.name || account.email || account.id}</strong>
                <div className={styles.testAccountType}>
                  <span>{accountTypeLabel(account.authType)}</span>
                  <small>账号</small>
                </div>
              </div>
            </div>
            <span
              className={`${styles.testAccountStatus} ${
                account.enabled ? styles.testAccountStatusActive : ''
              }`}
            >
              {account.enabled ? 'active' : 'inactive'}
            </span>
          </div>
        ) : null}

        <label className={styles.field}>
          <span className={styles.fieldLabel}>选择测试模型</span>
          {modelOptions.length > 0 ? (
            <select
              className={styles.select}
              value={model}
              onChange={(event) => {
                setModel(event.target.value);
                resetResult();
              }}
              disabled={testing}
              aria-label="连通性测试模型"
            >
              {model && !modelOptions.includes(model) ? (
                <option value={model}>{model}</option>
              ) : null}
              {modelOptions.map((item) => (
                <option value={item} key={item}>
                  {item}
                </option>
              ))}
            </select>
          ) : (
            <input
              className={styles.input}
              value={model}
              onChange={(event) => {
                setModel(event.target.value);
                resetResult();
              }}
              placeholder={catalogLoading ? '正在加载模型目录...' : '手工输入模型名称'}
              disabled={testing}
              aria-label="连通性测试模型"
            />
          )}
        </label>
        {catalogError ? <div className={styles.notice}>{catalogError}</div> : null}

        {isOpenAIAccount ? (
          <label className={styles.field}>
            <span className={styles.fieldLabel}>测试模式</span>
            <select
              className={styles.select}
              value={mode}
              onChange={(event) => {
                setMode(event.target.value as ProAccountTestMode);
                resetResult();
              }}
              disabled={testing}
              aria-label="连通性测试模式"
            >
              <option value="default">常规请求</option>
              {supportsCompact ? <option value="compact">Compact 探测</option> : null}
            </select>
          </label>
        ) : null}

        <div className={styles.testTerminalGroup}>
          <div ref={terminalRef} className={styles.testTerminal} role="log" aria-live="polite">
            {status === 'idle' ? (
              <div className={styles.testReady}>
                <span className={styles.testReadyPlay} aria-hidden="true" />
                <span>准备开始测试</span>
              </div>
            ) : null}

            {outputLines.map((line, index) => (
              <span
                className={`${styles.testTerminalLine} ${styles[`testTerminal${line.tone}`]}`}
                key={`${index}-${line.text}`}
              >
                {line.text || '\u00a0'}
              </span>
            ))}

            {status === 'connecting' ? (
              <div className={styles.testConnecting}>
                <IconRefreshCw size={15} />
                <span>正在连接到 API...</span>
              </div>
            ) : null}

            {status === 'success' ? (
              <div className={`${styles.testTerminalResult} ${styles.testTerminalResultSuccess}`}>
                <IconCheck size={16} />
                <span>测试完成!</span>
              </div>
            ) : null}

            {status === 'error' ? (
              <div className={`${styles.testTerminalResult} ${styles.testTerminalResultError}`}>
                <IconX size={16} />
                <div>
                  <span>{resultErrorMessage}</span>
                  {resultErrorDetails ? <pre>{resultErrorDetails}</pre> : null}
                </div>
              </div>
            ) : null}
          </div>

          {outputLines.length > 0 ? (
            <button
              type="button"
              className={styles.testCopyButton}
              onClick={copyOutput}
              title="复制测试输出"
              aria-label="复制测试输出"
            >
              <IconCopy size={14} />
            </button>
          ) : null}
        </div>

        <div className={styles.testHint}>
          <span>
            <IconModelCluster size={14} />
            测试模型
          </span>
          <span>
            <IconBot size={14} />
            提示词: "{TEST_PROMPT}"
          </span>
        </div>
      </div>
    </Modal>
  );
}
