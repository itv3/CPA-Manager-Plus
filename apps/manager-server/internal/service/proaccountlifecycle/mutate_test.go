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
	state          *migrationAccountState
	rebindErr      error
	updateRulesErr error
}

// Sync 在删除/替换后被 syncBindingsAfterMutation 调用,测试中无需刷新绑定
func (r *migrationRepository) Sync(_ context.Context, _ []model.ProAccountDiscovery, _ int64, _ bool) (model.ProAccountSyncResult, error) {
	return model.ProAccountSyncResult{}, nil
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

func (r *migrationRepository) UpdateModelRules(_ context.Context, accountID string, expectedVersion int64, allowedModels []string, modelMapping map[string]string, modelRuleVersion string, nowMS int64) (model.ProAccount, error) {
	if r.updateRulesErr != nil {
		return model.ProAccount{}, r.updateRulesErr
	}
	if r.state.account.ID != accountID || r.state.account.Version != expectedVersion {
		return model.ProAccount{}, ErrResourceVersionConflict
	}
	r.state.account.AllowedModels = append([]string(nil), allowedModels...)
	r.state.account.ModelMapping = cloneMap(modelMapping)
	r.state.account.ModelRuleVersion = modelRuleVersion
	r.state.account.UpdatedAtMS = nowMS
	r.state.account.Version++
	return r.state.account, nil
}

func (r *migrationRepository) UpdateMetadata(_ context.Context, accountID string, expectedVersion int64, name string, notes string, nowMS int64) (model.ProAccount, error) {
	if r.state.account.ID != accountID || r.state.account.Version != expectedVersion {
		return model.ProAccount{}, ErrResourceVersionConflict
	}
	if strings.TrimSpace(name) != "" {
		r.state.account.Name = strings.TrimSpace(name)
	}
	r.state.account.Notes = strings.TrimSpace(notes)
	r.state.account.UpdatedAtMS = nowMS
	r.state.account.Version++
	return r.state.account, nil
}

type migrationProbe struct {
	sourceType string
}

func (p migrationProbe) ProbeCandidate(context.Context, proaccountprobe.Input) (proaccountprobe.Result, error) {
	return proaccountprobe.Result{Platform: "openai", SourceType: p.sourceType, TestModel: "gpt-test"}, nil
}

type rejectingMigrationProbe struct {
	t *testing.T
}

func (p rejectingMigrationProbe) ProbeCandidate(context.Context, proaccountprobe.Input) (proaccountprobe.Result, error) {
	p.t.Helper()
	p.t.Fatal("编辑保存不应调用候选凭证探测")
	return proaccountprobe.Result{}, errors.New("编辑保存不应调用候选凭证探测")
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
	accounts           map[string]proaccountgateway.AccountSnapshot
	replacement        proaccountgateway.AccountSnapshot
	createInput        proaccountgateway.CreateAPIInput
	createErr          error
	failEnableAuth     string
	deleteErrors       map[string]error
	calls              []string
	editable           proaccountgateway.EditableAccount
	capabilities       proaccountgateway.Capabilities
	compatibility      proaccountgateway.OfficialClientCompatibility
	restored           []proaccountgateway.OfficialClientCompatibility
	proxyErr           error
	testCalls          int
	additionalAccounts []proaccountgateway.AccountSnapshot
}

func (g *migrationGateway) EditableAccount(context.Context, string, string, string, string) (proaccountgateway.EditableAccount, error) {
	if g.editable.BaseURL != "" || g.editable.OfficialClientCompatibility != nil {
		return g.editable, nil
	}
	return proaccountgateway.EditableAccount{BaseURL: "https://old.example/v1", Headers: map[string]string{}}, nil
}

func (g *migrationGateway) Capabilities(context.Context, string, string) (proaccountgateway.Capabilities, error) {
	result := g.capabilities
	result.AllowedModels = true
	return result, nil
}

func (g *migrationGateway) WriteAndVerifyOfficialClientCompatibility(_ context.Context, _ string, _ string, _ string, _ string, desired proaccountgateway.OfficialClientCompatibility) (proaccountgateway.OfficialClientCompatibility, proaccountgateway.OfficialClientCompatibility, error) {
	previous := g.compatibility
	g.compatibility = desired
	g.calls = append(g.calls, "compatibility:write")
	return previous, desired, nil
}

func (g *migrationGateway) RestoreOfficialClientCompatibility(_ context.Context, _ string, _ string, _ string, _ string, previous proaccountgateway.OfficialClientCompatibility) error {
	g.compatibility = previous
	g.restored = append(g.restored, previous)
	g.calls = append(g.calls, "compatibility:restore")
	return nil
}

func (g *migrationGateway) UpdateAccountProxy(context.Context, string, string, string, string, string) error {
	g.calls = append(g.calls, "proxy:update")
	return g.proxyErr
}

func (g *migrationGateway) WriteAndVerifyModelRules(_ context.Context, _ string, _ string, _ string, _ string, desired proaccountgateway.ModelRules) (proaccountgateway.ModelRules, proaccountgateway.ModelRules, error) {
	previous := proaccountgateway.ModelRules{AllowedModels: []string{}, ModelMapping: map[string]string{}, ModelRuleVersion: "rule-old"}
	desired.ModelRuleVersion = "rule-new"
	return previous, desired, nil
}

func (g *migrationGateway) RestoreModelRules(context.Context, string, string, string, string, proaccountgateway.ModelRules) error {
	return nil
}

func (g *migrationGateway) CreateDisabledAPI(_ context.Context, _ string, _ string, input proaccountgateway.CreateAPIInput) (proaccountgateway.AccountSnapshot, error) {
	g.createInput = input
	g.accounts[g.replacement.AuthIndex] = g.replacement
	g.calls = append(g.calls, "create:"+g.replacement.AuthIndex)
	return g.replacement, g.createErr
}

func (g *migrationGateway) TestAccount(context.Context, string, string, proaccountgateway.AccountReference, string) (proaccountgateway.ConnectivityResult, error) {
	g.testCalls++
	return proaccountgateway.ConnectivityResult{Success: true, StatusCode: 200}, nil
}

func (g *migrationGateway) Snapshot(context.Context, string, string) (proaccountgateway.SnapshotResult, error) {
	accounts := make([]proaccountgateway.AccountSnapshot, 0, len(g.accounts)+len(g.additionalAccounts))
	for _, account := range g.accounts {
		accounts = append(accounts, account)
	}
	accounts = append(accounts, g.additionalAccounts...)
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
	service.probe = rejectingMigrationProbe{t: t}
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
	if gateway.testCalls != 0 {
		t.Fatalf("编辑保存调用了连通性测试，次数 = %d", gateway.testCalls)
	}
}

func TestMigrateAPIRejectsCredentialBoundToAnotherAccountBeforeSwitch(t *testing.T) {
	service, state, gateway, _ := newMigrationService()
	gateway.additionalAccounts = []proaccountgateway.AccountSnapshot{{
		Platform: "openai", AuthType: "api", SourceType: proaccountgateway.SourceCodexAPIKey,
		SourceLocator: "index:2", AuthIndex: "auth-new", Enabled: true, ModelRuleVersion: "rule-other",
	}}

	result, err := service.Update(context.Background(), migrationUpdateInput())
	if !errors.Is(err, ErrCredentialAlreadyBound) {
		t.Fatalf("重复凭证错误 = %v", err)
	}
	if result.Operation.State != model.ProOperationStateFailed || result.Operation.ErrorCode != "replacement_credential_conflict" {
		t.Fatalf("重复凭证操作状态 = %#v", result.Operation)
	}
	if state.account.Binding.AuthIndex != "auth-old" || !state.account.Enabled {
		t.Fatalf("重复凭证冲突改变了原账号：%#v", state.account)
	}
	if _, exists := gateway.accounts["auth-new"]; exists {
		t.Fatal("重复凭证冲突后候选凭证未清理")
	}
	joined := strings.Join(gateway.calls, ",")
	if strings.Contains(joined, "enabled:") || joined != "create:auth-new,delete:auth-new" {
		t.Fatalf("重复凭证冲突仍执行了切换：%s", joined)
	}
	if gateway.testCalls != 0 {
		t.Fatalf("重复凭证冲突调用了连通性测试，次数 = %d", gateway.testCalls)
	}
}

func TestReplacementAPISourceTypeUsesCurrentOrExplicitProtocol(t *testing.T) {
	tests := []struct {
		name      string
		platform  string
		current   string
		mode      string
		want      string
		wantValid bool
	}{
		{name: "OpenAI 自动沿用 Responses", platform: "openai", current: proaccountgateway.SourceCodexAPIKey, mode: "auto", want: proaccountgateway.SourceCodexAPIKey, wantValid: true},
		{name: "OpenAI 自动沿用 Chat Completions", platform: "openai", current: proaccountgateway.SourceOpenAICompatibility, mode: "", want: proaccountgateway.SourceOpenAICompatibility, wantValid: true},
		{name: "OpenAI 显式切换 Responses", platform: "openai", current: proaccountgateway.SourceOpenAICompatibility, mode: "responses", want: proaccountgateway.SourceCodexAPIKey, wantValid: true},
		{name: "OpenAI 显式切换 Chat Completions", platform: "openai", current: proaccountgateway.SourceCodexAPIKey, mode: "chat_completions", want: proaccountgateway.SourceOpenAICompatibility, wantValid: true},
		{name: "Anthropic 沿用当前协议", platform: "anthropic", current: proaccountgateway.SourceClaudeAPIKey, mode: "auto", want: proaccountgateway.SourceClaudeAPIKey, wantValid: true},
		{name: "拒绝未知组合", platform: "openai", current: "unknown", mode: "auto", wantValid: false},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got, valid := replacementAPISourceType(test.platform, test.current, test.mode)
			if got != test.want || valid != test.wantValid {
				t.Fatalf("凭证类型 = %q,%t，期望 %q,%t", got, valid, test.want, test.wantValid)
			}
		})
	}
}

