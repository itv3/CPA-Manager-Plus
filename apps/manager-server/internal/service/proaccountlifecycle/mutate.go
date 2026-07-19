package proaccountlifecycle

import (
	"context"
	"errors"
	"strings"

	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/model"
	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/service/proaccountgateway"
	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/service/proaccountprobe"
	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/store"
)

// resolveBindingLocation 优先用稳定的 auth_index 反查凭证当前位置。
// 配置数组类账号(codex-api-key 等)在删除较早条目后索引会前移,直接使用
// 保存时的 index 定位会命中错误条目或返回 HTTP 400;auth_index 不受索引漂移影响。
// 返回值 gone=true 表示凭证在 Gateway 侧已不存在。
func (s *Service) resolveBindingLocation(ctx context.Context, setup store.Setup, account model.ProAccount) (sourceType string, sourceLocator string, gone bool) {
	sourceType = account.Binding.SourceType
	sourceLocator = account.Binding.SourceLocator
	authIndex := strings.TrimSpace(account.Binding.AuthIndex)
	if authIndex == "" {
		return sourceType, sourceLocator, false
	}
	snapshot, err := s.gateway.FindAccountByAuthIndex(ctx, setup.CPAUpstreamURL, setup.ManagementKey, authIndex)
	if err == nil {
		return snapshot.SourceType, snapshot.SourceLocator, false
	}
	if errors.Is(err, proaccountgateway.ErrGatewayAccountNotFound) {
		return sourceType, sourceLocator, true
	}
	return sourceType, sourceLocator, false
}

// syncBindingsAfterMutation 在删除或替换底层凭证后全量刷新绑定,
// 修正其余账号因配置数组索引前移产生的定位漂移。
func (s *Service) syncBindingsAfterMutation(ctx context.Context, setup store.Setup) {
	snapshot, err := s.gateway.Snapshot(ctx, setup.CPAUpstreamURL, setup.ManagementKey)
	if err != nil {
		return
	}
	discoveries := make([]model.ProAccountDiscovery, 0, len(snapshot.Accounts))
	for _, item := range snapshot.Accounts {
		discoveries = append(discoveries, discoveryFromSnapshot(item))
	}
	_, _ = s.repository.Sync(ctx, discoveries, s.now().UnixMilli(), false)
}

func (s *Service) SetEnabled(ctx context.Context, input MutationInput, enabled bool) (Result, error) {
	account, err := s.accounts.Get(ctx, strings.TrimSpace(input.AccountID))
	if err != nil {
		return Result{}, err
	}
	operationType := "disable"
	if enabled {
		operationType = "enable"
	}
	operation, created, err := s.start(ctx, input.OperationID, input.IdempotencyKey, operationType, account.ID, map[string]any{"enabled": enabled})
	if err != nil {
		return Result{Account: account, Operation: operation}, err
	}
	if !created {
		if operation.State == model.ProOperationStateEnabled {
			return Result{Account: account, Operation: operation}, nil
		}
		return Result{Account: account, Operation: operation}, ErrOperationState
	}
	if input.ExpectedVersion != account.Version {
		operation = s.fail(ctx, operation, "version_conflict", "账号版本已变化")
		return Result{Account: account, Operation: operation}, ErrResourceVersionConflict
	}
	if account.Binding == nil {
		return Result{Account: account, Operation: operation}, ErrInvalidRequest
	}
	setup, err := s.resolveSetup(ctx)
	if err != nil {
		return Result{Account: account, Operation: operation}, err
	}
	sourceType, sourceLocator, gone := s.resolveBindingLocation(ctx, setup, account)
	if gone {
		operation = s.fail(ctx, operation, "gateway_credential_missing", "Gateway 侧凭证已不存在,请同步存量或删除该账号")
		return Result{Account: account, Operation: operation}, ErrInvalidRequest
	}
	snapshot, err := s.gateway.SetAccountEnabled(ctx, setup.CPAUpstreamURL, setup.ManagementKey, sourceType, sourceLocator, enabled)
	if err != nil {
		operation = s.fail(ctx, operation, "gateway_status_update_failed", "Gateway 账号状态更新失败")
		return Result{Account: account, Operation: operation}, err
	}
	discovery := discoveryFromSnapshot(snapshot)
	discovery.Name = account.Name
	discovery.Email = account.Email
	updated, err := s.repository.RebindManaged(ctx, account.ID, input.ExpectedVersion, discovery, s.now().UnixMilli())
	if err != nil {
		_, _ = s.gateway.SetAccountEnabled(ctx, setup.CPAUpstreamURL, setup.ManagementKey, snapshot.SourceType, snapshot.SourceLocator, account.Enabled)
		operation = s.fail(ctx, operation, "manager_status_commit_failed", "Manager 状态提交失败，已恢复 Gateway 状态")
		return Result{Account: account, Operation: operation}, err
	}
	operation, err = s.transition(ctx, operation, model.ProOperationStateTested, account.ID, operation.Context, "", "", "restore_account_status")
	if err != nil {
		return Result{Account: updated, Operation: operation}, err
	}
	operation, err = s.transition(ctx, operation, model.ProOperationStateEnabled, account.ID, operation.Context, "", "", "")
	return Result{Account: updated, Operation: operation}, err
}

