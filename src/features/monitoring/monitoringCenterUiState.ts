export type MonitoringDataTab = 'accounts' | 'apiKeys' | 'realtime';

export const MONITORING_DATA_TABS: readonly MonitoringDataTab[] = [
  'accounts',
  'apiKeys',
  'realtime',
] as const;

export const DEFAULT_MONITORING_DATA_TAB: MonitoringDataTab = 'accounts';

export const MONITORING_CENTER_UI_STATE_STORAGE_KEY = 'monitoring.centerUiState';

export type MonitoringCenterUiState = {
  activeDataTab: MonitoringDataTab;
};

const TAB_SET = new Set<MonitoringDataTab>(MONITORING_DATA_TABS);

export const normalizeMonitoringDataTab = (value: unknown): MonitoringDataTab =>
  typeof value === 'string' && TAB_SET.has(value as MonitoringDataTab)
    ? (value as MonitoringDataTab)
    : DEFAULT_MONITORING_DATA_TAB;

export const normalizeMonitoringCenterUiState = (value: unknown): MonitoringCenterUiState => {
  if (!value || typeof value !== 'object' || Array.isArray(value)) {
    return { activeDataTab: DEFAULT_MONITORING_DATA_TAB };
  }

  const record = value as Record<string, unknown>;
  return {
    activeDataTab: normalizeMonitoringDataTab(record.activeDataTab),
  };
};

export const readMonitoringCenterUiState = (): MonitoringCenterUiState => {
  if (typeof window === 'undefined' || typeof window.localStorage === 'undefined') {
    return { activeDataTab: DEFAULT_MONITORING_DATA_TAB };
  }

  try {
    const raw = window.localStorage.getItem(MONITORING_CENTER_UI_STATE_STORAGE_KEY);
    if (raw) {
      return normalizeMonitoringCenterUiState(JSON.parse(raw));
    }
  } catch {
    // Ignore storage failures and fall back to defaults.
  }

  return { activeDataTab: DEFAULT_MONITORING_DATA_TAB };
};

export const writeMonitoringCenterUiState = (state: MonitoringCenterUiState) => {
  if (typeof window === 'undefined' || typeof window.localStorage === 'undefined') {
    return;
  }

  try {
    window.localStorage.setItem(
      MONITORING_CENTER_UI_STATE_STORAGE_KEY,
      JSON.stringify(normalizeMonitoringCenterUiState(state))
    );
  } catch {
    // Ignore storage failures and keep the runtime state in memory only.
  }
};
