import { useEffect, useState } from 'react';
import { Button } from '@/components/ui/Button';
import { Modal } from '@/components/ui/Modal';
import type { ProAccount, ProAccountConnectivityResult } from '@/services/api/proAccounts';
import { proAccountsApi } from '@/services/api/proAccounts';
import { createRequestIdentity, suggestedTestModel } from './accountFormUtils';
import styles from './AccountModals.module.scss';

interface AccountTestModalProps {
  open: boolean;
  account: ProAccount | null;
  managerBase: string;
  managementKey: string;
  onClose: () => void;
  onTested: () => void;
}

export function AccountTestModal({
  open,
  account,
  managerBase,
  managementKey,
  onClose,
  onTested,
}: AccountTestModalProps) {
  const [model, setModel] = useState('');
  const [testing, setTesting] = useState(false);
  const [result, setResult] = useState<ProAccountConnectivityResult | null>(null);
  const [error, setError] = useState('');

  useEffect(() => {
    if (!open || !account) return;
    setModel(suggestedTestModel('', account.allowedModels, account.modelMapping));
    setResult(null);
    setError('');
  }, [account, open]);

  const test = async () => {
    if (!account || !model.trim()) {
      setError('请输入连通性测试模型');
      return;
    }
    setTesting(true);
    setResult(null);
    setError('');
    try {
      const identity = createRequestIdentity('account-test');
      const response = await proAccountsApi.testAccount(
        managerBase,
        managementKey,
        account,
        model.trim(),
        identity.operationId,
        identity.idempotencyKey
      );
      setResult(response.connectivity);
      onTested();
    } catch (testError) {
      setError(testError instanceof Error ? testError.message : String(testError));
    } finally {
      setTesting(false);
    }
  };

  const footer = (
    <div className={styles.footer}>
      <Button variant="secondary" size="sm" onClick={onClose} disabled={testing}>
        关闭
      </Button>
      <Button variant="primary" size="sm" onClick={() => void test()} loading={testing}>
        开始测试
      </Button>
    </div>
  );

  return (
    <Modal
      open={open}
      title="账号连通性测试"
      onClose={onClose}
      footer={footer}
      width={560}
      closeDisabled={testing}
    >
      <div className={styles.body}>
        <label className={styles.field}>
          <span className={styles.fieldLabel}>客户端模型名</span>
          <input
            className={styles.input}
            value={model}
            onChange={(event) => setModel(event.target.value)}
          />
        </label>
        {result ? (
          <div className={result.success ? styles.success : styles.error}>
            {result.success
              ? `测试成功：${result.model}`
              : `测试失败：${result.errorCode || result.statusCode || '未知错误'}`}
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
