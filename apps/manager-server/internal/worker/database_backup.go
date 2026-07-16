package worker

import (
	"context"
	"log"
	"strings"
	"time"

	sqliterepo "github.com/seakee/cpa-manager-plus/apps/manager-server/internal/repository/sqlite"
	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/store"
)

type databaseBackuper interface {
	BackupDatabase(ctx context.Context, directory string, kind string, retention int) (store.DatabaseBackupResult, error)
}

type DatabaseBackupWorker struct {
	backup    databaseBackuper
	directory string
	retention int
	interval  time.Duration
}

func NewDatabaseBackupWorker(backup databaseBackuper, directory string, retention int, interval time.Duration) *DatabaseBackupWorker {
	return &DatabaseBackupWorker{
		backup:    backup,
		directory: strings.TrimSpace(directory),
		retention: retention,
		interval:  interval,
	}
}

func (w *DatabaseBackupWorker) Start(ctx context.Context) {
	if w == nil || w.backup == nil || w.directory == "" || w.interval <= 0 || w.retention <= 0 {
		return
	}
	go func() {
		w.runOnce(ctx)
		ticker := time.NewTicker(w.interval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				w.runOnce(ctx)
			}
		}
	}()
}

func (w *DatabaseBackupWorker) runOnce(ctx context.Context) {
	result, err := w.backup.BackupDatabase(ctx, w.directory, sqliterepo.BackupKindPeriodic, w.retention)
	if err != nil {
		if ctx.Err() == nil {
			log.Printf("SQLite 定期备份失败: %v", err)
		}
		return
	}
	log.Printf("SQLite 定期备份完成: %s", result.DatabasePath)
}