func (s *Service) Delete(ctx context.Context, input MutationInput) (Result, error) {
	account, err := s.accounts.Get(ctx, strings.TrimSpace(input.AccountID))
	if err != nil {
		return Result{}, err
	}
	operation, created, err := s.start(ctx, input.OperationID, input.IdempotencyKey, "delete", account.ID, map[string]any{})
	if err != nil {
		return Result{Account: account, Operation: operation}, err
	}
	if !created {
		if operation.State == model.ProOperationStateCancelled {
			return Result{Account: account, Operation: operation}, nil
		}
		return Result{Account: account, Operation: operation}, ErrOperationState
	}
	if input.ExpectedVersion != account.Version {
		operation = s.fail(ctx, operation, "version_conflict", "账号版本已变化")
		return Result{Account: account, Operation: operation}, ErrResourceVersionConflict
	}
	if account.Binding == nil {
		return Result{Account: account, Operation: operation}, ErrInvalidRequest
	}
	setup, err := s.resolveSetup(ctx)
	if err != nil {
		return Result{Account: account, Operation: operation}, err
	}
	sourceType, sourceLocator, gone := s.resolveBindingLocation(ctx, setup, account)
	contextValue := operation.Context
	contextValue["sourceType"] = sourceType
	contextValue["sourceLocator"] = sourceLocator
	contextValue["authIndex"] = account.Binding.AuthIndex
	operation, err = s.transition(ctx, operation, model.ProOperationStateCompensating, account.ID, contextValue, "", "正在删除底层凭证", "delete_credential")
	if err != nil {
		return Result{Account: account, Operation: operation}, err
	}
	// Gateway 侧凭证已不存在时只需清理 Manager 记录
	if !gone {
		if err := s.gateway.DeleteAccount(ctx, setup.CPAUpstreamURL, setup.ManagementKey, sourceType, sourceLocator); err != nil {
			return Result{Account: account, Operation: operation}, err
		}
	}
	deleted, err := s.repository.SoftDelete(ctx, account.ID, input.ExpectedVersion, s.now().UnixMilli())
	if err != nil {
		return Result{Account: account, Operation: operation}, err
	}
	operation, err = s.transition(ctx, operation, model.ProOperationStateCancelled, account.ID, contextValue, "", "底层凭证已删除", "delete_credential_completed")
	s.syncBindingsAfterMutation(ctx, setup)
	return Result{Account: deleted, Operation: operation}, err
}

