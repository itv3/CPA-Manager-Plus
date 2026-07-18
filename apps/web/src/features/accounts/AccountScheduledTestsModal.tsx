import { useEffect, useMemo, useState } from 'react';
import { Button } from '@/components/ui/Button';
import { Input } from '@/components/ui/Input';
import { Modal } from '@/components/ui/Modal';
import { Select } from '@/components/ui/Select';
import { ToggleSwitch } from '@/components/ui/ToggleSwitch';
import {
  IconChevronDown,
  IconPencil,
  IconPlus,
  IconRefreshCw,
  IconTimer,
  IconTrash2,
} from '@/components/ui/icons';
import {
  proAccountsApi,
  type ProAccount,
  type ProAccountScheduledTestPlan,
  type ProAccountScheduledTestPlanInput,
  type ProAccountScheduledTestResult,
} from '@/services/api/proAccounts';
import { useNotificationStore } from '@/stores';
import { createRequestIdentity, suggestedTestModel } from './accountFormUtils';
import styles from './AccountActionsModal.module.scss';

interface AccountScheduledTestsModalProps {
  open: boolean;
  account: ProAccount | null;
  managerBase: string;
  managementKey: string;
  onClose: () => void;
}

const DEFAULT_FORM: ProAccountScheduledTestPlanInput = {
  modelId: '',
  cronExpression: '*/30 * * * *',
  enabled: true,
  maxResults: 100,
  autoRecover: false,
};

const formatDateTime = (value?: number) => {
  if (!value) return '-';
  const date = new Date(value);
  return Number.isNaN(date.getTime()) ? '-' : date.toLocaleString();
};

const planToForm = (plan: ProAccountScheduledTestPlan): ProAccountScheduledTestPlanInput => ({
  modelId: plan.modelId,
  cronExpression: plan.cronExpression,
  enabled: plan.enabled,
  maxResults: plan.maxResults,
  autoRecover: false,
});

