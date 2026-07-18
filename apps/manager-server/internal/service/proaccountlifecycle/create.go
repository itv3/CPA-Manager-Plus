package proaccountlifecycle

import (
	"context"
	"net/url"
	"sort"
	"strings"

	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/model"
	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/service/proaccountgateway"
	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/service/proaccountprobe"
	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/store"
)

func (s *Service) CreateAPI(ctx context.Context, input CreateAPIInput) (Result, error) {
	input.Platform = strings.ToLower(strings.TrimSpace(input.Platform))
	if input.Platform != "openai" && input.Platform != "anthropic" && input.Platform != "gemini" {
		return Result{}, ErrUnsupportedAccountType
	}
	if strings.TrimSpace(input.APIKey) == "" || strings.TrimSpace(input.BaseURL) == "" || strings.TrimSpace(input.IdempotencyKey) == "" {
		return Result{}, ErrInvalidRequest
	}
	operation, created, err := s.start(ctx, input.OperationID, input.IdempotencyKey, "add", "", map[string]any{
		"platform": input.Platform, "authType": "api", "baseOrigin": safeBaseOrigin(input.BaseURL),
	})
	if err != nil {
		return Result{Operation: operation}, err
	}
	if !created {
		if (operation.State == model.ProOperationStateEnabled || operation.State == model.ProOperationStateSavedDisabled) && operation.ProAccountID != "" {
			account, getErr := s.accounts.Get(ctx, operation.ProAccountID)
			return Result{Account: account, Operation: operation, SavedDisabled: operation.State == model.ProOperationStateSavedDisabled}, getErr
		}
		if operation.State != model.ProOperationStateProbed && operation.State != model.ProOperationStateDraftCreated {
			return Result{Operation: operation}, ErrOperationState
		}
	}
	probe, err := s.probe.ProbeCandidate(ctx, proaccountprobe.Input{
		Platform: input.Platform, AuthType: "api", BaseURL: input.BaseURL, APIKey: input.APIKey,
		ProxyURL: input.ProxyURL, ProtocolMode: input.ProtocolMode, Model: input.TestModel,
		AllowedModels: input.AllowedModels, ModelMapping: input.ModelMapping, Headers: input.Headers,
	})
	if err != nil || probe.SourceType == "" {
		operation = s.fail(ctx, operation, "candidate_probe_failed", "候选账号探测失败")
		if err == nil {
			err = ErrConnectivityFailed
		}
		return Result{Operation: operation, Probe: &probe}, err
	}
	if operation.State == model.ProOperationStateDraftCreated {
		contextValue := operation.Context
		contextValue["sourceType"] = probe.SourceType
		contextValue["selectedProtocol"] = probe.SelectedProtocol
		operation, err = s.transition(ctx, operation, model.ProOperationStateProbed, "", contextValue, "", "", "")
		if err != nil {
			return Result{Operation: operation, Probe: &probe}, err
		}
	}
	setup, err := s.resolveSetup(ctx)
	if err != nil {
		return Result{Operation: operation, Probe: &probe}, err
	}
	capabilities, err := s.gateway.Capabilities(ctx, setup.CPAUpstreamURL, setup.ManagementKey)
	if err != nil || !capabilities.AllowedModels {
		return Result{Operation: operation, Probe: &probe}, ErrGatewayCapability
	}
	snapshot, err := s.gateway.CreateDisabledAPI(ctx, setup.CPAUpstreamURL, setup.ManagementKey, proaccountgateway.CreateAPIInput{
		Platform: input.Platform, SourceType: probe.SourceType, Name: input.Name,
		BaseURL: input.BaseURL, APIKey: input.APIKey, ProxyURL: input.ProxyURL, Headers: input.Headers,
		AllowedModels: input.AllowedModels, ModelMapping: input.ModelMapping, CatalogModels: probe.Models,
	})
	if err != nil {
		if snapshot.SourceType != "" && snapshot.SourceLocator != "" {
			_ = s.gateway.DeleteAccount(ctx, setup.CPAUpstreamURL, setup.ManagementKey, snapshot.SourceType, snapshot.SourceLocator)
		}
		operation = s.fail(ctx, operation, "credential_create_failed", "停用凭证创建失败")
		return Result{Operation: operation, Probe: &probe}, err
	}
	if name := strings.TrimSpace(input.Name); name != "" {
		snapshot.Name = name
	}
	synced, err := s.repository.Sync(ctx, []model.ProAccountDiscovery{discoveryFromSnapshot(snapshot)}, s.now().UnixMilli(), false)
	if err != nil || len(synced.Items) != 1 || synced.Items[0].ProAccountID == "" {
		_ = s.gateway.DeleteAccount(ctx, setup.CPAUpstreamURL, setup.ManagementKey, snapshot.SourceType, snapshot.SourceLocator)
		operation = s.fail(ctx, operation, "manager_account_create_failed", "统一账号创建失败，已清理底层凭证")
		if err == nil {
			err = ErrInvalidRequest
		}
		return Result{Operation: operation, Probe: &probe}, err
	}
	account, err := s.accounts.Get(ctx, synced.Items[0].ProAccountID)
	if err != nil {
		return Result{Operation: operation, Probe: &probe}, err
	}
	contextValue := operation.Context
	contextValue["sourceType"] = snapshot.SourceType
	contextValue["sourceLocator"] = snapshot.SourceLocator
	contextValue["authIndex"] = snapshot.AuthIndex
	operation, err = s.transition(ctx, operation, model.ProOperationStateCredentialSavedDisabled, account.ID, contextValue, "", "", "delete_new_credential")
	if err != nil {
		_ = s.gateway.DeleteAccount(ctx, setup.CPAUpstreamURL, setup.ManagementKey, snapshot.SourceType, snapshot.SourceLocator)
		return Result{Account: account, Operation: operation, Probe: &probe}, err
	}
	result, err := s.completeCredential(ctx, setup, operation, account, completeOptions{
		AllowedModels: input.AllowedModels, ModelMapping: input.ModelMapping,
		TestModel:    chooseClientTestModel(input.TestModel, input.AllowedModels, input.ModelMapping, probe.TestModel),
		SaveDisabled: input.SaveDisabled, DraftOnly: input.DraftOnly, SkipTest: input.SkipTest,
	})
	result.Probe = &probe
	return result, err
}

