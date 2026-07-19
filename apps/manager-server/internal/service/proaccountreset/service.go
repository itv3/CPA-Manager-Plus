package proaccountreset

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/model"
	proaccountrepo "github.com/seakee/cpa-manager-plus/apps/manager-server/internal/repository/proaccount"
	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/service/proaccountgateway"
	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/service/proaccountoperation"
	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/store"
)

const (
	resetCreditsURL = "https://chatgpt.com/backend-api/wham/rate-limit-reset-credits"
	resetConsumeURL = "https://chatgpt.com/backend-api/wham/rate-limit-reset-credits/consume"
)

var (
	ErrInvalidRequest        = errors.New("invalid openai reset request")
	ErrCapabilityUnavailable = errors.New("openai reset credits capability is unavailable")
	ErrNoCredits             = errors.New("no openai reset credits are available")
	ErrResetFailed           = errors.New("openai rate limit reset failed")
)

type Service struct {
	accounts   AccountReader
	setup      SetupResolver
	gateway    Gateway
	operations OperationService
	now        func() time.Time
}

type accountReference struct {
	authIndex string
	accountID string
}

func New(accounts AccountReader, setup SetupResolver, gateway Gateway, operations OperationService) *Service {
	return &Service{accounts: accounts, setup: setup, gateway: gateway, operations: operations, now: time.Now}
}

func (s *Service) Credits(ctx context.Context, accountID string) (CreditsResult, error) {
	account, err := s.accounts.Get(ctx, strings.TrimSpace(accountID))
	if err != nil {
		return CreditsResult{}, err
	}
	result, _, err := s.queryCredits(ctx, account)
	return result, err
}

func (s *Service) Reset(ctx context.Context, input ResetInput) (ResetResult, error) {
	if !input.Confirmed || strings.TrimSpace(input.OperationID) == "" || strings.TrimSpace(input.IdempotencyKey) == "" || input.ExpectedVersion <= 0 {
		return ResetResult{}, ErrInvalidRequest
	}
	account, err := s.accounts.Get(ctx, strings.TrimSpace(input.AccountID))
	if err != nil {
		return ResetResult{}, err
	}
	operation, created, err := s.operations.Start(ctx, proaccountoperation.StartInput{
		OperationID: input.OperationID, IdempotencyKey: input.IdempotencyKey,
		OperationType: "reset", ProAccountID: account.ID,
		Context: map[string]any{"confirmed": true},
	})
	if err != nil {
		return ResetResult{Operation: operation}, err
	}
	if !created {
		if operation.State == model.ProOperationStateEnabled {
			credits, _, queryErr := s.queryCredits(ctx, account)
			if queryErr != nil {
				credits = creditsRefreshFailed(s.now().UnixMilli())
			}
			return ResetResult{Credits: credits, Operation: operation}, nil
		}
		return ResetResult{Operation: operation}, ErrInvalidRequest
	}
	if account.Version != input.ExpectedVersion {
		operation = s.fail(ctx, operation, "resource_version_conflict", "账号版本已变化")
		return ResetResult{Operation: operation}, proaccountrepo.ErrVersionConflict
	}
	credits, reference, err := s.queryCredits(ctx, account)
	if err != nil {
		operation = s.fail(ctx, operation, "credits_query_failed", "无法查询官方重置次数")
		return ResetResult{Credits: credits, Operation: operation}, err
	}
	if credits.Capability != CapabilitySupported || credits.AvailableCount == nil {
		operation = s.fail(ctx, operation, "reset_credits_unavailable", "该账号未确认支持 reset credits")
		return ResetResult{Credits: credits, Operation: operation}, ErrCapabilityUnavailable
	}
	if *credits.AvailableCount <= 0 {
		operation = s.fail(ctx, operation, "reset_credits_exhausted", "当前没有可用重置次数")
		return ResetResult{Credits: credits, Operation: operation}, ErrNoCredits
	}
	setup, err := s.resolveSetup(ctx)
	if err != nil {
		operation = s.fail(ctx, operation, "gateway_connection_required", "Gateway 连接不可用")
		return ResetResult{Credits: credits, Operation: operation}, err
	}
	call, err := s.gateway.APICall(ctx, setup.CPAUpstreamURL, setup.ManagementKey, proaccountgateway.APICallRequest{
		AuthIndex: reference.authIndex, Method: http.MethodPost, URL: resetConsumeURL,
		Headers: resetCreditsHeaders(reference.accountID), Body: map[string]string{"redeem_request_id": operation.OperationID},
	})
	if err != nil || call.StatusCode < 200 || call.StatusCode >= 300 {
		operation = s.fail(ctx, operation, "reset_request_failed", "官方重置请求失败")
		return ResetResult{Credits: credits, Operation: operation}, ErrResetFailed
	}
	operation, err = s.operations.Transition(ctx, operation.OperationID, proaccountoperation.TransitionInput{
		ExpectedVersion: operation.Version, ProAccountID: account.ID,
		State: model.ProOperationStateTested, Context: operation.Context,
	})
	if err != nil {
		return ResetResult{Credits: credits, Operation: operation}, err
	}
	operation, err = s.operations.Transition(ctx, operation.OperationID, proaccountoperation.TransitionInput{
		ExpectedVersion: operation.Version, ProAccountID: account.ID,
		State: model.ProOperationStateEnabled, Context: operation.Context,
	})
	if err != nil {
		return ResetResult{Credits: credits, Operation: operation}, err
	}
	after, _, queryErr := s.queryCredits(ctx, account)
	if queryErr != nil || after.Capability != CapabilitySupported {
		after = creditsRefreshFailed(s.now().UnixMilli())
	}
	return ResetResult{Credits: after, Operation: operation}, nil
}

