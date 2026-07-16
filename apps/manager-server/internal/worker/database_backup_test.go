package worker

import (
	"context"
	"testing"
	"time"

	sqliterepo "github.com/seakee/cpa-manager-plus/apps/manager-server/internal/repository/sqlite"
	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/store"
)

type databaseBackupStub struct {
	calls     int
	directory string
	kind      string
	retention int
}

func (s *databaseBackupStub) BackupDatabase(_ context.Context, directory string, kind string, retention int) (store.DatabaseBackupResult, error) {
	s.calls++
	s.directory = directory
	s.kind = kind
	s.retention = retention
	return store.DatabaseBackupResult{DatabasePath: "/backup/usage.sqlite"}, nil
}

func TestDatabaseBackupWorkerRunsImmediatelyWithRetention(t *testing.T) {
	stub := &databaseBackupStub{}
	worker := NewDatabaseBackupWorker(stub, "/data/backups", 9, time.Hour)
	worker.runOnce(context.Background())
	if stub.calls != 1 || stub.directory != "/data/backups" || stub.kind != sqliterepo.BackupKindPeriodic || stub.retention != 9 {
		t.Fatalf("备份调用=%#v", stub)
	}
}
