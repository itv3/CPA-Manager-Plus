package proaccountbatch

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/model"
	proaccountrepo "github.com/seakee/cpa-manager-plus/apps/manager-server/internal/repository/proaccount"
	proaccountsvc "github.com/seakee/cpa-manager-plus/apps/manager-server/internal/service/proaccount"
	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/service/proaccountgateway"
	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/service/proaccountlifecycle"
	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/service/proaccounttest"
)

var ErrInvalidRequest = errors.New("invalid pro account batch request")

type Item struct {
	ProAccountID    string `json:"proAccountId"`
	ExpectedVersion int64  `json:"expectedVersion"`
	Model           string `json:"model,omitempty"`
}

type Input struct {
	OperationID    string
	IdempotencyKey string
	Action         string
	Items          []Item
}

type ItemResult struct {
	ProAccountID string                                `json:"proAccountId"`
	Success      bool                                  `json:"success"`
	Code         string                                `json:"code,omitempty"`
	Message      string                                `json:"message,omitempty"`
	Retryable    bool                                  `json:"retryable"`
	Account      *model.ProAccount                     `json:"account,omitempty"`
	Connectivity *proaccountgateway.ConnectivityResult `json:"connectivity,omitempty"`
}

type Result struct {
	Action    string       `json:"action"`
	Total     int          `json:"total"`
	Succeeded int          `json:"succeeded"`
	Failed    int          `json:"failed"`
	Items     []ItemResult `json:"items"`
}

type Lifecycle interface {
	SetEnabled(ctx context.Context, input proaccountlifecycle.MutationInput, enabled bool) (proaccountlifecycle.Result, error)
	Delete(ctx context.Context, input proaccountlifecycle.MutationInput) (proaccountlifecycle.Result, error)
}

type Tester interface {
	Test(ctx context.Context, input proaccounttest.Input) (proaccounttest.Result, error)
}

type Service struct {
	lifecycle Lifecycle
	tester    Tester
}

func New(lifecycle Lifecycle, tester Tester) *Service {
	return &Service{lifecycle: lifecycle, tester: tester}
}

func (s *Service) Execute(ctx context.Context, input Input) (Result, error) {
	input.OperationID = strings.TrimSpace(input.OperationID)
	input.IdempotencyKey = strings.TrimSpace(input.IdempotencyKey)
	input.Action = strings.ToLower(strings.TrimSpace(input.Action))
	if input.OperationID == "" || input.IdempotencyKey == "" || len(input.Items) == 0 || len(input.Items) > 100 || !validAction(input.Action) {
		return Result{}, ErrInvalidRequest
	}
	result := Result{Action: input.Action, Total: len(input.Items), Items: make([]ItemResult, 0, len(input.Items))}
	seen := map[string]struct{}{}
	for index, item := range input.Items {
		item.ProAccountID = strings.TrimSpace(item.ProAccountID)
		if item.ProAccountID == "" || item.ExpectedVersion <= 0 {
			result.Items = append(result.Items, ItemResult{ProAccountID: item.ProAccountID, Code: "invalid_batch_item", Message: "批量操作项目缺少必要字段"})
			result.Failed++
			continue
		}
		if _, exists := seen[item.ProAccountID]; exists {
			result.Items = append(result.Items, ItemResult{ProAccountID: item.ProAccountID, Code: "duplicate_batch_item", Message: "同一账号不能在批次中重复出现"})
			result.Failed++
			continue
		}
		seen[item.ProAccountID] = struct{}{}
		operationID := childIdentifier(input.OperationID, index, 128)
		idempotencyKey := childIdentifier(input.IdempotencyKey, index, 256)
		itemResult := s.executeOne(ctx, input.Action, item, operationID, idempotencyKey)
		result.Items = append(result.Items, itemResult)
		if itemResult.Success {
			result.Succeeded++
		} else {
			result.Failed++
		}
	}
	return result, nil
}

