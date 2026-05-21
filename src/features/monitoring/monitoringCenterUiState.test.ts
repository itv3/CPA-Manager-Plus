import { afterEach, beforeEach, describe, expect, it } from 'vitest';
import {
  DEFAULT_MONITORING_DATA_TAB,
  MONITORING_CENTER_UI_STATE_STORAGE_KEY,
  normalizeMonitoringCenterUiState,
  normalizeMonitoringDataTab,
  readMonitoringCenterUiState,
  writeMonitoringCenterUiState,
} from './monitoringCenterUiState';

type StorageLike = {
  getItem: (key: string) => string | null;
  setItem: (key: string, value: string) => void;
  removeItem: (key: string) => void;
  clear: () => void;
};

const createMemoryStorage = (): StorageLike => {
  const store = new Map<string, string>();
  return {
    getItem: (key) => (store.has(key) ? (store.get(key) as string) : null),
    setItem: (key, value) => {
      store.set(key, value);
    },
    removeItem: (key) => {
      store.delete(key);
    },
    clear: () => {
      store.clear();
    },
  };
};

const originalWindow = (globalThis as { window?: unknown }).window;

describe('monitoringCenterUiState', () => {
  let storage: StorageLike;

  beforeEach(() => {
    storage = createMemoryStorage();
    (globalThis as { window?: unknown }).window = { localStorage: storage };
  });

  afterEach(() => {
    if (originalWindow === undefined) {
      delete (globalThis as { window?: unknown }).window;
    } else {
      (globalThis as { window?: unknown }).window = originalWindow;
    }
  });

  it('falls back to default tab for unknown values', () => {
    expect(normalizeMonitoringDataTab('weird')).toBe(DEFAULT_MONITORING_DATA_TAB);
    expect(normalizeMonitoringDataTab(undefined)).toBe(DEFAULT_MONITORING_DATA_TAB);
    expect(normalizeMonitoringDataTab(42)).toBe(DEFAULT_MONITORING_DATA_TAB);
  });

  it('keeps known tab ids during normalization', () => {
    expect(normalizeMonitoringDataTab('accounts')).toBe('accounts');
    expect(normalizeMonitoringDataTab('apiKeys')).toBe('apiKeys');
    expect(normalizeMonitoringDataTab('realtime')).toBe('realtime');
  });

  it('normalizes ui state from arbitrary input', () => {
    expect(normalizeMonitoringCenterUiState(null)).toEqual({
      activeDataTab: DEFAULT_MONITORING_DATA_TAB,
    });
    expect(normalizeMonitoringCenterUiState({ activeDataTab: 'realtime' })).toEqual({
      activeDataTab: 'realtime',
    });
    expect(normalizeMonitoringCenterUiState({ activeDataTab: 'nope' })).toEqual({
      activeDataTab: DEFAULT_MONITORING_DATA_TAB,
    });
  });

  it('persists and reads ui state via localStorage', () => {
    writeMonitoringCenterUiState({ activeDataTab: 'apiKeys' });
    expect(JSON.parse(storage.getItem(MONITORING_CENTER_UI_STATE_STORAGE_KEY) ?? '{}')).toEqual({
      activeDataTab: 'apiKeys',
    });
    expect(readMonitoringCenterUiState()).toEqual({ activeDataTab: 'apiKeys' });
  });

  it('returns defaults when stored payload is invalid JSON', () => {
    storage.setItem(MONITORING_CENTER_UI_STATE_STORAGE_KEY, '{not json');
    expect(readMonitoringCenterUiState()).toEqual({
      activeDataTab: DEFAULT_MONITORING_DATA_TAB,
    });
  });
});
