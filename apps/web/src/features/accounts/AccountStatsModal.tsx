import { useCallback, useEffect, useRef, useState } from 'react';
import { Button } from '@/components/ui/Button';
import {
  IconChartLine,
  IconDatabaseZap,
  IconDollarSign,
  IconRefreshCw,
  IconSatellite,
  IconTimer,
} from '@/components/ui/icons';
import { Modal } from '@/components/ui/Modal';
import {
  proAccountsApi,
  type ProAccount,
  type ProAccountUsageResponse,
  type ProAccountUsageWindow,
} from '@/services/api/proAccounts';
import { formatCompactNumber, formatUsd } from '@/utils/usage';
import styles from './AccountStatsModal.module.scss';

interface AccountStatsModalProps {
  open: boolean;
  account: ProAccount | null;
  managerBase: string;
  managementKey: string;
  onClose: () => void;
  onUsageLoaded?: (
    usage: ProAccountUsageResponse,
    requestSource: 'passive' | 'active'
  ) => ProAccountUsageResponse | void;
}

type LoadingAction = 'initial' | 'official' | 'local' | null;

const accountName = (account: ProAccount) => account.name || account.email || account.id;

const platformLabel = (platform: string) => {
  const labels: Record<string, string> = {
    openai: 'OpenAI',
    anthropic: 'Anthropic',
    gemini: 'Gemini',
    antigravity: 'Antigravity',
    xai: 'Grok / xAI',
  };
  return labels[platform.toLowerCase()] ?? platform;
};

const authTypeLabel = (authType: string) => {
  const normalized = authType.trim().toLowerCase();
  if (normalized === 'api' || normalized === 'apikey') return 'API Key';
  if (normalized === 'oauth') return 'OAuth';
  if (normalized === 'vertex') return 'Vertex';
  return authType || '未知认证';
};

const sourceLabel = (source?: string) => {
  if (source === 'official') return '官方主动查询';
  if (source === 'passive') return '上游响应记录';
  if (source === 'local') return '本地聚合';
  return source || '未知来源';
};

const formatDateTime = (timestampMs?: number) => {
  if (!timestampMs) return '-';
  const date = new Date(timestampMs);
  if (Number.isNaN(date.getTime())) return '-';
  return date.toLocaleString('zh-CN', {
    year: 'numeric',
    month: '2-digit',
    day: '2-digit',
    hour: '2-digit',
    minute: '2-digit',
    second: '2-digit',
    hour12: false,
  });
};

const formatResetCountdown = (resetAtMs: number | undefined, nowMs: number) => {
  if (!resetAtMs) return '未提供重置时间';
  const remainingMs = resetAtMs - nowMs;
  if (remainingMs <= 0) return '重置时间已到';
  const totalMinutes = Math.max(1, Math.ceil(remainingMs / 60_000));
  const days = Math.floor(totalMinutes / 1_440);
  const hours = Math.floor((totalMinutes % 1_440) / 60);
  const minutes = totalMinutes % 60;
  if (days > 0) return `${days} 天 ${hours} 小时后`;
  if (hours > 0) return `${hours} 小时 ${minutes} 分钟后`;
  return `${minutes} 分钟后`;
};

const resolveUsedPercent = (window: ProAccountUsageWindow) => {
  const value =
    window.usedPercent !== undefined
      ? window.usedPercent
      : window.remainingPercent !== undefined
        ? 100 - window.remainingPercent
        : undefined;
  if (value === undefined || !Number.isFinite(value)) return null;
  return Math.min(100, Math.max(0, value));
};

const errorMessage = (error: unknown) =>
  error instanceof Error ? error.message : typeof error === 'string' ? error : '未知错误';

