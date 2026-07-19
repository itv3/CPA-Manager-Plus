import { useCallback, useEffect, useMemo, useState } from 'react';
import { useTranslation } from 'react-i18next';
import { Button } from '@/components/ui/Button';
import { Select } from '@/components/ui/Select';
import { ToggleSwitch } from '@/components/ui/ToggleSwitch';
import { IconRefreshCw } from '@/components/ui/icons';
import {
  proAccountsApi,
  type ProAccount,
  type ProAccountOfficialClientCompatibility,
} from '@/services/api/proAccounts';
import styles from './OfficialClientCompatibilityPanel.module.scss';

const CLAUDE_PROFILE = 'claude-desktop-2.1.215-v1';
const CODEX_PROFILE = 'codex-desktop-0.145.0-alpha.18-v1';
const PAGE_LIMIT = 100;

type CompatibilityFilter = 'all' | 'enabled' | 'disabled';

type CompatibilityRow = {
  account: ProAccount;
  compatibility: ProAccountOfficialClientCompatibility;
  supported: boolean;
  error?: string;
};

export interface OfficialClientCompatibilityPanelProps {
  managerBase: string;
  managementKey: string;
  available: boolean;
  disabled?: boolean;
}

const isOfficialClientAccount = (account: ProAccount) =>
  account.authType === 'api' &&
  (account.sourceType === 'config_claude_api_key' || account.sourceType === 'config_codex_api_key');

const defaultProfile = (platform: string) =>
  platform.toLowerCase() === 'anthropic' ? CLAUDE_PROFILE : CODEX_PROFILE;

const operationID = () => {
  if (typeof crypto !== 'undefined' && typeof crypto.randomUUID === 'function') {
    return crypto.randomUUID();
  }
  return `${Date.now()}-${Math.random().toString(16).slice(2)}`;
};

