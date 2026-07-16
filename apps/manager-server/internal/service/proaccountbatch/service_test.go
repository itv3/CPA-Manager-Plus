package proaccountbatch

import (
	"context"
	"testing"

	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/model"
	proaccountrepo "github.com/seakee/cpa-manager-plus/apps/manager-server/internal/repository/proaccount"
	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/service/proaccountgateway"
	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/service/proaccountlifecycle"
	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/service/proaccounttest"
)

type batchLifecycleStub struct {
	mutations []proaccountlifecycle.MutationInput
}

func (s *batchLifecycleStub) SetEnabled(_ context.Context, input proaccountlifecycle.MutationInput, enabled bool) (proaccountlifecycle.Result, error) {
	s.mutations = append(s.mutations, input)
	if input.AccountID == "stale" {
		return proaccountlifecycle.Result{}, proaccountrepo.ErrVersionConflict
	}
	return proaccountlifecycle.Result{Account: model.ProAccount{ID: input.AccountID, Enabled: enabled, Version: input.ExpectedVersion + 1}}, nil
}

func (s *batchLifecycleStub) Delete(_ context.Context, input proaccountlifecycle.MutationInput) (proaccountlifecycle.Result, error) {
	s.mutations = append(s.mutations, input)
	return proaccountlifecycle.Result{Account: model.ProAccount{ID: input.AccountID, DeletedAtMS: 1000}}, nil
}

type batchTesterStub struct{}

func (batchTesterStub) Test(_ context.Context, input proaccounttest.Input) (proaccounttest.Result, error) {
	if input.AccountID == "failed" {
		return proaccounttest.Result{
			Connectivity: proaccountgateway.ConnectivityResult{ErrorCode: "authentication_failed"},
			Operation:    model.ProAccountDraft{State: model.ProOperationStateFailed},
		}, nil
	}
	return proaccounttest.Result{
		Connectivity: proaccountgateway.ConnectivityResult{Success: true, Model: input.Model},
		Operation:    model.ProAccountDraft{State: model.ProOperationStateEnabled},
	}, nil
}

func TestBatchExecuteKeepsPerItemErrors(t *testing.T) {
	lifecycle := &batchLifecycleStub{}
	result, err := New(lifecycle, batchTesterStub{}).Execute(context.Background(), Input{
		OperationID: "batch-enable", IdempotencyKey: "batch-enable-key", Action: "enable",
		Items: []Item{{ProAccountID: "healthy", ExpectedVersion: 2}, {ProAccountID: "stale", ExpectedVersion: 4}},
	})
	if err != nil {
		t.Fatalf("批量启用：%v", err)
	}
	if result.Succeeded != 1 || result.Failed != 1 || !result.Items[0].Success || result.Items[1].Code != "resource_version_conflict" {
		t.Fatalf("批量结果 = %#v", result)
	}
	if len(lifecycle.mutations) != 2 || lifecycle.mutations[0].OperationID != "batch-enable:item:0" || lifecycle.mutations[1].IdempotencyKey != "batch-enable-key:item:1" {
		t.Fatalf("逐项操作标识 = %#v", lifecycle.mutations)
	}
}

func TestBatchTestClassifiesConnectivityFailure(t *testing.T) {
	result, err := New(&batchLifecycleStub{}, batchTesterStub{}).Execute(context.Background(), Input{
		OperationID: "batch-test", IdempotencyKey: "batch-test-key", Action: "test",
		Items: []Item{{ProAccountID: "healthy", ExpectedVersion: 1, Model: "gpt-test"}, {ProAccountID: "failed", ExpectedVersion: 1, Model: "gpt-test"}},
	})
	if err != nil {
		t.Fatalf("批量测试：%v", err)
	}
	if result.Succeeded != 1 || result.Failed != 1 || result.Items[1].Code != "authentication_failed" {
		t.Fatalf("测试结果 = %#v", result)
	}
}

func TestBatchRejectsDuplicateAccountWithoutCallingTwice(t *testing.T) {
	lifecycle := &batchLifecycleStub{}
	result, err := New(lifecycle, batchTesterStub{}).Execute(context.Background(), Input{
		OperationID: "batch-delete", IdempotencyKey: "batch-delete-key", Action: "delete",
		Items: []Item{{ProAccountID: "same", ExpectedVersion: 1}, {ProAccountID: "same", ExpectedVersion: 1}},
	})
	if err != nil {
		t.Fatalf("批量删除：%v", err)
	}
	if result.Succeeded != 1 || result.Failed != 1 || result.Items[1].Code != "duplicate_batch_item" || len(lifecycle.mutations) != 1 {
		t.Fatalf("重复结果 = %#v mutations=%#v", result, lifecycle.mutations)
	}
}