func (s *Service) Update(ctx context.Context, input UpdateInput) (Result, error) {
	account, err := s.accounts.Get(ctx, strings.TrimSpace(input.AccountID))
	if err != nil {
		return Result{}, err
	}
	operation, created, err := s.start(ctx, input.OperationID, input.IdempotencyKey, "edit", account.ID, map[string]any{})
	if err != nil {
		return Result{Account: account, Operation: operation}, err
	}
	if !created {
		if operation.State == model.ProOperationStateEnabled {
			return Result{Account: account, Operation: operation}, nil
		}
		return Result{Account: account, Operation: operation}, ErrOperationState
	}
	if input.ExpectedVersion != account.Version {
		operation = s.fail(ctx, operation, "version_conflict", "账号版本已变化")
		return Result{Account: account, Operation: operation}, ErrResourceVersionConflict
	}
	if account.Binding == nil {
		return Result{Account: account, Operation: operation}, ErrInvalidRequest
	}
	if strings.TrimSpace(input.APIKey) != "" || input.BaseURL != nil {
		return s.migrateAPI(ctx, input, operation, account)
	}
	return s.updateRulesAndName(ctx, input, operation, account)
}

func (s *Service) migrateAPI(ctx context.Context, input UpdateInput, operation model.ProAccountDraft, account model.ProAccount) (Result, error) {
	if account.AuthType != "api" || strings.TrimSpace(input.APIKey) == "" {
		operation = s.fail(ctx, operation, "new_api_key_required", "修改 API 地址或协议时必须提供新 API Key")
		return Result{Account: account, Operation: operation}, ErrInvalidRequest
	}
	setup, err := s.resolveSetup(ctx)
	if err != nil {
		return Result{Account: account, Operation: operation}, err
	}
	oldSourceType, oldSourceLocator, gone := s.resolveBindingLocation(ctx, setup, account)
	if gone {
		operation = s.fail(ctx, operation, "gateway_credential_missing", "Gateway 侧凭证已不存在,请同步存量或删除该账号")
		return Result{Account: account, Operation: operation}, ErrInvalidRequest
	}
	editable, err := s.gateway.EditableAccount(ctx, setup.CPAUpstreamURL, setup.ManagementKey, oldSourceType, oldSourceLocator)
	if err != nil {
		return Result{Account: account, Operation: operation}, err
	}
	baseURL := editable.BaseURL
	if input.BaseURL != nil {
		baseURL = strings.TrimSpace(*input.BaseURL)
	}
	// 未显式修改代理时沿用旧配置的代理,避免迁移后代理丢失
	proxyURL := editable.ProxyURL
	if input.ProxyURL != nil {
		proxyURL = strings.TrimSpace(*input.ProxyURL)
	}
	allowed := input.AllowedModels
	mapping := input.ModelMapping
	if allowed == nil {
		allowed = account.AllowedModels
	}
	if mapping == nil {
		mapping = account.ModelMapping
	}
	headers := editable.Headers
	if input.Headers != nil {
		headers = *input.Headers
	}
	probe, err := s.probe.ProbeCandidate(ctx, proaccountprobe.Input{
		Platform: account.Platform, AuthType: "api", BaseURL: baseURL, APIKey: input.APIKey,
		ProxyURL: proxyURL, ProtocolMode: input.ProtocolMode, Model: input.TestModel,
		AllowedModels: allowed, ModelMapping: mapping, Headers: headers,
	})
	if err != nil || probe.SourceType == "" {
		operation = s.fail(ctx, operation, "candidate_probe_failed", "新凭证预探测失败，旧配置未修改")
		if err == nil {
			err = ErrConnectivityFailed
		}
		return Result{Account: account, Operation: operation, Probe: &probe}, err
	}
	contextValue := operation.Context
	contextValue["newSourceType"] = probe.SourceType
	contextValue["oldSourceType"] = oldSourceType
	contextValue["oldSourceLocator"] = oldSourceLocator
	contextValue["oldAuthIndex"] = account.Binding.AuthIndex
	contextValue["oldEnabled"] = account.Enabled
	operation, err = s.transition(ctx, operation, model.ProOperationStateProbed, account.ID, contextValue, "", "", "delete_replacement_credential")
	if err != nil {
		return Result{Account: account, Operation: operation, Probe: &probe}, err
	}
	savedBaseURL := normalizeAPIBaseURLForSource(probe.SourceType, baseURL)
	newSnapshot, err := s.gateway.CreateDisabledAPI(ctx, setup.CPAUpstreamURL, setup.ManagementKey, proaccountgateway.CreateAPIInput{
		Platform: account.Platform, SourceType: probe.SourceType, Name: account.Name,
		BaseURL: savedBaseURL, APIKey: input.APIKey, ProxyURL: proxyURL, Headers: headers, AllowedModels: allowed, ModelMapping: mapping, CatalogModels: probe.Models,
	})
	if err != nil {
		if newSnapshot.SourceType != "" && newSnapshot.SourceLocator != "" {
			_ = s.gateway.DeleteAccount(ctx, setup.CPAUpstreamURL, setup.ManagementKey, newSnapshot.SourceType, newSnapshot.SourceLocator)
		}
		operation = s.fail(ctx, operation, "replacement_create_failed", "替换凭证创建失败，旧配置未修改")
		return Result{Account: account, Operation: operation, Probe: &probe}, err
	}
	contextValue["replacementSourceType"] = newSnapshot.SourceType
	contextValue["replacementSourceLocator"] = newSnapshot.SourceLocator
	contextValue["replacementAuthIndex"] = newSnapshot.AuthIndex
	operation, err = s.transition(ctx, operation, model.ProOperationStateCredentialSavedDisabled, account.ID, contextValue, "", "", "delete_replacement_credential")
	if err != nil {
		_ = s.gateway.DeleteAccount(ctx, setup.CPAUpstreamURL, setup.ManagementKey, newSnapshot.SourceType, newSnapshot.SourceLocator)
		return Result{Account: account, Operation: operation, Probe: &probe}, err
	}
	operation, err = s.transition(ctx, operation, model.ProOperationStateModelsConfigured, account.ID, contextValue, "", "", "delete_replacement_credential")
	if err != nil {
		return Result{Account: account, Operation: operation, Probe: &probe}, err
	}
	rules, _ := proaccountgateway.NormalizeModelRules(proaccountgateway.ModelRules{AllowedModels: allowed, ModelMapping: mapping})
	clientModel := chooseClientTestModel(input.TestModel, allowed, mapping, probe.TestModel)
	if !proaccountgateway.ModelAllowed(clientModel, rules) {
		_ = s.gateway.DeleteAccount(ctx, setup.CPAUpstreamURL, setup.ManagementKey, newSnapshot.SourceType, newSnapshot.SourceLocator)
		operation = s.fail(ctx, operation, "model_not_allowed", "测试模型不在有效白名单内，旧配置未修改")
		return Result{Account: account, Operation: operation, Probe: &probe}, ErrConnectivityFailed
	}
	connectivity, err := s.gateway.TestAccount(ctx, setup.CPAUpstreamURL, setup.ManagementKey, proaccountgateway.AccountReference{
		Platform: account.Platform, AuthType: account.AuthType, SourceType: newSnapshot.SourceType,
		SourceLocator: newSnapshot.SourceLocator, AuthIndex: newSnapshot.AuthIndex,
	}, proaccountgateway.ResolveMappedModel(clientModel, rules))
	if err != nil || !connectivity.Success {
		_ = s.gateway.DeleteAccount(ctx, setup.CPAUpstreamURL, setup.ManagementKey, newSnapshot.SourceType, newSnapshot.SourceLocator)
		operation = s.fail(ctx, operation, "replacement_test_failed", "替换凭证测试失败，旧配置未修改")
		return Result{Account: account, Operation: operation, Probe: &probe, Connectivity: &connectivity}, ErrConnectivityFailed
	}
	operation, err = s.transition(ctx, operation, model.ProOperationStateTested, account.ID, contextValue, "", "", "delete_replacement_credential")
	if err != nil {
		return Result{Account: account, Operation: operation, Probe: &probe, Connectivity: &connectivity}, err
	}
	snapshot, err := s.gateway.Snapshot(ctx, setup.CPAUpstreamURL, setup.ManagementKey)
	if err != nil {
		return Result{Account: account, Operation: operation, Probe: &probe, Connectivity: &connectivity}, err
	}
	projectedLocator, err := proaccountgateway.ProjectedLocatorAfterDelete(snapshot.Accounts,
		proaccountgateway.AccountSnapshot{SourceType: oldSourceType, SourceLocator: oldSourceLocator}, newSnapshot)
	if err != nil {
		return Result{Account: account, Operation: operation, Probe: &probe, Connectivity: &connectivity}, err
	}
	contextValue["replacementProjectedLocator"] = projectedLocator
	oldStatusChanged := !proaccountgateway.SharesEnabledState(snapshot.Accounts,
		proaccountgateway.AccountSnapshot{SourceType: oldSourceType, SourceLocator: oldSourceLocator})
	contextValue["oldStatusChanged"] = oldStatusChanged
	operation, err = s.transition(ctx, operation, model.ProOperationStateCompensating, account.ID, contextValue,
		"replacement_switch_pending", "正在切换到已测试的替换凭证", "rollback_replacement_switch")
	if err != nil {
		_ = s.deleteAccountByAuthIndex(ctx, setup, newSnapshot.AuthIndex)
		return Result{Account: account, Operation: operation, Probe: &probe, Connectivity: &connectivity}, err
	}

	if oldStatusChanged {
		if _, err = s.gateway.SetAccountEnabled(ctx, setup.CPAUpstreamURL, setup.ManagementKey,
			oldSourceType, oldSourceLocator, false); err != nil {
			operation, _ = s.rollbackReplacementSwitch(ctx, setup, operation)
			return Result{Account: account, Operation: operation, Probe: &probe, Connectivity: &connectivity}, err
		}
	}
	enabledSnapshot := newSnapshot
	if account.Enabled {
		enabledSnapshot, err = s.gateway.SetAccountEnabled(ctx, setup.CPAUpstreamURL, setup.ManagementKey,
			newSnapshot.SourceType, newSnapshot.SourceLocator, true)
		if err != nil {
			operation, _ = s.rollbackReplacementSwitch(ctx, setup, operation)
			return Result{Account: account, Operation: operation, Probe: &probe, Connectivity: &connectivity}, err
		}
	}
	enabledSnapshot.SourceLocator = projectedLocator
	discovery := discoveryFromSnapshot(enabledSnapshot)
	if input.Name != nil {
		discovery.Name = strings.TrimSpace(*input.Name)
	} else {
		discovery.Name = account.Name
	}
	updated, err := s.repository.RebindManaged(ctx, account.ID, input.ExpectedVersion, discovery, s.now().UnixMilli())
	if err != nil {
		operation, _ = s.rollbackReplacementSwitch(ctx, setup, operation)
		return Result{Account: account, Operation: operation, Probe: &probe, Connectivity: &connectivity}, err
	}
	if err = s.deleteAccountByAuthIndex(ctx, setup, account.Binding.AuthIndex); err != nil {
		return Result{Account: updated, Operation: operation, Probe: &probe, Connectivity: &connectivity}, err
	}
	operation, err = s.transition(ctx, operation, model.ProOperationStateEnabled, account.ID, contextValue, "", "", "")
	s.syncBindingsAfterMutation(ctx, setup)
	return Result{Account: updated, Operation: operation, Probe: &probe, Connectivity: &connectivity}, err
}

