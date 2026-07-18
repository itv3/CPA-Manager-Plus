package proaccountlifecycle

import (
	"context"
	"errors"
	"testing"

	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/model"
	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/service/proaccountgateway"
	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/service/proaccountoperation"
)

type reauthorizationSessionOperations struct {
	migrationOperations
	active     model.ProAccountDraft
	startCalls int
}

func (o *reauthorizationSessionOperations) Start(ctx context.Context, input proaccountoperation.StartInput) (model.ProAccountDraft, bool, error) {
	o.startCalls++
	return o.migrationOperations.Start(ctx, input)
}

func (o *reauthorizationSessionOperations) FindActiveReauthorization(_ context.Context, accountID string) (model.ProAccountDraft, bool, error) {
	if o.active.ProAccountID != accountID {
		return model.ProAccountDraft{}, false, nil
	}
	return o.active, true, nil
}

func TestStartReauthorizationRejectsAnotherActiveSession(t *testing.T) {
	service, operations := newReauthorizationSessionService()
	result, err := service.StartReauthorization(context.Background(), ReauthorizationStartInput{MutationInput: MutationInput{
		AccountID: "account-1", OperationID: "reauthorize-2", IdempotencyKey: "reauthorize-key-2", ExpectedVersion: 3,
	}})
	if !errors.Is(err, ErrReauthorizationInProgress) {
		t.Fatalf("并发会话错误 = %v", err)
	}
	if result.Operation.OperationID != "reauthorize-1" || operations.startCalls != 0 {
		t.Fatalf("并发会话结果 = %#v，Start 调用次数 = %d", result.Operation, operations.startCalls)
	}
}

func TestStartReauthorizationReplaysSameIdempotentSession(t *testing.T) {
	service, operations := newReauthorizationSessionService()
	result, err := service.StartReauthorization(context.Background(), ReauthorizationStartInput{MutationInput: MutationInput{
		AccountID: "account-1", OperationID: "reauthorize-retry", IdempotencyKey: "reauthorize-key-1", ExpectedVersion: 3,
	}})
	if err != nil {
		t.Fatalf("幂等重放重新授权：%v", err)
	}
	if result.Status != "wait" || result.Operation.OperationID != "reauthorize-1" || result.OAuth == nil ||
		result.OAuth.URL != "https://oauth.example/authorize" || result.OAuth.State != "oauth-state-1" {
		t.Fatalf("幂等重放结果 = %#v", result)
	}
	if operations.startCalls != 0 {
		t.Fatalf("幂等重放仍创建操作，Start 调用次数 = %d", operations.startCalls)
	}
}

func newReauthorizationSessionService() (*Service, *reauthorizationSessionOperations) {
	state := &migrationAccountState{account: model.ProAccount{
		ID: "account-1", Platform: "openai", AuthType: "oauth", SourceType: proaccountgateway.SourceAuthFile,
		Version: 3, Binding: &model.ProAccountBinding{
			ProAccountID: "account-1", SourceType: proaccountgateway.SourceAuthFile,
			SourceLocator: "codex-owner.json", AuthIndex: "auth-old",
		},
	}}
	operations := &reauthorizationSessionOperations{active: model.ProAccountDraft{
		OperationID: "reauthorize-1", IdempotencyKey: "reauthorize-key-1", OperationType: "reauthorize",
		ProAccountID: "account-1", State: model.ProOperationStateProbed, Version: 2,
		Context: map[string]any{"oauthURL": "https://oauth.example/authorize", "oauthState": "oauth-state-1"},
	}}
	service := New(
		migrationAccountReader{state: state},
		&migrationRepository{state: state},
		nil,
		nil,
		nil,
		operations,
	)
	return service, operations
}
