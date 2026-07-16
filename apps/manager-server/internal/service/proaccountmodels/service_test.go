package proaccountmodels_test

import (
	"context"
	"errors"
	"path/filepath"
	"testing"

	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/model"
	proaccountrepo "github.com/seakee/cpa-manager-plus/apps/manager-server/internal/repository/proaccount"
	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/repository/proaccountdraft"
	sqliterepo "github.com/seakee/cpa-manager-plus/apps/manager-server/internal/repository/sqlite"
	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/service/managerconfig"
	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/service/proaccountgateway"
	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/service/proaccountmodels"
	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/service/proaccountoperation"
	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/store"
)

type accountReaderStub struct {
	account model.ProAccount
}

func (s accountReaderStub) Get(context.Context, string) (model.ProAccount, error) {
	return s.account, nil
}

type conflictingAccountRepository struct{}

func (conflictingAccountRepository) UpdateModelRules(context.Context, string, int64, []string, map[string]string, string, int64) (model.ProAccount, error) {
	return model.ProAccount{}, proaccountrepo.ErrVersionConflict
}

type setupResolverStub struct{}

func (setupResolverStub) ResolveSetupWithSource(context.Context) (store.Setup, managerconfig.Source, bool, error) {
	return store.Setup{CPAUpstreamURL: "http://gateway.test", ManagementKey: "management-key"}, managerconfig.SourceDB, true, nil
}

type ruleGatewayStub struct {
	restored bool
}

func (s *ruleGatewayStub) WriteAndVerifyModelRules(context.Context, string, string, string, string, proaccountgateway.ModelRules) (proaccountgateway.ModelRules, proaccountgateway.ModelRules, error) {
	return proaccountgateway.ModelRules{
			AllowedModels: []string{"old"}, ModelMapping: map[string]string{}, ModelRuleVersion: "rule-old",
		}, proaccountgateway.ModelRules{
			AllowedModels: []string{"new"}, ModelMapping: map[string]string{}, ModelRuleVersion: "rule-new",
		}, nil
}

func (s *ruleGatewayStub) RestoreModelRules(context.Context, string, string, string, string, proaccountgateway.ModelRules) error {
	s.restored = true
	return nil
}

func TestUpdateRestoresGatewayRulesWhenManagerCASFails(t *testing.T) {
	db, err := sqliterepo.Open(filepath.Join(t.TempDir(), "manager.sqlite"))
	if err != nil {
		t.Fatalf("打开 SQLite：%v", err)
	}
	defer db.Close()
	operations := proaccountoperation.New(proaccountdraft.New(db))
	gateway := &ruleGatewayStub{}
	accountRepository := proaccountrepo.New(db)
	synced, err := accountRepository.Sync(context.Background(), []model.ProAccountDiscovery{{
		Platform: "openai", AuthType: "oauth", SourceType: "auth_file", Name: "测试账号",
		Enabled: true, AuthIndex: "auth-1", SourceLocator: "account.json", ModelRuleVersion: "rule-old",
	}}, 1000, false)
	if err != nil || len(synced.Items) != 1 {
		t.Fatalf("准备账号：result=%#v err=%v", synced, err)
	}
	account, ok, err := accountRepository.Get(context.Background(), synced.Items[0].ProAccountID)
	if err != nil || !ok {
		t.Fatalf("读取账号：ok=%v err=%v", ok, err)
	}
	service := proaccountmodels.New(accountReaderStub{account: account}, conflictingAccountRepository{}, setupResolverStub{}, gateway, operations)
	result, err := service.Update(context.Background(), proaccountmodels.Input{
		AccountID: account.ID, OperationID: "models-operation", IdempotencyKey: "models-key",
		ExpectedVersion: 1, AllowedModels: []string{"new"},
	})
	if !errors.Is(err, proaccountrepo.ErrVersionConflict) {
		t.Fatalf("更新错误 = %v", err)
	}
	if !gateway.restored {
		t.Fatal("Manager CAS 失败后必须恢复 Gateway 旧规则")
	}
	if result.Operation.State != model.ProOperationStateFailed || result.Operation.CompensationAction != "restore_model_rules_completed" {
		t.Fatalf("补偿操作 = %#v", result.Operation)
	}
}