func (s *Service) CompleteDraft(ctx context.Context, input CompleteDraftInput) (Result, error) {
	operation, err := s.operations.Get(ctx, strings.TrimSpace(input.OperationID))
	if err != nil {
		return Result{}, err
	}
	if operation.ProAccountID != strings.TrimSpace(input.AccountID) || operation.State != model.ProOperationStateCredentialSavedDisabled {
		return Result{Operation: operation}, ErrOperationState
	}
	account, err := s.accounts.Get(ctx, operation.ProAccountID)
	if err != nil {
		return Result{Operation: operation}, err
	}
	if input.ExpectedVersion != account.Version {
		return Result{Account: account, Operation: operation}, ErrResourceVersionConflict
	}
	setup, err := s.resolveSetup(ctx)
	if err != nil {
		return Result{Account: account, Operation: operation}, err
	}
	// CPA 与 sub2api 的 OAuth 创建在令牌兑换成功后直接保存并启用，不以额外连通性测试作为保存门槛。
	skipTest := strings.EqualFold(contextString(operation.Context, "authType"), "oauth") && account.AuthType == "oauth"
	return s.completeCredential(ctx, setup, operation, account, completeOptions{
		AllowedModels: input.AllowedModels, ModelMapping: input.ModelMapping,
		TestModel: input.TestModel, SaveDisabled: input.SaveDisabled, SkipTest: skipTest,
	})
}

// completeOptions 汇总凭证落地后的模型规则与启用策略。
type completeOptions struct {
	AllowedModels []string
	ModelMapping  map[string]string
	TestModel     string
	SaveDisabled  bool
	DraftOnly     bool
	// SkipTest 跳过最终连通性测试直接启用
	SkipTest bool
}

