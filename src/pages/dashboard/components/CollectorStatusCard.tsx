import { useCallback, useEffect, useState } from 'react';
import { useTranslation } from 'react-i18next';
import { IconCheck, IconInbox, IconSatellite, IconTimer } from '@/components/ui/icons';
import {
  usageServiceApi,
  type UsageServiceStatus,
} from '@/services/api/usageService';
import styles from './CollectorStatusCard.module.scss';

interface CollectorStatusCardProps {
  enabled: boolean;
  serviceBase: string;
  managementKey?: string;
  refreshSignal?: number;
}

const REFRESH_INTERVAL_MS = 60_000;

const formatCount = (value: number | undefined) =>
  Number.isFinite(value) ? Number(value).toLocaleString() : '-';

const formatTimestamp = (value: number | undefined, locale: string) => {
  if (!value || !Number.isFinite(value)) return '-';
  return new Date(value).toLocaleString(locale);
};

export function CollectorStatusCard({
  enabled,
  serviceBase,
  managementKey,
  refreshSignal,
}: CollectorStatusCardProps) {
  const { t, i18n } = useTranslation();
  const [status, setStatus] = useState<UsageServiceStatus | null>(null);
  const [loading, setLoading] = useState(false);
  const [error, setError] = useState('');

  const refresh = useCallback(async () => {
    if (!enabled || !serviceBase) {
      setStatus(null);
      return;
    }

    setLoading(true);
    setError('');
    try {
      const response = await usageServiceApi.getStatus(serviceBase, managementKey);
      setStatus(response);
    } catch (err) {
      setStatus(null);
      setError(err instanceof Error ? err.message : String(err));
    } finally {
      setLoading(false);
    }
  }, [enabled, managementKey, serviceBase]);

  useEffect(() => {
    void refresh();
  }, [refresh]);

  useEffect(() => {
    if (!refreshSignal) return;
    void refresh();
  }, [refresh, refreshSignal]);

  useEffect(() => {
    if (!enabled) return;
    const timer = window.setInterval(() => {
      void refresh();
    }, REFRESH_INTERVAL_MS);
    return () => window.clearInterval(timer);
  }, [enabled, refresh]);

  const collector = status?.collector;
  const rows = [
    {
      key: 'events',
      label: t('dashboard.collector_events'),
      value: loading && !status ? '...' : formatCount(status?.events),
      icon: <IconInbox size={18} />,
    },
    {
      key: 'deadLetters',
      label: t('dashboard.collector_dead_letters'),
      value: loading && !status ? '...' : formatCount(status?.deadLetters ?? collector?.deadLetters),
      icon: <IconShieldStatus />,
    },
    {
      key: 'mode',
      label: t('dashboard.collector_mode'),
      value: collector?.mode || collector?.collector || '-',
      icon: <IconSatellite size={18} />,
    },
    {
      key: 'queue',
      label: t('dashboard.collector_queue'),
      value: collector?.queue || '-',
      icon: <IconCheck size={18} />,
    },
  ];

  return (
    <section className={styles.card}>
      <div className={styles.header}>
        <h2>{t('dashboard.collector_status_title')}</h2>
        <span className={error ? styles.badgeError : styles.badgeOk}>
          {error ? t('dashboard.collector_unavailable') : collector?.mode || '-'}
        </span>
      </div>

      <div className={styles.statusGrid}>
        {rows.map((row) => (
          <div key={row.key} className={styles.statusTile}>
            <div className={styles.tileIcon}>{row.icon}</div>
            <span>{row.label}</span>
            <strong>{row.value}</strong>
          </div>
        ))}
      </div>

      <div className={styles.detailList}>
        <div>
          <span>{t('dashboard.collector_last_consumed')}</span>
          <strong>{formatTimestamp(collector?.lastConsumedAt, i18n.language)}</strong>
        </div>
        <div>
          <span>{t('dashboard.collector_last_inserted')}</span>
          <strong>{formatTimestamp(collector?.lastInsertedAt, i18n.language)}</strong>
        </div>
        <div>
          <span>{t('dashboard.collector_total_inserted')}</span>
          <strong>{formatCount(collector?.totalInserted)}</strong>
        </div>
        <div>
          <span>{t('dashboard.collector_total_skipped')}</span>
          <strong>{formatCount(collector?.totalSkipped)}</strong>
        </div>
      </div>

      {(collector?.lastError || error) && (
        <div className={styles.errorLine}>
          <span>{t('dashboard.collector_last_error')}</span>
          <strong>{collector?.lastError || error}</strong>
        </div>
      )}
    </section>
  );
}

function IconShieldStatus() {
  return <IconTimer size={18} />;
}
