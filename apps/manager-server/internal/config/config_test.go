package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestLoadCreatesDefaultConfig(t *testing.T) {
	clearConfigEnv(t)
	dir := t.TempDir()
	configPath := filepath.Join(dir, "config.json")
	t.Setenv(configEnvKey, configPath)

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if cfg.HTTPAddr != "0.0.0.0:18317" {
		t.Fatalf("HTTPAddr = %q", cfg.HTTPAddr)
	}
	if want := filepath.Join(dir, "data", "usage.sqlite"); cfg.DBPath != want {
		t.Fatalf("DBPath = %q, want %q", cfg.DBPath, want)
	}
	if !cfg.DashboardHourlyRollupEnabled {
		t.Fatal("DashboardHourlyRollupEnabled = false by default")
	}
	if !cfg.BackupEnabled || cfg.BackupInterval != 24*time.Hour || cfg.BackupRetention != 14 {
		t.Fatalf("默认备份配置=%#v", cfg)
	}
	if want := filepath.Join(dir, "data", "backups"); cfg.BackupDir != want {
		t.Fatalf("BackupDir=%q want=%q", cfg.BackupDir, want)
	}

	data, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatalf("read generated config: %v", err)
	}
	if !strings.Contains(string(data), `"dataDir": "./data"`) {
		t.Fatalf("generated config does not contain relative dataDir: %s", data)
	}
}

func TestLoadWithoutCreatingDefaultDoesNotCreateConfig(t *testing.T) {
	clearConfigEnv(t)
	dir := t.TempDir()
	configPath := filepath.Join(dir, "config.json")
	t.Setenv(configEnvKey, configPath)

	cfg, err := LoadWithoutCreatingDefault()
	if err != nil {
		t.Fatalf("LoadWithoutCreatingDefault() error = %v", err)
	}
	if want := filepath.Join(dir, "data", "usage.sqlite"); cfg.DBPath != want {
		t.Fatalf("DBPath = %q, want %q", cfg.DBPath, want)
	}
	if _, err := os.Stat(configPath); !os.IsNotExist(err) {
		t.Fatalf("config file exists or stat failed: %v", err)
	}
}

func TestLoadReadsConfigAndResolvesRelativePaths(t *testing.T) {
	clearConfigEnv(t)
	dir := t.TempDir()
	configPath := filepath.Join(dir, "config.json")
	secretPath := filepath.Join(dir, "secret.txt")
	if err := os.WriteFile(secretPath, []byte("secret-value\n"), 0o600); err != nil {
		t.Fatalf("write secret: %v", err)
	}
	if err := os.WriteFile(configPath, []byte(`{
  "httpAddr": "127.0.0.1:19000",
  "dataDir": "state",
	  "backupEnabled": false,
	  "backupDir": "database-backups",
	  "backupIntervalHours": 12,
	  "backupRetention": 7,
  "cpaUpstreamUrl": "http://cpa.local:8317",
  "managementKeyFile": "secret.txt",
  "collectorMode": "http",
  "queue": "custom-usage",
  "popSide": "left",
  "batchSize": 7,
	  "pollIntervalMs": 250,
	  "queryLimit": 900,
	  "pprofAddr": "127.0.0.1:6060",
	  "panelPath": "panel.html",
  "corsOrigins": ["http://panel.local"],
  "tlsSkipVerify": true,
  "quotaCooldownEnabled": true,
  "accountActionsEnabled": true,
  "accountActionsAutoDisable": true
}`), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}
	t.Setenv(configEnvKey, configPath)

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if cfg.HTTPAddr != "127.0.0.1:19000" {
		t.Fatalf("HTTPAddr = %q", cfg.HTTPAddr)
	}
	if want := filepath.Join(dir, "state", "usage.sqlite"); cfg.DBPath != want {
		t.Fatalf("DBPath = %q, want %q", cfg.DBPath, want)
	}
	if cfg.CPAUpstreamURL != "http://cpa.local:8317" {
		t.Fatalf("CPAUpstreamURL = %q", cfg.CPAUpstreamURL)
	}
	if cfg.ManagementKey != "secret-value" {
		t.Fatalf("ManagementKey = %q", cfg.ManagementKey)
	}
	if cfg.CollectorMode != "http" || cfg.Queue != "custom-usage" || cfg.PopSide != "left" {
		t.Fatalf("collector config = %#v", cfg)
	}
	if cfg.BatchSize != 7 || cfg.PollInterval != 250*time.Millisecond || cfg.QueryLimit != 900 {
		t.Fatalf("numeric config = %#v", cfg)
	}
	if cfg.PprofAddr != "127.0.0.1:6060" {
		t.Fatalf("PprofAddr = %q", cfg.PprofAddr)
	}
	if want := filepath.Join(dir, "panel.html"); cfg.PanelPath != want {
		t.Fatalf("PanelPath = %q, want %q", cfg.PanelPath, want)
	}
	if len(cfg.CORSOrigins) != 1 || cfg.CORSOrigins[0] != "http://panel.local" {
		t.Fatalf("CORSOrigins = %#v", cfg.CORSOrigins)
	}
	if !cfg.TLSSkipVerify {
		t.Fatal("TLSSkipVerify = false")
	}
	if !cfg.QuotaCooldownEnabled {
		t.Fatal("QuotaCooldownEnabled = false")
	}
	if !cfg.AccountActionsEnabled {
		t.Fatal("AccountActionsEnabled = false")
	}
	if !cfg.AccountActionsAutoDisable {
		t.Fatal("AccountActionsAutoDisable = false")
	}
	if cfg.BackupEnabled || cfg.BackupInterval != 12*time.Hour || cfg.BackupRetention != 7 {
		t.Fatalf("文件备份配置=%#v", cfg)
	}
	if want := filepath.Join(dir, "database-backups"); cfg.BackupDir != want {
		t.Fatalf("BackupDir=%q want=%q", cfg.BackupDir, want)
	}
}

