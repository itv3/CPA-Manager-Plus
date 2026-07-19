import { useCallback, useEffect, useMemo, useRef, useState, type ChangeEvent } from 'react';
import { useNavigate } from 'react-router-dom';
import { useTranslation } from 'react-i18next';
import { Button } from '@/components/ui/Button';
import { DropdownMenu } from '@/components/ui/DropdownMenu';
import { Select } from '@/components/ui/Select';
import { ToggleSwitch } from '@/components/ui/ToggleSwitch';
import {
  IconArrowUpFromLine,
  IconChartLine,
  IconChevronDown,
  IconCrosshair,
  IconDownload,
  IconExternalLink,
  IconInfo,
  IconKey,
  IconMoreVertical,
  IconPencil,
  IconPlus,
  IconRefreshCw,
  IconSearch,
  IconSettings,
  IconTimer,
  IconTrash2,
} from '@/components/ui/icons';
import { usePanelFeatureAvailability } from '@/hooks/usePanelFeatureAvailability';
import { useAuthStore, useNotificationStore } from '@/stores';
import { authFilesApi } from '@/services/api/authFiles';
import {
  proAccountsApi,
  type ProAccount,
  type ProAccountBatchAction,
  type ProAccountBindingReviewItem,
  type ProAccountCapabilitiesResponse,
  type ProAccountUsageResponse,
} from '@/services/api/proAccounts';
import { AccountBatchModal } from './AccountBatchModal';
import { AccountBindingReviewModal } from './AccountBindingReviewModal';
import { AccountEditModal } from './AccountEditModal';
import { AccountStatsModal } from './AccountStatsModal';
import { AccountReauthorizeModal } from './AccountReauthorizeModal';
import { AccountScheduledTestsModal } from './AccountScheduledTestsModal';
import { AccountTestModal } from './AccountTestModal';
import { AccountWizardModal } from './AccountWizardModal';
import { OpenAIQuotaResetControls } from './OpenAIQuotaResetControls';
import { createAccountLoadSequence, loadAllAccountPages } from './loadAllAccountPages';
import {
  accountReconcileContextKey,
  reconcileAccountsThenLoad,
  shouldReconcileAccountContext,
} from './accountRefresh';
import {
  accountSourceLabel,
  createRequestIdentity,
  usageRequestOptions,
  type UsageRefreshTrigger,
} from './accountFormUtils';
import { advancedAccountPath } from './accountNavigation';
import {
  accountStatusPresentation,
  accountActionAvailable,
  accountPlanLabel,
  buildAccountUsageWindowRows,
  formatRelativeDate,
  formatResetCountdown,
  isLocalUsageWindowSource,
  resolveUsageUsedPercent,
  shouldShowAccountUsagePlaceholder,
  usagePercentTone,
  usageWindowSourceTitle,
  usageWindowTone,
  usesSharedProviderSwitch,
} from './accountTablePresentation';
import { mergeUsageCacheEntry, type AccountUsageCacheEntry } from './accountUsageCache';
import { buildAccountAuthFileExportPlan } from './accountAuthFileExport';
import { prepareAuthFilesForUpload } from '@/features/authFiles/authFileUpload';
import { MAX_AUTH_FILE_SIZE } from '@/utils/constants';
import { downloadBlob } from '@/utils/download';
import { formatFileSize } from '@/utils/format';
import styles from './AccountsPage.module.scss';
import iconAntigravity from '@/assets/icons/antigravity.svg';
import iconClaude from '@/assets/icons/claude.svg';
import iconGemini from '@/assets/icons/gemini.svg';
import iconGrok from '@/assets/icons/grok.svg';
import iconOpenAI from '@/assets/icons/openai-light.svg';

const AUTO_REFRESH_INTERVAL_MS = 60_000;
const USAGE_CACHE_TTL_MS = 5 * 60_000;
const usageCache = new Map<string, AccountUsageCacheEntry>();
const AUTH_FILE_UPLOAD_FAILURE_STATUSES = new Set(['error', 'failed', 'failure', 'partial']);

const hasAuthFileUploadFailureStatus = (status: string): boolean =>
  AUTH_FILE_UPLOAD_FAILURE_STATUSES.has(status.trim().toLowerCase());

const PLATFORM_FILTER_OPTIONS = [
  { value: '', label: '全部平台' },
  { value: 'openai', label: 'OpenAI' },
  { value: 'anthropic', label: 'Anthropic' },
  { value: 'gemini', label: 'Gemini' },
  { value: 'antigravity', label: 'Antigravity' },
  { value: 'xai', label: 'Grok / xAI' },
];

const AUTH_TYPE_FILTER_OPTIONS = [
  { value: '', label: '全部认证方式' },
  { value: 'oauth', label: 'OAuth' },
  { value: 'api', label: 'API' },
  { value: 'vertex', label: 'Vertex' },
];

const ENABLED_FILTER_OPTIONS = [
  { value: '', label: '全部启用状态' },
  { value: 'true', label: '已启用' },
  { value: 'false', label: '已停用' },
];

const HEALTH_FILTER_OPTIONS = [
  { value: '', label: '全部健康状态' },
  { value: 'healthy', label: '健康' },
  { value: 'error', label: '错误' },
  { value: 'reauth_required', label: '需要重新授权' },
  { value: 'unknown', label: '未知' },
];

const formatDate = (value?: number) => {
  if (!value) return '-';
  const date = new Date(value);
  return Number.isNaN(date.getTime()) ? '-' : date.toLocaleString();
};

const compactNumber = (value: number) =>
  new Intl.NumberFormat(undefined, { notation: 'compact', maximumFractionDigits: 1 }).format(value);