func TestCreateAPINormalizesFullChatEndpointBeforeCompatibilitySave(t *testing.T) {
	stopErr := errors.New("停止在凭证保存阶段")
	gateway := &migrationGateway{
		accounts: map[string]proaccountgateway.AccountSnapshot{},
		replacement: proaccountgateway.AccountSnapshot{
			Platform: "openai", AuthType: "api", SourceType: proaccountgateway.SourceOpenAICompatibility,
			SourceLocator: "provider:0:key:0", AuthIndex: "auth-new", Enabled: false,
		},
		createErr: stopErr, deleteErrors: map[string]error{},
	}
	service := New(nil, nil, recoverySetupStub{}, gateway,
		migrationProbe{sourceType: proaccountgateway.SourceOpenAICompatibility}, &migrationOperations{})

	_, err := service.CreateAPI(context.Background(), CreateAPIInput{
		OperationID: "create-normalized", IdempotencyKey: "create-normalized-key",
		Platform: "openai", BaseURL: "https://opencode.ai/zen/go/v1/chat/completions",
		APIKey: "test-key", ProtocolMode: "chat_completions", TestModel: "glm-5.2",
	})
	if !errors.Is(err, stopErr) {
		t.Fatalf("创建流程错误 = %v，期望停在凭证保存阶段", err)
	}
	if gateway.createInput.BaseURL != "https://opencode.ai/zen/go/v1" {
		t.Fatalf("创建保存地址 = %q", gateway.createInput.BaseURL)
	}
}

