package proaccountreset

import (
	"context"
	"net/http"
	"testing"

	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/model"
	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/service/managerconfig"
	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/service/proaccountgateway"
	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/service/proaccountoperation"
	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/store"
)

type resetAccountReaderStub struct {
	account model.ProAccount
}

func (s resetAccountReaderStub) Get(context.Context, string) (model.ProAccount, error) {
	return s.account, nil
}

type resetSetupStub struct{}

func (resetSetupStub) ResolveSetupWithSource(context.Context) (store.Setup, managerconfig.Source, bool, error) {
	return store.Setup{CPAUpstreamURL: "http://gateway.test", ManagementKey: "management-key"}, managerconfig.SourceDB, true, nil
}

type resetGatewayStub struct {
	snapshot  proaccountgateway.SnapshotResult
	responses []proaccountgateway.APICallResult
	calls     []proaccountgateway.APICallRequest
}

func (s *resetGatewayStub) Snapshot(context.Context, string, string) (proaccountgateway.SnapshotResult, error) {
	return s.snapshot, nil
}

func (s *resetGatewayStub) APICall(_ context.Context, _ string, _ string, input proaccountgateway.APICallRequest) (proaccountgateway.APICallResult, error) {
	s.calls = append(s.calls, input)
	if len(s.responses) == 0 {
		return proaccountgateway.APICallResult{StatusCode: http.StatusInternalServerError}, nil
	}
	result := s.responses[0]
	s.responses = s.responses[1:]
	return result, nil
}

type resetOperationsStub struct {
	items map[string]model.ProAccountDraft
}

func (s *resetOperationsStub) Start(_ context.Context, input proaccountoperation.StartInput) (model.ProAccountDraft, bool, error) {
	if existing, ok := s.items[input.OperationID]; ok {
		return existing, false, nil
	}
	item := model.ProAccountDraft{
		OperationID: input.OperationID, IdempotencyKey: input.IdempotencyKey, OperationType: input.OperationType,
		ProAccountID: input.ProAccountID, State: model.ProOperationStateDraftCreated, Version: 1, Context: input.Context,
	}
	s.items[input.OperationID] = item
	return item, true, nil
}

func (s *resetOperationsStub) Transition(_ context.Context, operationID string, input proaccountoperation.TransitionInput) (model.ProAccountDraft, error) {
	item := s.items[operationID]
	item.State = input.State
	item.Version++
	item.ErrorCode = input.ErrorCode
	item.Context = input.Context
	s.items[operationID] = item
	return item, nil
}

func resetEligibleAccount() model.ProAccount {
	return model.ProAccount{
		ID: "account-1", Platform: "openai", AuthType: "oauth", Version: 5,
		Binding: &model.ProAccountBinding{SourceType: proaccountgateway.SourceAuthFile, SourceLocator: "codex.json", AuthIndex: "auth-1"},
	}
}

func resetSnapshot() proaccountgateway.SnapshotResult {
	return proaccountgateway.SnapshotResult{Accounts: []proaccountgateway.AccountSnapshot{{
		Platform: "openai", AuthType: "oauth", SourceType: proaccountgateway.SourceAuthFile,
		SourceLocator: "codex.json", AuthIndex: "auth-1", UpstreamAccountID: "chatgpt-account-1",
	}}}
}

