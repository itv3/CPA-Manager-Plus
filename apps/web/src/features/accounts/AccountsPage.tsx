import { useCallback, useEffect, useMemo, useState } from 'react';
import { useTranslation } from 'react-i18next';
import { Button } from '@/components/ui/Button';
import { Input } from '@/components/ui/Input';
import { IconRefreshCw, IconSearch } from '@/components/ui/icons';
import { usePanelFeatureAvailability } from '@/hooks/usePanelFeatureAvailability';
import { useAuthStore, useNotificationStore } from '@/stores';
import { proAccountsApi, type ProAccount } from '@/services/api/proAccounts';
import styles from './AccountsPage.module.scss';

const formatDate = (value?: number) => {
  if (!value) return '-';
  const date = new Date(value);
  return Number.isNaN(date.getTime()) ? '-' : date.toLocaleString();
};

const labelFor = (value: string) => value.replace(/_/g, ' ');

export function AccountsPage() {
  const { t } = useTranslation();
  const managementKey = useAuthStore((state) => state.managementKey);
  const featureAvailability = usePanelFeatureAvailability();
  const { showNotification } = useNotificationStore();
  const [items, setItems] = useState<ProAccount[]>([]);
  const [search, setSearch] = useState('');
  const [platform, setPlatform] = useState('');
  const [authType, setAuthType] = useState('');
  const [enabled, setEnabled] = useState('');
  const [loading, setLoading] = useState(false);
  const [syncing, setSyncing] = useState(false);
  const [error, setError] = useState('');

  const managerBase = featureAvailability.managerServiceBase;

  const loadAccounts = useCallback(async () => {
    if (!managerBase || !managementKey) return;
    setLoading(true);
    setError('');
    try {
      const result = await proAccountsApi.list(managerBase, managementKey, {
        limit: 100,
        search,
        platform,
        authType,
        enabled: enabled === '' ? undefined : enabled === 'true',
      });
      setItems(result.items);
    } catch (err) {
      const message = err instanceof Error ? err.message : String(err);
      setError(message);
    } finally {
      setLoading(false);
    }
  }, [authType, enabled, managementKey, managerBase, platform, search]);

  useEffect(() => {
    if (!featureAvailability.checking) void loadAccounts();
  }, [featureAvailability.checking, loadAccounts]);

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
    } catch (err) {
      showNotification(err instanceof Error ? err.message : String(err), 'error');
    } finally {
      setSyncing(false);
    }
  }, [loadAccounts, managementKey, managerBase, showNotification]);

  const rows = useMemo(() => items, [items]);

  return (
    <div className={styles.page}>
      <section className={styles.header}>
        <div>
          <h1 className={styles.title}>{t('accounts.title', { defaultValue: '统一账号管理' })}</h1>
          <p className={styles.description}>
            {t('accounts.description', {
              defaultValue: '集中查看 Gateway 认证文件账号。稳定账号 ID 和绑定历史由 Manager 保存，旧管理入口继续保留。',
            })}
          </p>
        </div>
        <div className={styles.actions}>
          <Button variant="primary" size="sm" onClick={syncAccounts} loading={syncing}>
            <IconRefreshCw size={15} />
            {t('accounts.sync', { defaultValue: '同步存量账号' })}
          </Button>
        </div>
      </section>

      <section className={styles.toolbar}>
        <Input
          className={styles.search}
          value={search}
          onChange={(event) => setSearch(event.target.value)}
          placeholder={t('accounts.search', { defaultValue: '搜索名称、邮箱或账号 ID' })}
          rightElement={<IconSearch size={15} />}
          aria-label={t('accounts.search', { defaultValue: '搜索名称、邮箱或账号 ID' })}
        />
        <select className={styles.select} value={platform} onChange={(event) => setPlatform(event.target.value)}>
          <option value="">全部平台</option>
          <option value="openai">OpenAI</option>
          <option value="anthropic">Anthropic</option>
          <option value="gemini">Gemini</option>
          <option value="antigravity">Antigravity</option>
          <option value="xai">Grok / xAI</option>
        </select>
        <select className={styles.select} value={authType} onChange={(event) => setAuthType(event.target.value)}>
          <option value="">全部认证方式</option>
          <option value="oauth">OAuth</option>
          <option value="api">API</option>
          <option value="vertex">Vertex</option>
        </select>
        <select className={styles.select} value={enabled} onChange={(event) => setEnabled(event.target.value)}>
          <option value="">全部状态</option>
          <option value="true">已启用</option>
          <option value="false">已停用</option>
        </select>
        <Button variant="secondary" size="sm" onClick={loadAccounts} loading={loading}>
          {t('common.refresh')}
        </Button>
      </section>

      <section className={styles.panel}>
        {error ? <div className={styles.error}>{error}</div> : null}
        {loading && rows.length === 0 ? <div className={styles.state}>加载中...</div> : null}
        {!loading && !error && rows.length === 0 ? (
          <div className={styles.state}>暂无统一账号。请先点击“同步存量账号”。</div>
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
                  <th>绑定</th>
                  <th>最近更新</th>
                </tr>
              </thead>
              <tbody>
                {rows.map((item) => (
                  <tr key={item.id}>
                    <td>
                      <div className={styles.accountName}>{item.name || item.email || item.id}</div>
                      {item.email && item.email !== item.name ? <div className={styles.accountMeta}>{item.email}</div> : null}
                      <div className={styles.muted}>{item.id}</div>
                    </td>
                    <td>
                      <span className={styles.badge}>{labelFor(item.platform)}</span>{' '}
                      <span className={styles.badge}>{labelFor(item.authType)}</span>
                    </td>
                    <td>
                      <span className={`${styles.badge} ${item.enabled ? styles.badgeHealthy : styles.badgeError}`}>
                        {item.enabled ? '已启用' : '已停用'}
                      </span>
                      <div className={styles.accountMeta}>{labelFor(item.healthStatus)}</div>
                    </td>
                    <td>
                      {item.allowedModels.length === 0 ? (
                        <span className={styles.muted}>允许全部模型</span>
                      ) : (
                        <div className={styles.modelList}>
                          {item.allowedModels.slice(0, 5).map((model) => (
                            <span className={styles.modelTag} key={model}>{model}</span>
                          ))}
                        </div>
                      )}
                    </td>
                    <td>
                      <div>{item.binding?.sourceType || item.sourceType}</div>
                      <div className={styles.accountMeta}>{item.binding?.sourceLocator || '-'}</div>
                      {item.binding?.authIndex ? <div className={styles.muted}>{item.binding.authIndex}</div> : null}
                    </td>
                    <td>{formatDate(item.updatedAtMs)}</td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
        ) : null}
      </section>
    </div>
  );
}