func TestCreateAPIRequiresOfficialClientCompatibilityCapability(t *testing.T) {
	gateway := &migrationGateway{
		accounts: map[string]proaccountgateway.AccountSnapshot{},
		replacement: proaccountgateway.AccountSnapshot{
			Platform: "openai", AuthType: "api", SourceType: proaccountgateway.SourceCodexAPIKey,
			SourceLocator: "index:0", AuthIndex: "auth-new", Enabled: false,
		},
		deleteErrors: map[string]error{},
	}
	service := New(nil, nil, recoverySetupStub{}, gateway,
		migrationProbe{sourceType: proaccountgateway.SourceCodexAPIKey}, &migrationOperations{})
	input := CreateAPIInput{
		OperationID: "create-compatibility", IdempotencyKey: "create-compatibility-key",
		Platform: "openai", BaseURL: "https://api.openai.com/v1", APIKey: "test-key", ProtocolMode: "responses",
		OfficialClientCompatibility: &proaccountgateway.OfficialClientCompatibility{Enabled: true},
	}

	_, err := service.CreateAPI(context.Background(), input)
	if !errors.Is(err, proaccountgateway.ErrOfficialClientCompatibilityUnsupported) {
		t.Fatalf("旧 Gateway 创建错误 = %v", err)
	}
	if gateway.createInput.SourceType != "" {
		t.Fatalf("能力不足时仍写入了凭据：%#v", gateway.createInput)
	}

	stopErr := errors.New("停止在凭据保存阶段")
	gateway.capabilities.OfficialClientCompatibility = true
	gateway.createErr = stopErr
	_, err = service.CreateAPI(context.Background(), input)
	if !errors.Is(err, stopErr) {
		t.Fatalf("新 Gateway 创建错误 = %v", err)
	}
	if gateway.createInput.OfficialClientCompatibility == nil || !gateway.createInput.OfficialClientCompatibility.Enabled {
		t.Fatalf("创建请求未携带兼容配置：%#v", gateway.createInput)
	}
}