const usageCacheKey = (managerBase: string, accountID: string) => `${managerBase}:${accountID}`;

const PLATFORM_PRESENTATION: Record<string, { label: string; icon: string }> = {
  openai: { label: 'OpenAI', icon: iconOpenAI },
  anthropic: { label: 'Anthropic', icon: iconClaude },
  gemini: { label: 'Gemini', icon: iconGemini },
  antigravity: { label: 'Antigravity', icon: iconAntigravity },
  xai: { label: 'Grok / xAI', icon: iconGrok },
};

const authTypeLabel = (value: string) => {
  if (value === 'oauth') return 'OAuth';
  if (value === 'api') return 'API';
  if (value === 'vertex') return 'Vertex';
  return value;
};

function PlatformTypeCell({ account }: { account: ProAccount }) {
  const platformKey = account.platform.trim().toLowerCase();
  const platform = PLATFORM_PRESENTATION[platformKey] ?? {
    label: account.platform,
    icon: '',
  };
  const planLabel = accountPlanLabel(account.planType, platformKey);
  return (
    <div className={styles.platformTypeCell}>
      <div className={styles.platformTypeBadge} data-platform={platformKey}>
        <span className={styles.platformSegment}>
          {platform.icon ? <img src={platform.icon} alt="" aria-hidden="true" /> : null}
          {platform.label}
        </span>
        <span className={styles.authTypeSegment}>
          <IconKey size={12} />
          {authTypeLabel(account.authType)}
        </span>
      </div>
      {planLabel ? (
        <div className={styles.platformMetaRow}>
          <span
            className={styles.planTypeBadge}
            data-plan={account.planType?.trim().toLowerCase()}
            data-platform={platformKey}
          >
            {planLabel}
          </span>
        </div>
      ) : null}
      <span className={styles.sourceTypeLabel}>
        {accountSourceLabel(account.binding?.sourceType || account.sourceType)}
      </span>
    </div>
  );
}

function UsageCell({
  account,
  managerBase,
  managementKey,
  passiveRefreshToken,
  nowMs,
  usageCacheRevision,
  onPlanTypeDiscovered,
}: {
  account: ProAccount;
  managerBase: string;
  managementKey: string;
  passiveRefreshToken: number;
  nowMs: number;
  usageCacheRevision: number;
  onPlanTypeDiscovered: (accountId: string, planType: string) => void;
}) {
  const rootRef = useRef<HTMLDivElement | null>(null);
  const inFlightRef = useRef(false);
  const attemptedRef = useRef(false);
  const lastRefreshTokenRef = useRef(passiveRefreshToken);
  const cached = usageCache.get(usageCacheKey(managerBase, account.id));
  const [usage, setUsage] = useState<ProAccountUsageResponse | null>(
    cached && Date.now() - cached.updatedAtMs < USAGE_CACHE_TTL_MS ? cached.value : null
  );
  const [loading, setLoading] = useState(false);
  const [visible, setVisible] = useState(false);
  const [error, setError] = useState('');
  const resetEligible = account.platform === 'openai' && account.authType === 'oauth';
  const usageWindows = usage ? buildAccountUsageWindowRows(account, usage) : [];

  const load = useCallback(
    async (trigger: UsageRefreshTrigger = 'initial', bypassCache = false) => {
      if (!managerBase || !managementKey || inFlightRef.current) return;
      const requestOptions = usageRequestOptions(trigger);
      const key = usageCacheKey(managerBase, account.id);
      const cachedValue = usageCache.get(key);
      if (
        requestOptions.source === 'passive' &&
        !bypassCache &&
        cachedValue &&
        Date.now() - cachedValue.updatedAtMs < USAGE_CACHE_TTL_MS
      ) {
        setUsage(cachedValue.value);
        return;
      }
      inFlightRef.current = true;
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
        const merged = mergeUsageCacheEntry(
          usageCache.get(key),
          value,
          requestOptions.source,
          Date.now(),
          USAGE_CACHE_TTL_MS
        );
        usageCache.set(key, merged);
        setUsage(merged.value);
        if (merged.value.planType?.trim()) {
          onPlanTypeDiscovered(account.id, merged.value.planType.trim());
        }
      } catch (loadError) {
        setError(loadError instanceof Error ? loadError.message : String(loadError));
      } finally {
        inFlightRef.current = false;
        setLoading(false);
      }
    },
    [account.id, managementKey, managerBase, onPlanTypeDiscovered]
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

  useEffect(() => {
    const next = usageCache.get(usageCacheKey(managerBase, account.id));
    if (next && Date.now() - next.updatedAtMs < USAGE_CACHE_TTL_MS) setUsage(next.value);
  }, [account.id, managerBase, usageCacheRevision]);

  return (
    <div className={styles.usageCell} ref={rootRef} aria-busy={loading}>
      {usage && (usage.local.requests > 0 || usage.local.totalTokens > 0) ? (
        <div className={styles.localUsage} aria-label="本地用量统计">
          <span>{compactNumber(usage.local.requests)} req</span>
          <span>{compactNumber(usage.local.totalTokens)}</span>
          <span title="官方价格估算成本">
            {usage.local.costKnown && usage.local.estimatedCost !== undefined
              ? `A $${usage.local.estimatedCost.toFixed(2)}`
              : '成本 -'}
          </span>
        </div>
      ) : null}
      {usageWindows.length ? (
        <div className={styles.usageWindows}>
          {usageWindows.slice(0, 4).map((window, index) => {
            const usedPercent = resolveUsageUsedPercent(window);
            const localPlaceholder =
              window.localPlaceholder || isLocalUsageWindowSource(window.source);
            return (
              <div
                className={styles.usageWindow}
                key={window.id}
                data-tone={usageWindowTone(`${window.id} ${window.label}`, index)}
                data-percent-tone={usagePercentTone(usedPercent)}
                data-source={localPlaceholder ? 'local' : 'official'}
                title={usageWindowSourceTitle(window.source)}
              >
                <span className={styles.usageWindowLabel}>{window.label}</span>
                <div className={styles.progressTrack}>
                  <span style={{ width: `${usedPercent ?? 0}%` }} />
                </div>
                <strong>{usedPercent === undefined ? '-' : `${Math.round(usedPercent)}%`}</strong>
                <small title={window.resetAtMs ? formatDate(window.resetAtMs) : ''}>
                  {localPlaceholder && usedPercent === undefined
                    ? '待采样'
                    : formatResetCountdown(window.resetAtMs, usedPercent, nowMs, false)}
                </small>
              </div>
            );
          })}
          {usageWindows.length > 4 ? (
            <div className={styles.usageMoreWindows}>+{usageWindows.length - 4} 个窗口</div>
          ) : null}
        </div>
      ) : shouldShowAccountUsagePlaceholder(account, loading) ? (
        <div
          className={`${styles.usagePlaceholder} ${
            account.healthStatus === 'reauth_required' ? styles.usageNeedsReauth : ''
          }`}
        >
          {loading
            ? '加载中...'
            : account.healthStatus === 'reauth_required'
              ? '需要重新授权'
              : '暂无官方配额数据'}
        </div>
      ) : null}
      {usage?.errorCode && usage.errorCode !== 'official_usage_unsupported' ? (
        <div className={styles.usageError} title={usage.errorMessage || usage.errorCode}>
          {usage.errorMessage || usage.errorCode}
        </div>
      ) : null}
      {error ? (
        <div className={styles.usageError} title={error}>
          {error}
        </div>
      ) : null}
      {resetEligible ? (
        <OpenAIQuotaResetControls
          account={account}
          managerBase={managerBase}
          managementKey={managementKey}
          usageSource={usage?.source}
          usageLoading={loading}
          onQueryUsage={() => load('manual-active', true)}
          onResetCompleted={() => load('manual-active', true)}
        />
      ) : (
        <div className={styles.usageActions}>
          {usage?.source === 'passive' ? (
            <span className={styles.passiveUsage}>被动采样</span>
          ) : null}
          <button type="button" onClick={() => void load('manual-active', true)} disabled={loading}>
            <IconRefreshCw size={11} />
            查询
          </button>
        </div>
      )}
    </div>
  );
}

