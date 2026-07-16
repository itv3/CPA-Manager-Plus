package sqlite

import (
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestBackupRestorePreservesUnifiedAccountState(t *testing.T) {
	ctx := context.Background()
	directory := t.TempDir()
	dbPath := filepath.Join(directory, "usage.sqlite")
	backupDirectory := filepath.Join(directory, "backups")
	db, err := Open(dbPath)
	if err != nil {
		t.Fatalf("打开 SQLite：%v", err)
	}
	insertBackupFixture(t, db)
	backup, err := CreateBackup(ctx, db, BackupOptions{
		Directory: backupDirectory,
		Kind:      BackupKindManual,
		CreatedAt: time.UnixMilli(5_000),
	})
	if err != nil {
		t.Fatalf("创建备份：%v", err)
	}
	if backup.Manifest.SchemaVersion != CurrentSchemaVersion || backup.Manifest.SHA256 == "" {
		t.Fatalf("备份清单=%#v", backup.Manifest)
	}
	if _, err := VerifyBackup(ctx, backup.DatabasePath); err != nil {
		t.Fatalf("校验备份：%v", err)
	}
	for _, statement := range []string{
		`delete from usage_events`,
		`delete from pro_account_drafts`,
		`delete from pro_account_bindings`,
		`delete from pro_accounts`,
	} {
		if _, err := db.Exec(statement); err != nil {
			t.Fatalf("修改待恢复数据库：%v", err)
		}
	}
	if err := db.Close(); err != nil {
		t.Fatalf("关闭待恢复数据库：%v", err)
	}
	restored, err := RestoreBackup(ctx, RestoreOptions{
		BackupPath:        backup.DatabasePath,
		TargetPath:        dbPath,
		BackupDirectory:   backupDirectory,
		RollbackRetention: 3,
	})
	if err != nil {
		t.Fatalf("恢复备份：%v", err)
	}
	if restored.Rollback == nil || restored.Rollback.Manifest.Kind != BackupKindPreRestore {
		t.Fatalf("恢复前回滚备份=%#v", restored.Rollback)
	}
	db, err = Open(dbPath)
	if err != nil {
		t.Fatalf("打开已恢复数据库：%v", err)
	}
	defer db.Close()
	var accountName string
	var accountVersion int64
	if err := db.QueryRow(`select name, version from pro_accounts where id = 'account-stable-uuid'`).Scan(&accountName, &accountVersion); err != nil {
		t.Fatalf("读取统一账号：%v", err)
	}
	if accountName != "恢复账号" || accountVersion != 2 {
		t.Fatalf("统一账号=name:%q version:%d", accountName, accountVersion)
	}
	var bindingCount, currentBindingCount int
	if err := db.QueryRow(`select count(*), sum(is_current) from pro_account_bindings where pro_account_id = 'account-stable-uuid'`).Scan(&bindingCount, &currentBindingCount); err != nil {
		t.Fatalf("读取绑定历史：%v", err)
	}
	if bindingCount != 2 || currentBindingCount != 1 {
		t.Fatalf("绑定历史=count:%d current:%d", bindingCount, currentBindingCount)
	}
	var draftState string
	if err := db.QueryRow(`select state from pro_account_drafts where operation_id = 'operation-draft'`).Scan(&draftState); err != nil {
		t.Fatalf("读取草稿：%v", err)
	}
	if draftState != "models_configured" {
		t.Fatalf("草稿状态=%q", draftState)
	}
	var attributedEvents int
	if err := db.QueryRow(`select count(*) from usage_events e join pro_account_bindings b
		on e.auth_index = b.auth_index and e.timestamp_ms >= b.valid_from_ms
		and (b.valid_to_ms is null or e.timestamp_ms < b.valid_to_ms)
		where b.pro_account_id = 'account-stable-uuid'`).Scan(&attributedEvents); err != nil {
		t.Fatalf("读取事件时间归属：%v", err)
	}
	if attributedEvents != 2 {
		t.Fatalf("事件时间归属数量=%d", attributedEvents)
	}
}

func TestBackupRetentionAndTamperDetection(t *testing.T) {
	ctx := context.Background()
	directory := t.TempDir()
	dbPath := filepath.Join(directory, "usage.sqlite")
	backupDirectory := filepath.Join(directory, "backups")
	db, err := Open(dbPath)
	if err != nil {
		t.Fatalf("打开 SQLite：%v", err)
	}
	defer db.Close()
	manual, err := CreateBackup(ctx, db, BackupOptions{
		Directory: backupDirectory,
		Kind:      BackupKindManual,
		CreatedAt: time.UnixMilli(1_000),
	})
	if err != nil {
		t.Fatalf("创建手工备份：%v", err)
	}
	for index := 0; index < 3; index++ {
		if _, err := CreateBackup(ctx, db, BackupOptions{
			Directory: backupDirectory,
			Kind:      BackupKindPeriodic,
			Retention: 2,
			CreatedAt: time.UnixMilli(int64(2_000 + index)),
		}); err != nil {
			t.Fatalf("创建定期备份 %d：%v", index, err)
		}
	}
	entries, err := os.ReadDir(backupDirectory)
	if err != nil {
		t.Fatalf("读取备份目录：%v", err)
	}
	periodicManifests := 0
	manualManifests := 0
	for _, entry := range entries {
		if !strings.HasSuffix(entry.Name(), ".manifest.json") {
			continue
		}
		raw, err := os.ReadFile(filepath.Join(backupDirectory, entry.Name()))
		if err != nil {
			t.Fatalf("读取清单：%v", err)
		}
		if strings.Contains(string(raw), `"kind": "periodic"`) {
			periodicManifests++
		}
		if strings.Contains(string(raw), `"kind": "manual"`) {
			manualManifests++
		}
	}
	if periodicManifests != 2 || manualManifests != 1 {
		t.Fatalf("保留结果=periodic:%d manual:%d", periodicManifests, manualManifests)
	}
	file, err := os.OpenFile(manual.DatabasePath, os.O_APPEND|os.O_WRONLY, 0)
	if err != nil {
		t.Fatalf("打开待篡改备份：%v", err)
	}
	if _, err := file.WriteString("tampered"); err != nil {
		_ = file.Close()
		t.Fatalf("篡改备份：%v", err)
	}
	_ = file.Close()
	if _, err := VerifyBackup(ctx, manual.DatabasePath); err == nil || !strings.Contains(err.Error(), "size mismatch") {
		t.Fatalf("篡改校验错误=%v", err)
	}
}

func TestCurrentSchemaBackupRequiresUnifiedAccountTables(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "incomplete.sqlite")
	db, err := sql.Open("sqlite", dataSourceName(path))
	if err != nil {
		t.Fatalf("打开不完整 SQLite：%v", err)
	}
	for _, statement := range []string{
		`create table settings (key text primary key, value text not null, updated_at_ms integer not null)`,
		`create table usage_events (id integer primary key)`,
		`pragma user_version = 1`,
	} {
		if _, err := db.Exec(statement); err != nil {
			_ = db.Close()
			t.Fatalf("创建不完整 SQLite：%v", err)
		}
	}
	if err := db.Close(); err != nil {
		t.Fatalf("关闭不完整 SQLite：%v", err)
	}
	if _, err := inspectDatabaseFile(ctx, path, true); err == nil || !strings.Contains(err.Error(), "pro_accounts") {
		t.Fatalf("缺少统一账号表的校验错误=%v", err)
	}
}

