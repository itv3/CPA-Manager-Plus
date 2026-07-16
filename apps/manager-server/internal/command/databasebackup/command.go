package databasebackup

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"path/filepath"
	"strings"

	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/config"
	sqliterepo "github.com/seakee/cpa-manager-plus/apps/manager-server/internal/repository/sqlite"
)

func Run(ctx context.Context, command string, args []string, stdout io.Writer, stderr io.Writer) error {
	switch strings.TrimSpace(command) {
	case "backup-db":
		return runBackup(ctx, args, stdout, stderr)
	case "verify-db-backup":
		return runVerify(ctx, args, stdout, stderr)
	case "restore-db":
		return runRestore(ctx, args, stdout, stderr)
	default:
		return fmt.Errorf("不支持的数据库维护命令 %q", command)
	}
}

func runBackup(ctx context.Context, args []string, stdout io.Writer, stderr io.Writer) error {
	var dbPath, backupDirectory string
	var retention int
	fs := flag.NewFlagSet("backup-db", flag.ContinueOnError)
	fs.SetOutput(stderr)
	fs.StringVar(&dbPath, "db-path", "", "SQLite 数据库路径；默认读取 Manager 配置")
	fs.StringVar(&backupDirectory, "backup-dir", "", "备份目录；默认读取 Manager 配置")
	fs.IntVar(&retention, "retention", 0, "手工备份保留数量；默认读取 Manager 配置")
	fs.Usage = func() {
		_, _ = fmt.Fprintln(stderr, "用法：cpa-manager-plus backup-db [--db-path PATH] [--backup-dir DIR] [--retention N]")
		fs.PrintDefaults()
	}
	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return nil
		}
		return err
	}
	if fs.NArg() > 0 {
		return fmt.Errorf("存在未识别参数 %q", fs.Arg(0))
	}
	dbPath, backupDirectory, configuredRetention, err := resolvePaths(dbPath, backupDirectory)
	if err != nil {
		return err
	}
	if retention <= 0 {
		retention = configuredRetention
	}
	result, err := sqliterepo.BackupPath(ctx, dbPath, sqliterepo.BackupOptions{
		Directory: backupDirectory,
		Kind:      sqliterepo.BackupKindManual,
		Retention: retention,
	})
	if err != nil {
		return err
	}
	_, _ = fmt.Fprintln(stdout, "SQLite 备份完成。")
	_, _ = fmt.Fprintf(stdout, "数据库：%s\n", result.DatabasePath)
	_, _ = fmt.Fprintf(stdout, "清单：%s\n", result.ManifestPath)
	_, _ = fmt.Fprintf(stdout, "Schema 版本：%d\n", result.Manifest.SchemaVersion)
	_, _ = fmt.Fprintf(stdout, "SHA256：%s\n", result.Manifest.SHA256)
	return nil
}

func runVerify(ctx context.Context, args []string, stdout io.Writer, stderr io.Writer) error {
	var backupPath string
	fs := flag.NewFlagSet("verify-db-backup", flag.ContinueOnError)
	fs.SetOutput(stderr)
	fs.StringVar(&backupPath, "backup", "", "备份数据库或清单路径")
	fs.Usage = func() {
		_, _ = fmt.Fprintln(stderr, "用法：cpa-manager-plus verify-db-backup --backup PATH")
		fs.PrintDefaults()
	}
	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return nil
		}
		return err
	}
	if fs.NArg() > 0 {
		return fmt.Errorf("存在未识别参数 %q", fs.Arg(0))
	}
	if strings.TrimSpace(backupPath) == "" {
		return errors.New("必须通过 --backup 指定备份")
	}
	result, err := sqliterepo.VerifyBackup(ctx, backupPath)
	if err != nil {
		return err
	}
	_, _ = fmt.Fprintln(stdout, "SQLite 备份校验通过。")
	_, _ = fmt.Fprintf(stdout, "数据库：%s\n", result.DatabasePath)
	_, _ = fmt.Fprintf(stdout, "Schema 版本：%d\n", result.Manifest.SchemaVersion)
	_, _ = fmt.Fprintf(stdout, "SHA256：%s\n", result.Manifest.SHA256)
	return nil
}

