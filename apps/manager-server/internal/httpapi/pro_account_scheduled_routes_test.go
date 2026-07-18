package httpapi

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/model"
	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/testutil"
)

func TestProAccountScheduledTestRoutesUseDedicatedHandler(t *testing.T) {
	cfg := testutil.NewConfig(t)
	handler, db := newCompatHandler(t, cfg, nil)
	_, err := db.ProAccounts.Sync(context.Background(), []model.ProAccountDiscovery{{
		Platform: "openai", AuthType: "oauth", SourceType: "auth_file",
		Name: "定时测试账号", Enabled: true, HealthStatus: "healthy",
		AllowedModels: []string{"gpt-5"}, AuthIndex: "scheduled-auth",
		SourceLocator: "scheduled-auth.json", SourceFingerprint: "scheduled-fingerprint",
	}}, time.Now().UnixMilli(), false)
	if err != nil {
		t.Fatalf("同步测试账号：%v", err)
	}
	accounts, err := db.ProAccounts.List(context.Background(), model.ProAccountListFilter{Limit: 10})
	if err != nil || len(accounts.Items) != 1 {
		t.Fatalf("读取测试账号 items=%d err=%v", len(accounts.Items), err)
	}
	account := accounts.Items[0]

	createRR := testutil.Request(t, handler, http.MethodPost,
		"/v0/pro/accounts/"+account.ID+"/scheduled-test-plans",
		fmt.Sprintf(`{"operation_id":"scheduled-route-op","idempotency_key":"scheduled-route-key","expected_version":%d,"model_id":"gpt-5","cron_expression":"*/5 * * * *","enabled":true}`, account.Version),
		testutil.AdminKey,
	)
	testutil.RequireStatus(t, createRR, http.StatusCreated)
	if body := createRR.Body.String(); !containsAll(body, `"proAccountId":"`+account.ID+`"`, `"modelId":"gpt-5"`) {
		t.Fatalf("创建响应 = %s", body)
	}

	listRR := testutil.Request(t, handler, http.MethodGet,
		"/v0/pro/accounts/"+account.ID+"/scheduled-test-plans", "", testutil.AdminKey)
	testutil.RequireStatus(t, listRR, http.StatusOK)
	if !containsAll(listRR.Body.String(), `"items"`, `"cronExpression":"*/5 * * * *"`) {
		t.Fatalf("列表响应 = %s", listRR.Body.String())
	}
}

func containsAll(value string, expected ...string) bool {
	for _, item := range expected {
		if !strings.Contains(value, item) {
			return false
		}
	}
	return true
}