func TestMigrateAPINormalizesFullChatEndpointBeforeCompatibilitySave(t *testing.T) {
	service, _, gateway, _ := newMigrationService()
	gateway.replacement = proaccountgateway.AccountSnapshot{
		Platform: "openai", AuthType: "api", SourceType: proaccountgateway.SourceOpenAICompatibility,
		SourceLocator: "provider:1:key:0", AuthIndex: "auth-new", Enabled: false,
	}
	stopErr := errors.New("停止在替换凭证保存阶段")
	gateway.createErr = stopErr
	fullEndpoint := "https://opencode.ai/zen/go/v1/chat/completions"
	input := migrationUpdateInput()
	input.BaseURL = &fullEndpoint
	input.ProtocolMode = "chat_completions"

	_, err := service.Update(context.Background(), input)
	if !errors.Is(err, stopErr) {
		t.Fatalf("编辑迁移错误 = %v，期望停在替换凭证保存阶段", err)
	}
	if gateway.createInput.BaseURL != "https://opencode.ai/zen/go/v1" {
		t.Fatalf("编辑保存地址 = %q", gateway.createInput.BaseURL)
	}
}

func TestMigrateAPIInheritsOfficialClientCompatibility(t *testing.T) {
	service, _, gateway, _ := newMigrationService()
	want := &proaccountgateway.OfficialClientCompatibility{
		Enabled: true, Profile: "codex-desktop-0.145.0-alpha.18-v1",
	}
	gateway.capabilities.OfficialClientCompatibility = true
	gateway.editable = proaccountgateway.EditableAccount{
		BaseURL: "https://old.example/v1", Headers: map[string]string{},
		OfficialClientCompatibilitySupported: true, OfficialClientCompatibility: want,
	}

	if _, err := service.Update(context.Background(), migrationUpdateInput()); err != nil {
		t.Fatalf("迁移继承兼容配置：%v", err)
	}
	if gateway.createInput.OfficialClientCompatibility == nil || *gateway.createInput.OfficialClientCompatibility != *want {
		t.Fatalf("替换凭证未继承兼容配置：%#v", gateway.createInput.OfficialClientCompatibility)
	}
}