export function AccountsPage() {
  const { t } = useTranslation();
  const navigate = useNavigate();
  const managementKey = useAuthStore((state) => state.managementKey);
  const featureAvailability = usePanelFeatureAvailability();
  const { showConfirmation, showNotification } = useNotificationStore();
  const [items, setItems] = useState<ProAccount[]>([]);
  const [capabilities, setCapabilities] = useState<ProAccountCapabilitiesResponse | null>(null);
  const [search, setSearch] = useState('');
  const [platform, setPlatform] = useState('');
  const [authType, setAuthType] = useState('');
  const [enabled, setEnabled] = useState('');
  const [healthStatus, setHealthStatus] = useState('');
  const [autoRefresh, setAutoRefresh] = useState(false);
  const [passiveRefreshToken, setPassiveRefreshToken] = useState(0);
  const [usageClockMs, setUsageClockMs] = useState(() => Date.now());
  const [usageCacheRevision, setUsageCacheRevision] = useState(0);
  const [loading, setLoading] = useState(false);
  const [syncing, setSyncing] = useState(false);
  const [authFileImporting, setAuthFileImporting] = useState(false);
  const [authFileExporting, setAuthFileExporting] = useState(false);
  const [rowActions, setRowActions] = useState<Set<string>>(new Set());
  const [error, setError] = useState('');
  const [wizardOpen, setWizardOpen] = useState(false);
  const [editingAccount, setEditingAccount] = useState<ProAccount | null>(null);
  const [testingAccount, setTestingAccount] = useState<ProAccount | null>(null);
  const [statsAccount, setStatsAccount] = useState<ProAccount | null>(null);
  const [reauthorizingAccount, setReauthorizingAccount] = useState<ProAccount | null>(null);
  const [scheduledTestsAccount, setScheduledTestsAccount] = useState<ProAccount | null>(null);
  const [selectedIDs, setSelectedIDs] = useState<Set<string>>(new Set());
  const [batchAction, setBatchAction] = useState<ProAccountBatchAction | null>(null);
  const [bindingReviews, setBindingReviews] = useState<ProAccountBindingReviewItem[]>([]);
  const [bindingReviewOpen, setBindingReviewOpen] = useState(false);
  const loadSequenceRef = useRef(createAccountLoadSequence());
  const reconcileContextRef = useRef('');
  const reconcileInFlightRef = useRef(false);
  const authFileInputRef = useRef<HTMLInputElement | null>(null);

  const managerBase = featureAvailability.managerServiceBase;

  const setRowActionBusy = (key: string, busy: boolean) => {
    setRowActions((current) => {
      const next = new Set(current);
      if (busy) next.add(key);
      else next.delete(key);
      return next;
    });
  };

  const handlePlanTypeDiscovered = useCallback((accountId: string, planType: string) => {
    setItems((current) =>
      current.map((item) =>
        item.id === accountId && item.planType !== planType ? { ...item, planType } : item
      )
    );
  }, []);

  const loadAccounts = useCallback(
    async (background = false) => {
      if (!managerBase || !managementKey) return;
      const requestID = loadSequenceRef.current.begin();
      if (!background) setLoading(true);
      setError('');
      try {
        const nextItems = await loadAllAccountPages((page) =>
          proAccountsApi.list(managerBase, managementKey, {
            limit: 100,
            cursor: page.cursor,
            search,
            platform,
            authType,
            enabled: enabled === '' ? undefined : enabled === 'true',
            healthStatus,
          })
        );
        if (!loadSequenceRef.current.isLatest(requestID)) return;
        setItems(nextItems);
        const availableIDs = new Set(nextItems.map((item) => item.id));
        setSelectedIDs((current) => new Set([...current].filter((id) => availableIDs.has(id))));
        if (background) setPassiveRefreshToken((value) => value + 1);
      } catch (loadError) {
        if (!loadSequenceRef.current.isLatest(requestID)) return;
        const message = loadError instanceof Error ? loadError.message : String(loadError);
        if (!background) setError(message);
      } finally {
        if (!background && loadSequenceRef.current.isLatest(requestID)) setLoading(false);
      }
    },
    [authType, enabled, healthStatus, managementKey, managerBase, platform, search]
  );

  const loadBindingReviews = useCallback(async () => {
    if (!managerBase || !managementKey) return;
    try {
      const result = await proAccountsApi.bindingReviews(managerBase, managementKey);
      setBindingReviews(result.items);
    } catch {
      setBindingReviews([]);
    }
  }, [managementKey, managerBase]);

  const reconcileAndLoadAccounts = useCallback(
    async (background = false, reportSyncError = false) => {
      if (!managerBase || !managementKey) return;
      if (reconcileInFlightRef.current) {
        await loadAccounts(background);
        return;
      }
      reconcileInFlightRef.current = true;
      try {
        await reconcileAccountsThenLoad({
          sync: () => proAccountsApi.sync(managerBase, managementKey),
          load: () => loadAccounts(background),
          onSyncError: reportSyncError
            ? (syncError) =>
                showNotification(
                  `认证状态同步失败，已显示现有账号数据：${
                    syncError instanceof Error ? syncError.message : String(syncError)
                  }`,
                  'warning'
                )
            : undefined,
        });
      } finally {
        reconcileInFlightRef.current = false;
      }
    },
    [loadAccounts, managementKey, managerBase, showNotification]
  );

  useEffect(() => {
    if (featureAvailability.checking) return;
    const contextKey = accountReconcileContextKey(managerBase, managementKey);
    const shouldReconcile = shouldReconcileAccountContext(reconcileContextRef.current, contextKey);
    const timer = window.setTimeout(() => {
      if (shouldReconcile) {
        reconcileContextRef.current = contextKey;
        void reconcileAndLoadAccounts();
      } else {
        void loadAccounts();
      }
    }, 250);
    return () => window.clearTimeout(timer);
  }, [
    featureAvailability.checking,
    loadAccounts,
    managementKey,
    managerBase,
    reconcileAndLoadAccounts,
  ]);

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
    if (!featureAvailability.checking) void loadBindingReviews();
  }, [featureAvailability.checking, loadBindingReviews]);

  useEffect(() => {
    if (!autoRefresh) return;
    const timer = window.setInterval(
      () => void reconcileAndLoadAccounts(true),
      AUTO_REFRESH_INTERVAL_MS
    );
    return () => window.clearInterval(timer);
  }, [autoRefresh, reconcileAndLoadAccounts]);

  useEffect(() => {
    const timer = window.setInterval(() => setUsageClockMs(Date.now()), AUTO_REFRESH_INTERVAL_MS);
    return () => window.clearInterval(timer);
  }, []);

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
      await loadBindingReviews();
    } catch (syncError) {
      showNotification(syncError instanceof Error ? syncError.message : String(syncError), 'error');
    } finally {
      setSyncing(false);
    }
  }, [loadAccounts, loadBindingReviews, managementKey, managerBase, showNotification]);

  const handleAuthFileImport = useCallback(
    async (event: ChangeEvent<HTMLInputElement>) => {
      const input = event.currentTarget;
      const selectedFiles = Array.from(input.files ?? []);
      if (selectedFiles.length === 0) return;

      const validFiles: File[] = [];
      const invalidFileNames: string[] = [];
      const oversizedFileNames: string[] = [];
      selectedFiles.forEach((file) => {
        if (!file.name.toLowerCase().endsWith('.json')) {
          invalidFileNames.push(file.name);
          return;
        }
        if (file.size > MAX_AUTH_FILE_SIZE) {
          oversizedFileNames.push(file.name);
          return;
        }
        validFiles.push(file);
      });

      if (invalidFileNames.length > 0) {
        showNotification(`已忽略非 JSON 文件：${invalidFileNames.join('、')}`, 'warning');
      }
      if (oversizedFileNames.length > 0) {
        showNotification(
          `已忽略超过 ${formatFileSize(MAX_AUTH_FILE_SIZE)} 的文件：${oversizedFileNames.join('、')}`,
          'warning'
        );
      }
      if (validFiles.length === 0) {
        input.value = '';
        return;
      }

      setAuthFileImporting(true);
      try {
        const prepared = await prepareAuthFilesForUpload(validFiles);
        const result =
          prepared.files.length > 0
            ? await authFilesApi.uploadFiles(prepared.files)
            : { status: 'error', uploaded: 0, files: [], failed: [] };
        const reportedFailures = [...prepared.failures, ...result.failed];
        const uploadHasFailureStatus = hasAuthFileUploadFailureStatus(result.status);
        const unreportedFailureCount = Math.max(
          0,
          prepared.files.length - result.uploaded - result.failed.length
        );
        const failureCount = reportedFailures.length + unreportedFailureCount;

        if (result.uploaded <= 0) {
          const failedNames = reportedFailures.map((item) => item.name).filter(Boolean);
          showNotification(
            failedNames.length > 0
              ? `认证文件导入失败：${failedNames.join('、')}`
              : '认证文件导入失败',
            'error'
          );
          return;
        }

        try {
          const syncResult = await proAccountsApi.sync(managerBase, managementKey);
          await Promise.all([loadAccounts(), loadBindingReviews()]);
          const convertedHint =
            prepared.convertedSourceCount > 0
              ? `，已转换 ${prepared.convertedSourceCount} 个 Sub2API 导出文件`
              : '';
          const uploadStatusHint = uploadHasFailureStatus
            ? `，CPA 返回异常状态 ${result.status}`
            : '';
          showNotification(
            `导入完成：认证文件 ${result.uploaded} 个，新增账号 ${syncResult.created}，更新 ${syncResult.updated}${convertedHint}${uploadStatusHint}`,
            failureCount > 0 ||
              uploadHasFailureStatus ||
              syncResult.pending > 0 ||
              syncResult.conflicts > 0
              ? 'warning'
              : 'success'
          );
        } catch (syncError) {
          showNotification(
            `已导入 ${result.uploaded} 个认证文件，但统一账号同步失败：${
              syncError instanceof Error ? syncError.message : String(syncError)
            }。请点击“同步存量”重试。`,
            'warning'
          );
        }

        if (failureCount > 0) {
          const failedNames = reportedFailures.map((item) => item.name).filter(Boolean);
          showNotification(
            `另有 ${failureCount} 个文件导入失败${
              failedNames.length > 0 ? `：${failedNames.join('、')}` : ''
            }`,
            'warning'
          );
        }
      } catch (importError) {
        showNotification(
          `认证文件导入失败：${
            importError instanceof Error ? importError.message : String(importError)
          }`,
          'error'
        );
      } finally {
        setAuthFileImporting(false);
        input.value = '';
      }
    },
    [loadAccounts, loadBindingReviews, managementKey, managerBase, showNotification]
  );

  const exportAuthFiles = useCallback(
    async (fileNames: string[], skippedAccountCount: number) => {
      if (authFileExporting) return;
      setAuthFileExporting(true);
      const failedFileNames: string[] = [];
      let successCount = 0;
      try {
        for (const fileName of fileNames) {
          try {
            const blob = await authFilesApi.downloadBlob(fileName);
            downloadBlob({
              filename: fileName,
              blob,
            });
            successCount += 1;
          } catch {
            failedFileNames.push(fileName);
          }
        }

        const skippedHint =
          skippedAccountCount > 0 ? `，跳过 ${skippedAccountCount} 个非认证文件账号` : '';
        if (failedFileNames.length === 0) {
          showNotification(`已开始导出 ${successCount} 个认证文件${skippedHint}`, 'success');
        } else {
          showNotification(
            `认证文件导出完成：成功 ${successCount}，失败 ${failedFileNames.length}${skippedHint}；失败文件：${failedFileNames.join('、')}`,
            'warning'
          );
        }
      } finally {
        setAuthFileExporting(false);
      }
    },
    [authFileExporting, showNotification]
  );

  const requestAuthFileExport = useCallback(() => {
    const plan = buildAccountAuthFileExportPlan(items, selectedIDs);
    if (plan.fileNames.length === 0) {
      showNotification(
        selectedIDs.size > 0
          ? '所选账号没有可导出的 CPA 认证文件'
          : '当前筛选结果没有可导出的 CPA 认证文件',
        'warning'
      );
      return;
    }

    const scope = selectedIDs.size > 0 ? '所选账号' : '当前筛选结果';
    const skippedHint =
      plan.skippedAccountCount > 0
        ? `其中 ${plan.skippedAccountCount} 个非认证文件账号将被跳过。`
        : '';
    const sharedHint =
      plan.partialSharedFileNames.length > 0
        ? `另有 ${plan.partialSharedFileNames.length} 个共享认证文件包含未选中的账号。`
        : '';
    showConfirmation({
      title: '导出 CPA 认证文件',
      message: `将从${scope}导出 ${plan.fileNames.length} 个完整 JSON 认证文件。文件包含未脱敏的 Token 等敏感凭证，请妥善保管。若认证文件由多个账号共享，内容可能包含当前筛选或勾选范围外账号的凭据。${skippedHint}${sharedHint}`,
      confirmText: '确认导出',
      cancelText: '取消',
      variant: 'primary',
      onConfirm: () => exportAuthFiles(plan.fileNames, plan.skippedAccountCount),
    });
  }, [exportAuthFiles, items, selectedIDs, showConfirmation, showNotification]);

  const toggleAccount = async (account: ProAccount, nextEnabled = !account.enabled) => {
    if (nextEnabled === account.enabled) return;
    const key = `${account.id}:toggle`;
    setRowActionBusy(key, true);
    try {
      const identity = createRequestIdentity(
        account.enabled ? 'account-disable' : 'account-enable'
      );
      const result = await proAccountsApi.setEnabled(
        managerBase,
        managementKey,
        account,
        nextEnabled,
        identity.operationId,
        identity.idempotencyKey
      );
      if (usesSharedProviderSwitch(account, items)) {
        try {
          const syncResult = await proAccountsApi.sync(managerBase, managementKey);
          await Promise.all([loadAccounts(), loadBindingReviews()]);
          showNotification(
            `Provider 调度已${nextEnabled ? '启用' : '停止'}，已同步 ${syncResult.updated} 个关联账号`,
            syncResult.conflicts > 0 || syncResult.pending > 0 ? 'warning' : 'success'
          );
        } catch (syncError) {
          setItems((current) =>
            current.map((item) => (item.id === account.id ? result.account : item))
          );
          showNotification(
            `Provider 已切换，但关联账号同步失败：${
              syncError instanceof Error ? syncError.message : String(syncError)
            }。请点击“同步存量”。`,
            'warning'
          );
        }
        return;
      }
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
      setRowActionBusy(key, false);
    }
  };

  const deleteAccount = async (account: ProAccount) => {
    const key = `${account.id}:delete`;
    setRowActionBusy(key, true);
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
      setRowActionBusy(key, false);
    }
  };

  const confirmDeleteAccount = (account: ProAccount) => {
    const name = account.name || account.email || account.id;
    showConfirmation({
      title: '删除账号',
      message: `确认删除账号“${name}”？底层凭证将同时删除，绑定历史会保留。`,
      confirmText: '删除',
      cancelText: '取消',
      variant: 'danger',
      onConfirm: () => deleteAccount(account),
    });
  };

  const refreshAccountToken = async (account: ProAccount) => {
    const key = `${account.id}:refresh-token`;
    setRowActionBusy(key, true);
    try {
      const identity = createRequestIdentity('account-refresh-token');
      await proAccountsApi.refreshToken(
        managerBase,
        managementKey,
        account,
        identity.operationId,
        identity.idempotencyKey
      );
      await reconcileAndLoadAccounts(true, true);
      showNotification('账号令牌已刷新', 'success');
    } catch (refreshError) {
      showNotification(
        refreshError instanceof Error ? refreshError.message : String(refreshError),
        'error'
      );
    } finally {
      setRowActionBusy(key, false);
    }
  };

  const requestToggleAccount = (account: ProAccount, nextEnabled: boolean) => {
    if (!usesSharedProviderSwitch(account, items)) {
      void toggleAccount(account, nextEnabled);
      return;
    }
    showConfirmation({
      title: nextEnabled ? '启用 Provider 调度' : '停止 Provider 调度',
      message: `“${account.name || account.email || account.id}”来自共享 Chat Completions Provider，此开关会同步影响该 Provider 下的其他 Key。`,
      confirmText: nextEnabled ? '确认启用' : '确认停止',
      cancelText: '取消',
      variant: nextEnabled ? 'primary' : 'danger',
      onConfirm: () => toggleAccount(account, nextEnabled),
    });
  };

  const acceptStatsUsage = (
    account: ProAccount,
    nextUsage: ProAccountUsageResponse,
    requestSource: 'passive' | 'active'
  ) => {
    const key = usageCacheKey(managerBase, account.id);
    const merged = mergeUsageCacheEntry(
      usageCache.get(key),
      nextUsage,
      requestSource,
      Date.now(),
      USAGE_CACHE_TTL_MS
    );
    usageCache.set(key, merged);
    setUsageCacheRevision((value) => value + 1);
    if (merged.value.planType?.trim()) {
      handlePlanTypeDiscovered(account.id, merged.value.planType.trim());
    }
    return merged.value;
  };

  const rows = useMemo(() => items, [items]);
  const selectedAccounts = useMemo(
    () => rows.filter((item) => selectedIDs.has(item.id)),
    [rows, selectedIDs]
  );
  const allVisibleSelected = rows.length > 0 && rows.every((item) => selectedIDs.has(item.id));

  const toggleSelected = (accountID: string, checked: boolean) => {
    setSelectedIDs((current) => {
      const next = new Set(current);
      if (checked) next.add(accountID);
      else next.delete(accountID);
      return next;
    });
  };

  const toggleAllVisible = (checked: boolean) => {
    setSelectedIDs((current) => {
      const next = new Set(current);
      rows.forEach((item) => {
        if (checked) next.add(item.id);
        else next.delete(item.id);
      });
      return next;
    });
  };

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
      </header>

      <section className={styles.toolbar} aria-label="账号筛选">
        <div className={styles.filterGroup}>
          <label className={styles.searchField}>
            <IconSearch size={17} />
            <input
              value={search}
              onChange={(event) => setSearch(event.target.value)}
              placeholder={t('accounts.search', { defaultValue: '搜索名称、邮箱或账号 ID' })}
              aria-label={t('accounts.search', { defaultValue: '搜索名称、邮箱或账号 ID' })}
            />
          </label>
          <Select
            value={platform}
            options={PLATFORM_FILTER_OPTIONS}
            onChange={setPlatform}
            className={styles.filterSelect}
            ariaLabel="平台筛选"
          />
          <Select
            value={authType}
            options={AUTH_TYPE_FILTER_OPTIONS}
            onChange={setAuthType}
            className={styles.filterSelect}
            ariaLabel="认证方式筛选"
          />
          <Select
            value={enabled}
            options={ENABLED_FILTER_OPTIONS}
            onChange={setEnabled}
            className={styles.filterSelect}
            ariaLabel="启用状态筛选"
          />
          <Select
            value={healthStatus}
            options={HEALTH_FILTER_OPTIONS}
            onChange={setHealthStatus}
            className={styles.filterSelectWide}
            ariaLabel="健康状态筛选"
          />
        </div>
        <div className={styles.toolbarActions}>
          <DropdownMenu
            ariaLabel="账号管理更多操作"
            disabled={authFileImporting || authFileExporting}
            triggerClassName={`btn btn-secondary ${styles.toolbarMoreTrigger}`}
            menuClassName={styles.toolbarMoreMenu}
            triggerIcon={<IconMoreVertical size={17} />}
            triggerLabel={
              <>
                <span>更多操作</span>
                <IconChevronDown size={14} />
              </>
            }
            items={[
              {
                key: 'import-auth-files',
                label: '导入 CPA 认证文件',
                icon: <IconArrowUpFromLine size={16} />,
                iconTone: 'green',
                onClick: () => authFileInputRef.current?.click(),
              },
              {
                key: 'export-auth-files',
                label:
                  selectedAccounts.length > 0
                    ? '导出选中的 CPA 认证文件'
                    : '导出当前结果的 CPA 认证文件',
                icon: <IconDownload size={16} />,
                iconTone: 'indigo',
                onClick: requestAuthFileExport,
              },
            ]}
          />
          <input
            ref={authFileInputRef}
            type="file"
            accept=".json,application/json"
            multiple
            hidden
            onChange={handleAuthFileImport}
          />
          <Button
            variant="secondary"
            iconOnly
            onClick={() => void reconcileAndLoadAccounts(false, true)}
            loading={loading}
            title="刷新账号列表"
            aria-label="刷新账号列表"
          >
            <IconRefreshCw size={17} />
          </Button>
          <div className={styles.autoRefreshControl}>
            <ToggleSwitch
              checked={autoRefresh}
              onChange={setAutoRefresh}
              label="自动刷新"
              ariaLabel="自动刷新账号与被动用量"
            />
          </div>
          {bindingReviews.length > 0 ? (
            <Button variant="secondary" onClick={() => setBindingReviewOpen(true)}>
              <IconSettings size={16} />
              待确认绑定 {bindingReviews.length}
            </Button>
          ) : null}
          <Button variant="secondary" onClick={syncAccounts} loading={syncing}>
            <IconRefreshCw size={16} />
            {t('accounts.sync', { defaultValue: '同步存量' })}
          </Button>
          <Button variant="primary" onClick={() => setWizardOpen(true)}>
            <IconPlus size={16} />
            添加账号
          </Button>
        </div>
      </section>

      <section className={styles.panel}>
        <div className={styles.bulkToolbar} aria-label="批量账号操作">
          <div className={styles.bulkSummary}>
            <strong>批量编辑账号</strong>
            <span>已选择 {selectedAccounts.length} 个账号</span>
          </div>
        </div>
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
                  <th className={styles.selectionColumn}>
                    <input
                      type="checkbox"
                      checked={allVisibleSelected}
                      onChange={(event) => toggleAllVisible(event.target.checked)}
                      aria-label="选择当前页全部账号"
                    />
                  </th>
                  <th>账号</th>
                  <th>平台 / 类型</th>
                  <th>状态</th>
                  <th>调度</th>
                  <th>允许模型</th>
                  <th>
                    <span className={styles.usageHeader} title="官方滚动配额窗口与当日本地统计">
                      用量窗口 <IconInfo size={13} />
                    </span>
                  </th>
                  <th>最近活动</th>
                  <th>操作</th>
                </tr>
              </thead>
              <tbody>
                {rows.map((item) => {
                  const advancedPath = advancedAccountPath(item);
                  const status = accountStatusPresentation(item);
                  const rowBusy = [...rowActions].some((key) => key.startsWith(`${item.id}:`));
                  const statusToneClass = {
                    success: styles.statusBadgeSuccess,
                    muted: styles.statusBadgeMuted,
                    warning: styles.statusBadgeWarning,
                    danger: styles.statusBadgeDanger,
                  }[status.tone];
                  return (
                    <tr key={item.id}>
                      <td className={styles.selectionColumn}>
                        <input
                          type="checkbox"
                          checked={selectedIDs.has(item.id)}
                          onChange={(event) => toggleSelected(item.id, event.target.checked)}
                          aria-label={`选择账号 ${item.name || item.email || item.id}`}
                        />
                      </td>
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
                        <PlatformTypeCell account={item} />
                      </td>
                      <td>
                        <span
                          className={`${styles.statusBadge} ${statusToneClass}`}
                          title={item.lastError || status.label}
                        >
                          {status.label}
                        </span>
                        {item.lastError ? (
                          <span
                            className={styles.statusErrorInfo}
                            title={item.lastError}
                            aria-label={`状态详情：${item.lastError}`}
                          >
                            <IconInfo size={14} />
                          </span>
                        ) : null}
                      </td>
                      <td className={styles.scheduleColumn}>
                        <ToggleSwitch
                          checked={item.enabled}
                          onChange={(nextEnabled) => requestToggleAccount(item, nextEnabled)}
                          disabled={rowBusy}
                          ariaLabel={`${item.enabled ? '停止' : '启用'}账号调度`}
                        />
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
                          nowMs={usageClockMs}
                          usageCacheRevision={usageCacheRevision}
                          onPlanTypeDiscovered={handlePlanTypeDiscovered}
                        />
                      </td>
                      <td>
                        <div className={styles.activityLine} title={formatDate(item.lastUsedAtMs)}>
                          使用 {formatRelativeDate(item.lastUsedAtMs)}
                        </div>
                        <div
                          className={styles.activityLine}
                          title={formatDate(item.lastTestedAtMs)}
                        >
                          测试 {formatRelativeDate(item.lastTestedAtMs)}
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
                            className={styles.rowActionButton}
                            onClick={() => setEditingAccount(item)}
                            disabled={rowBusy}
                            title="编辑账号"
                            aria-label="编辑账号"
                          >
                            <IconPencil size={15} />
                            <span>编辑</span>
                          </button>
                          <button
                            type="button"
                            className={`${styles.rowActionButton} ${styles.dangerAction}`}
                            onClick={() => confirmDeleteAccount(item)}
                            disabled={rowBusy}
                            title="删除账号"
                            aria-label="删除账号"
                          >
                            <IconTrash2 size={15} />
                            <span>删除</span>
                          </button>
                          <DropdownMenu
                            ariaLabel={`账号 ${item.name || item.email || item.id} 的更多操作`}
                            disabled={rowBusy}
                            triggerClassName={styles.moreActionTrigger}
                            menuClassName={styles.accountActionMenu}
                            triggerIcon={<IconMoreVertical size={15} />}
                            triggerLabel={<span>更多</span>}
                            items={[
                              {
                                key: 'test',
                                label: '测试连接',
                                icon: <IconCrosshair size={15} />,
                                iconTone: 'green',
                                onClick: () => setTestingAccount(item),
                              },
                              {
                                key: 'stats',
                                label: '查看统计',
                                icon: <IconChartLine size={15} />,
                                iconTone: 'indigo',
                                onClick: () => setStatsAccount(item),
                              },
                              ...(accountActionAvailable(item, capabilities, 'scheduledTests')
                                ? [
                                    {
                                      key: 'scheduled-tests',
                                      label: '定时测试',
                                      icon: <IconTimer size={15} />,
                                      iconTone: 'orange' as const,
                                      onClick: () => setScheduledTestsAccount(item),
                                    },
                                  ]
                                : []),
                              ...(accountActionAvailable(item, capabilities, 'reauthorize')
                                ? [
                                    {
                                      key: 'reauthorize',
                                      label: '重新授权',
                                      icon: <IconExternalLink size={15} />,
                                      tone: 'blue' as const,
                                      onClick: () => setReauthorizingAccount(item),
                                    },
                                  ]
                                : []),
                              ...(accountActionAvailable(item, capabilities, 'refreshToken')
                                ? [
                                    {
                                      key: 'refresh-token',
                                      label: '刷新令牌',
                                      icon: <IconRefreshCw size={15} />,
                                      tone: 'purple' as const,
                                      onClick: () => void refreshAccountToken(item),
                                    },
                                  ]
                                : []),
                              {
                                key: 'advanced',
                                label: '高级管理',
                                icon: <IconExternalLink size={15} />,
                                separatorBefore: true,
                                onClick: () => navigate(advancedPath),
                              },
                            ]}
                          />
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
      <AccountStatsModal
        open={statsAccount !== null}
        account={statsAccount}
        managerBase={managerBase}
        managementKey={managementKey}
        onClose={() => setStatsAccount(null)}
        onUsageLoaded={(nextUsage, requestSource) => {
          if (!statsAccount) return undefined;
          return acceptStatsUsage(statsAccount, nextUsage, requestSource);
        }}
      />
      <AccountReauthorizeModal
        open={reauthorizingAccount !== null}
        account={reauthorizingAccount}
        managerBase={managerBase}
        managementKey={managementKey}
        onClose={() => setReauthorizingAccount(null)}
        onCompleted={async (nextAccount) => {
          if (nextAccount) {
            setItems((current) =>
              current.map((item) => (item.id === nextAccount.id ? nextAccount : item))
            );
          }
          setReauthorizingAccount(null);
          await loadAccounts(true);
        }}
      />
      <AccountScheduledTestsModal
        open={scheduledTestsAccount !== null}
        account={scheduledTestsAccount}
        managerBase={managerBase}
        managementKey={managementKey}
        onClose={() => setScheduledTestsAccount(null)}
      />
      <AccountBatchModal
        open={batchAction !== null}
        action={batchAction}
        accounts={selectedAccounts}
        providerAccounts={items}
        managerBase={managerBase}
        managementKey={managementKey}
        onClose={() => {
          setBatchAction(null);
          setSelectedIDs(new Set());
        }}
        onCompleted={async (result) => {
          const sharedProviderBatch =
            (batchAction === 'enable' || batchAction === 'disable') &&
            selectedAccounts.some((account) => usesSharedProviderSwitch(account, items));
          let syncFailed = false;
          if (sharedProviderBatch) {
            try {
              await proAccountsApi.sync(managerBase, managementKey);
              await loadBindingReviews();
            } catch (syncError) {
              syncFailed = true;
              showNotification(
                `批量操作已完成，但共享 Provider 关联账号同步失败：${
                  syncError instanceof Error ? syncError.message : String(syncError)
                }`,
                'warning'
              );
            }
          }
          await loadAccounts();
          showNotification(
            `批量操作完成：成功 ${result.succeeded}，失败 ${result.failed}${
              sharedProviderBatch && !syncFailed ? '，共享 Provider 状态已同步' : ''
            }`,
            result.failed > 0 || syncFailed ? 'warning' : 'success'
          );
        }}
      />
      <AccountBindingReviewModal
        open={bindingReviewOpen}
        reviews={bindingReviews}
        managerBase={managerBase}
        managementKey={managementKey}
        onClose={() => setBindingReviewOpen(false)}
        onCompleted={(result) => {
          showNotification(
            `绑定确认完成：成功 ${result.succeeded}，失败 ${result.failed}`,
            result.failed > 0 ? 'warning' : 'success'
          );
          void loadAccounts();
          void loadBindingReviews();
        }}
      />
    </div>
  );
}
