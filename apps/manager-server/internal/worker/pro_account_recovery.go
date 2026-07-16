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
	recovery   interface {
		Recover(ctx context.Context, operation model.ProAccountDraft) error
	}
	interval time.Duration
}

func NewProAccountRecoveryWorker(operations proAccountOperationRecovery, recovery ...interface {
	Recover(ctx context.Context, operation model.ProAccountDraft) error
}) *ProAccountRecoveryWorker {
	wakeInterval := defaultProAccountRecoveryInterval
	var recoveryService interface {
		Recover(ctx context.Context, operation model.ProAccountDraft) error
	}
	if len(recovery) > 0 {
		recoveryService = recovery[0]
	}
	return &ProAccountRecoveryWorker{operations: operations, recovery: recoveryService, interval: wakeInterval}
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
			w.recover(ctx, item)
			continue
		}
		action := item.CompensationAction
		if action == "" {
			action = "resume_or_cleanup"
		}
		transitioned, err := w.operations.Transition(ctx, item.OperationID, proaccountoperation.TransitionInput{
			ExpectedVersion: item.Version, State: model.ProOperationStateCompensating,
			RetryCount: item.RetryCount, CleanupDeadlineMS: item.CleanupDeadlineMS,
			CompensationAction: action, ErrorCode: "cleanup_deadline_exceeded",
			ErrorSummary: "操作超过清理截止时间，等待恢复或补偿", Context: item.Context,
		})
		if err != nil && !errors.Is(err, proaccountoperation.ErrOperationVersionConflict) && ctx.Err() == nil {
			log.Printf("标记统一账号恢复操作失败 operation_id=%s: %v", item.OperationID, err)
			continue
		}
		if err == nil {
			w.recover(ctx, transitioned)
		}
	}
}

func (w *ProAccountRecoveryWorker) recover(ctx context.Context, operation model.ProAccountDraft) {
	if w.recovery == nil {
		return
	}
	if err := w.recovery.Recover(ctx, operation); err != nil && ctx.Err() == nil {
		log.Printf("恢复统一账号操作失败 operation_id=%s: %v", operation.OperationID, err)
	}
}
