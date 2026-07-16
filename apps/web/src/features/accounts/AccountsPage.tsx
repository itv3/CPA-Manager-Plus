import { useCallback, useEffect, useMemo, useRef, useState } from 'react';
import { Link } from 'react-router-dom';
import { useTranslation } from 'react-i18next';
import { Button } from '@/components/ui/Button';
import { Input } from '@/components/ui/Input';
import { ToggleSwitch } from '@/components/ui/ToggleSwitch';
import {
  IconCheck,
  IconCrosshair,
  IconPencil,
  IconPlus,
  IconRefreshCw,
  IconSearch,
  IconSettings,
  IconTrash2,
  IconX,
} from '@/components/ui/icons';
import { usePanelFeatureAvailability } from '@/hooks/usePanelFeatureAvailability';
import { useAuthStore, useNotificationStore } from '@/stores';
import {
  proAccountsApi,
  type ProAccount,
  type ProAccountCapabilitiesResponse,
  type ProAccountUsageResponse,
} from '@/services/api/proAccounts';
import { AccountEditModal } from './AccountEditModal';
import { AccountTestModal } from './AccountTestModal';
import { AccountWizardModal } from './AccountWizardModal';
import {
  accountDisplayName,
  accountSourceLabel,
  createRequestIdentity,
  usageRequestOptions,
  type UsageRefreshTrigger,
} from './accountFormUtils';
import styles from './AccountsPage.module.scss';

const AUTO_REFRESH_INTERVAL_MS = 60_000;
const USAGE_CACHE_TTL_MS = 5 * 60_000;
const usageCache = new Map<string, { value: ProAccountUsageResponse; updatedAt: number }>();

const formatDate = (value?: number) => {
  if (!value) return '-';
  const date = new Date(value);
  return Number.isNaN(date.getTime()) ? '-' : date.toLocaleString();
};

const labelFor = (value: string) => value.replace(/_/g, ' ');

const compactNumber = (value: number) =>
  new Intl.NumberFormat(undefined, { notation: 'compact', maximumFractionDigits: 1 }).format(value);

const usageCacheKey = (managerBase: string, accountID: string) => `${managerBase}:${accountID}`;

