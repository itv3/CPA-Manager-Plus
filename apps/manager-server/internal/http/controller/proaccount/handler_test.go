package proaccount

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/service/proaccountlifecycle"
)

func TestWriteLifecycleErrorReturnsCredentialConflictMessage(t *testing.T) {
	recorder := httptest.NewRecorder()
	(&Handler{}).writeLifecycleError(recorder, proaccountlifecycle.ErrCredentialAlreadyBound, proaccountlifecycle.Result{})

	if recorder.Code != http.StatusConflict {
		t.Fatalf("状态码 = %d，期望 %d", recorder.Code, http.StatusConflict)
	}
	var payload struct {
		Code      string `json:"code"`
		Message   string `json:"message"`
		Retryable bool   `json:"retryable"`
	}
	if err := json.NewDecoder(recorder.Body).Decode(&payload); err != nil {
		t.Fatalf("解析错误响应：%v", err)
	}
	if payload.Code != "credential_already_bound" || payload.Message != "该 API Key 已绑定到另一个账号，请先更新或删除原账号" || payload.Retryable {
		t.Fatalf("错误响应 = %#v", payload)
	}
}
