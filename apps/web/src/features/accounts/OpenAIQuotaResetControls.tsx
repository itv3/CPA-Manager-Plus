import { useEffect, useRef, useState } from 'react';
import { IconRefreshCw } from '@/components/ui/icons';
import {
  proAccountsApi,
  type ProAccount,
  type ProAccountResetCreditsResult,
} from '@/services/api/proAccounts';
import { useNotificationStore } from '@/stores';
import { createRequestIdentity } from './accountFormUtils';
import styles from './AccountsPage.module.scss';

interface OpenAIQuotaResetControlsProps {
  account: ProAccount;
  managerBase: string;
  managementKey: string;
  usageSource?: string;
  usageLoading: boolean;
  onQueryUsage: () => void | Promise<void>;
  onResetCompleted: () => void | Promise<void>;
}

const formatCreditExpiry = (value: number, style: 'short' | 'full'): string => {
  const date = new Date(value);
  if (Number.isNaN(date.getTime())) return String(value);

  const options: Intl.DateTimeFormatOptions = {
    month: '2-digit',
    day: '2-digit',
    hour: '2-digit',
    minute: '2-digit',
  };
  if (style === 'full') options.year = 'numeric';
  return new Intl.DateTimeFormat(undefined, options).format(date);
};

const errorMessage = (error: unknown): string =>
  error instanceof Error ? error.message : String(error);