func (s *Service) executeOne(ctx context.Context, action string, item Item, operationID string, idempotencyKey string) ItemResult {
	result := ItemResult{ProAccountID: item.ProAccountID}
	switch action {
	case "enable", "disable":
		lifecycleResult, err := s.lifecycle.SetEnabled(ctx, proaccountlifecycle.MutationInput{
			AccountID: item.ProAccountID, OperationID: operationID, IdempotencyKey: idempotencyKey,
			ExpectedVersion: item.ExpectedVersion,
		}, action == "enable")
		if err != nil {
			result.Code, result.Message, result.Retryable = classifyError(err)
			return result
		}
		result.Success = true
		result.Account = &lifecycleResult.Account
		return result
	case "delete":
		lifecycleResult, err := s.lifecycle.Delete(ctx, proaccountlifecycle.MutationInput{
			AccountID: item.ProAccountID, OperationID: operationID, IdempotencyKey: idempotencyKey,
			ExpectedVersion: item.ExpectedVersion,
		})
		if err != nil {
			result.Code, result.Message, result.Retryable = classifyError(err)
			return result
		}
		result.Success = true
		result.Account = &lifecycleResult.Account
		return result
	case "test":
		testResult, err := s.tester.Test(ctx, proaccounttest.Input{
			AccountID: item.ProAccountID, OperationID: operationID, IdempotencyKey: idempotencyKey,
			ExpectedVersion: item.ExpectedVersion, Model: strings.TrimSpace(item.Model),
		})
		result.Connectivity = &testResult.Connectivity
		if err != nil {
			result.Code, result.Message, result.Retryable = classifyError(err)
			return result
		}
		if !testResult.Connectivity.Success && testResult.Operation.State != model.ProOperationStateEnabled {
			result.Code = valueOr(testResult.Connectivity.ErrorCode, "connectivity_test_failed")
			result.Message = "账号连通性测试失败"
			result.Retryable = testResult.Connectivity.Retryable
			return result
		}
		result.Success = true
		return result
	default:
		result.Code = "invalid_batch_action"
		result.Message = "批量操作类型不受支持"
		return result
	}
}

func validAction(action string) bool {
	return action == "enable" || action == "disable" || action == "test" || action == "delete"
}

func childIdentifier(base string, index int, maxLength int) string {
	suffix := fmt.Sprintf(":item:%d", index)
	if len(base)+len(suffix) > maxLength {
		base = strings.TrimRight(base[:maxLength-len(suffix)], ":._-")
	}
	return base + suffix
}

func classifyError(err error) (string, string, bool) {
	switch {
	case errors.Is(err, proaccountrepo.ErrVersionConflict), errors.Is(err, proaccountlifecycle.ErrResourceVersionConflict), errors.Is(err, proaccounttest.ErrResourceVersionConflict):
		return "resource_version_conflict", "账号版本已变化，请刷新后重试", false
	case errors.Is(err, proaccountsvc.ErrAccountNotFound), errors.Is(err, proaccountrepo.ErrAccountNotFound):
		return "pro_account_not_found", "统一账号不存在", false
	case errors.Is(err, proaccounttest.ErrModelNotAllowed):
		return "model_not_allowed", "测试模型不在账号有效白名单内", false
	case errors.Is(err, proaccounttest.ErrAccountBindingMissing):
		return "binding_missing", "账号当前绑定不可用", false
	case errors.Is(err, proaccountlifecycle.ErrUnsupportedAccountType):
		return "unsupported_account_type", "账号类型不支持该操作", false
	case errors.Is(err, proaccountlifecycle.ErrOperationState):
		return "operation_state_conflict", "账号已有未完成或失败操作", true
	default:
		return "account_operation_failed", "账号操作失败", true
	}
}

func valueOr(value string, fallback string) string {
	if strings.TrimSpace(value) != "" {
		return value
	}
	return fallback
}
