package databasebackup

import (
	"bytes"
	"context"
	"path/filepath"
	"strings"
	"testing"

	sqliterepo "github.com/seakee/cpa-manager-plus/apps/manager-server/internal/repository/sqlite"
)

func TestDatabaseBackupCommandsRequireOfflineConfirmationAndRoundTrip(t *testing.T) {
	directory := t.TempDir()
	dbPath := filepath.Join(directory, "usage.sqlite")
	backupDirectory := filepath.Join(directory, "backups")
	db, err := sqliterepo.Open(dbPath)
	if err != nil {
		t.Fatalf("打开 SQLite：%v", err)
	}
	if _, err := db.Exec(`insert into settings (key, value, updated_at_ms) values ('command-fixture', 'before', 1)`); err != nil {
		t.Fatalf("写入夹具：%v", err)
	}
	if err := db.Close(); err != nil {
		t.Fatalf("关闭 SQLite：%v", err)
	}
	var stdout, stderr bytes.Buffer
	if err := Run(context.Background(), "backup-db", []string{
		"--db-path", dbPath,
		"--backup-dir", backupDirectory,
		"--retention", "2",
	}, &stdout, &stderr); err != nil {
		t.Fatalf("执行备份命令：%v stderr=%s", err, stderr.String())
	}
	lines := strings.Split(stdout.String(), "\n")
	var backupPath string
	for _, line := range lines {
		if strings.HasPrefix(line, "数据库：") {
			backupPath = strings.TrimPrefix(line, "数据库：")
		}
	}
	if backupPath == "" {
		t.Fatalf("备份输出=%q", stdout.String())
	}
	stdout.Reset()
	stderr.Reset()
	if err := Run(context.Background(), "verify-db-backup", []string{"--backup", backupPath}, &stdout, &stderr); err != nil || !strings.Contains(stdout.String(), "校验通过") {
		t.Fatalf("校验命令=stdout:%q stderr:%q err:%v", stdout.String(), stderr.String(), err)
	}
	if err := Run(context.Background(), "restore-db", []string{
		"--backup", backupPath,
		"--db-path", dbPath,
		"--backup-dir", backupDirectory,
	}, &stdout, &stderr); err == nil || !strings.Contains(err.Error(), "--confirm-stopped") {
		t.Fatalf("未确认恢复错误=%v", err)
	}
	stdout.Reset()
	stderr.Reset()
	if err := Run(context.Background(), "restore-db", []string{
		"--backup", backupPath,
		"--db-path", dbPath,
		"--backup-dir", backupDirectory,
		"--confirm-stopped",
	}, &stdout, &stderr); err != nil || !strings.Contains(stdout.String(), "恢复完成") {
		t.Fatalf("恢复命令=stdout:%q stderr:%q err:%v", stdout.String(), stderr.String(), err)
	}
}