func TestLoadEnvOverridesConfig(t *testing.T) {
	clearConfigEnv(t)
	dir := t.TempDir()
	configPath := filepath.Join(dir, "config.json")
	if err := os.WriteFile(configPath, []byte(`{
  "httpAddr": "127.0.0.1:19000",
  "dataDir": "state",
  "managementKeyFile": "secret.txt",
  "batchSize": 7
}`), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}
	t.Setenv(configEnvKey, configPath)
	t.Setenv("HTTP_ADDR", "127.0.0.1:19001")
	t.Setenv("USAGE_DATA_DIR", filepath.Join(dir, "env-data"))
	t.Setenv("CPA_MANAGEMENT_KEY", "env-secret")
	t.Setenv("USAGE_BATCH_SIZE", "12")
	t.Setenv("CPA_MANAGER_PPROF_ADDR", "[::1]:6061")
	t.Setenv("USAGE_DASHBOARD_HOURLY_ROLLUP_ENABLED", "false")
	t.Setenv("CPA_MANAGER_BACKUP_ENABLED", "true")
	t.Setenv("CPA_MANAGER_BACKUP_DIR", filepath.Join(dir, "env-backups"))
	t.Setenv("CPA_MANAGER_BACKUP_INTERVAL_HOURS", "6")
	t.Setenv("CPA_MANAGER_BACKUP_RETENTION", "5")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if cfg.HTTPAddr != "127.0.0.1:19001" {
		t.Fatalf("HTTPAddr = %q", cfg.HTTPAddr)
	}
	if want := filepath.Join(dir, "env-data", "usage.sqlite"); cfg.DBPath != want {
		t.Fatalf("DBPath = %q, want %q", cfg.DBPath, want)
	}
	if cfg.ManagementKey != "env-secret" {
		t.Fatalf("ManagementKey = %q", cfg.ManagementKey)
	}
	if cfg.BatchSize != 12 {
		t.Fatalf("BatchSize = %d", cfg.BatchSize)
	}
	if cfg.PprofAddr != "[::1]:6061" {
		t.Fatalf("PprofAddr = %q", cfg.PprofAddr)
	}
	if cfg.DashboardHourlyRollupEnabled {
		t.Fatal("DashboardHourlyRollupEnabled = true, want false")
	}
	if !cfg.BackupEnabled || cfg.BackupDir != filepath.Join(dir, "env-backups") || cfg.BackupInterval != 6*time.Hour || cfg.BackupRetention != 5 {
		t.Fatalf("环境变量备份配置=%#v", cfg)
	}
}

func TestNormalizeCollectorMode(t *testing.T) {
	cases := []struct {
		input string
		want  string
	}{
		{"", "auto"},
		{"AUTO", "auto"},
		{"http", "http"},
		{"HTTP", "http"},
		{"resp", "resp"},
		{"subscribe", "subscribe"},
		{" Subscribe ", "subscribe"},
		{"unknown", "auto"},
	}
	for _, tc := range cases {
		if got := normalizeCollectorMode(tc.input); got != tc.want {
			t.Errorf("normalizeCollectorMode(%q) = %q, want %q", tc.input, got, tc.want)
		}
	}
}

func clearConfigEnv(t *testing.T) {
	t.Helper()
	for _, key := range []string{
		configEnvKey,
		"HTTP_ADDR",
		"USAGE_DATA_DIR",
		"USAGE_DB_PATH",
		"CPA_UPSTREAM_URL",
		"CPA_MANAGEMENT_KEY",
		"CPA_MANAGEMENT_KEY_FILE",
		"USAGE_COLLECTOR_MODE",
		"USAGE_RESP_QUEUE",
		"USAGE_RESP_POP_SIDE",
		"USAGE_BATCH_SIZE",
		"USAGE_POLL_INTERVAL_MS",
		"USAGE_QUERY_LIMIT",
		"CPA_MANAGER_PPROF_ADDR",
		"USAGE_CORS_ORIGINS",
		"USAGE_RESP_TLS_SKIP_VERIFY",
		"USAGE_QUOTA_COOLDOWN_ENABLED",
		"USAGE_ACCOUNT_ACTIONS_ENABLED",
		"USAGE_ACCOUNT_ACTIONS_AUTO_DISABLE",
		"USAGE_DASHBOARD_HOURLY_ROLLUP_ENABLED",
		"CPA_MANAGER_BACKUP_ENABLED",
		"CPA_MANAGER_BACKUP_DIR",
		"CPA_MANAGER_BACKUP_INTERVAL_HOURS",
		"CPA_MANAGER_BACKUP_RETENTION",
		"PANEL_PATH",
	} {
		t.Setenv(key, "")
	}
}
