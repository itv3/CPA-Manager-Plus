package worker

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	proaccountscheduledtestsvc "github.com/seakee/cpa-manager-plus/apps/manager-server/internal/service/proaccountscheduledtest"
)

type scheduledDueRunnerStub struct {
	calls  atomic.Int64
	called chan struct{}
}

func (s *scheduledDueRunnerStub) RunDue(context.Context) (proaccountscheduledtestsvc.DueRunSummary, error) {
	s.calls.Add(1)
	s.called <- struct{}{}
	return proaccountscheduledtestsvc.DueRunSummary{}, nil
}

func TestProAccountScheduledTestRunnerRunsImmediatelyAndEveryInterval(t *testing.T) {
	service := &scheduledDueRunnerStub{called: make(chan struct{}, 4)}
	runner := NewProAccountScheduledTestRunner(service)
	runner.interval = 10 * time.Millisecond
	ctx, cancel := context.WithCancel(context.Background())
	runner.Start(ctx)
	for i := 0; i < 2; i++ {
		select {
		case <-service.called:
		case <-time.After(time.Second):
			cancel()
			t.Fatalf("等待第 %d 次调度超时", i+1)
		}
	}
	cancel()
	if service.calls.Load() < 2 {
		t.Fatalf("调度次数=%d，期望至少 2", service.calls.Load())
	}
}