func (s *Service) completeCredential(ctx context.Context, setup store.Setup, operation model.ProAccountDraft, account model.ProAccount, options completeOptions) (Result, error) {
	allowedModels := options.AllowedModels
	modelMapping := options.ModelMapping
	testModel := options.TestModel
	saveDisabled := options.SaveDisabled
	draftOnly := options.DraftOnly
	if account.Binding == nil {
		return Result{Account: account, Operation: operation}, ErrInvalidRequest
	}
	desired, err := proaccountgateway.NormalizeModelRules(proaccountgateway.ModelRules{AllowedModels: allowedModels, ModelMapping: modelMapping})
	if err != nil {
		return Result{Account: account, Operation: operation}, err
	}
	previous, applied, err := s.gateway.WriteAndVerifyModelRules(ctx, setup.CPAUpstreamURL, setup.ManagementKey, account.Binding.SourceType, account.Binding.SourceLocator, desired)
	if err != nil {
		return s.compensateCreated(ctx, setup, operation, account, "model_rules_failed", err)
	}
	updated, err := s.repository.UpdateModelRules(ctx, account.ID, account.Version, applied.AllowedModels, applied.ModelMapping, applied.ModelRuleVersion, s.now().UnixMilli())
	if err != nil {
		_ = s.gateway.RestoreModelRules(ctx, setup.CPAUpstreamURL, setup.ManagementKey, account.Binding.SourceType, account.Binding.SourceLocator, previous)
		return s.compensateCreated(ctx, setup, operation, account, "manager_rule_commit_failed", err)
	}
	account = updated
	operation, err = s.transition(ctx, operation, model.ProOperationStateModelsConfigured, account.ID, operation.Context, "", "", "delete_new_credential")
	if err != nil {
		return Result{Account: account, Operation: operation}, err
	}
	if draftOnly {
		operation, err = s.transition(ctx, operation, model.ProOperationStateSavedDisabled, account.ID, operation.Context, "", "账号已保存为停用状态", "")
		return Result{Account: account, Operation: operation, SavedDisabled: true}, err
	}
	var connectivity *proaccountgateway.ConnectivityResult
	if options.SkipTest {
		// 与 sub2api 创建行为一致:保存即启用,连通性由独立的测试入口验证
		operation, err = s.transition(ctx, operation, model.ProOperationStateTested, account.ID, operation.Context, "", "已跳过保存前连通性测试", "delete_new_credential")
		if err != nil {
			return Result{Account: account, Operation: operation}, err
		}
	} else {
		testModel = chooseClientTestModel(testModel, applied.AllowedModels, applied.ModelMapping, "")
		if !proaccountgateway.ModelAllowed(testModel, applied) {
			return s.handleFailedFinalTest(ctx, setup, operation, account, proaccountgateway.ConnectivityResult{ErrorCode: "model_not_allowed"}, saveDisabled)
		}
		upstreamModel := proaccountgateway.ResolveMappedModel(testModel, applied)
		result, err := s.gateway.TestAccount(ctx, setup.CPAUpstreamURL, setup.ManagementKey, proaccountgateway.AccountReference{
			Platform: account.Platform, AuthType: account.AuthType, SourceType: account.Binding.SourceType,
			SourceLocator: account.Binding.SourceLocator, AuthIndex: account.Binding.AuthIndex,
		}, upstreamModel)
		if err != nil {
			result.ErrorCode = "connectivity_request_failed"
			return s.handleFailedFinalTest(ctx, setup, operation, account, result, saveDisabled)
		}
		if !result.Success {
			return s.handleFailedFinalTest(ctx, setup, operation, account, result, saveDisabled)
		}
		connectivity = &result
		_, _ = s.repository.RecordTestResult(ctx, account.ID, true, "", s.now().UnixMilli())
		operation, err = s.transition(ctx, operation, model.ProOperationStateTested, account.ID, operation.Context, "", "", "delete_new_credential")
		if err != nil {
			return Result{Account: account, Operation: operation, Connectivity: connectivity}, err
		}
	}
	enabledSnapshot, err := s.gateway.SetAccountEnabled(ctx, setup.CPAUpstreamURL, setup.ManagementKey, account.Binding.SourceType, account.Binding.SourceLocator, true)
	if err != nil {
		return Result{Account: account, Operation: operation, Connectivity: connectivity}, err
	}
	discovery := discoveryFromSnapshot(enabledSnapshot)
	if account.Name != "" {
		discovery.Name = account.Name
	}
	account, err = s.repository.RebindManaged(ctx, account.ID, account.Version, discovery, s.now().UnixMilli())
	if err != nil {
		_, _ = s.gateway.SetAccountEnabled(ctx, setup.CPAUpstreamURL, setup.ManagementKey, enabledSnapshot.SourceType, enabledSnapshot.SourceLocator, false)
		return Result{Account: account, Operation: operation, Connectivity: connectivity}, err
	}
	operation, err = s.transition(ctx, operation, model.ProOperationStateEnabled, account.ID, operation.Context, "", "", "")
	return Result{Account: account, Operation: operation, Connectivity: connectivity}, err
}

