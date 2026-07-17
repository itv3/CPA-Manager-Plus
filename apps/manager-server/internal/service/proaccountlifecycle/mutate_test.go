package proaccountlifecycle

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/model"
	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/service/proaccountgateway"
	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/service/proaccountoperation"
	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/service/proaccountprobe"
)

type migrationAccountState struct {
	account model.ProAccount
}

type migrationAccountReader struct {
	state *migrationAccountState
}

func (r migrationAccountReader) Get(_ context.Context, id string) (model.ProAccount, error) {
	if r.state.account.ID != id {
		return model.ProAccount{}, errors.New("账号不存在")
	}
	return r.state.account, nil
}

type migrationRepository struct {
	AccountRepository
	state     *migrationAccountState
	rebindErr error
}

func (r *migrationRepository) RebindManaged(_ context.Context, accountID string, expectedVersion int64, discovery model.ProAccountDiscovery, nowMS int64) (model.ProAccount, error) {
	if r.rebindErr != nil {
		return model.ProAccount{}, r.rebindErr
	}
	if r.state.account.ID != accountID || r.state.account.Version != expectedVersion {
		return model.ProAccount{}, ErrResourceVersionConflict
	}
	updated := r.state.account
	updated.Name = discovery.Name
	updated.Enabled = discovery.Enabled
	updated.AllowedModels = append([]string(nil), discovery.AllowedModels...)
	updated.ModelMapping = cloneMap(discovery.ModelMapping)
	updated.ModelRuleVersion = discovery.ModelRuleVersion
	updated.SourceType = discovery.SourceType
	updated.UpdatedAtMS = nowMS
	updated.Version++
	updated.Binding = &model.ProAccountBinding{
		ProAccountID: accountID, SourceType: discovery.SourceType, SourceLocator: discovery.SourceLocator,
		AuthIndex: discovery.AuthIndex, SourceFingerprint: discovery.SourceFingerprint,
		BindingStatus: model.ProBindingStatusCurrent, IsCurrent: true, ValidFromMS: nowMS,
	}
	r.state.account = updated
	return updated, nil
}

type migrationProbe struct {
	sourceType string
}

func (p migrationProbe) ProbeCandidate(context.Context, proaccountprobe.Input) (proaccountprobe.Result, error) {
	return proaccountprobe.Result{Platform: "openai", SourceType: p.sourceType, TestModel: "gpt-test"}, nil
}

type migrationOperations struct {
	current     model.ProAccountDraft
	transitions []proaccountoperation.TransitionInput
}

func (o *migrationOperations) Start(_ context.Context, input proaccountoperation.StartInput) (model.ProAccountDraft, bool, error) {
	o.current = model.ProAccountDraft{
		OperationID: input.OperationID, IdempotencyKey: input.IdempotencyKey, OperationType: input.OperationType,
		ProAccountID: input.ProAccountID, State: model.ProOperationStateDraftCreated, Version: 1,
		CleanupDeadlineMS: 999999, Context: input.Context,
	}
	return o.current, true, nil
}

func (o *migrationOperations) Get(_ context.Context, operationID string) (model.ProAccountDraft, error) {
	if o.current.OperationID != operationID {
		return model.ProAccountDraft{}, errors.New("操作不存在")
	}
	return o.current, nil
}

func (o *migrationOperations) Transition(_ context.Context, operationID string, input proaccountoperation.TransitionInput) (model.ProAccountDraft, error) {
	if o.current.OperationID != operationID || o.current.Version != input.ExpectedVersion {
		return model.ProAccountDraft{}, proaccountoperation.ErrOperationVersionConflict
	}
	o.transitions = append(o.transitions, input)
	o.current.State = input.State
	o.current.Version++
	o.current.ProAccountID = input.ProAccountID
	o.current.CompensationAction = input.CompensationAction
	o.current.ErrorCode = input.ErrorCode
	o.current.ErrorSummary = input.ErrorSummary
	o.current.Context = input.Context
	return o.current, nil
}

