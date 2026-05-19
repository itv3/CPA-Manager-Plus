import type { CSSProperties } from 'react';
import { useTranslation } from 'react-i18next';
import {
  IconChartLine,
  IconDollarSign,
  IconInbox,
  IconTimer,
  IconTrendingUp,
} from '@/components/ui/icons';
import type { DashboardSummaryResponse } from '@/services/api/usageService';
import { formatCompactNumber, formatDurationMs, formatUsd } from '@/utils/usage';
import styles from './UsageMetricsCard.module.scss';

interface UsageMetricsCardProps {
  summary: DashboardSummaryResponse | null;
  topModels: DashboardSummaryResponse['top_models_today'];
  modelCostRank: DashboardSummaryResponse['model_cost_rank'];
  loading: boolean;
  error?: string;
  lastRefreshedAt: Date | null;
}

type BarStyle = CSSProperties & Record<'--share', number>;

const formatMetric = (value: number | undefined, digits = 0) => {
  if (value === undefined || !Number.isFinite(value)) return '-';
  return value.toLocaleString(undefined, {
    maximumFractionDigits: digits,
    minimumFractionDigits: digits,
  });
};

const formatPercent = (value: number | undefined) => {
  if (value === undefined || !Number.isFinite(value)) return '-';
  return `${(value * 100).toFixed(1)}%`;
};

export function UsageMetricsCard({
  summary,
  topModels,
  modelCostRank,
  loading,
  error,
  lastRefreshedAt,
}: UsageMetricsCardProps) {
  const { t, i18n } = useTranslation();
  const today = summary?.today;
  const rolling = summary?.rolling_30m;
  const loadingText = loading ? '...' : '-';
  const metrics = [
    {
      key: 'rpm',
      label: t('dashboard.rpm_30m'),
      value: rolling ? formatMetric(rolling.rpm, 2) : loadingText,
      icon: <IconChartLine size={18} />,
    },
    {
      key: 'tpm',
      label: t('dashboard.tpm_30m'),
      value: rolling ? formatCompactNumber(rolling.tpm) : loadingText,
      icon: <IconTrendingUp size={18} />,
    },
    {
      key: 'requests',
      label: t('dashboard.today_requests'),
      value: today ? formatMetric(today.total_calls) : loadingText,
      icon: <IconInbox size={18} />,
    },
    {
      key: 'cost',
      label: t('dashboard.today_cost'),
      value: today ? formatUsd(today.total_cost) : loadingText,
      icon: <IconDollarSign size={18} />,
    },
    {
      key: 'success',
      label: t('dashboard.success_rate'),
      value: today ? formatPercent(today.success_rate) : loadingText,
      icon: <IconTrendingUp size={18} />,
    },
    {
      key: 'latency',
      label: t('dashboard.avg_latency'),
      value: today
        ? formatDurationMs(today.average_latency_ms, { locale: i18n.language })
        : loadingText,
      icon: <IconTimer size={18} />,
    },
  ];

  return (
    <section className={styles.card}>
      <div className={styles.header}>
        <div>
          <h2>{t('dashboard.usage_metrics_title')}</h2>
          {lastRefreshedAt ? (
            <span>{lastRefreshedAt.toLocaleTimeString(i18n.language)}</span>
          ) : null}
        </div>
      </div>

      <div className={styles.metricGrid}>
        {metrics.map((metric) => (
          <div key={metric.key} className={styles.metric}>
            <div className={styles.metricIcon}>{metric.icon}</div>
            <span className={styles.metricLabel}>{metric.label}</span>
            <strong className={styles.metricValue}>{metric.value}</strong>
          </div>
        ))}
      </div>

      {error ? <div className={styles.error}>{error}</div> : null}

      {modelCostRank?.length ? (
        <div className={styles.modelRank}>
          <div className={styles.rankTitle}>{t('dashboard.model_cost_rank')}</div>
          {modelCostRank.slice(0, 4).map((model) => (
            <div key={model.model} className={styles.modelRankRow}>
              <div className={styles.modelRankMeta}>
                <span title={model.model}>{model.model}</span>
                <strong>{formatUsd(model.cost)}</strong>
              </div>
              <div className={styles.modelRankTrack}>
                <span
                  className={styles.modelRankBar}
                  style={{ '--share': model.cost_share } as BarStyle}
                />
              </div>
              <small>
                {formatCompactNumber(model.calls)} · {formatCompactNumber(model.tokens)}
              </small>
            </div>
          ))}
        </div>
      ) : topModels.length > 0 ? (
        <div className={styles.modelList}>
          {topModels.slice(0, 3).map((model) => (
            <div key={model.model} className={styles.modelRow}>
              <span title={model.model}>{model.model}</span>
              <strong>{formatCompactNumber(model.tokens)}</strong>
            </div>
          ))}
        </div>
      ) : null}
    </section>
  );
}
