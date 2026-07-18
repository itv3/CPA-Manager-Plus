import { useCallback, useEffect, useRef, useState } from 'react';
import { Button } from '@/components/ui/Button';
import { Input } from '@/components/ui/Input';
import { Modal } from '@/components/ui/Modal';
import { IconCheck, IconCopy, IconExternalLink, IconRefreshCw } from '@/components/ui/icons';
import { proAccountsApi, type ProAccount } from '@/services/api/proAccounts';
import { useNotificationStore } from '@/stores';
import { copyToClipboard } from '@/utils/clipboard';
import { createRequestIdentity } from './accountFormUtils';
import styles from './AccountActionsModal.module.scss';

interface AccountReauthorizeModalProps {
  open: boolean;
  account: ProAccount | null;
  managerBase: string;
  managementKey: string;
  onClose: () => void;
  onCompleted: (account?: ProAccount) => void | Promise<void>;
}

type ReauthorizationStatus = 'idle' | 'loading' | 'waiting' | 'success' | 'error';

interface ActiveReauthorizationOperation {
  operationId: string;
  accountId: string;
  managerBase: string;
  managementKey: string;
}

const POLL_INTERVAL_MS = 2_000;

export function AccountReauthorizeModal({
  open,
  account,
  managerBase,
  managementKey,
  onClose,
  onCompleted,
}: AccountReauthorizeModalProps) {
  const { showNotification } = useNotificationStore();
  const pollTimerRef = useRef<number | null>(null);
  const pollGenerationRef = useRef(0);
  const startGenerationRef = useRef(0);
  const startInFlightRef = useRef(false);
  const activeOperationRef = useRef<ActiveReauthorizationOperation | null>(null);
  const autoStartedAccountIdRef = useRef('');
  const accountRef = useRef(account);
  const managerBaseRef = useRef(managerBase);
  const managementKeyRef = useRef(managementKey);
  const onCompletedRef = useRef(onCompleted);
  const showNotificationRef = useRef(showNotification);
  const [status, setStatus] = useState<ReauthorizationStatus>('idle');
  const [authUrl, setAuthUrl] = useState('');
  const [callbackState, setCallbackState] = useState('');
  const [callbackInput, setCallbackInput] = useState('');
  const [error, setError] = useState('');
  const [callbackSubmitting, setCallbackSubmitting] = useState(false);
  const [copied, setCopied] = useState(false);

  useEffect(() => {
    accountRef.current = account;
  }, [account]);

  useEffect(() => {
    managerBaseRef.current = managerBase;
    managementKeyRef.current = managementKey;
  }, [managementKey, managerBase]);

  useEffect(() => {
    onCompletedRef.current = onCompleted;
  }, [onCompleted]);

  useEffect(() => {
    showNotificationRef.current = showNotification;
  }, [showNotification]);

  const clearPolling = useCallback(() => {
    pollGenerationRef.current += 1;
    if (pollTimerRef.current !== null) {
      window.clearTimeout(pollTimerRef.current);
      pollTimerRef.current = null;
    }
  }, []);

  const markSuccess = useCallback(
    async (operationId: string, nextAccount?: ProAccount) => {
      clearPolling();
      if (activeOperationRef.current?.operationId === operationId) {
        activeOperationRef.current = null;
      }
      setStatus('success');
      setError('');
      showNotificationRef.current('账号重新授权成功', 'success');
      await onCompletedRef.current(nextAccount);
    },
    [clearPolling]
  );

  const startPolling = useCallback(
    (operation: ActiveReauthorizationOperation) => {
      clearPolling();
      const generation = pollGenerationRef.current;
      const poll = async () => {
        if (generation !== pollGenerationRef.current) return;
        try {
          const result = await proAccountsApi.reauthorizationStatus(
            operation.managerBase,
            operation.managementKey,
            operation.accountId,
            operation.operationId
          );
          if (generation !== pollGenerationRef.current) return;
          if (result.status === 'ok') {
            await markSuccess(operation.operationId, result.account);
            return;
          } else if (result.status === 'error' || result.status === 'cancelled') {
            clearPolling();
            if (activeOperationRef.current?.operationId === operation.operationId) {
              activeOperationRef.current = null;
            }
            setStatus('error');
            setError('授权未完成，请重新生成授权链接');
            return;
          }
        } catch (pollError) {
          if (generation !== pollGenerationRef.current) return;
          clearPolling();
          setStatus('error');
          setError(pollError instanceof Error ? pollError.message : String(pollError));
          return;
        }
        if (generation === pollGenerationRef.current) {
          pollTimerRef.current = window.setTimeout(() => void poll(), POLL_INTERVAL_MS);
        }
      };
      pollTimerRef.current = window.setTimeout(() => void poll(), POLL_INTERVAL_MS);
    },
    [clearPolling, markSuccess]
  );

  const start = useCallback(async () => {
    const currentAccount = accountRef.current;
    const currentManagerBase = managerBaseRef.current;
    const currentManagementKey = managementKeyRef.current;
    if (!currentAccount || !currentManagerBase || !currentManagementKey || startInFlightRef.current)
      return;
    startInFlightRef.current = true;
    const generation = ++startGenerationRef.current;
    clearPolling();
    const previousOperation = activeOperationRef.current;
    activeOperationRef.current = null;
    setStatus('loading');
    setAuthUrl('');
    setCallbackInput('');
    setCallbackState('');
    setError('');
    setCopied(false);
    try {
      if (previousOperation) {
        await proAccountsApi
          .cancelReauthorization(
            previousOperation.managerBase,
            previousOperation.managementKey,
            previousOperation.accountId,
            previousOperation.operationId
          )
          .catch(() => undefined);
      }
      if (generation !== startGenerationRef.current) return;
      const identity = createRequestIdentity('account-reauthorize');
      const result = await proAccountsApi.startReauthorization(
        currentManagerBase,
        currentManagementKey,
        currentAccount,
        identity.operationId,
        identity.idempotencyKey
      );
      const operationId = result.operation?.operationId || identity.operationId;
      const operation: ActiveReauthorizationOperation = {
        operationId,
        accountId: currentAccount.id,
        managerBase: currentManagerBase,
        managementKey: currentManagementKey,
      };
      if (generation !== startGenerationRef.current) {
        void proAccountsApi
          .cancelReauthorization(
            operation.managerBase,
            operation.managementKey,
            operation.accountId,
            operation.operationId
          )
          .catch(() => undefined);
        return;
      }
      const url = result.oauth?.url || '';
      activeOperationRef.current = operation;
      setAuthUrl(url);
      setCallbackState(result.oauth?.state || '');
      setStatus(result.status === 'ok' ? 'success' : 'waiting');
      if (result.status === 'ok') {
        await markSuccess(operation.operationId, result.account);
      } else {
        startPolling(operation);
      }
    } catch (startError) {
      if (generation !== startGenerationRef.current) return;
      setStatus('error');
      setError(startError instanceof Error ? startError.message : String(startError));
    } finally {
      if (generation === startGenerationRef.current) {
        startInFlightRef.current = false;
      }
    }
  }, [clearPolling, markSuccess, startPolling]);

  useEffect(() => {
    const accountId = account?.id || '';
    if (!open || !accountId) {
      autoStartedAccountIdRef.current = '';
      clearPolling();
      return;
    }
    if (autoStartedAccountIdRef.current === accountId) return;
    autoStartedAccountIdRef.current = accountId;
    const timer = window.setTimeout(() => void start(), 0);
    return () => {
      window.clearTimeout(timer);
      clearPolling();
    };
  }, [account?.id, clearPolling, open, start]);

  useEffect(() => () => clearPolling(), [clearPolling]);

  const submitCallback = async () => {
    const operation = activeOperationRef.current;
    if (!operation || !callbackInput.trim()) {
      showNotification('请粘贴浏览器回调地址或授权码', 'warning');
      return;
    }
    setCallbackSubmitting(true);
    setError('');
    try {
      const result = await proAccountsApi.submitReauthorizationCallback(
        operation.managerBase,
        operation.managementKey,
        operation.accountId,
        operation.operationId,
        callbackInput.trim(),
        callbackState
      );
      if (activeOperationRef.current?.operationId !== operation.operationId) return;
      if (result.status === 'error') {
        activeOperationRef.current = null;
        throw new Error('授权回调处理失败');
      }
      if (result.status === 'ok') {
        await markSuccess(operation.operationId, result.account);
      } else if (result.status === 'cancelled') {
        activeOperationRef.current = null;
        throw new Error('授权会话已取消，请重新生成授权链接');
      } else {
        setStatus('waiting');
        startPolling(operation);
      }
    } catch (submitError) {
      setStatus('error');
      setError(submitError instanceof Error ? submitError.message : String(submitError));
    } finally {
      setCallbackSubmitting(false);
    }
  };

  const copyLink = async () => {
    const success = await copyToClipboard(authUrl);
    setCopied(success);
    showNotification(
      success ? '授权链接已复制' : '复制授权链接失败',
      success ? 'success' : 'error'
    );
  };

  const close = () => {
    startGenerationRef.current += 1;
    startInFlightRef.current = false;
    clearPolling();
    const operation = activeOperationRef.current;
    activeOperationRef.current = null;
    if (operation) {
      void proAccountsApi
        .cancelReauthorization(
          operation.managerBase,
          operation.managementKey,
          operation.accountId,
          operation.operationId
        )
        .catch(() => undefined);
    }
    onClose();
  };

  const statusText =
    status === 'loading'
      ? '正在生成授权链接...'
      : status === 'waiting'
        ? '等待同一账号完成授权'
        : status === 'success'
          ? '授权已完成'
          : status === 'error'
            ? error || '授权失败'
            : '';

  return (
    <Modal
      open={open}
      onClose={close}
      title="重新授权账号"
      width={640}
      closeDisabled={callbackSubmitting}
      footer={
        <div className={styles.footer}>
          <Button variant="secondary" size="sm" onClick={close} disabled={callbackSubmitting}>
            关闭
          </Button>
        </div>
      }
    >
      <div className={styles.actionModalBody}>
        <p className={styles.intro}>请使用与当前记录相同的账号登录，授权成功后凭证会原位更新。</p>
        <div className={styles.accountSummary}>
          <span>账号</span>
          <strong>{account?.name || account?.email || account?.id || '-'}</strong>
        </div>
        <section className={styles.oauthPanel}>
          <div className={styles.oauthPrimaryRow}>
            <Button
              type="button"
              onClick={() => window.open(authUrl, '_blank', 'noopener,noreferrer')}
              disabled={!authUrl || status === 'loading' || status === 'success'}
            >
              <IconExternalLink size={15} /> 打开授权链接
            </Button>
            {statusText ? (
              <span className={styles.actionStatus} data-status={status}>
                {statusText}
              </span>
            ) : null}
          </div>
          <div className={styles.linkPreview} title={authUrl || undefined}>
            {authUrl || '-'}
          </div>
          <div className={styles.inlineActions}>
            <Button
              variant="secondary"
              size="xs"
              onClick={() => void copyLink()}
              disabled={!authUrl}
            >
              <IconCopy size={13} /> {copied ? '已复制' : '复制链接'}
            </Button>
            <Button
              variant="secondary"
              size="xs"
              onClick={() => void start()}
              loading={status === 'loading'}
            >
              <IconRefreshCw size={13} /> 重新生成
            </Button>
          </div>
        </section>
        <section className={styles.callbackPanel}>
          <Input
            label="回调地址或授权码"
            placeholder="粘贴浏览器最终跳转地址，或直接粘贴授权码"
            value={callbackInput}
            onChange={(event) => setCallbackInput(event.target.value)}
            disabled={callbackSubmitting || status === 'success'}
          />
          <div className={styles.inlineActions}>
            <Button
              size="sm"
              onClick={() => void submitCallback()}
              loading={callbackSubmitting}
              disabled={status === 'success'}
            >
              <IconCheck size={14} /> 提交授权结果
            </Button>
          </div>
        </section>
      </div>
    </Modal>
  );
}