type migrationGateway struct {
	Gateway
	accounts       map[string]proaccountgateway.AccountSnapshot
	replacement    proaccountgateway.AccountSnapshot
	failEnableAuth string
	deleteErrors   map[string]error
	calls          []string
}

func (g *migrationGateway) EditableAccount(context.Context, string, string, string, string) (proaccountgateway.EditableAccount, error) {
	return proaccountgateway.EditableAccount{BaseURL: "https://old.example/v1", Headers: map[string]string{}}, nil
}

func (g *migrationGateway) CreateDisabledAPI(context.Context, string, string, proaccountgateway.CreateAPIInput) (proaccountgateway.AccountSnapshot, error) {
	g.accounts[g.replacement.AuthIndex] = g.replacement
	g.calls = append(g.calls, "create:"+g.replacement.AuthIndex)
	return g.replacement, nil
}

func (g *migrationGateway) TestAccount(context.Context, string, string, proaccountgateway.AccountReference, string) (proaccountgateway.ConnectivityResult, error) {
	return proaccountgateway.ConnectivityResult{Success: true, StatusCode: 200}, nil
}

func (g *migrationGateway) Snapshot(context.Context, string, string) (proaccountgateway.SnapshotResult, error) {
	accounts := make([]proaccountgateway.AccountSnapshot, 0, len(g.accounts))
	for _, account := range g.accounts {
		accounts = append(accounts, account)
	}
	return proaccountgateway.SnapshotResult{Accounts: accounts}, nil
}

func (g *migrationGateway) FindAccountByAuthIndex(_ context.Context, _ string, _ string, authIndex string) (proaccountgateway.AccountSnapshot, error) {
	account, ok := g.accounts[authIndex]
	if !ok {
		return proaccountgateway.AccountSnapshot{}, proaccountgateway.ErrGatewayAccountNotFound
	}
	return account, nil
}

func (g *migrationGateway) SetAccountEnabled(_ context.Context, _ string, _ string, sourceType string, sourceLocator string, enabled bool) (proaccountgateway.AccountSnapshot, error) {
	for authIndex, account := range g.accounts {
		if account.SourceType != sourceType || account.SourceLocator != sourceLocator {
			continue
		}
		g.calls = append(g.calls, fmt.Sprintf("enabled:%s:%t", authIndex, enabled))
		if enabled && authIndex == g.failEnableAuth {
			return proaccountgateway.AccountSnapshot{}, errors.New("启用替换凭证失败")
		}
		account.Enabled = enabled
		g.accounts[authIndex] = account
		return account, nil
	}
	return proaccountgateway.AccountSnapshot{}, proaccountgateway.ErrGatewayAccountNotFound
}

func (g *migrationGateway) DeleteAccount(_ context.Context, _ string, _ string, sourceType string, sourceLocator string) error {
	var deleted proaccountgateway.AccountSnapshot
	deletedAuthIndex := ""
	for authIndex, account := range g.accounts {
		if account.SourceType == sourceType && account.SourceLocator == sourceLocator {
			deleted = account
			deletedAuthIndex = authIndex
			break
		}
	}
	if deletedAuthIndex == "" {
		return proaccountgateway.ErrGatewayAccountNotFound
	}
	g.calls = append(g.calls, "delete:"+deletedAuthIndex)
	if err := g.deleteErrors[deletedAuthIndex]; err != nil {
		return err
	}
	all := make([]proaccountgateway.AccountSnapshot, 0, len(g.accounts))
	for _, account := range g.accounts {
		all = append(all, account)
	}
	delete(g.accounts, deletedAuthIndex)
	for authIndex, account := range g.accounts {
		locator, err := proaccountgateway.ProjectedLocatorAfterDelete(all, deleted, account)
		if err == nil {
			account.SourceLocator = locator
			g.accounts[authIndex] = account
		}
	}
	return nil
}

