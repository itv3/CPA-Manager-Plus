import { useEffect, useMemo, useRef, useState } from 'react';
import { Button } from '@/components/ui/Button';
import { Modal } from '@/components/ui/Modal';
import { SelectionCheckbox } from '@/components/ui/SelectionCheckbox';
import {
  proAccountsApi,
  type ProAccount,
  type ProAccountBatchAction,
  type ProAccountBatchResult,
} from '@/services/api/proAccounts';
import { createRequestIdentity, suggestedTestModel } from './accountFormUtils';
import { usesSharedProviderSwitch } from './accountTablePresentation';
import { executeAccountBatchChunks } from './accountBatchExecution';
import styles from './AccountModals.module.scss';

interface AccountBatchModalProps {
  open: boolean;
  action: ProAccountBatchAction | null;
  accounts: ProAccount[];
  providerAccounts: ProAccount[];
  managerBase: string;
  managementKey: string;
  onClose: () => void;
  onCompleted: (result: ProAccountBatchResult) => void | Promise<void>;
}

const ACTION_LABELS: Record<ProAccountBatchAction, string> = {
  enable: '批量启用',
  disable: '批量停用',
  test: '批量测试',
  delete: '批量删除',
};

export function AccountBatchModal({
  open,
  action,
  accounts,
  providerAccounts,
  managerBase,
  managementKey,
  onClose,
  onCompleted,
}: AccountBatchModalProps) {
  const [model, setModel] = useState('');
  const [targetAccounts, setTargetAccounts] = useState<ProAccount[]>([]);
  const [confirmed, setConfirmed] = useState(false);
  const [running, setRunning] = useState(false);
  const [result, setResult] = useState<ProAccountBatchResult | null>(null);
  const [error, setError] = useState('');
  const wasOpenRef = useRef(false);

  useEffect(() => {
    if (open && !wasOpenRef.current) {
      const first = accounts[0];
      setTargetAccounts(accounts);
      setModel(first ? suggestedTestModel('', first.allowedModels, first.modelMapping) : '');
      setConfirmed(false);
      setResult(null);
      setError('');
    }
    wasOpenRef.current = open;
  }, [accounts, open]);

  const title = action ? ACTION_LABELS[action] : '批量操作';
  const accountNames = useMemo(
    () => targetAccounts.map((account) => account.name || account.email || account.id),
    [targetAccounts]
  );
  const sharedProviderCount = useMemo(
    () =>
      targetAccounts.filter((account) => usesSharedProviderSwitch(account, providerAccounts))
        .length,
    [providerAccounts, targetAccounts]
  );

  const execute = async () => {
    if (!action || targetAccounts.length === 0) return;
    if (action === 'delete' && !confirmed) {
      setError('请确认删除底层凭证后再继续');
      return;
    }
    const items = targetAccounts.map((account) => ({
      account,
      model:
        action === 'test'
          ? model.trim() || suggestedTestModel('', account.allowedModels, account.modelMapping)
          : undefined,
    }));
    if (action === 'test' && items.some((item) => !item.model)) {
      setError('请输入批量测试模型');
      return;
    }
    setRunning(true);
    setError('');
    try {
      const response = await executeAccountBatchChunks(action, items, async (chunk, chunkIndex) => {
        const identity = createRequestIdentity(`account-batch-${action}-${chunkIndex}`);
        return proAccountsApi.batch(
          managerBase,
          managementKey,
          action,
          chunk,
          identity.operationId,
          identity.idempotencyKey
        );
      });
      setResult(response);
      await onCompleted(response);
    } catch (batchError) {
      setError(batchError instanceof Error ? batchError.message : String(batchError));
    } finally {
      setRunning(false);
    }
  };

  const footer = (
    <div className={styles.footer}>
      <Button variant="secondary" size="sm" onClick={onClose} disabled={running}>
        关闭
      </Button>
      {!result ? (
        <Button
          variant={action === 'delete' ? 'danger' : 'primary'}
          size="sm"
          onClick={() => void execute()}
          loading={running}
        >
          执行
        </Button>
      ) : null}
    </div>
  );

  return (
    <Modal
      open={open}
      title={title}
      onClose={onClose}
      footer={footer}
      width={680}
      closeDisabled={running}
    >
      <div className={styles.body}>
        <div className={styles.batchSummary}>已选择 {targetAccounts.length} 个账号</div>
        <div className={styles.compactList}>
          {accountNames.map((name, index) => (
            <span key={`${targetAccounts[index]?.id}:${name}`}>{name}</span>
          ))}
        </div>
        {(action === 'enable' || action === 'disable') && sharedProviderCount > 0 ? (
          <div className={styles.sharedWarning} role="note">
            其中 {sharedProviderCount} 个账号来自共享 Chat Completions Provider，操作会联动同
            Provider 下未选择的 Key；完成后将自动同步关联状态。
          </div>
        ) : null}
        {action === 'test' ? (
          <label className={styles.field}>
            <span className={styles.fieldLabel}>测试模型</span>
            <input
              className={styles.input}
              value={model}
              onChange={(event) => setModel(event.target.value)}
              placeholder="留空时按账号模型规则选择"
            />
          </label>
        ) : null}
        {action === 'delete' ? (
          <SelectionCheckbox
            checked={confirmed}
            onChange={setConfirmed}
            label="确认同时删除所选账号的底层凭证"
            ariaLabel="确认批量删除底层凭证"
          />
        ) : null}
        {result ? (
          <div className={styles.resultBlock}>
            <div className={result.failed > 0 ? styles.error : styles.success}>
              成功 {result.succeeded}，失败 {result.failed}
            </div>
            {result.items
              .filter((item) => !item.success)
              .map((item) => (
                <div className={styles.resultRow} key={item.proAccountId}>
                  <strong>{item.proAccountId}</strong>
                  <span>{item.code}</span>
                  <span>{item.message}</span>
                </div>
              ))}
          </div>
        ) : null}
        {error ? <div className={styles.error}>{error}</div> : null}
      </div>
    </Modal>
  );
}