func TestCreditsReturnsSupportedOnlyForValidPayload(t *testing.T) {
	gateway := &resetGatewayStub{
		snapshot: resetSnapshot(),
		responses: []proaccountgateway.APICallResult{{StatusCode: http.StatusOK, Body: `{
			"available_count":2,
			"credits":[{"id":"credit-1","reset_type":"codex_rate_limits","status":"available","expires_at":"2026-08-01T00:00:00Z"}]
		}`}},
	}
	service := New(resetAccountReaderStub{account: resetEligibleAccount()}, resetSetupStub{}, gateway, &resetOperationsStub{items: map[string]model.ProAccountDraft{}})
	result, err := service.Credits(context.Background(), "account-1")
	if err != nil {
		t.Fatalf("查询 reset credits：%v", err)
	}
	if result.Capability != CapabilitySupported || result.AvailableCount == nil || *result.AvailableCount != 2 || len(result.Credits) != 1 {
		t.Fatalf("查询结果 = %#v", result)
	}
	if len(gateway.calls) != 1 || gateway.calls[0].Headers["Chatgpt-Account-Id"] != "chatgpt-account-1" || gateway.calls[0].Headers["Authorization"] != "Bearer $TOKEN$" || gateway.calls[0].Headers["OpenAI-Beta"] != "codex-1" || gateway.calls[0].Headers["OAI-Language"] != "zh-CN" || gateway.calls[0].Headers["Sec-Fetch-Mode"] != "no-cors" {
		t.Fatalf("Gateway 请求 = %#v", gateway.calls)
	}
}

func TestParseCreditsAcceptsUpstreamPayloadVariants(t *testing.T) {
	tests := []struct {
		name          string
		body          string
		expectedCount int
		expectedDates int
	}{
		{
			name: "直接数组",
			body: `[
				{"reset_type":"codex_rate_limits","status":"available","expires_at":"2026-08-01T00:00:00Z"},
				{"resetType":"codex_rate_limits","status":"available"},
				{"reset_type":"other","status":"available"}
			]`,
			expectedCount: 2,
			expectedDates: 1,
		},
		{
			name:          "驼峰次数与 items 列表",
			body:          `{"availableCount":"3","items":[{"expiresAt":"2026-08-02T00:00:00Z"}]}`,
			expectedCount: 3,
			expectedDates: 1,
		},
		{
			name:          "rate_limit_reset_credits 列表",
			body:          `{"rate_limit_reset_credits":[{"reset_type":"codex_rate_limits","status":"available","expires_at":"2026-08-03T00:00:00Z"}]}`,
			expectedCount: 1,
			expectedDates: 1,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			result, ok := parseCredits(test.body)
			if !ok || result.AvailableCount == nil || *result.AvailableCount != test.expectedCount || len(result.Credits) != test.expectedDates {
				t.Fatalf("解析结果=%#v ok=%v", result, ok)
			}
		})
	}
}

func TestCreditsDoesNotProbeUnsupportedAccountType(t *testing.T) {
	account := resetEligibleAccount()
	account.AuthType = "api"
	gateway := &resetGatewayStub{}
	result, err := New(resetAccountReaderStub{account: account}, resetSetupStub{}, gateway, &resetOperationsStub{items: map[string]model.ProAccountDraft{}}).Credits(context.Background(), account.ID)
	if err != nil || result.Capability != CapabilityUnsupported || len(gateway.calls) != 0 {
		t.Fatalf("不支持账号结果=%#v err=%v calls=%#v", result, err, gateway.calls)
	}
}