func TestMigrateAPIRejectsEnabledCompatibilityForChatCompletions(t *testing.T) {
	service, _, gateway, _ := newMigrationService()
	gateway.capabilities.OfficialClientCompatibility = true
	gateway.editable = proaccountgateway.EditableAccount{
		BaseURL: "https://old.example/v1", Headers: map[string]string{},
		OfficialClientCompatibilitySupported: true,
		OfficialClientCompatibility: &proaccountgateway.OfficialClientCompatibility{
			Enabled: true, Profile: "codex-desktop-0.145.0-alpha.18-v1",
		},
	}
	input := migrationUpdateInput()
	input.ProtocolMode = "chat_completions"

	_, err := service.Update(context.Background(), input)
	if !errors.Is(err, proaccountgateway.ErrOfficialClientCompatibilityUnsupported) {
		t.Fatalf("迁移到 Chat Completions 错误 = %v", err)
	}
	if gateway.createInput.SourceType != "" {
		t.Fatalf("不支持的迁移仍创建了替换凭证：%#v", gateway.createInput)
	}
}

func TestHotUpdateRestoresCompatibilityWhenLaterStepFails(t *testing.T) {
	service, _, gateway, _ := newMigrationService()
	gateway.compatibility = proaccountgateway.OfficialClientCompatibility{
		Enabled: false, Profile: "codex-desktop-0.145.0-alpha.18-v1",
	}
	gateway.proxyErr = errors.New("代理更新失败")
	proxyURL := "http://proxy.example:8080"
	input := UpdateInput{
		MutationInput: MutationInput{
			AccountID: "account-1", OperationID: "hot-update", IdempotencyKey: "hot-update-key", ExpectedVersion: 1,
		},
		ProxyURL: &proxyURL,
		OfficialClientCompatibility: &proaccountgateway.OfficialClientCompatibility{
			Enabled: true, Profile: "codex-desktop-0.145.0-alpha.18-v1",
		},
	}

	_, err := service.Update(context.Background(), input)
	if err == nil {
		t.Fatal("代理失败时应返回错误")
	}
	if len(gateway.restored) != 1 || gateway.compatibility.Enabled {
		t.Fatalf("兼容配置未恢复：current=%#v restored=%#v", gateway.compatibility, gateway.restored)
	}
	if strings.Join(gateway.calls, ",") != "compatibility:write,proxy:update,compatibility:restore" {
		t.Fatalf("混合更新顺序 = %v", gateway.calls)
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

func TestMigrateAPICleansReplacementWhenRuntimeReadinessFails(t *testing.T) {
	service, state, gateway, _ := newMigrationService()
	gateway.createErr = proaccountgateway.ErrCredentialNotReady
	result, err := service.Update(context.Background(), migrationUpdateInput())
	if err == nil {
		t.Fatal("替换凭证运行时未就绪时应返回错误")
	}
	if result.Operation.State != model.ProOperationStateFailed {
		t.Fatalf("失败迁移终态 = %#v", result.Operation)
	}
	if _, replacementExists := gateway.accounts["auth-new"]; replacementExists {
		t.Fatal("运行时未就绪后遗留了替换凭证")
	}
	if oldAccount, oldExists := gateway.accounts["auth-old"]; !oldExists || !oldAccount.Enabled {
		t.Fatalf("运行时未就绪影响了旧凭证：%#v", gateway.accounts)
	}
	if state.account.Binding.AuthIndex != "auth-old" {
		t.Fatalf("运行时未就绪改变了 Manager 绑定：%#v", state.account.Binding)
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
	input := migrationUpdateInput()
	input.ProtocolMode = "auto"
	result, err := service.Update(context.Background(), input)
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
