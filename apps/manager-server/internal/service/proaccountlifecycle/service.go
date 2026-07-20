package proaccountlifecycle

import (
	"context"
	"errors"
	"strings"
	"sync"
	"time"

	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/model"
	proaccountrepo "github.com/seakee/cpa-manager-plus/apps/manager-server/internal/repository/proaccount"
	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/repository/proaccountdraft"
	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/service/proaccountgateway"
	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/service/proaccountoperation"
	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/store"
)

var (
	ErrInvalidRequest            = errors.New("invalid pro account lifecycle request")
	ErrUnsupportedAccountType    = errors.New("unsupported pro account type")
	ErrGatewayCapability         = errors.New("gateway capability is unavailable")
	ErrOperationState            = errors.New("pro account operation state is invalid")
	ErrOperationConflict         = errors.New("pro account operation does not match request")
	ErrResourceVersionConflict   = proaccountrepo.ErrVersionConflict
	ErrCredentialAlreadyBound    = errors.New("api credential is already bound to another account")
	ErrConnectivityFailed        = errors.New("account connectivity test failed")
	ErrOAuthCredentialMissing    = errors.New("oauth draft credential was not found")
	ErrOAuthCredentialAmbiguous  = errors.New("multiple oauth draft credentials require confirmation")
	ErrOAuthCallbackInvalid      = errors.New("oauth callback input is invalid")
	ErrOAuthStateMismatch        = errors.New("oauth callback state does not match operation")
	ErrOAuthIdentityMismatch     = errors.New("oauth replacement identity does not match target account")
	ErrReauthorizationRequired   = errors.New("oauth refresh credential requires reauthorization")
	ErrReauthorizationInProgress = errors.New("another reauthorization is already in progress for this account")
)

type Service struct {
	accounts   AccountReader
	repository AccountRepository
	setup      SetupResolver
	gateway    Gateway
	probe      ProbeService
	operations OperationService
	now        func() time.Time

	// 重新授权会跨多次 HTTP 轮询推进同一操作，必须串行执行，避免并发请求重复认领或清理同一草稿凭据。
	reauthorizationMu sync.Mutex
}

func New(accounts AccountReader, repository AccountRepository, setup SetupResolver, gateway Gateway, probe ProbeService, operations OperationService) *Service {
	return &Service{accounts: accounts, repository: repository, setup: setup, gateway: gateway, probe: probe, operations: operations, now: time.Now}
}

func (s *Service) resolveSetup(ctx context.Context) (store.Setup, error) {
	setup, _, ok, err := s.setup.ResolveSetupWithSource(ctx)
	if err != nil {
		return store.Setup{}, err
	}
	if !ok || strings.TrimSpace(setup.CPAUpstreamURL) == "" || strings.TrimSpace(setup.ManagementKey) == "" {
		return store.Setup{}, errors.New("gateway connection is not configured")
	}
	return setup, nil
}

func (s *Service) start(ctx context.Context, operationID string, idempotencyKey string, operationType string, accountID string, contextValue map[string]any) (model.ProAccountDraft, bool, error) {
	operation, created, err := s.operations.Start(ctx, proaccountoperation.StartInput{
		OperationID: operationID, IdempotencyKey: idempotencyKey,
		OperationType: operationType, ProAccountID: accountID, Context: contextValue,
	})
	if err != nil {
		return operation, false, err
	}
	expectedAccountID := strings.TrimSpace(accountID)
	if !created && (operation.OperationType != operationType || (expectedAccountID != "" && operation.ProAccountID != expectedAccountID)) {
		return operation, false, ErrOperationConflict
	}
	return operation, created, nil
}

func (s *Service) transition(ctx context.Context, operation model.ProAccountDraft, state string, accountID string, contextValue map[string]any, code string, summary string, action string) (model.ProAccountDraft, error) {
	return s.operations.Transition(ctx, operation.OperationID, proaccountoperation.TransitionInput{
		ExpectedVersion: operation.Version, ProAccountID: accountID, State: state,
		RetryCount: operation.RetryCount, CleanupDeadlineMS: operation.CleanupDeadlineMS,
		CompensationAction: action, ErrorCode: code, ErrorSummary: summary, Context: contextValue,
	})
}

func (s *Service) fail(ctx context.Context, operation model.ProAccountDraft, code string, summary string) model.ProAccountDraft {
	failed, err := s.transition(ctx, operation, model.ProOperationStateFailed, operation.ProAccountID, operation.Context, code, summary, operation.CompensationAction)
	if err == nil {
		return failed
	}
	return operation
}

func discoveryFromSnapshot(snapshot proaccountgateway.AccountSnapshot) model.ProAccountDiscovery {
	return model.ProAccountDiscovery{
		Platform: snapshot.Platform, AuthType: snapshot.AuthType, SourceType: snapshot.SourceType,
		PlanType: snapshot.PlanType, Name: snapshot.Name, Email: snapshot.Email, Enabled: snapshot.Enabled,
		HealthStatus: snapshot.HealthStatus, LastError: snapshot.LastError,
		AllowedModels: append([]string(nil), snapshot.AllowedModels...), ModelMapping: cloneMap(snapshot.ModelMapping),
		ModelRuleVersion: snapshot.ModelRuleVersion, ExpiresAtMS: snapshot.ExpiresAtMS,
		AuthIndex: snapshot.AuthIndex, SourceLocator: snapshot.SourceLocator,
		SourceFingerprint: snapshot.SourceFingerprint,
	}
}

func cloneMap(value map[string]string) map[string]string {
	result := make(map[string]string, len(value))
	for key, item := range value {
		result[key] = item
	}
	return result
}

func contextString(value map[string]any, key string) string {
	text, _ := value[key].(string)
	return strings.TrimSpace(text)
}

func operationErrorIsConflict(err error) bool {
	return errors.Is(err, proaccountdraft.ErrIdempotencyConflict) || errors.Is(err, proaccountdraft.ErrOperationIDConflict)
}