func TestSidecarQuarantineFailureRestoresPreviouslyMovedFiles(t *testing.T) {
	directory := t.TempDir()
	targetPath := filepath.Join(directory, "usage.sqlite")
	walPath := targetPath + "-wal"
	if err := os.WriteFile(walPath, []byte("wal"), 0o600); err != nil {
		t.Fatalf("创建 WAL：%v", err)
	}
	if err := os.Mkdir(targetPath+"-shm", 0o700); err != nil {
		t.Fatalf("创建非法 SHM：%v", err)
	}
	if _, err := quarantineDatabaseSidecars(targetPath); err == nil || !strings.Contains(err.Error(), "regular file") {
		t.Fatalf("隔离错误=%v", err)
	}
	if raw, err := os.ReadFile(walPath); err != nil || string(raw) != "wal" {
		t.Fatalf("WAL 未恢复：raw=%q err=%v", raw, err)
	}
	quarantined, err := filepath.Glob(walPath + ".restore-old-*")
	if err != nil || len(quarantined) != 0 {
		t.Fatalf("残留隔离文件=%v err=%v", quarantined, err)
	}
}

func TestLegacyMigrationBackupCanRestoreAndUpgrade(t *testing.T) {
	ctx := context.Background()
	directory := t.TempDir()
	dbPath := filepath.Join(directory, "legacy.sqlite")
	backupDirectory := filepath.Join(directory, "backups")
	db, err := Open(dbPath)
	if err != nil {
		t.Fatalf("打开旧数据库：%v", err)
	}
	for _, statement := range []string{
		`insert into usage_events (event_hash, timestamp_ms, timestamp, model, created_at_ms) values ('legacy-event', 1000, 'legacy', 'gpt-test', 1000)`,
		`pragma user_version = 0`,
	} {
		if _, err := db.Exec(statement); err != nil {
			_ = db.Close()
			t.Fatalf("创建旧数据库：%v", err)
		}
	}
	if err := db.Close(); err != nil {
		t.Fatalf("关闭旧数据库：%v", err)
	}
	backup, created, err := CreateMigrationBackupIfNeeded(ctx, dbPath, BackupOptions{
		Directory: backupDirectory,
		Retention: 3,
		CreatedAt: time.UnixMilli(1_000),
	})
	if err != nil || !created || backup.Manifest.SchemaVersion != 0 {
		t.Fatalf("迁移前备份=created:%v result:%#v err:%v", created, backup, err)
	}
	db, err = Open(dbPath)
	if err != nil {
		t.Fatalf("升级旧数据库：%v", err)
	}
	if err := db.Close(); err != nil {
		t.Fatalf("关闭升级数据库：%v", err)
	}
	if _, created, err := CreateMigrationBackupIfNeeded(ctx, dbPath, BackupOptions{Directory: backupDirectory}); err != nil || created {
		t.Fatalf("当前版本不应重复迁移备份=created:%v err:%v", created, err)
	}
	restoredPath := filepath.Join(directory, "restored.sqlite")
	for _, sidecar := range []string{restoredPath + "-wal", restoredPath + "-shm", restoredPath + "-journal"} {
		if err := os.WriteFile(sidecar, []byte("旧边车"), 0o600); err != nil {
			t.Fatalf("创建旧边车文件：%v", err)
		}
	}
	if _, err := RestoreBackup(ctx, RestoreOptions{BackupPath: backup.DatabasePath, TargetPath: restoredPath}); err != nil {
		t.Fatalf("恢复旧版本备份：%v", err)
	}
	for _, sidecar := range []string{restoredPath + "-wal", restoredPath + "-shm", restoredPath + "-journal"} {
		if _, err := os.Stat(sidecar); !os.IsNotExist(err) {
			t.Fatalf("恢复后旧边车仍存在：%s err=%v", sidecar, err)
		}
	}
	db, err = Open(restoredPath)
	if err != nil {
		t.Fatalf("升级恢复后的旧版本：%v", err)
	}
	defer db.Close()
	version, err := SchemaVersion(db)
	if err != nil || version != CurrentSchemaVersion {
		t.Fatalf("恢复升级版本=%d err=%v", version, err)
	}
	var eventCount int
	if err := db.QueryRow(`select count(*) from usage_events where event_hash = 'legacy-event'`).Scan(&eventCount); err != nil || eventCount != 1 {
		t.Fatalf("旧事件保留=count:%d err:%v", eventCount, err)
	}
}