func runRestore(ctx context.Context, args []string, stdout io.Writer, stderr io.Writer) error {
	var backupPath, dbPath, backupDirectory string
	var rollbackRetention int
	var confirmStopped bool
	fs := flag.NewFlagSet("restore-db", flag.ContinueOnError)
	fs.SetOutput(stderr)
	fs.StringVar(&backupPath, "backup", "", "要恢复的备份数据库或清单路径")
	fs.StringVar(&dbPath, "db-path", "", "恢复目标 SQLite 路径；默认读取 Manager 配置")
	fs.StringVar(&backupDirectory, "backup-dir", "", "恢复前回滚备份目录；默认读取 Manager 配置")
	fs.IntVar(&rollbackRetention, "rollback-retention", 0, "恢复前回滚备份保留数量；默认读取 Manager 配置")
	fs.BoolVar(&confirmStopped, "confirm-stopped", false, "确认 Manager Server 已停止")
	fs.Usage = func() {
		_, _ = fmt.Fprintln(stderr, "用法：cpa-manager-plus restore-db --backup PATH --confirm-stopped [--db-path PATH]")
		fs.PrintDefaults()
	}
	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return nil
		}
		return err
	}
	if fs.NArg() > 0 {
		return fmt.Errorf("存在未识别参数 %q", fs.Arg(0))
	}
	if strings.TrimSpace(backupPath) == "" {
		return errors.New("必须通过 --backup 指定备份")
	}
	if !confirmStopped {
		return errors.New("恢复前必须停止 Manager Server，并显式传入 --confirm-stopped")
	}
	dbPath, backupDirectory, configuredRetention, err := resolvePaths(dbPath, backupDirectory)
	if err != nil {
		return err
	}
	if rollbackRetention <= 0 {
		rollbackRetention = configuredRetention
	}
	result, err := sqliterepo.RestoreBackup(ctx, sqliterepo.RestoreOptions{
		BackupPath:        backupPath,
		TargetPath:        dbPath,
		BackupDirectory:   backupDirectory,
		RollbackRetention: rollbackRetention,
	})
	if err != nil {
		return err
	}
	_, _ = fmt.Fprintln(stdout, "SQLite 恢复完成。")
	_, _ = fmt.Fprintf(stdout, "恢复目标：%s\n", result.RestoredPath)
	_, _ = fmt.Fprintf(stdout, "来源备份：%s\n", result.Backup.DatabasePath)
	if result.Rollback != nil {
		_, _ = fmt.Fprintf(stdout, "恢复前回滚备份：%s\n", result.Rollback.DatabasePath)
	}
	_, _ = fmt.Fprintln(stdout, "请启动 Manager Server，并检查健康状态和统一账号数据。")
	return nil
}

func resolvePaths(dbPath string, backupDirectory string) (string, string, int, error) {
	dbPath = strings.TrimSpace(dbPath)
	backupDirectory = strings.TrimSpace(backupDirectory)
	configuredRetention := 14
	if dbPath == "" || backupDirectory == "" {
		cfg, err := config.LoadWithoutCreatingDefault()
		if err != nil {
			return "", "", 0, fmt.Errorf("读取 Manager 配置：%w", err)
		}
		if dbPath == "" {
			dbPath = strings.TrimSpace(cfg.DBPath)
		}
		if backupDirectory == "" {
			backupDirectory = strings.TrimSpace(cfg.BackupDir)
		}
		configuredRetention = cfg.BackupRetention
	}
	if dbPath == "" {
		return "", "", 0, errors.New("SQLite 数据库路径为空，请传入 --db-path")
	}
	if backupDirectory == "" {
		backupDirectory = filepath.Join(filepath.Dir(dbPath), "backups")
	}
	return dbPath, backupDirectory, configuredRetention, nil
}
