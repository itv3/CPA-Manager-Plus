package proaccountrebind

import (
	"context"
	"testing"

	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/model"
	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/service/managerconfig"
	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/service/proaccountgateway"
	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/service/proaccountoperation"
	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/store"
)

type rebindAccountsStub struct {
	accounts map[string]model.ProAccount
}

func (s rebindAccountsStub) Get(_ context.Context, id string) (model.ProAccount, error) {
	return s.accounts[id], nil
}

type rebindRepositoryStub struct {
	reviews  map[int64]model.ProAccountBindingReview
	rebound  []string
	resolved []int64
}

func (s *rebindRepositoryStub) ListBindingReviews(context.Context, []string, int) ([]model.ProAccountBindingReview, error) {
	result := make([]model.ProAccountBindingReview, 0, len(s.reviews))
	for _, item := range s.reviews {
		result = append(result, item)
	}
	return result, nil
}

func (s *rebindRepositoryStub) GetBindingReview(_ context.Context, id int64) (model.ProAccountBindingReview, bool, error) {
	item, ok := s.reviews[id]
	return item, ok, nil
}

func (s *rebindRepositoryStub) RebindFromReview(_ context.Context, reviewID int64, accountID string, expectedVersion int64, discovery model.ProAccountDiscovery, nowMS int64) (model.ProAccount, error) {
	item := s.reviews[reviewID]
	item.ResolutionStatus = "resolved"
	item.ResolvedAccountID = accountID
	item.ResolvedAtMS = nowMS
	s.reviews[reviewID] = item
	s.resolved = append(s.resolved, reviewID)
	s.rebound = append(s.rebound, accountID+":"+discovery.SourceLocator)
	return model.ProAccount{ID: accountID, Version: expectedVersion + 1, Binding: &model.ProAccountBinding{
		SourceType: discovery.SourceType, SourceLocator: discovery.SourceLocator, AuthIndex: discovery.AuthIndex,
	}}, nil
}

type rebindSetupStub struct{}

func (rebindSetupStub) ResolveSetupWithSource(context.Context) (store.Setup, managerconfig.Source, bool, error) {
	return store.Setup{CPAUpstreamURL: "http://gateway.test", ManagementKey: "management-key"}, managerconfig.SourceDB, true, nil
}

type rebindGatewayStub struct {
	accounts []proaccountgateway.AccountSnapshot
}

func (s rebindGatewayStub) Snapshot(context.Context, string, string) (proaccountgateway.SnapshotResult, error) {
	return proaccountgateway.SnapshotResult{Accounts: s.accounts}, nil
}

type rebindOperationsStub struct {
	operations map[string]model.ProAccountDraft
}

func (s *rebindOperationsStub) Start(_ context.Context, input proaccountoperation.StartInput) (model.ProAccountDraft, bool, error) {
	if existing, ok := s.operations[input.OperationID]; ok {
		return existing, false, nil
	}
	item := model.ProAccountDraft{
		OperationID: input.OperationID, IdempotencyKey: input.IdempotencyKey, OperationType: input.OperationType,
		ProAccountID: input.ProAccountID, State: model.ProOperationStateDraftCreated, Version: 1, Context: input.Context,
	}
	s.operations[input.OperationID] = item
	return item, true, nil
}

func (s *rebindOperationsStub) Transition(_ context.Context, operationID string, input proaccountoperation.TransitionInput) (model.ProAccountDraft, error) {
	item := s.operations[operationID]
	item.State = input.State
	item.Version++
	item.Context = input.Context
	s.operations[operationID] = item
	return item, nil
}

func TestConfirmRebindKeepsAccountIDAndResolvesReview(t *testing.T) {
	account := model.ProAccount{ID: "account-1", Version: 3, Binding: &model.ProAccountBinding{SourceType: "auth_file", SourceLocator: "/old/account.json", AuthIndex: "old-auth"}}
	repository := &rebindRepositoryStub{reviews: map[int64]model.ProAccountBindingReview{1: {
		ID: 1, SourceType: "auth_file", SourceLocator: "/new/account.json", AuthIndex: "new-auth",
		ResolutionStatus: model.ProBindingResolutionPending, CandidateIDs: []string{account.ID},
	}}}
	operations := &rebindOperationsStub{operations: map[string]model.ProAccountDraft{}}
	service := New(rebindAccountsStub{accounts: map[string]model.ProAccount{account.ID: account}}, repository, rebindSetupStub{}, rebindGatewayStub{accounts: []proaccountgateway.AccountSnapshot{{
		Platform: "openai", AuthType: "oauth", SourceType: "auth_file", SourceLocator: "/new/account.json", AuthIndex: "new-auth", Enabled: true,
	}}}, operations)

	result, err := service.Confirm(context.Background(), ConfirmInput{
		OperationID: "rebind-operation", IdempotencyKey: "rebind-key",
		Items: []ConfirmItem{{ReviewID: 1, ProAccountID: account.ID, ExpectedVersion: 3}},
	})
	if err != nil {
		t.Fatalf("确认重绑：%v", err)
	}
	if result.Succeeded != 1 || result.Items[0].Account == nil || result.Items[0].Account.ID != account.ID {
		t.Fatalf("重绑结果 = %#v", result)
	}
	if len(repository.rebound) != 1 || repository.rebound[0] != "account-1:/new/account.json" || len(repository.resolved) != 1 {
		t.Fatalf("仓库调用 rebound=%#v resolved=%#v", repository.rebound, repository.resolved)
	}
	if operations.operations["rebind-operation:review:1"].State != model.ProOperationStateEnabled {
		t.Fatalf("操作终态 = %#v", operations.operations)
	}
}

func TestConfirmRejectsDuplicateTargetsBeforeRebind(t *testing.T) {
	account := model.ProAccount{ID: "account-1", Version: 1}
	repository := &rebindRepositoryStub{reviews: map[int64]model.ProAccountBindingReview{}}
	service := New(rebindAccountsStub{accounts: map[string]model.ProAccount{account.ID: account}}, repository, rebindSetupStub{}, rebindGatewayStub{}, &rebindOperationsStub{operations: map[string]model.ProAccountDraft{}})
	result, err := service.Confirm(context.Background(), ConfirmInput{
		OperationID: "rebind-operation", IdempotencyKey: "rebind-key",
		Items: []ConfirmItem{{ReviewID: 1, ProAccountID: account.ID, ExpectedVersion: 1}, {ReviewID: 2, ProAccountID: account.ID, ExpectedVersion: 1}},
	})
	if err != nil {
		t.Fatalf("确认重绑：%v", err)
	}
	if result.Failed != 2 || result.Items[0].Code != "duplicate_rebind_target" || len(repository.rebound) != 0 {
		t.Fatalf("重复目标结果 = %#v", result)
	}
}
