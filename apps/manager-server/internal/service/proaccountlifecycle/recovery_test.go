package proaccountlifecycle

import (
	"context"
	"errors"
	"reflect"
	"testing"
	"time"

	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/model"
	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/service/managerconfig"
	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/service/proaccountgateway"
	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/service/proaccountoperation"
	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/store"
)

type recoveryAccountReaderStub struct {
	account model.ProAccount
	err     error
}

func (s recoveryAccountReaderStub) Get(context.Context, string) (model.ProAccount, error) {
	return s.account, s.err
}

type recoveryRepositoryStub struct {
	AccountRepository
	softDeleted bool
}

func (s *recoveryRepositoryStub) SoftDelete(_ context.Context, _ string, _ int64, nowMS int64) (model.ProAccount, error) {
	s.softDeleted = true
	return model.ProAccount{ID: "account-1", DeletedAtMS: nowMS}, nil
}

type recoveryGatewayStub struct {
	Gateway
	deletedSourceType string
	deletedLocator    string
	restoredRules     proaccountgateway.ModelRules
	accounts          map[string]proaccountgateway.AccountSnapshot
	enabled           map[string]bool
}

func (s *recoveryGatewayStub) DeleteAccount(_ context.Context, _ string, _ string, sourceType string, sourceLocator string) error {
	s.deletedSourceType = sourceType
	s.deletedLocator = sourceLocator
	for authIndex, account := range s.accounts {
		if account.SourceType == sourceType && account.SourceLocator == sourceLocator {
			delete(s.accounts, authIndex)
		}
	}
	return nil
}

func (s *recoveryGatewayStub) FindAccountByAuthIndex(_ context.Context, _ string, _ string, authIndex string) (proaccountgateway.AccountSnapshot, error) {
	account, ok := s.accounts[authIndex]
	if !ok {
		return proaccountgateway.AccountSnapshot{}, proaccountgateway.ErrGatewayAccountNotFound
	}
	return account, nil
}

func (s *recoveryGatewayStub) SetAccountEnabled(_ context.Context, _ string, _ string, sourceType string, sourceLocator string, enabled bool) (proaccountgateway.AccountSnapshot, error) {
	for authIndex, account := range s.accounts {
		if account.SourceType == sourceType && account.SourceLocator == sourceLocator {
			account.Enabled = enabled
			s.accounts[authIndex] = account
			if s.enabled == nil {
				s.enabled = map[string]bool{}
			}
			s.enabled[authIndex] = enabled
			return account, nil
		}
	}
	return proaccountgateway.AccountSnapshot{}, proaccountgateway.ErrGatewayAccountNotFound
}

func (s *recoveryGatewayStub) RestoreModelRules(_ context.Context, _ string, _ string, _ string, _ string, previous proaccountgateway.ModelRules) error {
	s.restoredRules = previous
	return nil
}

type recoverySetupStub struct{}

func (recoverySetupStub) ResolveSetupWithSource(context.Context) (store.Setup, managerconfig.Source, bool, error) {
	return store.Setup{CPAUpstreamURL: "http://gateway.test", ManagementKey: "management-key"}, managerconfig.SourceDB, true, nil
}

type recoveryOperationStub struct {
	OperationService
	lastTransition proaccountoperation.TransitionInput
}

func (s *recoveryOperationStub) Transition(_ context.Context, operationID string, input proaccountoperation.TransitionInput) (model.ProAccountDraft, error) {
	s.lastTransition = input
	return model.ProAccountDraft{
		OperationID: operationID, ProAccountID: input.ProAccountID, State: input.State,
		Version: input.ExpectedVersion + 1, CompensationAction: input.CompensationAction,
		ErrorCode: input.ErrorCode, ErrorSummary: input.ErrorSummary, Context: input.Context,
	}, nil
}

func recoveryAccount() model.ProAccount {
	return model.ProAccount{
		ID: "account-1", Version: 4,
		Binding: &model.ProAccountBinding{
			SourceType: "auth_file", SourceLocator: "oauth-account.json", AuthIndex: "auth-1",
		},
	}
}