func TestMigrateAPISwitchesWithoutDeletingOldCredentialEarly(t *testing.T) {
	service, state, gateway, _ := newMigrationService()
	result, err := service.Update(context.Background(), migrationUpdateInput())
	if err != nil {
		t.Fatalf("迁移 API 凭证：%v", err)
	}
	if result.Operation.State != model.ProOperationStateEnabled {
		t.Fatalf("迁移终态 = %#v", result.Operation)
	}
	if state.account.ID != "account-1" || state.account.Binding.AuthIndex != "auth-new" || state.account.Binding.SourceLocator != "index:0" {
		t.Fatalf("迁移后账号 = %#v", state.account)
	}
	if _, exists := gateway.accounts["auth-old"]; exists {
		t.Fatal("成功迁移后旧凭证仍然存在")
	}
	wantOrder := "enabled:auth-old:false,enabled:auth-new:true,delete:auth-old"
	joined := strings.Join(gateway.calls, ",")
	if !strings.Contains(joined, wantOrder) {
		t.Fatalf("切换调用顺序 = %s，期望包含 %s", joined, wantOrder)
	}
}

func TestMigrateAPIRestoresOldCredentialWhenReplacementEnableFails(t *testing.T) {
	service, state, gateway, _ := newMigrationService()
	gateway.failEnableAuth = "auth-new"
	result, err := service.Update(context.Background(), migrationUpdateInput())
	if err == nil {
		t.Fatal("替换凭证启用失败时应返回错误")
	}
	if result.Operation.State != model.ProOperationStateFailed {
		t.Fatalf("失败迁移终态 = %#v", result.Operation)
	}
	oldAccount, oldExists := gateway.accounts["auth-old"]
	if !oldExists || !oldAccount.Enabled {
		t.Fatalf("旧凭证未恢复：%#v", gateway.accounts)
	}
	if _, replacementExists := gateway.accounts["auth-new"]; replacementExists {
		t.Fatal("失败迁移后替换凭证未清理")
	}
	if state.account.Binding.AuthIndex != "auth-old" {
		t.Fatalf("失败迁移改变了 Manager 绑定：%#v", state.account.Binding)
	}
}

func TestMigrateAPIRecoveryFinishesDeleteAfterManagerRebind(t *testing.T) {
	service, state, gateway, operations := newMigrationService()
	gateway.deleteErrors["auth-old"] = errors.New("删除响应暂时失败")
	result, err := service.Update(context.Background(), migrationUpdateInput())
	if err == nil {
		t.Fatal("旧凭证删除失败时应返回错误")
	}
	if result.Operation.State != model.ProOperationStateCompensating || state.account.Binding.AuthIndex != "auth-new" {
		t.Fatalf("待恢复状态 = operation:%#v account:%#v", result.Operation, state.account)
	}
	delete(gateway.deleteErrors, "auth-old")
	if err = service.Recover(context.Background(), operations.current); err != nil {
		t.Fatalf("恢复完成旧凭证删除：%v", err)
	}
	if operations.current.State != model.ProOperationStateEnabled {
		t.Fatalf("恢复终态 = %#v", operations.current)
	}
	if _, oldExists := gateway.accounts["auth-old"]; oldExists {
		t.Fatal("恢复后旧凭证仍然存在")
	}
	if replacement, exists := gateway.accounts["auth-new"]; !exists || replacement.SourceLocator != "index:0" {
		t.Fatalf("恢复后替换凭证 = %#v", gateway.accounts)
	}
}

