package proaccountmodels

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/model"
	proaccountrepo "github.com/seakee/cpa-manager-plus/apps/manager-server/internal/repository/proaccount"
	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/service/managerconfig"
	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/service/proaccountgateway"
	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/service/proaccountoperation"
	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/store"
)

var (
	ErrAccountBindingMissing   = errors.New("pro account binding is unavailable")
	ErrOperationInProgress     = errors.New("pro account operation is already in progress")
	ErrOperationAlreadyFailed  = errors.New("pro account operation has already failed")
	ErrResourceVersionConflict = proaccountrepo.ErrVersionConflict
)

type Input struct {
	AccountID       string
	OperationID     string
	IdempotencyKey  string
	ExpectedVersion int64
	AllowedModels   []string
	ModelMapping    map[string]string
}

type Result struct {
	Account   model.ProAccount      `json:"account"`
	Operation model.ProAccountDraft `json:"operation"`
}

type AccountReader interface {
	Get(ctx context.Context, id string) (model.ProAccount, error)
}

type AccountRepository interface {
	UpdateModelRules(ctx context.Context, accountID string, expectedVersion int64, allowedModels []string, modelMapping map[string]string, modelRuleVersion string, nowMS int64) (model.ProAccount, error)
}

type SetupResolver interface {
	ResolveSetupWithSource(ctx context.Context) (store.Setup, managerconfig.Source, bool, error)
}

type RuleGateway interface {
	WriteAndVerifyModelRules(ctx context.Context, baseURL string, managementKey string, sourceType string, sourceLocator string, desired proaccountgateway.ModelRules) (proaccountgateway.ModelRules, proaccountgateway.ModelRules, error)
	RestoreModelRules(ctx context.Context, baseURL string, managementKey string, sourceType string, sourceLocator string, previous proaccountgateway.ModelRules) error
}

type OperationService interface {
	Start(ctx context.Context, input proaccountoperation.StartInput) (model.ProAccountDraft, bool, error)
	Transition(ctx context.Context, operationID string, input proaccountoperation.TransitionInput) (model.ProAccountDraft, error)
}

type Service struct {
	accounts   AccountReader
	repository AccountRepository
	setup      SetupResolver
	gateway    RuleGateway
	operations OperationService
	now        func() time.Time
}

func New(accounts AccountReader, repository AccountRepository, setup SetupResolver, gateway RuleGateway, operations OperationService) *Service {
	return &Service{accounts: accounts, repository: repository, setup: setup, gateway: gateway, operations: operations, now: time.Now}
}

