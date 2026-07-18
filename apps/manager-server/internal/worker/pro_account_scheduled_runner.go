package worker

import (
	"context"
	"log"
	"time"

	proaccountscheduledtestsvc "github.com/seakee/cpa-manager-plus/apps/manager-server/internal/service/proaccountscheduledtest"
)

const defaultProAccountScheduledTestInterval = time.Minute

type proAccountScheduledTestDueRunner interface {
	RunDue(ctx context.Context) (proaccountscheduledtestsvc.DueRunSummary, error)
}

type ProAccountScheduledTestRunner struct {
	service  proAccountScheduledTestDueRunner
	interval time.Duration
}

func NewProAccountScheduledTestRunner(service proAccountScheduledTestDueRunner) *ProAccountScheduledTestRunner {
	return &ProAccountScheduledTestRunner{service: service, interval: defaultProAccountScheduledTestInterval}
}

func (w *ProAccountScheduledTestRunner) Start(ctx context.Context) {
	if w == nil || w.service == nil {
		return
	}
	go func() {
		w.runOnce(ctx)
		interval := w.interval
		if interval <= 0 {
			interval = defaultProAccountScheduledTestInterval
		}
		ticker := time.NewTicker(interval)
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

func (w *ProAccountScheduledTestRunner) runOnce(ctx context.Context) {
	summary, err := w.service.RunDue(ctx)
	if err != nil {
		if ctx.Err() == nil {
			log.Printf("扫描统一账号定时测试失败: %v", err)
		}
		return
	}
	if summary.Due > 0 {
		log.Printf("统一账号定时测试完成 due=%d completed=%d failed=%d", summary.Due, summary.Completed, summary.Failed)
	}
}