func (s *Service) handleFailedFinalTest(ctx context.Context, setup store.Setup, operation model.ProAccountDraft, account model.ProAccount, connectivity proaccountgateway.ConnectivityResult, saveDisabled bool) (Result, error) {
	code := connectivity.ErrorCode
	if code == "" {
		code = "connectivity_test_failed"
	}
	_, _ = s.repository.RecordTestResult(ctx, account.ID, false, code, s.now().UnixMilli())
	if saveDisabled {
		var err error
		operation, err = s.transition(ctx, operation, model.ProOperationStateSavedDisabled, account.ID, operation.Context, code, "连通性测试失败，凭证按用户选择保持停用", "")
		return Result{Account: account, Operation: operation, Connectivity: &connectivity, SavedDisabled: true}, err
	}
	result, err := s.compensateCreated(ctx, setup, operation, account, code, ErrConnectivityFailed)
	result.Connectivity = &connectivity
	return result, err
}

func (s *Service) compensateCreated(ctx context.Context, setup store.Setup, operation model.ProAccountDraft, account model.ProAccount, code string, cause error) (Result, error) {
	operation, transitionErr := s.transition(ctx, operation, model.ProOperationStateCompensating, account.ID, operation.Context, code, "正在清理未完成的停用凭证", "delete_new_credential")
	if transitionErr != nil {
		return Result{Account: account, Operation: operation}, transitionErr
	}
	if account.Binding == nil {
		return Result{Account: account, Operation: operation}, cause
	}
	if err := s.gateway.DeleteAccount(ctx, setup.CPAUpstreamURL, setup.ManagementKey, account.Binding.SourceType, account.Binding.SourceLocator); err != nil {
		return Result{Account: account, Operation: operation}, cause
	}
	deleted, err := s.repository.SoftDelete(ctx, account.ID, account.Version, s.now().UnixMilli())
	if err == nil {
		account = deleted
	}
	operation = s.fail(ctx, operation, code, "未完成的停用凭证已清理")
	return Result{Account: account, Operation: operation}, cause
}

func chooseClientTestModel(requested string, allowed []string, mapping map[string]string, fallback string) string {
	if requested = strings.TrimSpace(requested); requested != "" {
		return requested
	}
	if len(allowed) > 0 {
		values := append([]string(nil), allowed...)
		sort.Strings(values)
		for _, value := range values {
			if !strings.Contains(value, "*") {
				return value
			}
		}
	}
	if len(mapping) > 0 {
		aliases := make([]string, 0, len(mapping))
		for alias := range mapping {
			if !strings.Contains(alias, "*") {
				aliases = append(aliases, alias)
			}
		}
		sort.Strings(aliases)
		if len(aliases) > 0 {
			return aliases[0]
		}
	}
	return strings.TrimSpace(fallback)
}

func safeBaseOrigin(raw string) string {
	parsed, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || parsed.Scheme == "" || parsed.Host == "" {
		return ""
	}
	return strings.ToLower(parsed.Scheme) + "://" + strings.ToLower(parsed.Host)
}