export function AccountScheduledTestsModal({
  open,
  account,
  managerBase,
  managementKey,
  onClose,
}: AccountScheduledTestsModalProps) {
  const { showConfirmation, showNotification } = useNotificationStore();
  const [plans, setPlans] = useState<ProAccountScheduledTestPlan[]>([]);
  const [results, setResults] = useState<ProAccountScheduledTestResult[]>([]);
  const [models, setModels] = useState<string[]>([]);
  const [form, setForm] = useState<ProAccountScheduledTestPlanInput>(DEFAULT_FORM);
  const [showForm, setShowForm] = useState(false);
  const [editingPlanId, setEditingPlanId] = useState<number | null>(null);
  const [expandedPlanId, setExpandedPlanId] = useState<number | null>(null);
  const [loading, setLoading] = useState(false);
  const [saving, setSaving] = useState(false);
  const [loadingResults, setLoadingResults] = useState(false);
  const [error, setError] = useState('');

  const modelOptions = useMemo(
    () =>
      [...new Set([...(account?.allowedModels ?? []), ...models])]
        .map((item) => item.trim())
        .filter((item) => item && !item.includes('*'))
        .map((item) => ({ value: item, label: item })),
    [account?.allowedModels, models]
  );

  const resetForm = (nextModels = modelOptions.map((option) => option.value)) => {
    setForm({
      ...DEFAULT_FORM,
      modelId: suggestedTestModel(
        '',
        account?.allowedModels ?? [],
        account?.modelMapping ?? {},
        nextModels
      ),
    });
    setEditingPlanId(null);
  };

  useEffect(() => {
    if (!open || !account) return;
    let cancelled = false;
    setLoading(true);
    setError('');
    setPlans([]);
    setResults([]);
    setShowForm(false);
    setEditingPlanId(null);
    setExpandedPlanId(null);
    void Promise.all([
      proAccountsApi.listScheduledTests(managerBase, managementKey, account.id),
      proAccountsApi.modelCatalog(managerBase, managementKey, account.id).catch(() => ({
        models: [],
        upstream: [],
        builtIn: [],
        manual: [],
      })),
    ])
      .then(([nextPlans, catalog]) => {
        if (cancelled) return;
        setPlans(nextPlans);
        setModels(catalog.models ?? []);
        const nextModels = [
          ...new Set([...(account.allowedModels ?? []), ...(catalog.models ?? [])]),
        ];
        setForm({
          ...DEFAULT_FORM,
          modelId: suggestedTestModel('', account.allowedModels, account.modelMapping, nextModels),
        });
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

  const createPlan = async () => {
    if (!account || !form.modelId.trim() || !form.cronExpression.trim()) return;
    setSaving(true);
    setError('');
    try {
      const identity = createRequestIdentity('account-scheduled-test-create');
      const created = await proAccountsApi.createScheduledTest(
        managerBase,
        managementKey,
        account,
        { ...form, modelId: form.modelId.trim(), cronExpression: form.cronExpression.trim() },
        identity.operationId,
        identity.idempotencyKey
      );
      setPlans((current) => [created, ...current]);
      setShowForm(false);
      resetForm();
      showNotification('定时测试计划已创建', 'success');
    } catch (createError) {
      setError(createError instanceof Error ? createError.message : String(createError));
    } finally {
      setSaving(false);
    }
  };

  const updatePlan = async () => {
    if (!account || editingPlanId === null || !form.modelId.trim() || !form.cronExpression.trim())
      return;
    setSaving(true);
    setError('');
    try {
      const updated = await proAccountsApi.updateScheduledTest(
        managerBase,
        managementKey,
        account.id,
        editingPlanId,
        { ...form, modelId: form.modelId.trim(), cronExpression: form.cronExpression.trim() }
      );
      setPlans((current) => current.map((plan) => (plan.id === updated.id ? updated : plan)));
      resetForm();
      showNotification('定时测试计划已更新', 'success');
    } catch (updateError) {
      setError(updateError instanceof Error ? updateError.message : String(updateError));
    } finally {
      setSaving(false);
    }
  };

  const togglePlan = async (plan: ProAccountScheduledTestPlan, enabled: boolean) => {
    if (!account) return;
    setPlans((current) =>
      current.map((item) => (item.id === plan.id ? { ...item, enabled } : item))
    );
    try {
      const updated = await proAccountsApi.updateScheduledTest(
        managerBase,
        managementKey,
        account.id,
        plan.id,
        { enabled }
      );
      setPlans((current) => current.map((item) => (item.id === plan.id ? updated : item)));
    } catch (toggleError) {
      setPlans((current) =>
        current.map((item) => (item.id === plan.id ? { ...item, enabled: plan.enabled } : item))
      );
      showNotification(
        toggleError instanceof Error ? toggleError.message : String(toggleError),
        'error'
      );
    }
  };

  const deletePlan = (plan: ProAccountScheduledTestPlan) => {
    if (!account) return;
    showConfirmation({
      title: '删除定时测试计划',
      message: `确认删除模型“${plan.modelId}”的定时测试计划？历史结果也将一并删除。`,
      confirmText: '删除',
      cancelText: '取消',
      variant: 'danger',
      onConfirm: async () => {
        try {
          await proAccountsApi.deleteScheduledTest(managerBase, managementKey, account.id, plan.id);
          setPlans((current) => current.filter((item) => item.id !== plan.id));
          if (expandedPlanId === plan.id) {
            setExpandedPlanId(null);
            setResults([]);
          }
          showNotification('定时测试计划已删除', 'success');
        } catch (deleteError) {
          showNotification(
            deleteError instanceof Error ? deleteError.message : String(deleteError),
            'error'
          );
        }
      },
    });
  };

  const toggleResults = async (plan: ProAccountScheduledTestPlan) => {
    if (!account) return;
    if (expandedPlanId === plan.id) {
      setExpandedPlanId(null);
      setResults([]);
      return;
    }
    setExpandedPlanId(plan.id);
    setResults([]);
    setLoadingResults(true);
    try {
      setResults(
        await proAccountsApi.listScheduledTestResults(
          managerBase,
          managementKey,
          account.id,
          plan.id
        )
      );
    } catch (resultError) {
      showNotification(
        resultError instanceof Error ? resultError.message : String(resultError),
        'error'
      );
    } finally {
      setLoadingResults(false);
    }
  };

  const formNode = (
    <section
      className={styles.planForm}
      aria-label={editingPlanId !== null ? '编辑定时测试计划' : '添加定时测试计划'}
    >
      <strong>{editingPlanId !== null ? '编辑计划' : '添加计划'}</strong>
      <div className={styles.planFormGrid}>
        <label>
          <span>模型</span>
          <Select
            value={form.modelId}
            options={modelOptions}
            onChange={(modelId) => setForm((current) => ({ ...current, modelId }))}
            placeholder="选择测试模型"
            ariaLabel="定时测试模型"
          />
        </label>
        <Input
          label="Cron 表达式"
          value={form.cronExpression}
          onChange={(event) =>
            setForm((current) => ({ ...current, cronExpression: event.target.value }))
          }
          placeholder="*/30 * * * *"
          hint="分钟 小时 日期 月份 星期；例如 */30 * * * * 表示每 30 分钟"
        />
        <Input
          label="最大保留结果"
          type="number"
          min={1}
          max={500}
          value={form.maxResults}
          onChange={(event) =>
            setForm((current) => ({
              ...current,
              maxResults: Math.min(500, Math.max(1, Number(event.target.value) || 1)),
            }))
          }
        />
        <div className={styles.planToggleFields}>
          <ToggleSwitch
            checked={form.enabled}
            onChange={(enabled) => setForm((current) => ({ ...current, enabled }))}
            label="启用计划"
          />
        </div>
      </div>
      <div className={styles.planFormActions}>
        <Button
          variant="secondary"
          size="sm"
          onClick={() => {
            setShowForm(false);
            resetForm();
          }}
          disabled={saving}
        >
          取消
        </Button>
        <Button
          size="sm"
          onClick={() => void (editingPlanId !== null ? updatePlan() : createPlan())}
          loading={saving}
          disabled={!form.modelId.trim() || !form.cronExpression.trim()}
        >
          保存
        </Button>
      </div>
    </section>
  );

  return (
    <Modal
      open={open}
      onClose={onClose}
      title="定时测试"
      width={860}
      closeDisabled={saving}
      footer={
        <div className={styles.footer}>
          <Button variant="secondary" size="sm" onClick={onClose} disabled={saving}>
            关闭
          </Button>
        </div>
      }
    >
      <div className={styles.scheduledBody}>
        <div className={styles.scheduledHeader}>
          <div>
            <strong>{account?.name || account?.email || account?.id || '-'}</strong>
            <span>按 Cron 周期自动执行账号连通性测试，并保留最近结果。</span>
          </div>
          <Button
            size="sm"
            onClick={() => {
              resetForm();
              setShowForm((current) => !current);
            }}
          >
            <IconPlus size={14} /> 添加计划
          </Button>
        </div>
        {showForm && editingPlanId === null ? formNode : null}
        {error ? <div className={styles.actionError}>{error}</div> : null}
        {loading ? (
          <div className={styles.loadingState}>
            <IconRefreshCw size={16} /> 正在加载定时测试计划...
          </div>
        ) : plans.length === 0 ? (
          <div className={styles.emptyPlans}>
            <IconTimer size={28} />
            <span>暂无定时测试计划</span>
          </div>
        ) : (
          <div className={styles.planList}>
            {plans.map((plan) => (
              <article className={styles.planCard} key={plan.id}>
                <div className={styles.planHeader}>
                  <button type="button" onClick={() => void toggleResults(plan)}>
                    <span>
                      <strong>{plan.modelId}</strong>
                      <code>{plan.cronExpression}</code>
                    </span>
                    <IconChevronDown
                      size={15}
                      className={expandedPlanId === plan.id ? styles.expandedChevron : ''}
                    />
                  </button>
                  <div className={styles.planScheduleInfo}>
                    <span>上次 {formatDateTime(plan.lastRunAtMs)}</span>
                    <span>下次 {formatDateTime(plan.nextRunAtMs)}</span>
                  </div>
                  <ToggleSwitch
                    checked={plan.enabled}
                    onChange={(enabled) => void togglePlan(plan, enabled)}
                    ariaLabel={`${plan.enabled ? '停用' : '启用'}定时测试计划`}
                  />
                  <div className={styles.planActions}>
                    <button
                      type="button"
                      title="编辑计划"
                      onClick={() => {
                        setForm(planToForm(plan));
                        setEditingPlanId(plan.id);
                        setShowForm(false);
                      }}
                    >
                      <IconPencil size={14} />
                    </button>
                    <button type="button" title="删除计划" onClick={() => deletePlan(plan)}>
                      <IconTrash2 size={14} />
                    </button>
                  </div>
                </div>
                {editingPlanId === plan.id ? formNode : null}
                {expandedPlanId === plan.id ? (
                  <div className={styles.planResults}>
                    <strong>最近结果</strong>
                    {loadingResults ? (
                      <span>加载中...</span>
                    ) : results.length === 0 ? (
                      <span>暂无测试结果</span>
                    ) : (
                      results.map((result) => (
                        <div className={styles.resultRow} key={result.id}>
                          <span data-status={result.status}>
                            {result.status === 'success'
                              ? '成功'
                              : result.status === 'running'
                                ? '运行中'
                                : '失败'}
                          </span>
                          <small>{result.latencyMs ? `${result.latencyMs}ms` : '-'}</small>
                          <time>{formatDateTime(result.startedAtMs)}</time>
                          <p title={result.errorMessage || result.responseText}>
                            {result.errorMessage || result.responseText || '-'}
                          </p>
                        </div>
                      ))
                    )}
                  </div>
                ) : null}
              </article>
            ))}
          </div>
        )}
      </div>
    </Modal>
  );
}