func TestRecoverDeleteUsesCurrentBindingWhenContextLocatorIsMissing(t *testing.T) {
	account := recoveryAccount()
	repository := &recoveryRepositoryStub{}
	gateway := &recoveryGatewayStub{}
	operations := &recoveryOperationStub{}
	service := New(recoveryAccountReaderStub{account: account}, repository, recoverySetupStub{}, gateway, nil, operations)
	service.now = func() time.Time { return time.UnixMilli(5000) }

	err := service.Recover(context.Background(), model.ProAccountDraft{
		OperationID: "operation-1", OperationType: "add", ProAccountID: account.ID,
		State: model.ProOperationStateCompensating, Version: 3,
		CompensationAction: "delete_new_credential", Context: map[string]any{},
	})
	if err != nil {
		t.Fatalf("恢复删除：%v", err)
	}
	if gateway.deletedSourceType != "auth_file" || gateway.deletedLocator != "oauth-account.json" {
		t.Fatalf("删除定位 = %s/%s", gateway.deletedSourceType, gateway.deletedLocator)
	}
	if !repository.softDeleted || operations.lastTransition.State != model.ProOperationStateFailed {
		t.Fatalf("补偿结果 softDeleted=%v transition=%#v", repository.softDeleted, operations.lastTransition)
	}
}

func TestRecoverReplacementWithoutLocatorDoesNotDeleteCurrentBinding(t *testing.T) {
	account := recoveryAccount()
	gateway := &recoveryGatewayStub{}
	service := New(recoveryAccountReaderStub{account: account}, &recoveryRepositoryStub{}, recoverySetupStub{}, gateway, nil, &recoveryOperationStub{})

	err := service.Recover(context.Background(), model.ProAccountDraft{
		OperationID: "operation-2", OperationType: "edit", ProAccountID: account.ID,
		State: model.ProOperationStateCompensating, Version: 2,
		CompensationAction: "delete_replacement_credential", Context: map[string]any{},
	})
	if err != nil {
		t.Fatalf("恢复未创建的替换凭证：%v", err)
	}
	if gateway.deletedLocator != "" {
		t.Fatalf("不应删除当前绑定，实际删除 = %s", gateway.deletedLocator)
	}
}

func TestRecoverReplacementSwitchRollsBackWhenManagerStillUsesOldBinding(t *testing.T) {
	account := recoveryAccount()
	gateway := &recoveryGatewayStub{accounts: map[string]proaccountgateway.AccountSnapshot{
		"auth-1": {SourceType: "config_codex_api_key", SourceLocator: "index:0", AuthIndex: "auth-1", Enabled: false},
		"auth-2": {SourceType: "config_codex_api_key", SourceLocator: "index:1", AuthIndex: "auth-2", Enabled: true},
	}}
	operations := &recoveryOperationStub{}
	service := New(recoveryAccountReaderStub{account: account}, &recoveryRepositoryStub{}, recoverySetupStub{}, gateway, nil, operations)
	err := service.Recover(context.Background(), replacementSwitchOperation(account.ID))
	if err != nil {
		t.Fatalf("回滚替换切换：%v", err)
	}
	if _, exists := gateway.accounts["auth-2"]; exists {
		t.Fatal("回滚后替换凭证仍然存在")
	}
	if !gateway.enabled["auth-1"] {
		t.Fatal("回滚后旧凭证未恢复启用")
	}
	if operations.lastTransition.State != model.ProOperationStateFailed || operations.lastTransition.CompensationAction != "rollback_replacement_switch_completed" {
		t.Fatalf("回滚终态 = %#v", operations.lastTransition)
	}
}

func TestRecoverReplacementSwitchFinishesForwardWhenManagerUsesReplacement(t *testing.T) {
	account := recoveryAccount()
	account.Binding = &model.ProAccountBinding{SourceType: "config_codex_api_key", SourceLocator: "index:0", AuthIndex: "auth-2"}
	gateway := &recoveryGatewayStub{accounts: map[string]proaccountgateway.AccountSnapshot{
		"auth-1": {SourceType: "config_codex_api_key", SourceLocator: "index:0", AuthIndex: "auth-1", Enabled: false},
		"auth-2": {SourceType: "config_codex_api_key", SourceLocator: "index:1", AuthIndex: "auth-2", Enabled: true},
	}}
	operations := &recoveryOperationStub{}
	service := New(recoveryAccountReaderStub{account: account}, &recoveryRepositoryStub{}, recoverySetupStub{}, gateway, nil, operations)
	err := service.Recover(context.Background(), replacementSwitchOperation(account.ID))
	if err != nil {
		t.Fatalf("向前完成替换切换：%v", err)
	}
	if _, exists := gateway.accounts["auth-1"]; exists {
		t.Fatal("向前恢复后旧凭证仍然存在")
	}
	if _, exists := gateway.accounts["auth-2"]; !exists {
		t.Fatal("向前恢复误删替换凭证")
	}
	if operations.lastTransition.State != model.ProOperationStateEnabled {
		t.Fatalf("向前恢复终态 = %#v", operations.lastTransition)
	}
}