function UsageCell({
  account,
  managerBase,
  managementKey,
  passiveRefreshToken,
}: {
  account: ProAccount;
  managerBase: string;
  managementKey: string;
  passiveRefreshToken: number;
}) {
  const rootRef = useRef<HTMLDivElement | null>(null);
  const attemptedRef = useRef(false);
  const lastRefreshTokenRef = useRef(passiveRefreshToken);
  const cached = usageCache.get(usageCacheKey(managerBase, account.id));
  const [usage, setUsage] = useState<ProAccountUsageResponse | null>(
    cached && Date.now() - cached.updatedAt < USAGE_CACHE_TTL_MS ? cached.value : null
  );
  const [loading, setLoading] = useState(false);
  const [visible, setVisible] = useState(false);
  const [error, setError] = useState('');

  const load = useCallback(
    async (trigger: UsageRefreshTrigger = 'initial', bypassCache = false) => {
      if (!managerBase || !managementKey || loading) return;
      const requestOptions = usageRequestOptions(trigger);
      const key = usageCacheKey(managerBase, account.id);
      const cachedValue = usageCache.get(key);
      if (
        requestOptions.source === 'passive' &&
        !bypassCache &&
        cachedValue &&
        Date.now() - cachedValue.updatedAt < USAGE_CACHE_TTL_MS
      ) {
        setUsage(cachedValue.value);
        return;
      }
      setLoading(true);
      setError('');
      try {
        const value = await proAccountsApi.usage(
          managerBase,
          managementKey,
          account.id,
          requestOptions.source,
          requestOptions.force
        );
        usageCache.set(key, { value, updatedAt: Date.now() });
        setUsage(value);
      } catch (loadError) {
        setError(loadError instanceof Error ? loadError.message : String(loadError));
      } finally {
        setLoading(false);
      }
    },
    [account.id, loading, managementKey, managerBase]
  );

  useEffect(() => {
    const element = rootRef.current;
    if (!element) return;
    if (typeof IntersectionObserver === 'undefined') {
      setVisible(true);
      return;
    }
    const observer = new IntersectionObserver(
      (entries) => {
        if (entries.some((entry) => entry.isIntersecting)) {
          setVisible(true);
          observer.disconnect();
        }
      },
      { rootMargin: '160px' }
    );
    observer.observe(element);
    return () => observer.disconnect();
  }, []);

  useEffect(() => {
    if (visible && !usage && !loading && !attemptedRef.current) {
      attemptedRef.current = true;
      void load('initial');
    }
  }, [load, loading, usage, visible]);

  useEffect(() => {
    if (!visible || passiveRefreshToken === lastRefreshTokenRef.current) return;
    lastRefreshTokenRef.current = passiveRefreshToken;
    void load('automatic', true);
  }, [load, passiveRefreshToken, visible]);

  return (
    <div className={styles.usageCell} ref={rootRef} aria-busy={loading}>
      {usage?.officialWindows.length ? (
        <div className={styles.usageWindows}>
          {usage.officialWindows.map((window) => (
            <div className={styles.usageWindow} key={window.id}>
              <span>{window.label}</span>
              <div className={styles.progressTrack}>
                <span
                  style={{ width: `${Math.min(100, Math.max(0, window.usedPercent || 0))}%` }}
                />
              </div>
              <strong>
                {window.usedPercent === undefined ? '-' : `${Math.round(window.usedPercent)}%`}
              </strong>
              <small title={window.resetAtMs ? formatDate(window.resetAtMs) : ''}>
                {window.resetAtMs ? formatDate(window.resetAtMs) : ''}
              </small>
            </div>
          ))}
        </div>
      ) : (
        <div className={styles.usagePlaceholder}>{loading ? '加载中...' : '暂无官方配额数据'}</div>
      )}
      {usage ? (
        <div className={styles.localUsage}>
          <span>{compactNumber(usage.local.requests)} 次</span>
          <span>{compactNumber(usage.local.totalTokens)} Token</span>
          <span>
            {usage.local.costKnown && usage.local.estimatedCost !== undefined
              ? `$${usage.local.estimatedCost.toFixed(2)}`
              : '成本 -'}
          </span>
        </div>
      ) : null}
      {usage?.errorCode ? (
        <div className={styles.usageError} title={usage.errorMessage || usage.errorCode}>
          {usage.errorMessage || usage.errorCode}
        </div>
      ) : null}
      {error ? (
        <div className={styles.usageError} title={error}>
          {error}
        </div>
      ) : null}
      <div className={styles.usageActions}>
        <button type="button" onClick={() => void load('manual-passive', true)} disabled={loading}>
          刷新
        </button>
        <button type="button" onClick={() => void load('manual-active', true)} disabled={loading}>
          查询
        </button>
      </div>
    </div>
  );
}

