package proaccountlifecycle

import (
	"context"
	"errors"
	"testing"

	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/model"
)

func TestCancelDraftRejectsSavedDisabledTerminalState(t *testing.T) {
	operations := &migrationOperations{current: model.ProAccountDraft{
		OperationID: "saved-disabled-operation", OperationType: "add",
		ProAccountID: "account-1", State: model.ProOperationStateSavedDisabled, Version: 4,
	}}
	service := New(nil, nil, nil, nil, nil, operations)

	result, err := service.CancelDraft(context.Background(), operations.current.OperationID)
	if !errors.Is(err, ErrOperationState) || result.Operation.State != model.ProOperationStateSavedDisabled {
		t.Fatalf("取消停用终态：result=%#v err=%v", result, err)
	}
}

func TestParseOAuthCallbackInputSupportsURLQueryAndCode(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  parsedOAuthCallback
	}{
		{name: "完整地址", input: "http://localhost:1455/auth/callback?code=url-code&state=url-state", want: parsedOAuthCallback{Code: "url-code", State: "url-state"}},
		{name: "查询参数", input: "code=query-code&state=query-state", want: parsedOAuthCallback{Code: "query-code", State: "query-state"}},
		{name: "单独 Code", input: "single-code", want: parsedOAuthCallback{Code: "single-code"}},
		{name: "带等号的 Code", input: "single-code==", want: parsedOAuthCallback{Code: "single-code=="}},
		{name: "带标签的 Code", input: "Code: labeled-code", want: parsedOAuthCallback{Code: "labeled-code"}},
		{name: "错误回调", input: "?error=access_denied&state=error-state", want: parsedOAuthCallback{State: "error-state", Error: "access_denied"}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got, err := parseOAuthCallbackInput(test.input)
			if err != nil || got != test.want {
				t.Fatalf("解析结果 = %#v, err=%v", got, err)
			}
		})
	}
}

func TestParseOAuthCallbackInputRejectsMissingCode(t *testing.T) {
	_, err := parseOAuthCallbackInput("state=only-state")
	if !errors.Is(err, ErrOAuthCallbackInvalid) {
		t.Fatalf("错误 = %v，期望回调参数无效", err)
	}
}
