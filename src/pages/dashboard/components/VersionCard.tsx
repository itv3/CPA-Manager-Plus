import { useEffect, useMemo, useState } from 'react';
import { useTranslation } from 'react-i18next';
import type { TFunction } from 'i18next';
import { versionApi } from '@/services/api';
import type { ConnectionStatus } from '@/types';
import { compareVersions, type VersionComparison } from '@/utils/version';
import styles from './VersionCard.module.scss';

interface VersionCardProps {
  appVersion: string;
  apiVersion: string;
  apiBase: string;
  serverBuildDate?: string;
  connectionStatus: ConnectionStatus;
  refreshSignal?: number;
}

interface LatestVersions {
  latestApp: string;
  latestApi: string;
}

const readManagerLatestTag = (data: Record<string, unknown> | undefined | null): string => {
  if (!data) return '';
  const raw = data.tag_name ?? data.name ?? data.latest_version ?? data.latest;
  return typeof raw === 'string' ? raw : raw == null ? '' : String(raw);
};

const readApiLatestVersion = (data: Record<string, unknown> | undefined | null): string => {
  if (!data) return '';
  const raw = data['latest-version'] ?? data.latest_version ?? data.latest;
  return typeof raw === 'string' ? raw : raw == null ? '' : String(raw);
};

const renderBadge = (
  comparison: VersionComparison,
  latest: string,
  t: TFunction
): { label: string; className: string } | null => {
  if (comparison === null) return null;
  if (comparison > 0) {
    const display = latest.trim().replace(/^[vV]+/, '');
    return {
      label: t('dashboard.version_update_available', { version: `v${display}` }),
      className: styles.badgeUpdate,
    };
  }
  if (comparison === 0) {
    return { label: t('dashboard.version_is_latest'), className: styles.badgeLatest };
  }
  return null;
};

export function VersionCard({
  appVersion,
  apiVersion,
  apiBase,
  serverBuildDate,
  connectionStatus,
  refreshSignal,
}: VersionCardProps) {
  const { t, i18n } = useTranslation();
  const [latest, setLatest] = useState<LatestVersions>({ latestApp: '', latestApi: '' });

  useEffect(() => {
    let cancelled = false;

    const tasks: Array<Promise<Partial<LatestVersions>>> = [
      versionApi
        .checkManagerLatest()
        .then((data) => ({ latestApp: readManagerLatestTag(data) }))
        .catch(() => ({})),
    ];

    if (connectionStatus === 'connected') {
      tasks.push(
        versionApi
          .checkLatest()
          .then((data) => ({ latestApi: readApiLatestVersion(data) }))
          .catch(() => ({}))
      );
    }

    Promise.all(tasks).then((results) => {
      if (cancelled) return;
      const merged = results.reduce<LatestVersions>(
        (acc, partial) => ({
          latestApp: partial.latestApp ?? acc.latestApp,
          latestApi: partial.latestApi ?? acc.latestApi,
        }),
        { latestApp: '', latestApi: '' }
      );
      setLatest(merged);
    });

    return () => {
      cancelled = true;
    };
  }, [connectionStatus, refreshSignal]);

  const appBadge = useMemo(
    () => renderBadge(compareVersions(latest.latestApp, appVersion), latest.latestApp, t),
    [appVersion, latest.latestApp, t]
  );
  const apiBadge = useMemo(
    () => renderBadge(compareVersions(latest.latestApi, apiVersion), latest.latestApi, t),
    [apiVersion, latest.latestApi, t]
  );

  const buildTimeDisplay = serverBuildDate
    ? new Date(serverBuildDate).toLocaleString(i18n.language)
    : t('dashboard.version_unknown');

  const dotClass =
    connectionStatus === 'connected'
      ? styles.connected
      : connectionStatus === 'connecting'
        ? styles.connecting
        : styles.disconnected;

  const connectionLabel = t(
    connectionStatus === 'connected'
      ? 'common.connected_status'
      : connectionStatus === 'connecting'
        ? 'common.connecting_status'
        : 'common.disconnected_status'
  );

  return (
    <section className={styles.versionCard}>
      <h2 className={styles.heading}>{t('dashboard.system_overview')}</h2>
      <div className={styles.grid}>
        <div className={styles.tile}>
          <div className={styles.tileHeader}>
            <span className={styles.tileLabel}>{t('dashboard.app_version')}</span>
            {appBadge && <span className={`${styles.badge} ${appBadge.className}`}>{appBadge.label}</span>}
          </div>
          <div className={styles.tileValue}>{appVersion || t('dashboard.version_unknown')}</div>
        </div>

        <div className={styles.tile}>
          <div className={styles.tileHeader}>
            <span className={styles.tileLabel}>{t('dashboard.api_version')}</span>
            {apiBadge && <span className={`${styles.badge} ${apiBadge.className}`}>{apiBadge.label}</span>}
          </div>
          <div className={styles.tileValue}>{apiVersion || t('dashboard.version_unknown')}</div>
        </div>

        <div className={styles.tile}>
          <div className={styles.tileHeader}>
            <span className={styles.tileLabel}>{t('dashboard.build_time')}</span>
          </div>
          <div className={styles.tileValue}>{buildTimeDisplay}</div>
        </div>

        <div className={styles.tile}>
          <div className={styles.tileHeader}>
            <span className={styles.tileLabel}>{t('dashboard.connection')}</span>
          </div>
          <div className={styles.connectionValue}>
            <span className={`${styles.statusDot} ${dotClass}`} />
            <span>{connectionLabel}</span>
          </div>
          {apiBase && <div className={styles.tileSub}>{apiBase}</div>}
        </div>
      </div>
    </section>
  );
}