func creditsRefreshFailed(nowMS int64) CreditsResult {
	return CreditsResult{
		Capability: CapabilitySupported, Credits: []Credit{}, UpdatedAtMS: nowMS,
		ErrorCode: "credits_refresh_failed", Retryable: true,
	}
}

func (s *Service) queryCredits(ctx context.Context, account model.ProAccount) (CreditsResult, accountReference, error) {
	result := CreditsResult{Capability: CapabilityUnsupported, Credits: []Credit{}, UpdatedAtMS: s.now().UnixMilli()}
	if account.Platform != "openai" || account.AuthType != "oauth" || account.Binding == nil || account.Binding.SourceType != proaccountgateway.SourceAuthFile {
		return result, accountReference{}, nil
	}
	if strings.TrimSpace(account.Binding.AuthIndex) == "" {
		result.Capability = CapabilityUnknown
		result.ErrorCode = "binding_missing"
		return result, accountReference{}, nil
	}
	setup, err := s.resolveSetup(ctx)
	if err != nil {
		return CreditsResult{}, accountReference{}, err
	}
	snapshot, err := s.gateway.Snapshot(ctx, setup.CPAUpstreamURL, setup.ManagementKey)
	if err != nil {
		result.Capability = CapabilityUnknown
		result.ErrorCode = "gateway_snapshot_failed"
		result.Retryable = true
		return result, accountReference{}, nil
	}
	reference := accountReference{authIndex: account.Binding.AuthIndex}
	found := false
	for _, current := range snapshot.Accounts {
		if current.SourceType == account.Binding.SourceType && current.SourceLocator == account.Binding.SourceLocator && current.AuthIndex == account.Binding.AuthIndex {
			reference.accountID = strings.TrimSpace(current.UpstreamAccountID)
			found = true
			break
		}
	}
	if !found {
		result.Capability = CapabilityUnknown
		result.ErrorCode = "gateway_account_missing"
		result.Retryable = true
		return result, accountReference{}, nil
	}
	call, err := s.gateway.APICall(ctx, setup.CPAUpstreamURL, setup.ManagementKey, proaccountgateway.APICallRequest{
		AuthIndex: reference.authIndex, Method: http.MethodGet, URL: resetCreditsURL,
		Headers: resetCreditsHeaders(reference.accountID),
	})
	if err != nil {
		result.Capability = CapabilityUnknown
		result.ErrorCode = "credits_query_failed"
		result.Retryable = true
		return result, reference, nil
	}
	if call.StatusCode == http.StatusNotFound || call.StatusCode == http.StatusMethodNotAllowed || call.StatusCode == http.StatusNotImplemented {
		return result, reference, nil
	}
	if call.StatusCode < 200 || call.StatusCode >= 300 {
		result.Capability = CapabilityUnknown
		result.ErrorCode = classifyStatus(call.StatusCode)
		result.Retryable = call.StatusCode == http.StatusTooManyRequests || call.StatusCode >= 500
		return result, reference, nil
	}
	parsed, ok := parseCredits(call.Body)
	if !ok {
		result.Capability = CapabilityUnknown
		result.ErrorCode = "credits_payload_invalid"
		result.Retryable = true
		return result, reference, nil
	}
	parsed.UpdatedAtMS = s.now().UnixMilli()
	return parsed, reference, nil
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

func (s *Service) fail(ctx context.Context, operation model.ProAccountDraft, code string, summary string) model.ProAccountDraft {
	failed, err := s.operations.Transition(ctx, operation.OperationID, proaccountoperation.TransitionInput{
		ExpectedVersion: operation.Version, ProAccountID: operation.ProAccountID,
		State: model.ProOperationStateFailed, ErrorCode: code, ErrorSummary: summary, Context: operation.Context,
	})
	if err == nil {
		return failed
	}
	return operation
}

func usageHeaders(accountID string) map[string]string {
	result := map[string]string{
		"Authorization": "Bearer $TOKEN$",
		"Content-Type":  "application/json",
		"User-Agent":    "CLIProxyAPI-Pro/1.0",
	}
	if accountID = strings.TrimSpace(accountID); accountID != "" {
		result["Chatgpt-Account-Id"] = accountID
	}
	return result
}

func resetCreditsHeaders(accountID string) map[string]string {
	result := usageHeaders(accountID)
	result["Accept"] = "application/json"
	result["OpenAI-Beta"] = "codex-1"
	result["OAI-Language"] = "zh-CN"
	result["Originator"] = "Codex Desktop"
	result["Sec-Fetch-Site"] = "none"
	result["Sec-Fetch-Mode"] = "no-cors"
	result["Sec-Fetch-Dest"] = "empty"
	result["Priority"] = "u=4, i"
	return result
}

func parseCredits(raw string) (CreditsResult, bool) {
	decoder := json.NewDecoder(strings.NewReader(strings.TrimSpace(raw)))
	decoder.UseNumber()
	var payload any
	if err := decoder.Decode(&payload); err != nil {
		return CreditsResult{}, false
	}

	var rawCount any
	var hasCount bool
	var rawCredits []any
	var hasCredits bool
	switch typed := payload.(type) {
	case []any:
		rawCredits = typed
		hasCredits = true
	case map[string]any:
		rawCount, hasCount = firstValue(typed, "available_count", "availableCount")
		rawCredits, hasCredits = firstCreditsList(typed, "credits", "rate_limit_reset_credits", "items", "data")
	default:
		return CreditsResult{}, false
	}
	if !hasCount && !hasCredits {
		return CreditsResult{}, false
	}
	credits := make([]Credit, 0)
	availableCreditCount := 0
	for _, rawItem := range rawCredits {
		item, ok := rawItem.(map[string]any)
		if !ok {
			continue
		}
		resetType := stringValue(item, "reset_type", "resetType")
		if resetType != "" && !strings.EqualFold(resetType, "codex_rate_limits") {
			continue
		}
		status := stringValue(item, "status")
		if status != "" && !strings.EqualFold(status, "available") {
			continue
		}
		availableCreditCount++
		expiresAt := parseTimeMS(first(item, "expires_at", "expiresAt"))
		if expiresAt <= 0 {
			continue
		}
		credits = append(credits, Credit{ID: stringValue(item, "id"), ExpiresAtMS: expiresAt})
	}
	var count *int
	if hasCount && rawCount != nil {
		parsed, ok := intValue(rawCount)
		if ok && parsed >= 0 {
			count = &parsed
		} else if !hasCredits {
			return CreditsResult{}, false
		}
	}
	if count == nil && hasCredits {
		creditCount := availableCreditCount
		count = &creditCount
	}
	return CreditsResult{Capability: CapabilitySupported, AvailableCount: count, Credits: credits}, true
}

func firstCreditsList(value map[string]any, keys ...string) ([]any, bool) {
	for _, key := range keys {
		item, ok := value[key]
		if !ok || item == nil {
			continue
		}
		items, ok := item.([]any)
		if !ok {
			continue
		}
		return items, true
	}
	return nil, false
}

func firstValue(value map[string]any, keys ...string) (any, bool) {
	for _, key := range keys {
		if item, ok := value[key]; ok {
			return item, true
		}
	}
	return nil, false
}

func first(value map[string]any, keys ...string) any {
	item, _ := firstValue(value, keys...)
	return item
}

func stringValue(value map[string]any, keys ...string) string {
	item, _ := firstValue(value, keys...)
	text, _ := item.(string)
	return strings.TrimSpace(text)
}

func intValue(value any) (int, bool) {
	switch typed := value.(type) {
	case json.Number:
		parsed, err := strconv.Atoi(typed.String())
		return parsed, err == nil
	case float64:
		return int(typed), typed == float64(int(typed))
	case int:
		return typed, true
	case string:
		parsed, err := strconv.Atoi(strings.TrimSpace(typed))
		return parsed, err == nil
	default:
		return 0, false
	}
}

func parseTimeMS(value any) int64 {
	switch typed := value.(type) {
	case string:
		if parsed, err := time.Parse(time.RFC3339, strings.TrimSpace(typed)); err == nil {
			return parsed.UnixMilli()
		}
	case json.Number:
		if parsed, err := typed.Int64(); err == nil {
			if parsed < 100000000000 {
				return parsed * 1000
			}
			return parsed
		}
	case float64:
		parsed := int64(typed)
		if parsed < 100000000000 {
			return parsed * 1000
		}
		return parsed
	}
	return 0
}

func classifyStatus(status int) string {
	if status == http.StatusUnauthorized || status == http.StatusForbidden {
		return "authentication_failed"
	}
	if status == http.StatusTooManyRequests {
		return "rate_limited"
	}
	if status >= 500 {
		return "upstream_unavailable"
	}
	return "credits_query_failed"
}