export function AccountsPage() {
  const { t } = useTranslation();
  const managementKey = useAuthStore((state) => state.managementKey);
  const featureAvailability = usePanelFeatureAvailability();
  const { showNotification } = useNotificationStore();
  const [items, setItems] = useState<ProAccount[]>([]);
  const [capabilities, setCapabilities] = useState<ProAccountCapabilitiesResponse | null>(null);
  const [search, setSearch] = useState('');
  const [platform, setPlatform] = useState('');
  const [authType, setAuthType] = useState('');
  const [enabled, setEnabled] = useState('');
  const [healthStatus, setHealthStatus] = useState('');
  const [autoRefresh, setAutoRefresh] = useState(false);
  const [passiveRefreshToken, setPassiveRefreshToken] = useState(0);
  const [loading, setLoading] = useState(false);
  const [syncing, setSyncing] = useState(false);
  const [rowAction, setRowAction] = useState('');
  const [error, setError] = useState('');
  const [wizardOpen, setWizardOpen] = useState(false);
  const [editingAccount, setEditingAccount] = useState<ProAccount | null>(null);
  const [testingAccount, setTestingAccount] = useState<ProAccount | null>(null);

  const managerBase = featureAvailability.managerServiceBase;

  const loadAccounts = useCallback(
    async (background = false) => {
      if (!managerBase || !managementKey) return;
      if (!background) setLoading(true);
      setError('');
      try {
        const result = await proAccountsApi.list(managerBase, managementKey, {
          limit: 100,
          search,
          platform,
          authType,
          enabled: enabled === '' ? undefined : enabled === 'true',
          healthStatus,
        });
        setItems(result.items);
        if (background) setPassiveRefreshToken((value) => value + 1);
      } catch (loadError) {
        const message = loadError instanceof Error ? loadError.message : String(loadError);
        if (!background) setError(message);
      } finally {
        if (!background) setLoading(false);
      }
    },
    [authType, enabled, healthStatus, managementKey, managerBase, platform, search]
  );

  useEffect(() => {
    if (featureAvailability.checking) return;
    const timer = window.setTimeout(() => void loadAccounts(), 250);
    return () => window.clearTimeout(timer);
  }, [featureAvailability.checking, loadAccounts]);

  useEffect(() => {
    if (!managerBase || !managementKey || featureAvailability.checking) return;
    let cancelled = false;
    void proAccountsApi
      .capabilities(managerBase, managementKey)
      .then((result) => {
        if (!cancelled) setCapabilities(result);
      })
      .catch(() => {
        if (!cancelled) setCapabilities(null);
      });
    return () => {
      cancelled = true;
    };
  }, [featureAvailability.checking, managementKey, managerBase]);

  useEffect(() => {
    if (!autoRefresh) return;
    const timer = window.setInterval(() => void loadAccounts(true), AUTO_REFRESH_INTERVAL_MS);
    return () => window.clearInterval(timer);
  }, [autoRefresh, loadAccounts]);

  const syncAccounts = useCallback(async () => {
    if (!managerBase || !managementKey) return;
    setSyncing(true);
    try {
      const result = await proAccountsApi.sync(managerBase, managementKey);
      showNotification(
        `同步完成：新增 ${result.created}，更新 ${result.updated}，待确认 ${result.pending}，冲突 ${result.conflicts}`,
        result.conflicts > 0 || result.pending > 0 ? 'warning' : 'success'
      );
      await loadAccounts();
    } catch (syncError) {
      showNotification(syncError instanceof Error ? syncError.message : String(syncError), 'error');
    } finally {
      setSyncing(false);
    }
  }, [loadAccounts, managementKey, managerBase, showNotification]);

  const toggleAccount = async (account: ProAccount) => {
    const key = `${account.id}:toggle`;
    setRowAction(key);
    try {
      const identity = createRequestIdentity(
        account.enabled ? 'account-disable' : 'account-enable'
      );
      const result = await proAccountsApi.setEnabled(
        managerBase,
        managementKey,
        account,
        !account.enabled,
        identity.operationId,
        identity.idempotencyKey
      );
      setItems((current) =>
        current.map((item) => (item.id === account.id ? result.account : item))
      );
      showNotification(result.account.enabled ? '账号已启用' : '账号已停用', 'success');
    } catch (toggleError) {
      showNotification(
        toggleError instanceof Error ? toggleError.message : String(toggleError),
        'error'
      );
      await loadAccounts();
    } finally {
      setRowAction('');
    }
  };

  const deleteAccount = async (account: ProAccount) => {
    const name = account.name || account.email || account.id;
    if (!window.confirm(`确认删除账号“${name}”？底层凭证将同时删除，绑定历史会保留。`)) return;
    const key = `${account.id}:delete`;
    setRowAction(key);
    try {
      const identity = createRequestIdentity('account-delete');
      await proAccountsApi.deleteAccount(
        managerBase,
        managementKey,
        account,
        identity.operationId,
        identity.idempotencyKey
      );
      usageCache.delete(usageCacheKey(managerBase, account.id));
      setItems((current) => current.filter((item) => item.id !== account.id));
      showNotification('账号已删除', 'success');
    } catch (deleteError) {
      showNotification(
        deleteError instanceof Error ? deleteError.message : String(deleteError),
        'error'
      );
      await loadAccounts();
    } finally {
      setRowAction('');
    }
  };

  const rows = useMemo(() => items, [items]);

  const refreshAfterSave = async (message: string, savedDisabled = false) => {
    showNotification(
      savedDisabled ? `${message}，账号保持停用` : message,
      savedDisabled ? 'warning' : 'success'
    );
    await loadAccounts();
  };

  return (
    <div className={styles.page}>
      <header className={styles.header}>
        <h1 className={styles.title}>{t('accounts.title', { defaultValue: '统一账号管理' })}</h1>
        <div className={styles.headerActions}>
          <Button variant="secondary" size="sm" onClick={syncAccounts} loading={syncing}>
            <IconRefreshCw size={15} />
            {t('accounts.sync', { defaultValue: '同步存量' })}
          </Button>
          <Button variant="primary" size="sm" onClick={() => setWizardOpen(true)}>
            <IconPlus size={15} />
            添加账号
          </Button>
        </div>
      </header>

      <section className={styles.toolbar} aria-label="账号筛选">
        <Input
          className={styles.search}
          value={search}
          onChange={(event) => setSearch(event.target.value)}
          placeholder={t('accounts.search', { defaultValue: '搜索名称、邮箱或账号 ID' })}
          rightElement={<IconSearch size={15} />}
          aria-label={t('accounts.search', { defaultValue: '搜索名称、邮箱或账号 ID' })}
        />
        <select
          className={styles.select}
          value={platform}
          onChange={(event) => setPlatform(event.target.value)}
          aria-label="平台筛选"
        >
          <option value="">全部平台</option>
          <option value="openai">OpenAI</option>
          <option value="anthropic">Anthropic</option>
          <option value="gemini">Gemini</option>
          <option value="antigravity">Antigravity</option>
          <option value="xai">Grok / xAI</option>
        </select>
        <select
          className={styles.select}
          value={authType}
          onChange={(event) => setAuthType(event.target.value)}
          aria-label="认证方式筛选"
        >
          <option value="">全部认证方式</option>
          <option value="oauth">OAuth</option>
          <option value="api">API</option>
          <option value="vertex">Vertex</option>
        </select>
        <select
          className={styles.select}
          value={enabled}
          onChange={(event) => setEnabled(event.target.value)}
          aria-label="启用状态筛选"
        >
          <option value="">全部启用状态</option>
          <option value="true">已启用</option>
          <option value="false">已停用</option>
        </select>
        <select
          className={styles.select}
          value={healthStatus}
          onChange={(event) => setHealthStatus(event.target.value)}
          aria-label="健康状态筛选"
        >
          <option value="">全部健康状态</option>
          <option value="healthy">健康</option>
          <option value="error">错误</option>
          <option value="reauth_required">需要重新授权</option>
          <option value="unknown">未知</option>
        </select>
        <ToggleSwitch
          checked={autoRefresh}
          onChange={setAutoRefresh}
          label="自动刷新"
          ariaLabel="自动刷新账号与被动用量"
        />
        <Button
          variant="ghost"
          size="sm"
          iconOnly
          onClick={() => void loadAccounts()}
          loading={loading}
          title="刷新账号列表"
          aria-label="刷新账号列表"
        >
          <IconRefreshCw size={16} />
        </Button>
      </section>

      <section className={styles.panel}>
        {error ? <div className={styles.error}>{error}</div> : null}
        {loading && rows.length === 0 ? <div className={styles.state}>加载中...</div> : null}
        {!loading && !error && rows.length === 0 ? (
          <div className={styles.state}>暂无统一账号</div>
        ) : null}
        {rows.length > 0 ? (
          <div className={styles.tableWrap}>
            <table className={styles.table}>
              <thead>
                <tr>
                  <th>账号</th>
                  <th>平台 / 类型</th>
                  <th>状态</th>
                  <th>允许模型</th>
                  <th>用量窗口</th>
                  <th>最近活动</th>
                  <th>操作</th>
                </tr>
              </thead>
              <tbody>
                {rows.map((item) => {
                  const advancedPath =
                    item.authType === 'oauth' || item.authType === 'vertex'
                      ? '/auth-files'
                      : '/ai-providers';
                  return (
                    <tr key={item.id}>
                      <td>
                        <div className={styles.accountName}>
                          {item.name || item.email || item.id}
                        </div>
                        {item.email && item.email !== item.name ? (
                          <div className={styles.accountMeta}>{item.email}</div>
                        ) : null}
                        <div className={styles.muted} title={item.id}>
                          {item.id}
                        </div>
                      </td>
                      <td>
                        <span className={styles.badge}>
                          {accountDisplayName(item.platform, item.authType)}
                        </span>
                        <div className={styles.accountMeta}>
                          {accountSourceLabel(item.binding?.sourceType || item.sourceType)}
                        </div>
                      </td>
                      <td>
                        <span
                          className={`${styles.badge} ${item.enabled ? styles.badgeHealthy : styles.badgeMuted}`}
                        >
                          {item.enabled ? '已启用' : '已停用'}
                        </span>
                        <div
                          className={`${styles.accountMeta} ${item.healthStatus === 'error' ? styles.textError : ''}`}
                        >
                          {labelFor(item.healthStatus)}
                        </div>
                      </td>
                      <td>
                        {item.allowedModels.length === 0 ? (
                          <span className={styles.muted}>允许全部模型</span>
                        ) : (
                          <div className={styles.modelList} title={item.allowedModels.join(', ')}>
                            {item.allowedModels.slice(0, 4).map((model) => (
                              <span className={styles.modelTag} key={model}>
                                {model}
                              </span>
                            ))}
                            {item.allowedModels.length > 4 ? (
                              <span className={styles.modelTag}>
                                +{item.allowedModels.length - 4}
                              </span>
                            ) : null}
                          </div>
                        )}
                      </td>
                      <td>
                        <UsageCell
                          account={item}
                          managerBase={managerBase}
                          managementKey={managementKey}
                          passiveRefreshToken={passiveRefreshToken}
                        />
                      </td>
                      <td>
                        <div className={styles.activityLine}>
                          使用 {formatDate(item.lastUsedAtMs)}
                        </div>
                        <div className={styles.activityLine}>
                          测试 {formatDate(item.lastTestedAtMs)}
                        </div>
                        {item.lastError ? (
                          <div className={styles.lastError} title={item.lastError}>
                            {item.lastError}
                          </div>
                        ) : null}
                      </td>
                      <td>
                        <div className={styles.rowActions}>
                          <button
                            type="button"
                            onClick={() => setEditingAccount(item)}
                            title="编辑账号"
                            aria-label="编辑账号"
                          >
                            <IconPencil size={15} />
                          </button>
                          <button
                            type="button"
                            onClick={() => setTestingAccount(item)}
                            title="测试账号"
                            aria-label="测试账号"
                          >
                            <IconCrosshair size={15} />
                          </button>
                          <button
                            type="button"
                            onClick={() => void toggleAccount(item)}
                            disabled={rowAction !== ''}
                            title={item.enabled ? '停用账号' : '启用账号'}
                            aria-label={item.enabled ? '停用账号' : '启用账号'}
                          >
                            {item.enabled ? <IconX size={15} /> : <IconCheck size={15} />}
                          </button>
                          <button
                            type="button"
                            className={styles.dangerAction}
                            onClick={() => void deleteAccount(item)}
                            disabled={rowAction !== ''}
                            title="删除账号"
                            aria-label="删除账号"
                          >
                            <IconTrash2 size={15} />
                          </button>
                          <Link
                            to={advancedPath}
                            className={styles.iconLink}
                            title="高级管理"
                            aria-label="高级管理"
                          >
                            <IconSettings size={15} />
                          </Link>
                        </div>
                      </td>
                    </tr>
                  );
                })}
              </tbody>
            </table>
          </div>
        ) : null}
      </section>

      <AccountWizardModal
        open={wizardOpen}
        managerBase={managerBase}
        managementKey={managementKey}
        capabilities={capabilities}
        onClose={() => setWizardOpen(false)}
        onSaved={(result) => void refreshAfterSave('账号已添加', result.savedDisabled)}
      />
      <AccountEditModal
        open={editingAccount !== null}
        account={editingAccount}
        managerBase={managerBase}
        managementKey={managementKey}
        onClose={() => setEditingAccount(null)}
        onSaved={() => void refreshAfterSave('账号已更新')}
      />
      <AccountTestModal
        open={testingAccount !== null}
        account={testingAccount}
        managerBase={managerBase}
        managementKey={managementKey}
        onClose={() => setTestingAccount(null)}
        onTested={() => void loadAccounts(true)}
      />
    </div>
  );
}
