package proaccountrebind

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/model"
	proaccountrepo "github.com/seakee/cpa-manager-plus/apps/manager-server/internal/repository/proaccount"
	proaccountsvc "github.com/seakee/cpa-manager-plus/apps/manager-server/internal/service/proaccount"
	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/service/proaccountgateway"
	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/service/proaccountoperation"
)

var ErrInvalidRequest = errors.New("invalid pro account rebind request")

type Service struct {
	accounts   AccountReader
	repository Repository
	setup      SetupResolver
	gateway    Gateway
	operations OperationService
	now        func() time.Time
}

func New(accounts AccountReader, repository Repository, setup SetupResolver, gateway Gateway, operations OperationService) *Service {
	return &Service{accounts: accounts, repository: repository, setup: setup, gateway: gateway, operations: operations, now: time.Now}
}

func (s *Service) List(ctx context.Context, limit int) ([]Review, error) {
	items, err := s.repository.ListBindingReviews(ctx, nil, limit)
	if err != nil {
		return nil, err
	}
	result := make([]Review, 0, len(items))
	for _, item := range items {
		view := Review{Review: item, Candidates: []model.ProAccount{}}
		for _, candidateID := range item.CandidateIDs {
			account, getErr := s.accounts.Get(ctx, candidateID)
			if getErr == nil && account.DeletedAtMS == 0 {
				view.Candidates = append(view.Candidates, account)
			}
		}
		result = append(result, view)
	}
	return result, nil
}

func (s *Service) Confirm(ctx context.Context, input ConfirmInput) (Result, error) {
	input.OperationID = strings.TrimSpace(input.OperationID)
	input.IdempotencyKey = strings.TrimSpace(input.IdempotencyKey)
	if input.OperationID == "" || input.IdempotencyKey == "" || len(input.Items) == 0 || len(input.Items) > 100 {
		return Result{}, ErrInvalidRequest
	}
	setup, _, ok, err := s.setup.ResolveSetupWithSource(ctx)
	if err != nil {
		return Result{}, err
	}
	if !ok || strings.TrimSpace(setup.CPAUpstreamURL) == "" || strings.TrimSpace(setup.ManagementKey) == "" {
		return Result{}, errors.New("gateway connection is not configured")
	}
	snapshot, err := s.gateway.Snapshot(ctx, setup.CPAUpstreamURL, setup.ManagementKey)
	if err != nil {
		return Result{}, err
	}
	discoveries := make(map[string]proaccountgateway.AccountSnapshot, len(snapshot.Accounts))
	for _, account := range snapshot.Accounts {
		discoveries[discoveryKey(account.SourceType, account.SourceLocator, account.AuthIndex)] = account
	}
	targetCounts := map[string]int{}
	for _, item := range input.Items {
		targetCounts[strings.TrimSpace(item.ProAccountID)]++
	}
	result := Result{Total: len(input.Items), Items: make([]ItemResult, 0, len(input.Items))}
	for _, item := range input.Items {
		if targetCounts[strings.TrimSpace(item.ProAccountID)] > 1 {
			result.Items = append(result.Items, ItemResult{
				ReviewID: item.ReviewID, ProAccountID: item.ProAccountID,
				Code: "duplicate_rebind_target", Message: "同一批次不能把多个发现项绑定到同一账号",
			})
			result.Failed++
			continue
		}
		itemResult := s.confirmOne(ctx, input, item, discoveries)
		result.Items = append(result.Items, itemResult)
		if itemResult.Success {
			result.Succeeded++
		} else {
			result.Failed++
		}
	}
	return result, nil
}

