import type { ReactNode } from 'react';
import type { TFunction } from 'i18next';
import { Button } from '@/components/ui/Button';
import { IconFilter } from '@/components/ui/icons';
import {
  PaginationControls,
  RecentPattern,
  StatusBadge,
} from '@/features/monitoring/components/MonitoringShared';
import { MonitoringPanel } from '@/features/monitoring/components/MonitoringPanel';
import { formatPercent } from '@/features/monitoring/components/accountOverviewPresentation';
import { buildRealtimeSourceDisplay } from '@/features/monitoring/realtimeSourceDisplay';
import type { MonitoringEventRow } from '@/features/monitoring/hooks/useMonitoringData';
import { maskSensitiveText } from '@/utils/format';
import { formatCompactNumber, formatDurationMs, formatUsd } from '@/utils/usage';
import styles from '../MonitoringCenterPage.module.scss';

type RealtimeLogRow = MonitoringEventRow & {
  requestCount: number;
  successRate: number;
  streamKey: string;
  recentPattern: boolean[];
};

type PaginationState<T> = {
  currentPage: number;
  totalPages: number;
  pageItems: T[];
  startItem: number;
  endItem: number;
};

type RealtimeEventsPanelProps = {
  embedded?: boolean;
  rows: RealtimeLogRow[];
  pagination: PaginationState<RealtimeLogRow>;
  pageSize: number;
  scopedFailureCount: number;
  failedOnlyActive: boolean;
  eventsHasMore: boolean;
  eventsLoadingMore: boolean;
  overallLoading: boolean;
  hasPrices: boolean;
  locale: string;
  emptyState: ReactNode;
  t: TFunction;
  onToggleFailedOnly: () => void;
  onPageChange: (page: number) => void;
  onPageSizeChange: (pageSize: number) => void;
  onLoadMoreEvents: () => void;
};

export type RealtimeEventsPanelActionsProps = {
  rowCount: number;
  scopedFailureCount: number;
  failedOnlyActive: boolean;
  t: TFunction;
  onToggleFailedOnly: () => void;
};

const REALTIME_PAGE_SIZE_OPTIONS = [10, 50, 100, 150, 300] as const;

const buildRealtimeMetaText = (row: MonitoringEventRow) => {
  const text = `${row.endpointMethod} ${row.endpointPath}`.trim();
  return maskSensitiveText(text || '-');
};

export function RealtimeEventsPanelActions({
  rowCount,
  scopedFailureCount,
  failedOnlyActive,
  t,
  onToggleFailedOnly,
}: RealtimeEventsPanelActionsProps) {
  return (
    <div className={`${styles.inlineMetrics} ${styles.realtimeHeaderActions}`}>
      <span>{`${t('monitoring.log_rows')}: ${rowCount}`}</span>
      <span>{`${t('monitoring.recent_failures')}: ${scopedFailureCount}`}</span>
      <button
        type="button"
        className={[styles.filterToggleChip, failedOnlyActive ? styles.filterToggleChipActive : '']
          .filter(Boolean)
          .join(' ')}
        onClick={onToggleFailedOnly}
      >
        <IconFilter size={14} aria-hidden="true" />
        {t('monitoring.filter_status_failed')}
      </button>
    </div>
  );
}

