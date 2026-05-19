import { useCallback, useEffect, useMemo, useState } from 'react';
import { useTranslation } from 'react-i18next';
import {
  IconCheck,
  IconFileText,
  IconSatellite,
  IconShield,
  IconTimer,
} from '@/components/ui/icons';
import { logsApi, type ErrorLogFile } from '@/services/api/logs';
import type {
  DashboardChannelHealth,
  DashboardFailureSource,
  DashboardRecentFailure,
} from '@/services/api/usageService';
import { formatDurationMs } from '@/utils/usage';
import styles from './HealthAlertsCard.module.scss';

interface HealthAlertsCardProps {
  enabled: boolean;
  loading: boolean;
  recentFailures: DashboardRecentFailure[];
  channelHealth: DashboardChannelHealth[];
  failureSources: DashboardFailureSource[];
  refreshSignal?: number;
}

const REFRESH_INTERVAL_MS = 60_000;

const shortHash = (value: string) => {
  const trimmed = value.trim();
  if (!trimmed) return '-';
  return trimmed.length <= 12 ? trimmed : `${trimmed.slice(0, 6)}...${trimmed.slice(-4)}`;
};

export function HealthAlertsCard({
  enabled,
  loading,
  recentFailures,
  channelHealth,
  failureSources,
  refreshSignal,
}: HealthAlertsCardProps) {
  const { t, i18n } = useTranslation();
  const [errorLogs, setErrorLogs] = useState<ErrorLogFile[]>([]);

  const refreshLogs = useCallback(async () => {
    if (!enabled) {
      setErrorLogs([]);
      return;
    }
    try {
      const response = await logsApi.fetchErrorLogs();
      setErrorLogs(Array.isArray(response.files) ? response.files.slice(0, 3) : []);
    } catch {
      setErrorLogs([]);
    }
  }, [enabled]);

  useEffect(() => {
    let cancelled = false;
    queueMicrotask(() => {
      if (cancelled) return;
      void refreshLogs();
    });
    return () => {
      cancelled = true;
    };
  }, [refreshLogs]);

  useEffect(() => {
    if (!refreshSignal) return;
    let cancelled = false;
    queueMicrotask(() => {
      if (cancelled) return;
      void refreshLogs();
    });
    return () => {
      cancelled = true;
    };
  }, [refreshLogs, refreshSignal]);

  useEffect(() => {
    if (!enabled) return;
    const timer = window.setInterval(() => {
      void refreshLogs();
    }, REFRESH_INTERVAL_MS);
    return () => window.clearInterval(timer);
  }, [enabled, refreshLogs]);

  const visibleFailures = useMemo(() => recentFailures.slice(0, 3), [recentFailures]);
  const abnormalChannels = useMemo(
    () => channelHealth.filter((row) => row.tone !== 'good').slice(0, 3),
    [channelHealth]
  );
  const visibleFailureSources = useMemo(
    () => failureSources.filter((row) => row.failures > 0).slice(0, 3),
    [failureSources]
  );
  const formatFailureRate = useMemo(
    () =>
      new Intl.NumberFormat(i18n.language, {
        style: 'percent',
        maximumFractionDigits: 1,
      }).format,
    [i18n.language]
  );
  const hasAlerts =
    visibleFailures.length > 0 ||
    abnormalChannels.length > 0 ||
    visibleFailureSources.length > 0 ||
    errorLogs.length > 0;
  const isLoading = loading;

  return (
    <section className={styles.card}>
      <div className={styles.header}>
        <h2>{t('dashboard.health_alerts_title')}</h2>
        <span className={hasAlerts ? styles.badgeWarn : styles.badgeOk}>
          {hasAlerts ? (
            visibleFailures.length +
            abnormalChannels.length +
            visibleFailureSources.length +
            errorLogs.length
          ) : (
            <IconCheck size={14} />
          )}
        </span>
      </div>

      {visibleFailures.length > 0 ? (
        <div className={styles.section}>
          <div className={styles.sectionTitle}>
            <IconShield size={16} />
            <span>{t('dashboard.health_recent_failures')}</span>
          </div>
          {visibleFailures.map((failure) => (
            <div key={`${failure.timestamp_ms}-${failure.source_hash}`} className={styles.alertRow}>
              <div>
                <strong title={failure.model}>{failure.model || '-'}</strong>
                <span>
                  {new Date(failure.timestamp_ms).toLocaleTimeString(i18n.language)} -{' '}
                  {shortHash(failure.source_hash || failure.api_key_hash)}
                </span>
              </div>
              <em>{formatDurationMs(failure.duration_ms, { locale: i18n.language })}</em>
            </div>
          ))}
        </div>
      ) : null}

      {abnormalChannels.length > 0 ? (
        <div className={styles.section}>
          <div className={styles.sectionTitle}>
            <IconSatellite size={16} />
            <span>{t('dashboard.health_abnormal_channels')}</span>
          </div>
          {abnormalChannels.map((channel) => {
            const channelName =
              channel.auth_index === '-'
                ? t('dashboard.health_unlinked_channel')
                : channel.auth_index;
            return (
              <div key={channel.auth_index} className={styles.alertRow}>
                <div>
                  <strong title={channelName}>{channelName}</strong>
                  <span>
                    {t('dashboard.health_channel_failure_summary', {
                      failures: channel.failures,
                      calls: channel.calls,
                      rate: formatFailureRate(channel.failure_rate),
                    })}
                  </span>
                </div>
                <em>{formatFailureRate(channel.failure_rate)}</em>
              </div>
            );
          })}
        </div>
      ) : null}

      {visibleFailureSources.length > 0 ? (
        <div className={styles.section}>
          <div className={styles.sectionTitle}>
            <IconShield size={16} />
            <span>{t('dashboard.health_failure_sources')}</span>
          </div>
          {visibleFailureSources.map((source) => (
            <div key={`${source.source_hash}-${source.auth_index}`} className={styles.alertRow}>
              <div>
                <strong title={source.source_hash}>{shortHash(source.source_hash)}</strong>
                <span>
                  {t('dashboard.health_channel_failure_summary', {
                    failures: source.failures,
                    calls: source.calls,
                    rate: formatFailureRate(source.failure_rate),
                  })}
                </span>
              </div>
              <em>{formatFailureRate(source.failure_rate)}</em>
            </div>
          ))}
        </div>
      ) : null}

      {errorLogs.length > 0 ? (
        <div className={styles.section}>
          <div className={styles.sectionTitle}>
            <IconFileText size={16} />
            <span>{t('dashboard.health_error_logs')}</span>
          </div>
          {errorLogs.map((file) => (
            <div key={file.name} className={styles.alertRow}>
              <div>
                <strong title={file.name}>{file.name}</strong>
                <span>
                  {file.modified
                    ? new Date(file.modified).toLocaleString(i18n.language)
                    : shortHash(file.name)}
                </span>
              </div>
              <IconTimer size={14} />
            </div>
          ))}
        </div>
      ) : null}

      {!hasAlerts ? (
        <div className={styles.emptyState}>
          <IconCheck size={22} />
          <span>{isLoading ? '...' : t('dashboard.health_no_alerts')}</span>
        </div>
      ) : null}
    </section>
  );
}
