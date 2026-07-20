package proaccounttest

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/model"
	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/service/managerconfig"
	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/service/proaccountgateway"
	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/service/proaccountoperation"
	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/store"
)

var (
	ErrModelNotAllowed         = errors.New("test model is not allowed for this account")
	ErrInvalidTestMode         = errors.New("account test mode is invalid")
	ErrAccountBindingMissing   = errors.New("pro account binding is unavailable")
	ErrResourceVersionConflict = errors.New("pro account version conflict")
)

type Input struct {
	AccountID       string
	OperationID     string
	IdempotencyKey  string
	ExpectedVersion int64
	Model           string
	Mode            string
}

type Result struct {
	Connectivity proaccountgateway.ConnectivityResult `json:"connectivity"`
	Operation    model.ProAccountDraft                `json:"operation"`
	Account      model.ProAccount                     `json:"account"`
}

type AccountReader interface {
	Get(ctx context.Context, id string) (model.ProAccount, error)
}

type AccountRepository interface {
	RecordTestResult(ctx context.Context, accountID string, success bool, errorCode string, nowMS int64) (model.ProAccount, error)
}

type SetupResolver interface {
	ResolveSetupWithSource(ctx context.Context) (store.Setup, managerconfig.Source, bool, error)
}

type Gateway interface {
	TestAccountWithMode(ctx context.Context, gatewayBaseURL string, managementKey string, account proaccountgateway.AccountReference, modelName string, mode string) (proaccountgateway.ConnectivityResult, error)
}

type OperationService interface {
	Start(ctx context.Context, input proaccountoperation.StartInput) (model.ProAccountDraft, bool, error)
	Transition(ctx context.Context, operationID string, input proaccountoperation.TransitionInput) (model.ProAccountDraft, error)
}

type Service struct {
	accounts   AccountReader
	repository AccountRepository
	setup      SetupResolver
	gateway    Gateway
	operations OperationService
	now        func() time.Time
}

func New(accounts AccountReader, repository AccountRepository, setup SetupResolver, gateway Gateway, operations OperationService) *Service {
	return &Service{accounts: accounts, repository: repository, setup: setup, gateway: gateway, operations: operations, now: time.Now}
}

