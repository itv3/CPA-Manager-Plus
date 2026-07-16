package worker

import (
	"context"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/model"
	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/repository/proaccountdraft"
	sqliterepo "github.com/seakee/cpa-manager-plus/apps/manager-server/internal/repository/sqlite"
	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/service/proaccountoperation"
)

type recoveryRecorder struct {
	mu    sync.Mutex
	items []model.ProAccountDraft
}

func (r *recoveryRecorder) Recover(_ context.Context, operation model.ProAccountDraft) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.items = append(r.items, operation)
	return nil
}

func TestProAccountRecoveryWorkerMarksExpiredOperationForCompensation(t *testing.T) {
	db, err := sqliterepo.Open(filepath.Join(t.TempDir(), "manager.sqlite"))
	if err != nil {
		t.Fatalf("打开 SQLite：%v", err)
	}
	defer db.Close()
	operations := proaccountoperation.New(proaccountdraft.New(db))
	created, _, err := operations.Start(context.Background(), proaccountoperation.StartInput{
		OperationID: "expired-operation", IdempotencyKey: "expired-key", OperationType: "add",
		CleanupDeadlineMS: time.Now().Add(-time.Minute).UnixMilli(),
	})
	if err != nil {
		t.Fatalf("创建操作：%v", err)
	}
	// Start 会修正已经过期的截止时间，因此直接通过仓库构造到期状态。
	repository := proaccountdraft.New(db)
	if _, err := repository.Update(context.Background(), created.OperationID, created.Version, model.ProAccountDraftUpdate{
		State: model.ProOperationStateProbed, CleanupDeadlineMS: time.Now().Add(-time.Second).UnixMilli(), Context: created.Context,
	}, time.Now().UnixMilli()); err != nil {
		t.Fatalf("设置到期状态：%v", err)
	}
	worker := NewProAccountRecoveryWorker(operations)
	worker.runOnce(context.Background())
	item, err := operations.Get(context.Background(), created.OperationID)
	if err != nil {
		t.Fatalf("读取操作：%v", err)
	}
	if item.State != model.ProOperationStateCompensating || item.ErrorCode != "cleanup_deadline_exceeded" {
		t.Fatalf("恢复状态 = %#v", item)
	}
}

func TestProAccountRecoveryWorkerExecutesCompensationService(t *testing.T) {
	db, err := sqliterepo.Open(filepath.Join(t.TempDir(), "manager.sqlite"))
	if err != nil {
		t.Fatalf("打开 SQLite：%v", err)
	}
	defer db.Close()
	operations := proaccountoperation.New(proaccountdraft.New(db))
	created, _, err := operations.Start(context.Background(), proaccountoperation.StartInput{
		OperationID: "recovery-operation", IdempotencyKey: "recovery-key", OperationType: "add",
	})
	if err != nil {
		t.Fatalf("创建操作：%v", err)
	}
	repository := proaccountdraft.New(db)
	compensating, err := repository.Update(context.Background(), created.OperationID, created.Version, model.ProAccountDraftUpdate{
		State: model.ProOperationStateCompensating, CleanupDeadlineMS: time.Now().Add(-time.Second).UnixMilli(),
		CompensationAction: "resume_or_cleanup", Context: created.Context,
	}, time.Now().UnixMilli())
	if err != nil {
		t.Fatalf("设置补偿状态：%v", err)
	}
	recorder := &recoveryRecorder{}
	NewProAccountRecoveryWorker(operations, recorder).runOnce(context.Background())
	recorder.mu.Lock()
	defer recorder.mu.Unlock()
	if len(recorder.items) != 1 || recorder.items[0].OperationID != compensating.OperationID || recorder.items[0].State != model.ProOperationStateCompensating {
		t.Fatalf("恢复调用 = %#v", recorder.items)
	}
}