export function OpenAIQuotaResetControls({
  account,
  managerBase,
  managementKey,
  usageSource,
  usageLoading,
  onQueryUsage,
  onResetCompleted,
}: OpenAIQuotaResetControlsProps) {
  const { showConfirmation } = useNotificationStore();
  const requestSequenceRef = useRef(0);
  const busyRef = useRef(false);
  const [credits, setCredits] = useState<ProAccountResetCreditsResult | null>(null);
  const [queryLoading, setQueryLoading] = useState(false);
  const [resetting, setResetting] = useState(false);
  const [error, setError] = useState('');
  const [resetMessage, setResetMessage] = useState('');
  const [showExpiryDetails, setShowExpiryDetails] = useState(false);

  const availableCount = credits?.availableCount;
  const canReset = credits?.capability === 'supported' && (availableCount ?? 0) > 0;
  const expirations = (credits?.credits ?? [])
    .map((credit) => credit.expiresAtMs)
    .filter((value): value is number => typeof value === 'number' && Number.isFinite(value))
    .sort((left, right) => left - right);
  const primaryExpiry = expirations[0];
  const hiddenExpiryCount = Math.max(expirations.length - 1, 0);

  useEffect(() => {
    requestSequenceRef.current += 1;
    busyRef.current = false;
    setCredits(null);
    setQueryLoading(false);
    setResetting(false);
    setError('');
    setResetMessage('');
    setShowExpiryDetails(false);
  }, [account.id]);

  const applyCreditsResult = (result: ProAccountResetCreditsResult) => {
    setCredits(result);
    setShowExpiryDetails(false);
    if (result.capability === 'supported') return;

    const reason = result.errorCode ? `（${result.errorCode}）` : '';
    setError(
      result.capability === 'unsupported'
        ? '该账号不支持官方重置次数'
        : `无法确认官方重置次数${reason}`
    );
  };

  const queryCredits = async () => {
    if (busyRef.current) return;
    const requestSequence = ++requestSequenceRef.current;
    busyRef.current = true;
    setQueryLoading(true);
    setError('');
    setResetMessage('');
    setShowExpiryDetails(false);
    try {
      const result = await proAccountsApi.resetCredits(managerBase, managementKey, account.id);
      if (requestSequence !== requestSequenceRef.current) return;
      applyCreditsResult(result);
    } catch (queryError) {
      if (requestSequence !== requestSequenceRef.current) return;
      setError(errorMessage(queryError));
    } finally {
      if (requestSequence === requestSequenceRef.current) {
        busyRef.current = false;
        setQueryLoading(false);
      }
    }
  };

  const resetQuota = async () => {
    if (busyRef.current || !canReset) return;
    const requestSequence = ++requestSequenceRef.current;
    busyRef.current = true;
    setResetting(true);
    setError('');
    setResetMessage('');
    try {
      const identity = createRequestIdentity('account-openai-reset');
      const result = await proAccountsApi.resetOpenAI(
        managerBase,
        managementKey,
        account,
        identity.operationId,
        identity.idempotencyKey
      );
      if (requestSequence !== requestSequenceRef.current) return;
      setCredits(result.credits);

      // 重置接口虽然返回最新次数，仍再次查询上游，保持与参考实现一致。
      const refreshResults = await Promise.allSettled([
        proAccountsApi.resetCredits(managerBase, managementKey, account.id),
        Promise.resolve(onResetCompleted()),
      ]);
      if (requestSequence !== requestSequenceRef.current) return;
      const creditRefresh = refreshResults[0];
      if (creditRefresh.status === 'fulfilled') {
        applyCreditsResult(creditRefresh.value);
        setError('');
        setResetMessage('官方配额已重置');
      } else {
        setResetMessage(`官方配额已重置；次数刷新失败：${errorMessage(creditRefresh.reason)}`);
      }
    } catch (resetError) {
      if (requestSequence !== requestSequenceRef.current) return;
      setError(errorMessage(resetError));
    } finally {
      if (requestSequence === requestSequenceRef.current) {
        busyRef.current = false;
        setResetting(false);
      }
    }
  };

  const confirmReset = () => {
    if (!canReset) return;
    const name = account.name || account.email || account.id;
    showConfirmation({
      title: '确认重置周限',
      message: `将为“${name}”消耗 1 次重置次数立即恢复当前窗口，剩余 ${availableCount} 次。此操作不可撤销，确定继续吗？`,
      confirmText: '确认重置',
      cancelText: '取消',
      variant: 'danger',
      onConfirm: resetQuota,
    });
  };

  const countTitle = credits ? '点击刷新剩余重置次数' : '点击查询剩余重置次数';
  const resetTitle = !credits
    ? '先点击“次数”加载剩余重置次数'
    : credits.capability !== 'supported'
      ? '当前无法确认可用重置次数'
      : !canReset
        ? '没有可用的重置次数'
        : '消耗 1 次重置次数以立即恢复当前窗口';

  return (
    <div className={styles.openAIResetControls}>
      <div className={styles.usageActions}>
        {usageSource === 'passive' ? <span className={styles.passiveUsage}>被动采样</span> : null}
        <button type="button" onClick={onQueryUsage} disabled={usageLoading} title="查询官方用量">
          <IconRefreshCw size={11} />
          查询
        </button>
        <button
          type="button"
          onClick={() => void queryCredits()}
          disabled={queryLoading || resetting}
          title={countTitle}
        >
          <IconRefreshCw className={queryLoading ? styles.spinningIcon : undefined} size={11} />
          <span>次数{credits ? ` ${availableCount ?? '-'}` : ''}</span>
        </button>
        <button
          type="button"
          className={styles.resetUsageAction}
          onClick={confirmReset}
          disabled={queryLoading || resetting || !canReset}
          title={resetTitle}
        >
          <IconRefreshCw className={resetting ? styles.spinningIcon : undefined} size={11} />
          重置
        </button>
      </div>

      {primaryExpiry !== undefined ? (
        <div className={styles.resetCreditExpiries}>
          <div className={styles.resetCreditExpiryRow}>
            <span
              className={styles.resetCreditExpiryBadge}
              title={`重置次数到期时间：${formatCreditExpiry(primaryExpiry, 'full')}`}
            >
              到期 {formatCreditExpiry(primaryExpiry, 'short')}
            </span>
            {hiddenExpiryCount > 0 ? (
              <button
                type="button"
                className={styles.resetCreditExpiryToggle}
                aria-expanded={showExpiryDetails}
                aria-label={showExpiryDetails ? '收起重置次数到期时间' : '展开重置次数到期时间'}
                data-testid="reset-credit-expiry-toggle"
                onClick={() => setShowExpiryDetails((value) => !value)}
              >
                +{hiddenExpiryCount}
              </button>
            ) : null}
          </div>
          {showExpiryDetails && expirations.length > 1 ? (
            <div
              className={styles.resetCreditExpiryDetails}
              data-testid="reset-credit-expiry-details"
            >
              {expirations.map((expiresAt, index) => (
                <span key={`${expiresAt}-${index}`}>{formatCreditExpiry(expiresAt, 'short')}</span>
              ))}
            </div>
          ) : null}
        </div>
      ) : null}

      {error ? (
        <div className={styles.usageError} title={error}>
          {error}
        </div>
      ) : resetMessage ? (
        <div className={styles.resetMessage} title={resetMessage}>
          {resetMessage}
        </div>
      ) : null}
    </div>
  );
}