func TestMigrateAPISharedProviderDoesNotDisableSiblingKeys(t *testing.T) {
	service, state, gateway, _ := newMigrationService()
	state.account.SourceType = proaccountgateway.SourceOpenAICompatibility
	state.account.Binding.SourceType = proaccountgateway.SourceOpenAICompatibility
	state.account.Binding.SourceLocator = "provider:0:key:0"
	gateway.accounts = map[string]proaccountgateway.AccountSnapshot{
		"auth-old": {
			Platform: "openai", AuthType: "api", SourceType: proaccountgateway.SourceOpenAICompatibility,
			SourceLocator: "provider:0:key:0", AuthIndex: "auth-old", Enabled: true, ModelRuleVersion: "rule-old",
		},
		"auth-sibling": {
			Platform: "openai", AuthType: "api", SourceType: proaccountgateway.SourceOpenAICompatibility,
			SourceLocator: "provider:0:key:1", AuthIndex: "auth-sibling", Enabled: true, ModelRuleVersion: "rule-sibling",
		},
	}
	gateway.replacement = proaccountgateway.AccountSnapshot{
		Platform: "openai", AuthType: "api", SourceType: proaccountgateway.SourceOpenAICompatibility,
		SourceLocator: "provider:1:key:0", AuthIndex: "auth-new", Enabled: false, ModelRuleVersion: "rule-new",
	}
	service.probe = migrationProbe{sourceType: proaccountgateway.SourceOpenAICompatibility}
	result, err := service.Update(context.Background(), migrationUpdateInput())
	if err != nil {
		t.Fatalf("迁移共享 Provider Key：%v", err)
	}
	if strings.Contains(strings.Join(gateway.calls, ","), "enabled:auth-old:false") {
		t.Fatalf("迁移单个 Key 时停用了共享 Provider：%v", gateway.calls)
	}
	if sibling, exists := gateway.accounts["auth-sibling"]; !exists || !sibling.Enabled {
		t.Fatalf("共享 Provider 的其他 Key 受到影响：%#v", gateway.accounts)
	}
	if result.Account.Binding.SourceLocator != "provider:1:key:0" {
		t.Fatalf("共享 Provider 删除后的替换定位 = %q", result.Account.Binding.SourceLocator)
	}
}

func newMigrationService() (*Service, *migrationAccountState, *migrationGateway, *migrationOperations) {
	state := &migrationAccountState{account: model.ProAccount{
		ID: "account-1", Platform: "openai", AuthType: "api", SourceType: proaccountgateway.SourceCodexAPIKey,
		Name: "OpenAI API", Enabled: true, HealthStatus: model.ProAccountHealthUnknown,
		AllowedModels: []string{}, ModelMapping: map[string]string{}, Version: 1,
		Binding: &model.ProAccountBinding{
			ProAccountID: "account-1", SourceType: proaccountgateway.SourceCodexAPIKey,
			SourceLocator: "index:0", AuthIndex: "auth-old", BindingStatus: model.ProBindingStatusCurrent, IsCurrent: true,
		},
	}}
	gateway := &migrationGateway{
		accounts: map[string]proaccountgateway.AccountSnapshot{
			"auth-old": {
				Platform: "openai", AuthType: "api", SourceType: proaccountgateway.SourceCodexAPIKey,
				SourceLocator: "index:0", AuthIndex: "auth-old", Enabled: true, ModelRuleVersion: "rule-old",
			},
		},
		replacement: proaccountgateway.AccountSnapshot{
			Platform: "openai", AuthType: "api", SourceType: proaccountgateway.SourceCodexAPIKey,
			SourceLocator: "index:1", AuthIndex: "auth-new", Enabled: false, ModelRuleVersion: "rule-new",
		},
		deleteErrors: map[string]error{},
	}
	operations := &migrationOperations{}
	repository := &migrationRepository{state: state}
	service := New(migrationAccountReader{state: state}, repository, recoverySetupStub{}, gateway,
		migrationProbe{sourceType: proaccountgateway.SourceCodexAPIKey}, operations)
	return service, state, gateway, operations
}

func migrationUpdateInput() UpdateInput {
	baseURL := "https://new.example/v1"
	return UpdateInput{
		MutationInput: MutationInput{
			AccountID: "account-1", OperationID: "migration-operation", IdempotencyKey: "migration-key", ExpectedVersion: 1,
		},
		BaseURL: &baseURL, APIKey: "new-key", ProtocolMode: "responses", TestModel: "gpt-test",
	}
}
