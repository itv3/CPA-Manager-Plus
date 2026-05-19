import type { CSSProperties } from 'react';
import { useTranslation } from 'react-i18next';
import type {
  DashboardHourlyActivityPoint,
  DashboardTokenMixSegment,
  DashboardTrafficPoint,
} from '@/services/api/usageService';
import { formatCompactNumber } from '@/utils/usage';
import styles from './TrafficOverviewCard.module.scss';

interface TrafficOverviewCardProps {
  timeline: DashboardTrafficPoint[];
  hourlyActivity: DashboardHourlyActivityPoint[];
  tokenMix: DashboardTokenMixSegment[];
  loading: boolean;
}

type ChartStyle = CSSProperties & Record<'--calls-share' | '--tokens-share', number>;
type HeatStyle = CSSProperties & Record<'--intensity', number>;
type TokenStyle = CSSProperties & Record<'--share', number>;

const formatHour = (bucketMs: number, locale: string) =>
  new Date(bucketMs).toLocaleTimeString(locale, {
    hour: '2-digit',
    minute: '2-digit',
  });

const tokenLabelKey = (key: string) => {
  switch (key) {
    case 'input':
      return 'dashboard.token_mix_input';
    case 'output':
      return 'dashboard.token_mix_output';
    case 'reasoning':
      return 'dashboard.token_mix_reasoning';
    case 'cached':
      return 'dashboard.token_mix_cached';
    default:
      return key;
  }
};

export function TrafficOverviewCard({
  timeline,
  hourlyActivity,
  tokenMix,
  loading,
}: TrafficOverviewCardProps) {
  const { t, i18n } = useTranslation();
  const hasData = timeline.some((point) => point.calls > 0 || point.tokens > 0);

  return (
    <section className={styles.card}>
      <div className={styles.header}>
        <div>
          <h2>{t('dashboard.traffic_overview_title')}</h2>
          <span>{t('dashboard.traffic_overview_subtitle')}</span>
        </div>
      </div>

      <div className={styles.timelineChart} aria-label={t('dashboard.traffic_overview_title')}>
        {timeline.map((point) => (
          <div
            key={point.bucket_ms}
            className={styles.timelineColumn}
            style={
              {
                '--calls-share': point.calls_share,
                '--tokens-share': point.tokens_share,
              } as ChartStyle
            }
            title={`${formatHour(point.bucket_ms, i18n.language)} · ${t('dashboard.today_requests')}: ${formatCompactNumber(point.calls)} · ${t('monitoring.total_tokens')}: ${formatCompactNumber(point.tokens)}`}
          >
            <span className={styles.callsBar} />
            <span className={styles.tokensBar} />
          </div>
        ))}
        {!hasData ? (
          <div className={styles.emptyOverlay}>
            {loading ? '...' : t('dashboard.no_traffic_data')}
          </div>
        ) : null}
      </div>

      <div className={styles.legend}>
        <span className={styles.callsLegend}>{t('dashboard.traffic_calls')}</span>
        <span className={styles.tokensLegend}>{t('dashboard.traffic_tokens')}</span>
      </div>

      <div className={styles.activityStrip} aria-label={t('dashboard.hourly_activity')}>
        {hourlyActivity.map((point) => (
          <span
            key={`${point.hour_index}-${point.bucket_ms}`}
            className={styles.activityCell}
            style={{ '--intensity': point.intensity } as HeatStyle}
            title={`${formatHour(point.bucket_ms, i18n.language)} · ${formatCompactNumber(point.calls)} calls`}
          />
        ))}
      </div>

      <div className={styles.tokenMix}>
        <div className={styles.sectionTitle}>{t('dashboard.token_mix')}</div>
        <div className={styles.tokenSegments}>
          {tokenMix.map((segment) => (
            <span
              key={segment.key}
              className={`${styles.tokenSegment} ${styles[`token_${segment.key}`] ?? ''}`}
              style={{ '--share': segment.share } as TokenStyle}
              title={`${t(tokenLabelKey(segment.key))}: ${formatCompactNumber(segment.tokens)}`}
            />
          ))}
        </div>
        <div className={styles.tokenLabels}>
          {tokenMix.map((segment) => (
            <span key={segment.key}>
              {t(tokenLabelKey(segment.key))} {formatCompactNumber(segment.tokens)}
            </span>
          ))}
        </div>
      </div>
    </section>
  );
}