func (s *Service) Test(ctx context.Context, input Input) (Result, error) {
	account, err := s.accounts.Get(ctx, strings.TrimSpace(input.AccountID))
	if err != nil {
		return Result{}, err
	}
	mode := strings.ToLower(strings.TrimSpace(input.Mode))
	if mode == "" {
		mode = proaccountgateway.ConnectivityModeDefault
	}
	operation, created, err := s.operations.Start(ctx, proaccountoperation.StartInput{
		OperationID: input.OperationID, IdempotencyKey: input.IdempotencyKey,
		OperationType: "test", ProAccountID: account.ID, Context: map[string]any{"model": input.Model, "mode": mode},
	})
	if err != nil {
		return Result{}, err
	}
	if !created {
		if operation.State == model.ProOperationStateEnabled {
			return Result{Account: account, Operation: operation}, nil
		}
		return Result{Account: account, Operation: operation}, errors.New("account test operation is not restartable")
	}
	if input.ExpectedVersion <= 0 || input.ExpectedVersion != account.Version {
		operation = s.fail(ctx, operation, "version_conflict")
		return Result{Operation: operation}, ErrResourceVersionConflict
	}
	if mode != proaccountgateway.ConnectivityModeDefault && mode != proaccountgateway.ConnectivityModeCompact {
		operation = s.fail(ctx, operation, "invalid_test_mode")
		return Result{Operation: operation}, ErrInvalidTestMode
	}
	if account.Binding == nil || account.Binding.AuthIndex == "" || account.Binding.SourceLocator == "" {
		operation = s.fail(ctx, operation, "binding_missing")
		return Result{Operation: operation}, ErrAccountBindingMissing
	}
	// Compact 仅 Responses(codex)账号支持,提前拦截避免对 Chat Completions 账号发出无效的 compact 请求
	if mode == proaccountgateway.ConnectivityModeCompact &&
		(!strings.EqualFold(account.Platform, "openai") || account.Binding.SourceType != proaccountgateway.SourceCodexAPIKey) {
		operation = s.fail(ctx, operation, "invalid_test_mode")
		return Result{Operation: operation}, ErrInvalidTestMode
	}
	rules, err := proaccountgateway.NormalizeModelRules(proaccountgateway.ModelRules{AllowedModels: account.AllowedModels, ModelMapping: account.ModelMapping})
	if err != nil {
		operation = s.fail(ctx, operation, "invalid_model_rules")
		return Result{Operation: operation}, err
	}
	clientModel := strings.TrimSpace(input.Model)
	if !proaccountgateway.ModelAllowed(clientModel, rules) {
		operation = s.fail(ctx, operation, "model_not_allowed")
		return Result{Operation: operation}, ErrModelNotAllowed
	}
	mappedModel := proaccountgateway.ResolveMappedModel(clientModel, rules)
	setup, _, ok, err := s.setup.ResolveSetupWithSource(ctx)
	if err != nil || !ok {
		operation = s.fail(ctx, operation, "gateway_connection_required")
		if err == nil {
			err = errors.New("gateway connection is not configured")
		}
		return Result{Operation: operation}, err
	}
	connectivity, err := s.gateway.TestAccountWithMode(ctx, setup.CPAUpstreamURL, setup.ManagementKey, proaccountgateway.AccountReference{
		Platform: account.Platform, AuthType: account.AuthType, SourceType: account.Binding.SourceType,
		SourceLocator: account.Binding.SourceLocator, AuthIndex: account.Binding.AuthIndex,
	}, mappedModel, mode)
	connectivity.Model = clientModel
	connectivity.MappedModel = mappedModel
	if connectivity.UpstreamModel == "" {
		connectivity.UpstreamModel = mappedModel
	}
	if err != nil {
		operation = s.fail(ctx, operation, "connectivity_request_failed")
		if updated, updateErr := s.repository.RecordTestResult(ctx, account.ID, false, "connectivity_request_failed", s.now().UnixMilli()); updateErr == nil {
			account = updated
		}
		return Result{Account: account, Operation: operation}, err
	}
	if !connectivity.Success {
		operation = s.fail(ctx, operation, connectivity.ErrorCode)
		if updated, updateErr := s.repository.RecordTestResult(ctx, account.ID, false, connectivity.ErrorCode, s.now().UnixMilli()); updateErr == nil {
			account = updated
		}
		return Result{Account: account, Connectivity: connectivity, Operation: operation}, nil
	}
	operation, err = s.operations.Transition(ctx, operation.OperationID, proaccountoperation.TransitionInput{
		ExpectedVersion: operation.Version, State: model.ProOperationStateTested, Context: operation.Context,
	})
	if err != nil {
		return Result{Account: account, Connectivity: connectivity, Operation: operation}, err
	}
	if updated, updateErr := s.repository.RecordTestResult(ctx, account.ID, true, "", s.now().UnixMilli()); updateErr == nil {
		account = updated
	}
	operation, err = s.operations.Transition(ctx, operation.OperationID, proaccountoperation.TransitionInput{
		ExpectedVersion: operation.Version, State: model.ProOperationStateEnabled, Context: operation.Context,
	})
	return Result{Account: account, Connectivity: connectivity, Operation: operation}, err
}

func (s *Service) fail(ctx context.Context, operation model.ProAccountDraft, code string) model.ProAccountDraft {
	failed, err := s.operations.Transition(ctx, operation.OperationID, proaccountoperation.TransitionInput{
		ExpectedVersion: operation.Version, State: model.ProOperationStateFailed,
		ErrorCode: code, ErrorSummary: code, Context: operation.Context,
	})
	if err == nil {
		return failed
	}
	return operation
}