func (s *Service) confirmOne(ctx context.Context, input ConfirmInput, item ConfirmItem, discoveries map[string]proaccountgateway.AccountSnapshot) ItemResult {
	result := ItemResult{ReviewID: item.ReviewID, ProAccountID: strings.TrimSpace(item.ProAccountID)}
	if item.ReviewID <= 0 || result.ProAccountID == "" || item.ExpectedVersion <= 0 {
		result.Code = "invalid_rebind_item"
		result.Message = "重绑项目缺少必要字段"
		return result
	}
	review, ok, err := s.repository.GetBindingReview(ctx, item.ReviewID)
	if err != nil || !ok {
		result.Code = "binding_review_not_found"
		result.Message = "待确认绑定不存在"
		result.Retryable = err != nil
		return result
	}
	if !contains(review.CandidateIDs, result.ProAccountID) {
		result.Code = "invalid_rebind_candidate"
		result.Message = "所选账号不在待确认候选中"
		return result
	}
	account, err := s.accounts.Get(ctx, result.ProAccountID)
	if err != nil || account.DeletedAtMS != 0 {
		result.Code = "pro_account_not_found"
		result.Message = "统一账号不存在或已删除"
		return result
	}
	discoverySnapshot, ok := discoveries[discoveryKey(review.SourceType, review.SourceLocator, review.AuthIndex)]
	if !ok {
		result.Code = "binding_discovery_missing"
		result.Message = "Gateway 当前快照中已找不到该发现项，请重新同步"
		result.Retryable = true
		return result
	}
	operationID := childIdentifier(input.OperationID, "review", strconv.FormatInt(item.ReviewID, 10), 128)
	idempotencyKey := childIdentifier(input.IdempotencyKey, "review", strconv.FormatInt(item.ReviewID, 10), 256)
	operation, created, err := s.operations.Start(ctx, proaccountoperation.StartInput{
		OperationID: operationID, IdempotencyKey: idempotencyKey, OperationType: "rebind",
		ProAccountID: account.ID, Context: map[string]any{"reviewId": item.ReviewID},
	})
	if err != nil {
		result.Code, result.Message, result.Retryable = classifyError(err)
		return result
	}
	if !created {
		if operation.State == model.ProOperationStateEnabled {
			result.Success = true
			result.Account = &account
			return result
		}
		result.Code = "operation_state_conflict"
		result.Message = "重绑操作正在处理或已失败"
		result.Retryable = operation.State == model.ProOperationStateCompensating
		return result
	}
	discovery := discoveryFromSnapshot(discoverySnapshot)
	account, err = s.repository.RebindFromReview(ctx, review.ID, account.ID, item.ExpectedVersion, discovery, s.now().UnixMilli())
	if err != nil {
		return s.failItem(ctx, operation, result, err)
	}
	operation, err = s.operations.Transition(ctx, operation.OperationID, proaccountoperation.TransitionInput{
		ExpectedVersion: operation.Version, ProAccountID: account.ID, State: model.ProOperationStateTested,
		Context: operation.Context,
	})
	if err != nil {
		result.Code, result.Message, result.Retryable = classifyError(err)
		return result
	}
	_, err = s.operations.Transition(ctx, operation.OperationID, proaccountoperation.TransitionInput{
		ExpectedVersion: operation.Version, ProAccountID: account.ID, State: model.ProOperationStateEnabled,
		Context: operation.Context,
	})
	if err != nil {
		result.Code, result.Message, result.Retryable = classifyError(err)
		return result
	}
	result.Success = true
	result.Account = &account
	return result
}

func (s *Service) failItem(ctx context.Context, operation model.ProAccountDraft, result ItemResult, cause error) ItemResult {
	code, message, retryable := classifyError(cause)
	_, _ = s.operations.Transition(ctx, operation.OperationID, proaccountoperation.TransitionInput{
		ExpectedVersion: operation.Version, ProAccountID: operation.ProAccountID,
		State: model.ProOperationStateFailed, ErrorCode: code, ErrorSummary: message, Context: operation.Context,
	})
	result.Code = code
	result.Message = message
	result.Retryable = retryable
	return result
}

func classifyError(err error) (string, string, bool) {
	switch {
	case errors.Is(err, proaccountrepo.ErrVersionConflict):
		return "resource_version_conflict", "账号版本已变化，请刷新后重试", false
	case errors.Is(err, proaccountrepo.ErrBindingReviewCandidateInvalid):
		return "invalid_rebind_candidate", "所选账号不在待确认候选中", false
	case errors.Is(err, proaccountrepo.ErrBindingReviewStateConflict):
		return "binding_review_state_conflict", "待确认绑定状态已变化", false
	case errors.Is(err, proaccountsvc.ErrAccountNotFound), errors.Is(err, proaccountrepo.ErrAccountNotFound):
		return "pro_account_not_found", "统一账号不存在", false
	case errors.Is(err, proaccountoperation.ErrOperationVersionConflict):
		return "operation_version_conflict", "操作状态已变化", true
	default:
		return "rebind_failed", "账号重绑失败", true
	}
}

func discoveryKey(sourceType string, sourceLocator string, authIndex string) string {
	return strings.TrimSpace(sourceType) + "\x00" + strings.TrimSpace(sourceLocator) + "\x00" + strings.TrimSpace(authIndex)
}

func discoveryFromSnapshot(snapshot proaccountgateway.AccountSnapshot) model.ProAccountDiscovery {
	return model.ProAccountDiscovery{
		Platform: snapshot.Platform, AuthType: snapshot.AuthType, SourceType: snapshot.SourceType,
		Name: snapshot.Name, Email: snapshot.Email, Enabled: snapshot.Enabled,
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

func contains(values []string, expected string) bool {
	for _, value := range values {
		if value == expected {
			return true
		}
	}
	return false
}

func childIdentifier(base string, kind string, value string, maxLength int) string {
	suffix := ":" + kind + ":" + value
	base = strings.TrimSpace(base)
	if len(base)+len(suffix) > maxLength {
		base = strings.TrimRight(base[:maxLength-len(suffix)], ":._-")
	}
	return fmt.Sprintf("%s%s", base, suffix)
}