func (s *Service) updateRulesAndName(ctx context.Context, input UpdateInput, operation model.ProAccountDraft, account model.ProAccount) (Result, error) {
	setup, err := s.resolveSetup(ctx)
	if err != nil {
		return Result{Account: account, Operation: operation}, err
	}
	sourceType, sourceLocator, gone := s.resolveBindingLocation(ctx, setup, account)
	if gone {
		operation = s.fail(ctx, operation, "gateway_credential_missing", "Gateway 侧凭证已不存在,请同步存量或删除该账号")
		return Result{Account: account, Operation: operation}, ErrInvalidRequest
	}
	// 仅代理变更走热更新:不重建凭证,auth_index 与绑定不漂移
	if input.ProxyURL != nil {
		if err := s.gateway.UpdateAccountProxy(ctx, setup.CPAUpstreamURL, setup.ManagementKey, sourceType, sourceLocator, *input.ProxyURL); err != nil {
			operation = s.fail(ctx, operation, "proxy_update_failed", "账号代理更新失败")
			return Result{Account: account, Operation: operation}, err
		}
	}
	allowed := input.AllowedModels
	mapping := input.ModelMapping
	if allowed == nil {
		allowed = account.AllowedModels
	}
	if mapping == nil {
		mapping = account.ModelMapping
	}
	previous, applied, err := s.gateway.WriteAndVerifyModelRules(ctx, setup.CPAUpstreamURL, setup.ManagementKey, sourceType, sourceLocator, proaccountgateway.ModelRules{
		AllowedModels: allowed, ModelMapping: mapping,
	})
	if err != nil {
		operation = s.fail(ctx, operation, "model_rules_failed", "模型规则更新失败")
		return Result{Account: account, Operation: operation}, err
	}
	updated, err := s.repository.UpdateModelRules(ctx, account.ID, input.ExpectedVersion, applied.AllowedModels, applied.ModelMapping, applied.ModelRuleVersion, s.now().UnixMilli())
	if err != nil {
		_ = s.gateway.RestoreModelRules(ctx, setup.CPAUpstreamURL, setup.ManagementKey, sourceType, sourceLocator, previous)
		operation = s.fail(ctx, operation, "manager_rule_commit_failed", "Manager 提交失败，已恢复 Gateway 规则")
		return Result{Account: account, Operation: operation}, err
	}
	account = updated
	operation, err = s.transition(ctx, operation, model.ProOperationStateModelsConfigured, account.ID, operation.Context, "", "", "restore_model_rules")
	if err != nil {
		return Result{Account: account, Operation: operation}, err
	}
	if input.Name != nil && strings.TrimSpace(*input.Name) != account.Name {
		discovery := model.ProAccountDiscovery{
			Platform: account.Platform, AuthType: account.AuthType, SourceType: sourceType,
			Name: strings.TrimSpace(*input.Name), Email: account.Email, Enabled: account.Enabled,
			HealthStatus: account.HealthStatus, LastError: account.LastError,
			AllowedModels: account.AllowedModels, ModelMapping: account.ModelMapping, ModelRuleVersion: account.ModelRuleVersion,
			ExpiresAtMS: account.ExpiresAtMS, AuthIndex: account.Binding.AuthIndex,
			SourceLocator: sourceLocator, SourceFingerprint: account.Binding.SourceFingerprint,
		}
		account, err = s.repository.RebindManaged(ctx, account.ID, account.Version, discovery, s.now().UnixMilli())
		if err != nil {
			return Result{Account: account, Operation: operation}, err
		}
	}
	operation, err = s.transition(ctx, operation, model.ProOperationStateEnabled, account.ID, operation.Context, "", "", "")
	return Result{Account: account, Operation: operation}, err
}

func (s *Service) Details(ctx context.Context, accountID string) (model.ProAccount, proaccountgateway.EditableAccount, error) {
	account, err := s.accounts.Get(ctx, strings.TrimSpace(accountID))
	if err != nil {
		return model.ProAccount{}, proaccountgateway.EditableAccount{}, err
	}
	if account.Binding == nil {
		return account, proaccountgateway.EditableAccount{}, ErrInvalidRequest
	}
	setup, err := s.resolveSetup(ctx)
	if err != nil {
		return account, proaccountgateway.EditableAccount{}, err
	}
	sourceType, sourceLocator, gone := s.resolveBindingLocation(ctx, setup, account)
	if gone {
		return account, proaccountgateway.EditableAccount{}, proaccountgateway.ErrGatewayAccountNotFound
	}
	editable, err := s.gateway.EditableAccount(ctx, setup.CPAUpstreamURL, setup.ManagementKey, sourceType, sourceLocator)
	return account, editable, err
}