export function OfficialClientCompatibilityPanel({
  managerBase,
  managementKey,
  available,
  disabled = false,
}: OfficialClientCompatibilityPanelProps) {
  const { t } = useTranslation();
  const [rows, setRows] = useState<CompatibilityRow[]>([]);
  const [filter, setFilter] = useState<CompatibilityFilter>('all');
  const [loading, setLoading] = useState(false);
  const [mutatingID, setMutatingID] = useState('');
  const [error, setError] = useState('');

  const loadRows = useCallback(async () => {
    if (!available || !managerBase || !managementKey) {
      setRows([]);
      return;
    }
    setLoading(true);
    setError('');
    try {
      const accounts: ProAccount[] = [];
      let cursor = '';
      do {
        const page = await proAccountsApi.list(managerBase, managementKey, {
          authType: 'api',
          cursor: cursor || undefined,
          limit: PAGE_LIMIT,
        });
        accounts.push(...page.items.filter(isOfficialClientAccount));
        cursor = page.nextCursor || '';
      } while (cursor);

      const details = await Promise.all(
        accounts.map(async (account): Promise<CompatibilityRow> => {
          try {
            const response = await proAccountsApi.details(managerBase, managementKey, account.id);
            return {
              account: response.item,
              compatibility: response.editable.officialClientCompatibility ?? {
                enabled: false,
                profile: '',
                tlsProfile: '',
              },
              supported: Boolean(response.editable.officialClientCompatibilitySupported),
            };
          } catch (detailError) {
            return {
              account,
              compatibility: { enabled: false, profile: '', tlsProfile: '' },
              supported: false,
              error: detailError instanceof Error ? detailError.message : String(detailError),
            };
          }
        })
      );
      setRows(
        details.sort(
          (left, right) => left.account.name?.localeCompare(right.account.name ?? '') ?? 0
        )
      );
    } catch (loadError) {
      setError(loadError instanceof Error ? loadError.message : String(loadError));
    } finally {
      setLoading(false);
    }
  }, [available, managementKey, managerBase]);

  useEffect(() => {
    void loadRows();
  }, [loadRows]);

  const visibleRows = useMemo(
    () =>
      rows.filter((row) => {
        if (filter === 'enabled') return row.compatibility.enabled;
        if (filter === 'disabled') return !row.compatibility.enabled;
        return true;
      }),
    [filter, rows]
  );

  const handleToggle = useCallback(
    async (row: CompatibilityRow, enabled: boolean) => {
      const id = row.account.id;
      setMutatingID(id);
      setError('');
      const compatibility: ProAccountOfficialClientCompatibility = {
        ...row.compatibility,
        enabled,
        profile: row.compatibility.profile || defaultProfile(row.account.platform),
      };
      try {
        const operationId = operationID();
        const idempotencyKey = operationID();
        await proAccountsApi.update(managerBase, managementKey, id, {
          operationId,
          idempotencyKey,
          expectedVersion: row.account.version,
          allowedModels: row.account.allowedModels,
          modelMapping: row.account.modelMapping,
          officialClientCompatibility: compatibility,
        });
        const response = await proAccountsApi.details(managerBase, managementKey, id);
        setRows((current) =>
          current.map((item) =>
            item.account.id === id
              ? {
                  account: response.item,
                  compatibility: response.editable.officialClientCompatibility ?? compatibility,
                  supported: Boolean(response.editable.officialClientCompatibilitySupported),
                }
              : item
          )
        );
      } catch (toggleError) {
        setError(toggleError instanceof Error ? toggleError.message : String(toggleError));
      } finally {
        setMutatingID('');
      }
    },
    [managementKey, managerBase]
  );

  const filterOptions = useMemo(
    () => [
      { value: 'all', label: t('config_management.visual.sections.pro.compatibility_filter_all') },
      {
        value: 'enabled',
        label: t('config_management.visual.sections.pro.compatibility_filter_enabled'),
      },
      {
        value: 'disabled',
        label: t('config_management.visual.sections.pro.compatibility_filter_disabled'),
      },
    ],
    [t]
  );

  return (
    <div className={styles.root}>
      <div className={styles.header}>
        <div>
          <h3 className={styles.title}>
            {t('config_management.visual.sections.pro.compatibility_title')}
          </h3>
          <p className={styles.description}>
            {t('config_management.visual.sections.pro.compatibility_description')}
          </p>
        </div>
        <div className={styles.toolbar}>
          <Select
            value={filter}
            options={filterOptions}
            onChange={(value) => setFilter(value as CompatibilityFilter)}
            ariaLabel={t('config_management.visual.sections.pro.compatibility_filter_label')}
            disabled={!available || loading}
            fullWidth={false}
            className={styles.filter}
          />
          <Button
            type="button"
            variant="secondary"
            size="sm"
            iconOnly
            title={t('config_management.visual.sections.pro.compatibility_refresh')}
            aria-label={t('config_management.visual.sections.pro.compatibility_refresh')}
            disabled={!available || loading}
            loading={loading}
            onClick={() => void loadRows()}
          >
            {!loading ? <IconRefreshCw size={16} /> : null}
          </Button>
        </div>
      </div>

      {!available ? (
        <div className={styles.state}>
          {t('config_management.visual.sections.pro.compatibility_unavailable')}
        </div>
      ) : error ? (
        <div className={styles.error}>{error}</div>
      ) : loading && rows.length === 0 ? (
        <div className={styles.state}>
          {t('config_management.visual.sections.pro.compatibility_loading')}
        </div>
      ) : visibleRows.length === 0 ? (
        <div className={styles.state}>
          {t('config_management.visual.sections.pro.compatibility_empty')}
        </div>
      ) : (
        <div className={styles.tableWrap}>
          <table className={styles.table}>
            <thead>
              <tr>
                <th>{t('config_management.visual.sections.pro.compatibility_account')}</th>
                <th>{t('config_management.visual.sections.pro.compatibility_platform')}</th>
                <th>{t('config_management.visual.sections.pro.compatibility_switch')}</th>
                <th>{t('config_management.visual.sections.pro.compatibility_status')}</th>
              </tr>
            </thead>
            <tbody>
              {visibleRows.map((row) => {
                const isAnthropic = row.account.platform.toLowerCase() === 'anthropic';
                const busy = mutatingID === row.account.id;
                return (
                  <tr key={row.account.id}>
                    <td
                      data-label={t('config_management.visual.sections.pro.compatibility_account')}
                    >
                      <div className={styles.accountName}>{row.account.name || row.account.id}</div>
                      <div className={styles.accountMeta}>
                        {row.account.binding?.authIndex || row.account.id}
                      </div>
                    </td>
                    <td
                      data-label={t('config_management.visual.sections.pro.compatibility_platform')}
                    >
                      <span
                        className={`${styles.platformBadge} ${
                          isAnthropic ? styles.anthropic : styles.openai
                        }`}
                      >
                        {isAnthropic ? 'Anthropic' : 'OpenAI'}
                      </span>
                    </td>
                    <td
                      data-label={t('config_management.visual.sections.pro.compatibility_switch')}
                    >
                      <ToggleSwitch
                        checked={row.compatibility.enabled}
                        disabled={disabled || busy || !row.supported || Boolean(row.error)}
                        ariaLabel={t(
                          'config_management.visual.sections.pro.compatibility_switch_aria',
                          { account: row.account.name || row.account.id }
                        )}
                        onChange={(enabled) => void handleToggle(row, enabled)}
                      />
                    </td>
                    <td
                      data-label={t('config_management.visual.sections.pro.compatibility_status')}
                    >
                      {row.error ? (
                        <span className={styles.rowError}>{row.error}</span>
                      ) : row.compatibility.enabled ? (
                        <div className={styles.statusList}>
                          <span className={styles.profileBadge}>
                            {isAnthropic ? 'Claude Code' : 'Codex Desktop'}
                          </span>
                          <span className={styles.profileText}>{row.compatibility.profile}</span>
                        </div>
                      ) : (
                        <span className={styles.inactive}>
                          {t('config_management.visual.sections.pro.compatibility_inactive')}
                        </span>
                      )}
                    </td>
                  </tr>
                );
              })}
            </tbody>
          </table>
        </div>
      )}
    </div>
  );
}
