package proaccountscheduledtest

import (
	"context"
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/model"
	proaccountscheduledtestrepo "github.com/seakee/cpa-manager-plus/apps/manager-server/internal/repository/proaccountscheduledtest"
	sqliterepo "github.com/seakee/cpa-manager-plus/apps/manager-server/internal/repository/sqlite"
	proaccountsvc "github.com/seakee/cpa-manager-plus/apps/manager-server/internal/service/proaccount"
	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/service/proaccountgateway"
	proaccountscheduledtestsvc "github.com/seakee/cpa-manager-plus/apps/manager-server/internal/service/proaccountscheduledtest"
	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/service/proaccounttest"
)

type scheduledVerifierStub struct{}

func (scheduledVerifierStub) VerifyPanelHeader(context.Context, string) (bool, error) {
	return true, nil
}

func (scheduledVerifierStub) PanelUsesExternalManagementKey(context.Context) (bool, error) {
	return false, nil
}

type scheduledAccountStub struct {
	account model.ProAccount
}

func (s scheduledAccountStub) Get(_ context.Context, id string) (model.ProAccount, error) {
	if id != s.account.ID {
		return model.ProAccount{}, proaccountsvc.ErrAccountNotFound
	}
	return s.account, nil
}

type scheduledTesterStub struct{}

func (scheduledTesterStub) Test(context.Context, proaccounttest.Input) (proaccounttest.Result, error) {
	return proaccounttest.Result{Connectivity: proaccountgateway.ConnectivityResult{
		Success: true, StatusCode: http.StatusOK, DurationMS: 8, ResponsePreview: "ok",
	}}, nil
}

func TestHandlerMatchesFrontendContract(t *testing.T) {
	handler, db := newScheduledHandler(t)

	conflict := requestScheduled(t, handler, http.MethodPost,
		"/v0/pro/accounts/account-a/scheduled-test-plans",
		`{"operation_id":"op","idempotency_key":"key","expected_version":8,"model_id":"gpt-5","cron_expression":"*/5 * * * *"}`)
	if conflict.Code != http.StatusConflict {
		t.Fatalf("版本冲突状态=%d body=%s", conflict.Code, conflict.Body.String())
	}

	createdRR := requestScheduled(t, handler, http.MethodPost,
		"/v0/pro/accounts/account-a/scheduled-test-plans",
		`{"operation_id":"op","idempotency_key":"key","expected_version":7,"model_id":"gpt-5","cron_expression":"*/5 * * * *","enabled":true,"max_results":2,"auto_recover":false}`)
	if createdRR.Code != http.StatusCreated {
		t.Fatalf("创建状态=%d body=%s", createdRR.Code, createdRR.Body.String())
	}
	if strings.Contains(createdRR.Body.String(), `"accountId"`) || strings.Contains(createdRR.Body.String(), `"model":`) {
		t.Fatalf("创建响应使用了错误字段：%s", createdRR.Body.String())
	}
	var created planResponse
	decodeScheduledJSON(t, createdRR, &created)
	if created.ID <= 0 || created.ProAccountID != "account-a" || created.ModelID != "gpt-5" || created.MaxResults != 2 {
		t.Fatalf("创建响应 = %#v", created)
	}

	listRR := requestScheduled(t, handler, http.MethodGet,
		"/v0/pro/accounts/account-a/scheduled-test-plans", "")
	if listRR.Code != http.StatusOK || !strings.Contains(listRR.Body.String(), `"proAccountId":"account-a"`) {
		t.Fatalf("列表响应=%d %s", listRR.Code, listRR.Body.String())
	}

	updatedRR := requestScheduled(t, handler, http.MethodPut,
		"/v0/pro/accounts/account-a/scheduled-test-plans/"+strconvFormat(created.ID),
		`{"enabled":false,"auto_recover":false}`)
	if updatedRR.Code != http.StatusOK {
		t.Fatalf("更新状态=%d body=%s", updatedRR.Code, updatedRR.Body.String())
	}
	var updated planResponse
	decodeScheduledJSON(t, updatedRR, &updated)
	if updated.Enabled || updated.AutoRecover {
		t.Fatalf("更新响应 = %#v", updated)
	}

	runRR := requestScheduled(t, handler, http.MethodPost,
		"/v0/pro/accounts/account-a/scheduled-test-plans/"+strconvFormat(created.ID)+"/run", "")
	if runRR.Code != http.StatusOK || !strings.Contains(runRR.Body.String(), `"status":"success"`) {
		t.Fatalf("立即执行响应=%d %s", runRR.Code, runRR.Body.String())
	}
	resultsRR := requestScheduled(t, handler, http.MethodGet,
		"/v0/pro/accounts/account-a/scheduled-test-plans/"+strconvFormat(created.ID)+"/results?limit=20", "")
	if resultsRR.Code != http.StatusOK || !strings.Contains(resultsRR.Body.String(), `"responseText":"ok"`) {
		t.Fatalf("结果响应=%d %s", resultsRR.Code, resultsRR.Body.String())
	}

	wrongAccountRR := requestScheduled(t, handler, http.MethodDelete,
		"/v0/pro/accounts/account-b/scheduled-test-plans/"+strconvFormat(created.ID), "")
	if wrongAccountRR.Code != http.StatusNotFound {
		t.Fatalf("跨账号删除状态=%d body=%s", wrongAccountRR.Code, wrongAccountRR.Body.String())
	}

	var count int
	if err := db.QueryRow(`select count(*) from pro_account_scheduled_tests`).Scan(&count); err != nil {
		t.Fatalf("查询计划数量：%v", err)
	}
	if count != 1 {
		t.Fatalf("跨账号删除后计划数量=%d", count)
	}
}

func newScheduledHandler(t *testing.T) (*Handler, *sql.DB) {
	t.Helper()
	db, err := sqliterepo.Open(filepath.Join(t.TempDir(), "manager.sqlite"))
	if err != nil {
		t.Fatalf("打开 SQLite：%v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	_, err = db.Exec(`insert into pro_accounts (
		id, platform, auth_type, source_type, enabled, health_status,
		allowed_models_json, model_mapping_json, created_at_ms, updated_at_ms, version
	) values ('account-a', 'openai', 'oauth', 'auth_file', 1, 'healthy', '["gpt-5"]', '{}', 1, 1, 7)`)
	if err != nil {
		t.Fatalf("写入测试账号：%v", err)
	}
	account := model.ProAccount{
		ID: "account-a", Platform: "openai", AuthType: "oauth", SourceType: "auth_file",
		Enabled: true, AllowedModels: []string{"gpt-5"}, ModelMapping: map[string]string{}, Version: 7,
	}
	service := proaccountscheduledtestsvc.New(
		proaccountscheduledtestrepo.New(db), scheduledAccountStub{account: account}, scheduledTesterStub{},
	)
	return New(service, scheduledVerifierStub{}), db
}

func requestScheduled(t *testing.T, handler *Handler, method string, path string, body string) *httptest.ResponseRecorder {
	t.Helper()
	request := httptest.NewRequest(method, path, strings.NewReader(body))
	request.Header.Set("Authorization", "Bearer test")
	response := httptest.NewRecorder()
	handler.Handle(response, request)
	return response
}

func decodeScheduledJSON(t *testing.T, response *httptest.ResponseRecorder, target any) {
	t.Helper()
	if err := json.Unmarshal(response.Body.Bytes(), target); err != nil {
		t.Fatalf("解析响应：%v，body=%s", err, response.Body.String())
	}
}

func strconvFormat(value int64) string {
	return strconv.FormatInt(value, 10)
}
