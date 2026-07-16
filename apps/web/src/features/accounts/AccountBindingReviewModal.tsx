import { useEffect, useMemo, useRef, useState } from 'react';
import { Button } from '@/components/ui/Button';
import { Modal } from '@/components/ui/Modal';
import {
  proAccountsApi,
  type ProAccountBindingReviewItem,
  type ProAccountRebindResult,
} from '@/services/api/proAccounts';
import { createRequestIdentity } from './accountFormUtils';
import styles from './AccountModals.module.scss';

interface AccountBindingReviewModalProps {
  open: boolean;
  reviews: ProAccountBindingReviewItem[];
  managerBase: string;
  managementKey: string;
  onClose: () => void;
  onCompleted: (result: ProAccountRebindResult) => void;
}

export function AccountBindingReviewModal({
  open,
  reviews,
  managerBase,
  managementKey,
  onClose,
  onCompleted,
}: AccountBindingReviewModalProps) {
  const [selectedReviews, setSelectedReviews] = useState<Set<number>>(new Set());
  const [targetReviews, setTargetReviews] = useState<ProAccountBindingReviewItem[]>([]);
  const [candidateByReview, setCandidateByReview] = useState<Record<number, string>>({});
  const [running, setRunning] = useState(false);
  const [result, setResult] = useState<ProAccountRebindResult | null>(null);
  const [error, setError] = useState('');
  const wasOpenRef = useRef(false);

  useEffect(() => {
    if (open && !wasOpenRef.current) {
      const candidates: Record<number, string> = {};
      const selected = new Set<number>();
      reviews.forEach((item) => {
        if (item.candidates.length === 1) {
          candidates[item.review.id] = item.candidates[0].id;
          selected.add(item.review.id);
        }
      });
      setTargetReviews(reviews);
      setCandidateByReview(candidates);
      setSelectedReviews(selected);
      setResult(null);
      setError('');
    }
    wasOpenRef.current = open;
  }, [open, reviews]);

  const selectedItems = useMemo(
    () =>
      targetReviews
        .filter((item) => selectedReviews.has(item.review.id))
        .map((item) => ({
          reviewId: item.review.id,
          account: item.candidates.find(
            (candidate) => candidate.id === candidateByReview[item.review.id]
          ),
        })),
    [candidateByReview, selectedReviews, targetReviews]
  );

  const toggleReview = (reviewID: number, checked: boolean) => {
    setSelectedReviews((current) => {
      const next = new Set(current);
      if (checked) next.add(reviewID);
      else next.delete(reviewID);
      return next;
    });
  };

  const submit = async () => {
    if (selectedItems.length === 0) {
      setError('请选择至少一条待确认绑定');
      return;
    }
    if (selectedItems.some((item) => !item.account)) {
      setError('请为所有选中项目选择候选账号');
      return;
    }
    const accountIDs = selectedItems.map((item) => item.account?.id ?? '');
    if (new Set(accountIDs).size !== accountIDs.length) {
      setError('同一批次不能把多个发现项绑定到同一账号');
      return;
    }
    setRunning(true);
    setError('');
    try {
      const identity = createRequestIdentity('account-rebind');
      const response = await proAccountsApi.rebind(
        managerBase,
        managementKey,
        selectedItems.map((item) => ({ reviewId: item.reviewId, account: item.account! })),
        identity.operationId,
        identity.idempotencyKey
      );
      setResult(response);
      onCompleted(response);
    } catch (rebindError) {
      setError(rebindError instanceof Error ? rebindError.message : String(rebindError));
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
        <Button variant="primary" size="sm" onClick={() => void submit()} loading={running}>
          确认重绑
        </Button>
      ) : null}
    </div>
  );

  return (
    <Modal
      open={open}
      title="绑定漂移确认"
      onClose={onClose}
      footer={footer}
      width={860}
      closeDisabled={running}
    >
      <div className={styles.body}>
        <div className={styles.reviewTableWrap}>
          <table className={styles.reviewTable}>
            <thead>
              <tr>
                <th aria-label="选择" />
                <th>漂移类型</th>
                <th>当前发现</th>
                <th>候选账号</th>
              </tr>
            </thead>
            <tbody>
              {targetReviews.map((item) => {
                const hasCandidate = item.candidates.length > 0;
                return (
                  <tr key={item.review.id}>
                    <td>
                      <input
                        type="checkbox"
                        checked={selectedReviews.has(item.review.id)}
                        onChange={(event) => toggleReview(item.review.id, event.target.checked)}
                        disabled={!hasCandidate || running}
                        aria-label={`选择漂移 ${item.review.id}`}
                      />
                    </td>
                    <td>{item.review.driftType === 'file_path' ? '文件路径' : 'API 凭证'}</td>
                    <td>
                      <div className={styles.reviewLocator} title={item.review.sourceLocator}>
                        {item.review.sourceLocator}
                      </div>
                      <small>{item.review.reasonCode}</small>
                    </td>
                    <td>
                      <select
                        className={styles.select}
                        value={candidateByReview[item.review.id] ?? ''}
                        onChange={(event) =>
                          setCandidateByReview((current) => ({
                            ...current,
                            [item.review.id]: event.target.value,
                          }))
                        }
                        disabled={!hasCandidate || running}
                        aria-label={`漂移 ${item.review.id} 候选账号`}
                      >
                        <option value="">请选择候选账号</option>
                        {item.candidates.map((candidate) => (
                          <option key={candidate.id} value={candidate.id}>
                            {candidate.name || candidate.email || candidate.id}
                          </option>
                        ))}
                      </select>
                    </td>
                  </tr>
                );
              })}
            </tbody>
          </table>
        </div>
        {result ? (
          <div className={styles.resultBlock}>
            <div className={result.failed > 0 ? styles.error : styles.success}>
              成功 {result.succeeded}，失败 {result.failed}
            </div>
            {result.items
              .filter((item) => !item.success)
              .map((item) => (
                <div className={styles.resultRow} key={item.reviewId}>
                  <strong>记录 {item.reviewId}</strong>
                  <span>{item.code}</span>
                  <span>{item.message}</span>
                </div>
              ))}
          </div>
        ) : null}
        {targetReviews.length === 0 ? <div className={styles.notice}>暂无待确认绑定</div> : null}
        {error ? <div className={styles.error}>{error}</div> : null}
      </div>
    </Modal>
  );
}