func insertBackupFixture(t *testing.T, db *sql.DB) {
	t.Helper()
	statements := []string{
		`insert into pro_accounts (
			id, platform, auth_type, source_type, name, enabled, health_status,
			allowed_models_json, model_mapping_json, created_at_ms, updated_at_ms, version
		) values ('account-stable-uuid', 'openai', 'oauth', 'auth_file', '恢复账号', 1, 'healthy',
			'["gpt-test"]', '{}', 1000, 2000, 2)`,
		`insert into pro_account_bindings (
			pro_account_id, auth_index, source_type, source_locator, binding_status, is_current,
			valid_from_ms, valid_to_ms, first_seen_at_ms, last_seen_at_ms, created_at_ms, updated_at_ms
		) values ('account-stable-uuid', 'auth-old', 'auth_file', '/old.json', 'historical', 0,
			1000, 2000, 1000, 1999, 1000, 2000)`,
		`insert into pro_account_bindings (
			pro_account_id, auth_index, source_type, source_locator, binding_status, is_current,
			valid_from_ms, first_seen_at_ms, last_seen_at_ms, created_at_ms, updated_at_ms
		) values ('account-stable-uuid', 'auth-new', 'auth_file', '/new.json', 'current', 1,
			2000, 2000, 3000, 2000, 3000)`,
		`insert into pro_account_drafts (
			operation_id, idempotency_key, operation_type, pro_account_id, state, version,
			retry_count, cleanup_deadline_ms, context_json, created_at_ms, updated_at_ms
		) values ('operation-draft', 'operation-draft-key', 'edit', 'account-stable-uuid',
			'models_configured', 3, 0, 999999, '{}', 1000, 2000)`,
		`insert into usage_events (
			event_hash, timestamp_ms, timestamp, model, auth_index, input_tokens, total_tokens, created_at_ms
		) values ('event-old', 1500, 'old', 'gpt-test', 'auth-old', 10, 10, 1500)`,
		`insert into usage_events (
			event_hash, timestamp_ms, timestamp, model, auth_index, input_tokens, total_tokens, created_at_ms
		) values ('event-new', 2500, 'new', 'gpt-test', 'auth-new', 20, 20, 2500)`,
	}
	for _, statement := range statements {
		if _, err := db.Exec(statement); err != nil {
			t.Fatalf("写入备份夹具：%v", err)
		}
	}
}
