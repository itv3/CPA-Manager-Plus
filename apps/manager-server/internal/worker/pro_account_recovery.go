package worker

import (
	"context"
	"errors"
	"log"
	"time"

	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/model"
	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/service/proaccountoperation"
)

const defaultProAccountRecoveryInterval = time.Minute

type proAccountOperationRecovery interface {
	ListRecoverable(ctx context.Context, limit int) ([]model.ProAccountDraft, error)
	Transition(ctx context.Context, operationID string, input proaccountoperation.TransitionInput) (model.ProAccountDraft, error)
}

type ProAccountRecoveryWorker struct {
	operations proAccountOperationRecovery
	interval   time.Duration
}

func NewProAccountRecoveryWorker(operations proAccountOperationRecovery, interval ...time.Duration) *ProAccountRecoveryWorker {
	wakeInterval := defaultProAccountRecoveryInterval
	if len(interval) > 0 && interval[0] > 0 {
		wakeInterval = interval[0]
	}
	return &ProAccountRecoveryWorker{operations: operations, interval: wakeInterval}
}

func (w *ProAccountRecoveryWorker) Start(ctx context.Context) {
	if w == nil || w.operations == nil {
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

func (w *ProAccountRecoveryWorker) runOnce(ctx context.Context) {
	items, err := w.operations.ListRecoverable(ctx, 100)
	if err != nil {
		if ctx.Err() == nil {
			log.Printf("扫描统一账号恢复操作失败: %v", err)
		}
		return
	}
	for _, item := range items {
		if item.State == model.ProOperationStateCompensating {
			continue
		}
		action := item.CompensationAction
		if action == "" {
			action = "resume_or_cleanup"
		}
		_, err := w.operations.Transition(ctx, item.OperationID, proaccountoperation.TransitionInput{
			ExpectedVersion: item.Version, State: model.ProOperationStateCompensating,
			RetryCount: item.RetryCount, CleanupDeadlineMS: item.CleanupDeadlineMS,
			CompensationAction: action, ErrorCode: "cleanup_deadline_exceeded",
			ErrorSummary: "操作超过清理截止时间，等待恢复或补偿", Context: item.Context,
		})
		if err != nil && !errors.Is(err, proaccountoperation.ErrOperationVersionConflict) && ctx.Err() == nil {
			log.Printf("标记统一账号恢复操作失败 operation_id=%s: %v", item.OperationID, err)
		}
	}
}