export function RealtimeEventsPanel({
  embedded = false,
  rows,
  pagination,
  pageSize,
  scopedFailureCount,
  failedOnlyActive,
  eventsHasMore,
  eventsLoadingMore,
  overallLoading,
  hasPrices,
  locale,
  emptyState,
  t,
  onToggleFailedOnly,
  onPageChange,
  onPageSizeChange,
  onLoadMoreEvents,
}: RealtimeEventsPanelProps) {
  const actions = (
    <RealtimeEventsPanelActions
      rowCount={rows.length}
      scopedFailureCount={scopedFailureCount}
      failedOnlyActive={failedOnlyActive}
      t={t}
      onToggleFailedOnly={onToggleFailedOnly}
    />
  );
  const content = (
    <>
      <div className={styles.tableWrapper}>
        <table className={`${styles.table} ${styles.realtimeTable}`}>
          <thead>
            <tr>
              <th>{t('monitoring.column_type')}</th>
              <th>{t('monitoring.column_model')}</th>
              <th>{t('monitoring.recent_status')}</th>
              <th>{t('monitoring.request_status')}</th>
              <th>{t('monitoring.column_success_rate')}</th>
              <th>{t('monitoring.total_calls')}</th>
              <th>{t('monitoring.column_latency')}</th>
              <th>{t('monitoring.column_time')}</th>
              <th>{t('monitoring.this_call_usage')}</th>
              <th>{t('monitoring.this_call_cost')}</th>
            </tr>
          </thead>
          <tbody>
            {pagination.pageItems.map((row) => {
              const sourceDisplay = buildRealtimeSourceDisplay(row, t);
              const showResolvedModel =
                row.resolvedModel &&
                row.resolvedModel.trim() &&
                row.resolvedModel.trim() !== row.model;
              return (
                <tr key={row.id} className={row.failed ? styles.logRowFailed : undefined}>
                  <td>
                    <div className={styles.logTypeCell}>
                      <div className={styles.primaryCell}>
                        <span>{sourceDisplay.primary}</span>
                        {sourceDisplay.meta ? <small>{sourceDisplay.meta}</small> : null}
                      </div>
                    </div>
                  </td>
                  <td>
                    <div className={styles.primaryCell}>
                      <span className={styles.monoCell}>{row.model}</span>
                      {showResolvedModel ? (
                        <small className={styles.monoCell}>
                          {t('monitoring.resolved_model_label', { model: row.resolvedModel })}
                        </small>
                      ) : null}
                      <small className={styles.monoCell}>{buildRealtimeMetaText(row)}</small>
                    </div>
                  </td>
                  <td>
                    <div className={styles.recentStatusCell}>
                      <RecentPattern pattern={row.recentPattern} variant="plain" />
                    </div>
                  </td>
                  <td>
                    <StatusBadge tone={row.failed ? 'bad' : 'good'}>
                      {row.failed ? t('monitoring.result_failed') : t('monitoring.result_success')}
                    </StatusBadge>
                  </td>
                  <td
                    className={
                      row.successRate >= 0.95
                        ? styles.goodText
                        : row.successRate >= 0.85
                          ? styles.warnText
                          : styles.badText
                    }
                  >
                    {formatPercent(row.successRate)}
                  </td>
                  <td>{formatCompactNumber(row.requestCount)}</td>
                  <td>
                    <span
                      className={
                        row.latencyMs !== null && row.latencyMs >= 30000
                          ? styles.badText
                          : row.latencyMs !== null && row.latencyMs >= 15000
                            ? styles.warnText
                            : undefined
                      }
                    >
                      {formatDurationMs(row.latencyMs, { locale })}
                    </span>
                  </td>
                  <td>{new Date(row.timestampMs).toLocaleString(locale)}</td>
                  <td>
                    <div className={styles.primaryCell}>
                      <span>{formatCompactNumber(row.totalTokens)}</span>
                      <small>{`I ${formatCompactNumber(row.inputTokens)} \u00b7 O ${formatCompactNumber(row.outputTokens)} \u00b7 C ${formatCompactNumber(row.cachedTokens)}`}</small>
                    </div>
                  </td>
                  <td>{hasPrices ? formatUsd(row.totalCost) : '--'}</td>
                </tr>
              );
            })}
            {rows.length === 0 ? (
              <tr>
                <td colSpan={10}>{emptyState}</td>
              </tr>
            ) : null}
          </tbody>
        </table>
      </div>
      <PaginationControls
        count={rows.length}
        currentPage={pagination.currentPage}
        totalPages={pagination.totalPages}
        startItem={pagination.startItem}
        endItem={pagination.endItem}
        pageSize={pageSize}
        pageSizeOptions={REALTIME_PAGE_SIZE_OPTIONS}
        onPageChange={onPageChange}
        onPageSizeChange={onPageSizeChange}
        t={t}
      />
      {rows.length > 0 ? (
        <div className={styles.loadMoreEventsBar}>
          {eventsHasMore ? (
            <Button
              variant="secondary"
              size="sm"
              onClick={onLoadMoreEvents}
              disabled={eventsLoadingMore || overallLoading}
            >
              {eventsLoadingMore ? t('common.loading') : t('monitoring.load_more_events')}
            </Button>
          ) : (
            <span>{t('monitoring.no_more_events')}</span>
          )}
        </div>
      ) : null}
    </>
  );

  if (embedded) {
    return content;
  }

  return (
    <MonitoringPanel
      title={t('monitoring.realtime_table_title')}
      subtitle={t('monitoring.realtime_table_desc')}
      className={styles.realtimePanel}
      extra={actions}
    >
      {content}
    </MonitoringPanel>
  );
}