func replacementSwitchOperation(accountID string) model.ProAccountDraft {
	return model.ProAccountDraft{
		OperationID: "replacement-switch", OperationType: "edit", ProAccountID: accountID,
		State: model.ProOperationStateCompensating, Version: 5, CompensationAction: "rollback_replacement_switch",
		Context: map[string]any{
			"oldAuthIndex": "auth-1", "oldEnabled": true, "oldStatusChanged": true,
			"replacementAuthIndex": "auth-2", "replacementSourceType": "config_codex_api_key",
			"replacementSourceLocator": "index:1", "replacementProjectedLocator": "index:0",
		},
	}
}

func TestRecoverRestoresRulesFromPersistedContext(t *testing.T) {
	account := recoveryAccount()
	gateway := &recoveryGatewayStub{}
	operations := &recoveryOperationStub{}
	service := New(recoveryAccountReaderStub{account: account}, &recoveryRepositoryStub{}, recoverySetupStub{}, gateway, nil, operations)

	err := service.Recover(context.Background(), model.ProAccountDraft{
		OperationID: "operation-3", OperationType: "edit", ProAccountID: account.ID,
		State: model.ProOperationStateCompensating, Version: 4,
		CompensationAction: "restore_model_rules",
		Context: map[string]any{
			"previousAllowedModels": []string{"old-model"},
			"previousModelMapping":  map[string]string{"old-alias": "old-model"},
		},
	})
	if err != nil {
		t.Fatalf("恢复模型规则：%v", err)
	}
	want := proaccountgateway.ModelRules{AllowedModels: []string{"old-model"}, ModelMapping: map[string]string{"old-alias": "old-model"}}
	if !reflect.DeepEqual(gateway.restoredRules, want) {
		t.Fatalf("恢复规则 = %#v", gateway.restoredRules)
	}
	if operations.lastTransition.State != model.ProOperationStateFailed || operations.lastTransition.CompensationAction != "restore_model_rules_completed" {
		t.Fatalf("补偿终态 = %#v", operations.lastTransition)
	}
}

func TestRecoverRestoreRulesRequiresCurrentBinding(t *testing.T) {
	account := recoveryAccount()
	account.Binding = nil
	service := New(recoveryAccountReaderStub{account: account}, &recoveryRepositoryStub{}, recoverySetupStub{}, &recoveryGatewayStub{}, nil, &recoveryOperationStub{})
	err := service.Recover(context.Background(), model.ProAccountDraft{
		OperationID: "operation-4", OperationType: "edit", ProAccountID: account.ID,
		State: model.ProOperationStateCompensating, Version: 1,
		CompensationAction: "restore_model_rules", Context: map[string]any{},
	})
	if !errors.Is(err, ErrInvalidRequest) {
		t.Fatalf("错误 = %v", err)
	}
}

func TestRecoverUnknownActionNeverDeletesExistingCredential(t *testing.T) {
	account := recoveryAccount()
	repository := &recoveryRepositoryStub{}
	gateway := &recoveryGatewayStub{}
	operations := &recoveryOperationStub{}
	service := New(recoveryAccountReaderStub{account: account}, repository, recoverySetupStub{}, gateway, nil, operations)
	err := service.Recover(context.Background(), model.ProAccountDraft{
		OperationID: "operation-reset", OperationType: "reset", ProAccountID: account.ID,
		State: model.ProOperationStateCompensating, Version: 2,
		CompensationAction: "resume_or_cleanup", Context: map[string]any{},
	})
	if err != nil {
		t.Fatalf("恢复未知补偿：%v", err)
	}
	if gateway.deletedLocator != "" || repository.softDeleted {
		t.Fatalf("未知补偿不应删除凭证或账号：gateway=%#v repository=%#v", gateway, repository)
	}
	if operations.lastTransition.State != model.ProOperationStateFailed || operations.lastTransition.CompensationAction != "manual_recovery_required" {
		t.Fatalf("未知补偿终态 = %#v", operations.lastTransition)
	}
}