export function AccountStatsModal({
  open,
  account,
  managerBase,
  managementKey,
  onClose,
  onUsageLoaded,
}: AccountStatsModalProps) {
  const requestSequenceRef = useRef(0);
  const inFlightRef = useRef(false);
  const onUsageLoadedRef = useRef(onUsageLoaded);
  const [usage, setUsage] = useState<ProAccountUsageResponse | null>(null);
  const [loadingAction, setLoadingAction] = useState<LoadingAction>(null);
  const [requestError, setRequestError] = useState('');
  const [nowMs, setNowMs] = useState(() => Date.now());

  useEffect(() => {
    onUsageLoadedRef.current = onUsageLoaded;
  }, [onUsageLoaded]);

  const loadUsage = useCallback(
    async (source: 'passive' | 'active', force: boolean, action: Exclude<LoadingAction, null>) => {
      if (!account || inFlightRef.current) return;
      const requestSequence = ++requestSequenceRef.current;
      inFlightRef.current = true;
      setLoadingAction(action);
      setRequestError('');
      try {
        const response = await proAccountsApi.usage(
          managerBase,
          managementKey,
          account.id,
          source,
          force
        );
        if (requestSequence !== requestSequenceRef.current) return;
        const resolvedUsage = onUsageLoadedRef.current?.(response, source) || response;
        setUsage(resolvedUsage);
        setNowMs(Date.now());
      } catch (error) {
        if (requestSequence !== requestSequenceRef.current) return;
        setRequestError(errorMessage(error));
      } finally {
        if (requestSequence === requestSequenceRef.current) {
          inFlightRef.current = false;
          setLoadingAction(null);
        }
      }
    },
    [account, managementKey, managerBase]
  );

  useEffect(() => {
    if (!open || !account) return;
    setUsage(null);
    setRequestError('');
    inFlightRef.current = false;
    void loadUsage('passive', false, 'initial');
    return () => {
      requestSequenceRef.current += 1;
      inFlightRef.current = false;
    };
  }, [account, loadUsage, open]);

  useEffect(() => {
    if (!open) return;
    setNowMs(Date.now());
    const timer = globalThis.setInterval(() => setNowMs(Date.now()), 30_000);
    return () => globalThis.clearInterval(timer);
  }, [open]);

  const busy = loadingAction !== null;
  const footer = (
    <div className={styles.footer}>
      <Button variant="secondary" size="sm" onClick={onClose}>
        关闭
      </Button>
      <div className={styles.footerActions}>
        <Button
          variant="secondary"
          size="sm"
          loading={loadingAction === 'local'}
          disabled={!account || busy}
          onClick={() => void loadUsage('passive', false, 'local')}
          aria-label="刷新本地统计"
        >
          <IconRefreshCw size={15} />
          刷新本地统计
        </Button>
        <Button
          variant="primary"
          size="sm"
          loading={loadingAction === 'official'}
          disabled={!account || busy}
          onClick={() => void loadUsage('active', true, 'official')}
          aria-label="查询官方配额"
        >
          <IconSatellite size={15} />
          查询官方配额
        </Button>
      </div>
    </div>
  );

  const local = usage?.local;
  const tokenMetrics = local
    ? [
        { label: '输入 Token', value: local.inputTokens },
        { label: '输出 Token', value: local.outputTokens },
        {
          label: '缓存 Token',
          value: local.cachedTokens,
          detail: `读取 ${formatCompactNumber(local.cacheReadTokens)} · 写入 ${formatCompactNumber(local.cacheCreationTokens)}`,
        },
        { label: '推理 Token', value: local.reasoningTokens },
        { label: '总 Token', value: local.totalTokens, emphasized: true },
      ]
    : [];

  return (
    <Modal
      open={open}
      title="账号用量统计"
      onClose={onClose}
      footer={footer}
      width={900}
      className={styles.modal}
    >
      <div className={styles.body} aria-busy={busy}>
        {account ? (
          <div className={styles.accountHeader}>
            <span className={styles.accountIcon} aria-hidden="true">
              <IconChartLine size={21} />
            </span>
            <div className={styles.accountIdentity}>
              <strong>{accountName(account)}</strong>
              <span>
                {platformLabel(account.platform)} · {authTypeLabel(account.authType)}
              </span>
            </div>
            <span
              className={`${styles.statusBadge} ${account.enabled ? styles.statusEnabled : styles.statusDisabled}`}
            >
              {account.enabled ? '已启用' : '已停用'}
            </span>
          </div>
        ) : (
          <div className={styles.emptyState}>未选择账号</div>
        )}

        {loadingAction === 'initial' && !usage ? (
          <div className={styles.loadingState}>
            <span className={styles.loadingSpinner} aria-hidden="true" />
            <strong>正在加载账号统计</strong>
            <span>先读取本地聚合及已捕获的官方窗口</span>
          </div>
        ) : null}

        {requestError ? (
          <div className={styles.errorBanner} role="alert">
            <strong>统计加载失败</strong>
            <span>{requestError}</span>
          </div>
        ) : null}

        {usage ? (
          <>
            <section className={styles.section}>
              <div className={styles.sectionHeader}>
                <div>
                  <strong>官方用量窗口</strong>
                  <span>窗口由上游平台设定，到期后自动滚动重置</span>
                </div>
                <span className={styles.sectionBadge}>{usage.officialWindows.length} 个窗口</span>
              </div>
              {usage.officialWindows.length > 0 ? (
                <div className={styles.windowGrid}>
                  {usage.officialWindows.map((window) => {
                    const usedPercent = resolveUsedPercent(window);
                    const progressTone =
                      usedPercent !== null && usedPercent >= 100
                        ? styles.progressDanger
                        : usedPercent !== null && usedPercent >= 80
                          ? styles.progressWarning
                          : styles.progressNormal;
                    return (
                      <article className={styles.windowCard} key={window.id}>
                        <div className={styles.windowHeader}>
                          <strong>{window.label}</strong>
                          <span>
                            {usedPercent === null ? '用量未知' : `已用 ${Math.round(usedPercent)}%`}
                          </span>
                        </div>
                        <div
                          className={styles.progressTrack}
                          role="progressbar"
                          aria-label={`${window.label}已用比例`}
                          aria-valuemin={0}
                          aria-valuemax={100}
                          aria-valuenow={usedPercent ?? undefined}
                        >
                          <span
                            className={progressTone}
                            style={{ width: `${usedPercent ?? 0}%` }}
                          />
                        </div>
                        <div className={styles.windowDetails}>
                          <span title={formatDateTime(window.resetAtMs)}>
                            <IconTimer size={14} />
                            {formatResetCountdown(window.resetAtMs, nowMs)}
                          </span>
                          <small>{sourceLabel(window.source)}</small>
                        </div>
                      </article>
                    );
                  })}
                </div>
              ) : (
                <div className={styles.windowEmpty}>
                  <IconSatellite size={21} />
                  <div>
                    <strong>暂无官方配额窗口</strong>
                    <span>可点击“查询官方配额”主动读取；部分账号类型可能不支持。</span>
                  </div>
                </div>
              )}
            </section>

            {usage.errorCode ? (
              <div className={styles.officialError} role="status">
                <div>
                  <strong>官方配额查询未完成</strong>
                  <span>{usage.errorMessage || usage.errorCode}</span>
                </div>
                <span>{usage.retryable ? '可重试' : usage.errorCode}</span>
              </div>
            ) : null}

            <section className={styles.section}>
              <div className={styles.sectionHeader}>
                <div>
                  <strong>本地请求统计</strong>
                  <span>来自 Manager 聚合，不等同于上游官方账单</span>
                </div>
                <span className={styles.sectionBadge}>今日</span>
              </div>
              {local ? (
                <>
                  <div className={styles.summaryGrid}>
                    <article className={`${styles.summaryCard} ${styles.summaryPrimary}`}>
                      <span>请求总数</span>
                      <strong>{formatCompactNumber(local.requests)}</strong>
                      <small>全部调用</small>
                    </article>
                    <article className={`${styles.summaryCard} ${styles.summarySuccess}`}>
                      <span>成功请求</span>
                      <strong>{formatCompactNumber(local.successes)}</strong>
                      <small>
                        {local.requests > 0
                          ? `成功率 ${Math.round((local.successes / local.requests) * 100)}%`
                          : '暂无请求'}
                      </small>
                    </article>
                    <article className={`${styles.summaryCard} ${styles.summaryDanger}`}>
                      <span>失败请求</span>
                      <strong>{formatCompactNumber(local.failures)}</strong>
                      <small>本地记录的失败调用</small>
                    </article>
                    <article className={`${styles.summaryCard} ${styles.summaryCost}`}>
                      <span>估算成本</span>
                      <strong>
                        {local.costKnown && local.estimatedCost !== undefined
                          ? formatUsd(local.estimatedCost)
                          : '--'}
                      </strong>
                      <small>{local.costKnown ? '单一估算值' : '当前价格信息不足'}</small>
                      <IconDollarSign size={17} aria-hidden="true" />
                    </article>
                  </div>

                  <div className={styles.tokenGrid}>
                    {tokenMetrics.map((metric) => (
                      <article
                        className={`${styles.tokenCard} ${metric.emphasized ? styles.tokenCardEmphasized : ''}`}
                        key={metric.label}
                      >
                        <span>{metric.label}</span>
                        <strong title={metric.value.toLocaleString('zh-CN')}>
                          {formatCompactNumber(metric.value)}
                        </strong>
                        <small>{metric.detail || '本地聚合'}</small>
                      </article>
                    ))}
                  </div>
                </>
              ) : null}
            </section>

            <div className={styles.metaPanel}>
              <span className={styles.metaIcon} aria-hidden="true">
                <IconDatabaseZap size={18} />
              </span>
              <dl>
                <div>
                  <dt>数据来源</dt>
                  <dd>{sourceLabel(usage.source)}</dd>
                </div>
                <div>
                  <dt>更新时间</dt>
                  <dd>{formatDateTime(usage.updatedAtMs)}</dd>
                </div>
                <div>
                  <dt>统计区间</dt>
                  <dd>
                    {formatDateTime(usage.local.fromMs)} — {formatDateTime(usage.local.toMs)}
                  </dd>
                </div>
                <div>
                  <dt>最后活动</dt>
                  <dd>{formatDateTime(usage.local.lastActivityAtMs)}</dd>
                </div>
              </dl>
            </div>
          </>
        ) : null}
      </div>
    </Modal>
  );
}
