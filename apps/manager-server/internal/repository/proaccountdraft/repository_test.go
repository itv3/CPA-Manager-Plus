package proaccountdraft_test

import (
	"context"
	"errors"
	"path/filepath"
	"testing"

	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/model"
	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/repository/proaccountdraft"
	sqliterepo "github.com/seakee/cpa-manager-plus/apps/manager-server/internal/repository/sqlite"
)

func TestRepositoryEnforcesIdempotencyAndOptimisticLocking(t *testing.T) {
	db, err := sqliterepo.Open(filepath.Join(t.TempDir(), "manager.sqlite"))
	if err != nil {
		t.Fatalf("打开 SQLite：%v", err)
	}
	defer db.Close()
	repository := proaccountdraft.New(db)
	ctx := context.Background()
	input := model.ProAccountDraftCreate{
		OperationID: "operation-1", IdempotencyKey: "idempotency-1", OperationType: "add",
		CleanupDeadlineMS: 2000, Context: map[string]any{"platform": "openai"},
	}
	created, isNew, err := repository.Create(ctx, input, 1000)
	if err != nil || !isNew || created.Version != 1 || created.State != model.ProOperationStateDraftCreated {
		t.Fatalf("创建操作：item=%#v new=%v err=%v", created, isNew, err)
	}

	replayedInput := input
	replayedInput.OperationID = "operation-replayed"
	replayed, isNew, err := repository.Create(ctx, replayedInput, 1100)
	if err != nil || isNew || replayed.OperationID != created.OperationID {
		t.Fatalf("幂等重放：item=%#v new=%v err=%v", replayed, isNew, err)
	}
	conflicting := input
	conflicting.OperationID = "operation-conflict"
	conflicting.OperationType = "edit"
	if _, _, err := repository.Create(ctx, conflicting, 1200); !errors.Is(err, proaccountdraft.ErrIdempotencyConflict) {
		t.Fatalf("幂等冲突错误 = %v", err)
	}

	updated, err := repository.Update(ctx, created.OperationID, created.Version, model.ProAccountDraftUpdate{
		State: model.ProOperationStateProbed, CleanupDeadlineMS: 2000, Context: created.Context,
	}, 1300)
	if err != nil || updated.Version != 2 || updated.State != model.ProOperationStateProbed {
		t.Fatalf("更新操作：item=%#v err=%v", updated, err)
	}
	if _, err := repository.Update(ctx, created.OperationID, created.Version, model.ProAccountDraftUpdate{
		State: model.ProOperationStateFailed, CleanupDeadlineMS: 2000,
	}, 1400); !errors.Is(err, proaccountdraft.ErrVersionConflict) {
		t.Fatalf("旧版本更新错误 = %v", err)
	}
}

func TestRepositoryRecoveryScanExcludesTerminalStates(t *testing.T) {
	db, err := sqliterepo.Open(filepath.Join(t.TempDir(), "manager.sqlite"))
	if err != nil {
		t.Fatalf("打开 SQLite：%v", err)
	}
	defer db.Close()
	repository := proaccountdraft.New(db)
	ctx := context.Background()
	for _, item := range []struct {
		id    string
		state string
	}{
		{id: "recoverable", state: model.ProOperationStateCompensating},
		{id: "enabled", state: model.ProOperationStateEnabled},
		{id: "cancelled", state: model.ProOperationStateCancelled},
		{id: "failed", state: model.ProOperationStateFailed},
	} {
		created, _, createErr := repository.Create(ctx, model.ProAccountDraftCreate{
			OperationID: item.id, IdempotencyKey: "key-" + item.id, OperationType: "add", CleanupDeadlineMS: 1000,
		}, 500)
		if createErr != nil {
			t.Fatalf("创建 %s：%v", item.id, createErr)
		}
		if item.state != model.ProOperationStateDraftCreated {
			if _, updateErr := repository.Update(ctx, item.id, created.Version, model.ProAccountDraftUpdate{
				State: item.state, CleanupDeadlineMS: 1000,
			}, 600); updateErr != nil {
				t.Fatalf("更新 %s：%v", item.id, updateErr)
			}
		}
	}
	items, err := repository.ListRecoverable(ctx, 2000, 100)
	if err != nil {
		t.Fatalf("扫描恢复操作：%v", err)
	}
	if len(items) != 1 || items[0].OperationID != "recoverable" {
		t.Fatalf("恢复扫描结果 = %#v", items)
	}
}