func (s *Service) Update(ctx context.Context, input Input) (Result, error) {
	input.AccountID = strings.TrimSpace(input.AccountID)
	account, err := s.accounts.Get(ctx, input.AccountID)
	if err != nil {
		return Result{}, err
	}
	operation, created, err := s.operations.Start(ctx, proaccountoperation.StartInput{
		OperationID: input.OperationID, IdempotencyKey: input.IdempotencyKey,
		OperationType: "model_update", ProAccountID: account.ID,
		Context: map[string]any{"sourceType": account.SourceType, "sourceLocator": bindingLocator(account)},
	})
	if err != nil {
		return Result{}, err
	}
	if !created {
		switch operation.State {
		case model.ProOperationStateEnabled:
			current, getErr := s.accounts.Get(ctx, account.ID)
			return Result{Account: current, Operation: operation}, getErr
		case model.ProOperationStateFailed, model.ProOperationStateCancelled:
			return Result{Operation: operation}, ErrOperationAlreadyFailed
		default:
			return Result{Operation: operation}, ErrOperationInProgress
		}
	}
	if input.ExpectedVersion <= 0 || input.ExpectedVersion != account.Version {
		operation = s.fail(ctx, operation, "version_conflict", "账号版本已变化")
		return Result{Operation: operation}, ErrResourceVersionConflict
	}
	if account.Binding == nil || strings.TrimSpace(account.Binding.SourceLocator) == "" {
		operation = s.fail(ctx, operation, "binding_missing", "账号缺少当前底层绑定")
		return Result{Operation: operation}, ErrAccountBindingMissing
	}
	desired, err := proaccountgateway.NormalizeModelRules(proaccountgateway.ModelRules{AllowedModels: input.AllowedModels, ModelMapping: input.ModelMapping})
	if err != nil {
		operation = s.fail(ctx, operation, "invalid_model_rules", "模型规则无效")
		return Result{Operation: operation}, err
	}
	setup, _, ok, err := s.setup.ResolveSetupWithSource(ctx)
	if err != nil || !ok {
		operation = s.fail(ctx, operation, "gateway_connection_required", "Gateway 连接尚未配置")
		if err == nil {
			err = errors.New("gateway connection is not configured")
		}
		return Result{Operation: operation}, err
	}
	previous, applied, err := s.gateway.WriteAndVerifyModelRules(ctx, setup.CPAUpstreamURL, setup.ManagementKey, account.Binding.SourceType, account.Binding.SourceLocator, desired)
	if err != nil {
		operation = s.compensate(ctx, operation, setup, account, previous, "gateway_rule_write_failed", err)
		return Result{Operation: operation}, err
	}
	operation, err = s.operations.Transition(ctx, operation.OperationID, proaccountoperation.TransitionInput{
		ExpectedVersion: operation.Version, State: model.ProOperationStateModelsConfigured,
		Context: map[string]any{
			"sourceType": account.Binding.SourceType, "sourceLocator": account.Binding.SourceLocator,
			"modelRuleVersion":      applied.ModelRuleVersion,
			"previousAllowedModels": previous.AllowedModels, "previousModelMapping": previous.ModelMapping,
		},
	})
	if err != nil {
		_ = s.gateway.RestoreModelRules(ctx, setup.CPAUpstreamURL, setup.ManagementKey, account.Binding.SourceType, account.Binding.SourceLocator, previous)
		return Result{}, err
	}
	updated, err := s.repository.UpdateModelRules(ctx, account.ID, input.ExpectedVersion, applied.AllowedModels, applied.ModelMapping, applied.ModelRuleVersion, s.now().UnixMilli())
	if err != nil {
		operation = s.compensate(ctx, operation, setup, account, previous, "manager_rule_commit_failed", err)
		return Result{Operation: operation}, err
	}
	operation, err = s.operations.Transition(ctx, operation.OperationID, proaccountoperation.TransitionInput{
		ExpectedVersion: operation.Version, State: model.ProOperationStateEnabled, Context: operation.Context,
	})
	if err != nil {
		return Result{Account: updated, Operation: operation}, err
	}
	return Result{Account: updated, Operation: operation}, nil
}

func (s *Service) compensate(ctx context.Context, operation model.ProAccountDraft, setup store.Setup, account model.ProAccount, previous proaccountgateway.ModelRules, code string, cause error) model.ProAccountDraft {
	contextValue := map[string]any{
		"sourceType": account.Binding.SourceType, "sourceLocator": account.Binding.SourceLocator,
		"previousAllowedModels": previous.AllowedModels, "previousModelMapping": previous.ModelMapping,
	}
	transitioned, err := s.operations.Transition(ctx, operation.OperationID, proaccountoperation.TransitionInput{
		ExpectedVersion: operation.Version, State: model.ProOperationStateCompensating,
		CompensationAction: "restore_model_rules", ErrorCode: code,
		ErrorSummary: safeCause(cause), Context: contextValue,
	})
	if err != nil {
		return operation
	}
	operation = transitioned
	if strings.TrimSpace(previous.ModelRuleVersion) == "" {
		return operation
	}
	if err := s.gateway.RestoreModelRules(ctx, setup.CPAUpstreamURL, setup.ManagementKey, account.Binding.SourceType, account.Binding.SourceLocator, previous); err != nil {
		return operation
	}
	failed, err := s.operations.Transition(ctx, operation.OperationID, proaccountoperation.TransitionInput{
		ExpectedVersion: operation.Version, State: model.ProOperationStateFailed,
		CompensationAction: "restore_model_rules_completed", ErrorCode: code,
		ErrorSummary: safeCause(cause), Context: contextValue,
	})
	if err == nil {
		operation = failed
	}
	return operation
}

func (s *Service) fail(ctx context.Context, operation model.ProAccountDraft, code string, summary string) model.ProAccountDraft {
	failed, err := s.operations.Transition(ctx, operation.OperationID, proaccountoperation.TransitionInput{
		ExpectedVersion: operation.Version, State: model.ProOperationStateFailed,
		ErrorCode: code, ErrorSummary: summary, Context: operation.Context,
	})
	if err == nil {
		return failed
	}
	return operation
}

func bindingLocator(account model.ProAccount) string {
	if account.Binding == nil {
		return ""
	}
	return account.Binding.SourceLocator
}

func safeCause(err error) string {
	if err == nil {
		return ""
	}
	return fmt.Sprintf("%T", err)
}