func TestResetRequiresConfirmationAndIsIdempotent(t *testing.T) {
	gateway := &resetGatewayStub{
		snapshot: resetSnapshot(),
		responses: []proaccountgateway.APICallResult{
			{StatusCode: http.StatusOK, Body: `{"available_count":2,"credits":[]}`},
			{StatusCode: http.StatusOK, Body: `{}`},
			{StatusCode: http.StatusOK, Body: `{"available_count":1,"credits":[]}`},
			{StatusCode: http.StatusOK, Body: `{"available_count":1,"credits":[]}`},
		},
	}
	operations := &resetOperationsStub{items: map[string]model.ProAccountDraft{}}
	service := New(resetAccountReaderStub{account: resetEligibleAccount()}, resetSetupStub{}, gateway, operations)
	if _, err := service.Reset(context.Background(), ResetInput{AccountID: "account-1"}); err != ErrInvalidRequest {
		t.Fatalf("未确认错误 = %v", err)
	}
	input := ResetInput{
		AccountID: "account-1", OperationID: "reset-operation", IdempotencyKey: "reset-key",
		ExpectedVersion: 5, Confirmed: true,
	}
	result, err := service.Reset(context.Background(), input)
	if err != nil || result.Operation.State != model.ProOperationStateEnabled || result.Credits.AvailableCount == nil || *result.Credits.AvailableCount != 1 {
		t.Fatalf("重置结果=%#v err=%v", result, err)
	}
	replayed, err := service.Reset(context.Background(), input)
	if err != nil || replayed.Operation.State != model.ProOperationStateEnabled {
		t.Fatalf("幂等重放=%#v err=%v", replayed, err)
	}
	consumeCalls := 0
	for _, call := range gateway.calls {
		if call.URL == resetConsumeURL {
			consumeCalls++
			if call.Method != http.MethodPost || call.Body.(map[string]string)["redeem_request_id"] != "reset-operation" || call.Headers["OpenAI-Beta"] != "codex-1" || call.Headers["Sec-Fetch-Dest"] != "empty" {
				t.Fatalf("消费请求 = %#v", call)
			}
		}
	}
	if consumeCalls != 1 {
		t.Fatalf("消费次数 = %d，calls=%#v", consumeCalls, gateway.calls)
	}
}

func TestResetReplaySucceedsAfterLastCreditWasConsumed(t *testing.T) {
	gateway := &resetGatewayStub{
		snapshot: resetSnapshot(),
		responses: []proaccountgateway.APICallResult{
			{StatusCode: http.StatusOK, Body: `{"available_count":1,"credits":[]}`},
			{StatusCode: http.StatusOK, Body: `{}`},
			{StatusCode: http.StatusOK, Body: `{"available_count":0,"credits":[]}`},
			{StatusCode: http.StatusOK, Body: `{"available_count":0,"credits":[]}`},
		},
	}
	operations := &resetOperationsStub{items: map[string]model.ProAccountDraft{}}
	service := New(resetAccountReaderStub{account: resetEligibleAccount()}, resetSetupStub{}, gateway, operations)
	input := ResetInput{
		AccountID: "account-1", OperationID: "reset-last-credit", IdempotencyKey: "reset-last-credit-key",
		ExpectedVersion: 5, Confirmed: true,
	}
	first, err := service.Reset(context.Background(), input)
	if err != nil || first.Operation.State != model.ProOperationStateEnabled || first.Credits.AvailableCount == nil || *first.Credits.AvailableCount != 0 {
		t.Fatalf("首次重置=%#v err=%v", first, err)
	}
	replayed, err := service.Reset(context.Background(), input)
	if err != nil || replayed.Operation.State != model.ProOperationStateEnabled || replayed.Credits.AvailableCount == nil || *replayed.Credits.AvailableCount != 0 {
		t.Fatalf("最后一次 credit 重放=%#v err=%v", replayed, err)
	}
	consumeCalls := 0
	for _, call := range gateway.calls {
		if call.URL == resetConsumeURL {
			consumeCalls++
		}
	}
	if consumeCalls != 1 {
		t.Fatalf("消费次数 = %d，calls=%#v", consumeCalls, gateway.calls)
	}
}

func TestCreditsTreatsInvalidPayloadAsUnknown(t *testing.T) {
	gateway := &resetGatewayStub{snapshot: resetSnapshot(), responses: []proaccountgateway.APICallResult{{StatusCode: http.StatusOK, Body: `{"unexpected":true}`}}}
	result, err := New(resetAccountReaderStub{account: resetEligibleAccount()}, resetSetupStub{}, gateway, &resetOperationsStub{items: map[string]model.ProAccountDraft{}}).Credits(context.Background(), "account-1")
	if err != nil || result.Capability != CapabilityUnknown || result.ErrorCode != "credits_payload_invalid" {
		t.Fatalf("无效响应结果=%#v err=%v", result, err)
	}
}
