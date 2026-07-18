package proaccountlifecycle

import (
	"context"
	"errors"
	"strings"

	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/model"
	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/repository/proaccountdraft"
	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/service/proaccountgateway"
	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/store"
)

type reauthorizationGateway interface {
	ListRuntimeModels(ctx context.Context, baseURL string, managementKey string, authIndex string, sourceLocator string) ([]string, error)
}

type credentialDraftFinalizer interface {
	FinalizeCredentialDraft(ctx context.Context, baseURL string, managementKey string, sourceType string, sourceLocator string, enabled bool) (proaccountgateway.AccountSnapshot, error)
}

type activeReauthorizationFinder interface {
	FindActiveReauthorization(ctx context.Context, accountID string) (model.ProAccountDraft, bool, error)
}

func (s *Service) StartReauthorization(ctx context.Context, input ReauthorizationStartInput) (OAuthResult, error) {
	s.reauthorizationMu.Lock()
	defer s.reauthorizationMu.Unlock()

	account, err := s.accounts.Get(ctx, strings.TrimSpace(input.AccountID))
	if err != nil {
		return OAuthResult{}, err
	}
	if finder, ok := s.operations.(activeReauthorizationFinder); ok {
		active, found, findErr := finder.FindActiveReauthorization(ctx, account.ID)
		if findErr != nil {
			return OAuthResult{Account: &account}, findErr
		}
		if found {
			if active.IdempotencyKey == strings.TrimSpace(input.IdempotencyKey) {
				return replayReauthorization(active, account)
			}
			// 操作 ID 相同时仍交给仓储判定 ID/幂等键冲突，保持原有错误语义。
			if active.OperationID != strings.TrimSpace(input.OperationID) {
				return OAuthResult{Operation: active, Account: &account}, ErrReauthorizationInProgress
			}
		}
	}
	operation, created, err := s.start(ctx, input.OperationID, input.IdempotencyKey, "reauthorize", account.ID, map[string]any{})
	if err != nil {
		if errors.Is(err, proaccountdraft.ErrActiveReauthorization) {
			return OAuthResult{Operation: operation, Account: &account}, ErrReauthorizationInProgress
		}
		return OAuthResult{Operation: operation, Account: &account}, err
	}
	if !created {
		return replayReauthorization(operation, account)
	}
	if input.ExpectedVersion != account.Version {
		operation = s.fail(ctx, operation, "version_conflict", "账号版本已变化")
		return OAuthResult{Operation: operation, Account: &account}, ErrResourceVersionConflict
	}
	if !supportsTargetedReauthorization(account) {
		operation = s.fail(ctx, operation, "reauthorization_unsupported", "当前仅支持 OpenAI Codex OAuth 账号定向重新授权")
		return OAuthResult{Operation: operation, Account: &account}, ErrUnsupportedAccountType
	}
	if _, ok := s.gateway.(reauthorizationGateway); !ok {
		operation = s.fail(ctx, operation, "gateway_capability_missing", "Gateway 缺少安全重新授权所需能力")
		return OAuthResult{Operation: operation, Account: &account}, ErrGatewayCapability
	}
	setup, err := s.resolveSetup(ctx)
	if err != nil {
		operation = s.fail(ctx, operation, "gateway_connection_unavailable", "Gateway 连接不可用")
		return OAuthResult{Operation: operation, Account: &account}, err
	}
	capabilities, err := s.gateway.Capabilities(ctx, setup.CPAUpstreamURL, setup.ManagementKey)
	if err != nil || !capabilities.TargetedReauthorization || !capabilities.CredentialDraft || !capabilities.AllowedModels {
		operation = s.fail(ctx, operation, "gateway_capability_missing", "Gateway 缺少 OAuth 草稿或模型规则能力")
		return OAuthResult{Operation: operation, Account: &account}, ErrGatewayCapability
	}
	oldSnapshot, err := s.gateway.FindAccountByAuthIndex(ctx, setup.CPAUpstreamURL, setup.ManagementKey, account.Binding.AuthIndex)
	if err != nil || !strings.EqualFold(oldSnapshot.Provider, "codex") {
		operation = s.fail(ctx, operation, "target_identity_unavailable", "无法确认目标 Codex 账号身份")
		return OAuthResult{Operation: operation, Account: &account}, ErrOAuthIdentityMismatch
	}
	snapshot, err := s.gateway.Snapshot(ctx, setup.CPAUpstreamURL, setup.ManagementKey)
	if err != nil {
		operation = s.fail(ctx, operation, "gateway_snapshot_failed", "无法读取 Gateway 账号快照")
		return OAuthResult{Operation: operation, Account: &account}, err
	}
	knownDrafts := make([]string, 0)
	for _, item := range snapshot.Accounts {
		if item.Platform == account.Platform && item.CredentialDraft {
			knownDrafts = append(knownDrafts, item.SourceLocator)
		}
	}
	contextValue := map[string]any{
		"platform":             account.Platform,
		"expectedVersion":      account.Version,
		"knownDraftLocators":   knownDrafts,
		"oldAuthIndex":         account.Binding.AuthIndex,
		"oldSourceType":        oldSnapshot.SourceType,
		"oldSourceLocator":     oldSnapshot.SourceLocator,
		"oldProvider":          oldSnapshot.Provider,
		"oldEmail":             valueOr(oldSnapshot.Email, account.Email),
		"oldUpstreamAccountID": oldSnapshot.UpstreamAccountID,
		"oldFingerprint":       oldSnapshot.SourceFingerprint,
		"oldEnabled":           oldSnapshot.Enabled,
	}
	oauth, err := s.gateway.StartOAuth(ctx, setup.CPAUpstreamURL, setup.ManagementKey, account.Platform)
	if err != nil {
		operation = s.fail(ctx, operation, "oauth_start_failed", "OAuth 授权启动失败")
		return OAuthResult{Operation: operation, Account: &account}, err
	}
	contextValue["oauthState"] = oauth.State
	contextValue["oauthURL"] = oauth.URL
	operation, err = s.transition(ctx, operation, model.ProOperationStateProbed, account.ID, contextValue, "", "正在等待重新授权", "cancel_oauth_session")
	if err != nil {
		_ = s.gateway.CancelOAuth(ctx, setup.CPAUpstreamURL, setup.ManagementKey, oauth.State)
		return OAuthResult{Operation: operation, Account: &account}, err
	}
	return OAuthResult{Operation: operation, OAuth: &oauth, Status: "wait", Account: &account}, nil
}

func replayReauthorization(operation model.ProAccountDraft, account model.ProAccount) (OAuthResult, error) {
	result := OAuthResult{Operation: operation, Account: &account}
	switch operation.State {
	case model.ProOperationStateEnabled:
		result.Status = "ok"
		return result, nil
	case model.ProOperationStateFailed, model.ProOperationStateCancelled, model.ProOperationStateSavedDisabled:
		return result, ErrOperationState
	default:
		result.Status = "wait"
		state := contextString(operation.Context, "oauthState")
		if rawURL := contextString(operation.Context, "oauthURL"); rawURL != "" || state != "" {
			result.OAuth = &proaccountgateway.OAuthStartResult{URL: rawURL, State: state}
		}
		return result, nil
	}
}

func (s *Service) ReauthorizationStatus(ctx context.Context, accountID string, operationID string) (OAuthResult, error) {
	s.reauthorizationMu.Lock()
	defer s.reauthorizationMu.Unlock()

	operation, account, err := s.reauthorizationOperation(ctx, accountID, operationID)
	if err != nil {
		return OAuthResult{Operation: operation}, err
	}
	switch operation.State {
	case model.ProOperationStateEnabled:
		return OAuthResult{Operation: operation, Status: "ok", Account: &account}, nil
	case model.ProOperationStateFailed:
		return OAuthResult{Operation: operation, Status: "error", Account: &account}, nil
	case model.ProOperationStateCancelled:
		return OAuthResult{Operation: operation, Status: "cancelled", Account: &account}, nil
	case model.ProOperationStateCompensating:
		if recoverErr := s.Recover(ctx, operation); recoverErr != nil {
			return OAuthResult{Operation: operation, Status: "wait", Account: &account}, nil
		}
		operation, account, err = s.reauthorizationOperation(ctx, accountID, operationID)
		if err != nil {
			return OAuthResult{Operation: operation}, err
		}
		if operation.State == model.ProOperationStateEnabled {
			return OAuthResult{Operation: operation, Status: "ok", Account: &account}, nil
		}
		return OAuthResult{Operation: operation, Status: "error", Account: &account}, nil
	case model.ProOperationStateProbed:
	default:
		return OAuthResult{Operation: operation, Account: &account}, ErrOperationState
	}

	setup, err := s.resolveSetup(ctx)
	if err != nil {
		return OAuthResult{Operation: operation, Account: &account}, err
	}
	state := contextString(operation.Context, "oauthState")
	status, err := s.gateway.OAuthStatus(ctx, setup.CPAUpstreamURL, setup.ManagementKey, state)
	if err != nil {
		return OAuthResult{Operation: operation, Account: &account}, err
	}
	if status.Status == "wait" {
		return OAuthResult{Operation: operation, Status: "wait", Account: &account}, nil
	}
	if status.Status != "ok" {
		_ = s.gateway.CancelOAuth(ctx, setup.CPAUpstreamURL, setup.ManagementKey, state)
		operation = s.fail(ctx, operation, "oauth_failed", "OAuth 授权失败或已经超时")
		return OAuthResult{Operation: operation, Status: "error", Account: &account}, nil
	}

	replacement, candidateCount, err := s.findReauthorizationCandidate(ctx, setup.CPAUpstreamURL, setup.ManagementKey, operation)
	if err != nil {
		return OAuthResult{Operation: operation, Account: &account}, err
	}
	if candidateCount == 0 {
		return OAuthResult{Operation: operation, Status: "credential_pending", Account: &account}, nil
	}
	if candidateCount > 1 {
		return OAuthResult{Operation: operation, Status: "ambiguous", Account: &account}, ErrOAuthCredentialAmbiguous
	}
	contextValue := operation.Context
	contextValue["replacementSourceType"] = replacement.SourceType
	contextValue["replacementSourceLocator"] = replacement.SourceLocator
	contextValue["replacementAuthIndex"] = replacement.AuthIndex
	operation, err = s.transition(ctx, operation, model.ProOperationStateCredentialSavedDisabled, account.ID, contextValue, "", "新 OAuth 凭据已保存为停用草稿", "delete_replacement_credential")
	if err != nil {
		_ = s.deleteAccountByAuthIndex(ctx, setup, replacement.AuthIndex)
		return OAuthResult{Operation: operation, Account: &account}, err
	}
	if !matchesReauthorizationIdentity(operation.Context, replacement) {
		return s.failReauthorizationCandidate(ctx, setup.CPAUpstreamURL, setup.ManagementKey, operation, account, replacement.AuthIndex,
			"oauth_identity_mismatch", "新授权账号与目标账号身份不一致", ErrOAuthIdentityMismatch)
	}

	rules := proaccountgateway.ModelRules{AllowedModels: account.AllowedModels, ModelMapping: account.ModelMapping}
	_, applied, err := s.gateway.WriteAndVerifyModelRules(ctx, setup.CPAUpstreamURL, setup.ManagementKey, replacement.SourceType, replacement.SourceLocator, rules)
	if err != nil {
		return s.failReauthorizationCandidate(ctx, setup.CPAUpstreamURL, setup.ManagementKey, operation, account, replacement.AuthIndex,
			"replacement_rules_failed", "新凭据模型规则写入失败", err)
	}
	operation, err = s.transition(ctx, operation, model.ProOperationStateModelsConfigured, account.ID, contextValue, "", "新凭据模型规则已校验", "delete_replacement_credential")
	if err != nil {
		return OAuthResult{Operation: operation, Account: &account}, err
	}
	modelLister := s.gateway.(reauthorizationGateway)
	models, err := modelLister.ListRuntimeModels(ctx, setup.CPAUpstreamURL, setup.ManagementKey, replacement.AuthIndex, replacement.SourceLocator)
	if err != nil {
		return s.failReauthorizationCandidate(ctx, setup.CPAUpstreamURL, setup.ManagementKey, operation, account, replacement.AuthIndex,
			"replacement_models_unavailable", "无法读取新凭据模型列表", err)
	}
	testModel := chooseClientTestModel("", applied.AllowedModels, applied.ModelMapping, firstString(models))
	if testModel == "" {
		return s.failReauthorizationCandidate(ctx, setup.CPAUpstreamURL, setup.ManagementKey, operation, account, replacement.AuthIndex,
			"replacement_model_missing", "新凭据没有可测试模型", ErrConnectivityFailed)
	}
	testModel = proaccountgateway.ResolveMappedModel(testModel, applied)
	connectivity, err := s.gateway.TestAccount(ctx, setup.CPAUpstreamURL, setup.ManagementKey, proaccountgateway.AccountReference{
		Platform: replacement.Platform, AuthType: replacement.AuthType, SourceType: replacement.SourceType,
		SourceLocator: replacement.SourceLocator, AuthIndex: replacement.AuthIndex,
	}, testModel)
	if err != nil || !connectivity.Success {
		if err == nil {
			err = ErrConnectivityFailed
		}
		return s.failReauthorizationCandidate(ctx, setup.CPAUpstreamURL, setup.ManagementKey, operation, account, replacement.AuthIndex,
			"replacement_test_failed", "新凭据连通性测试失败，旧凭据保持不变", err)
	}
	operation, err = s.transition(ctx, operation, model.ProOperationStateTested, account.ID, contextValue, "", "新凭据身份与连通性测试通过", "delete_replacement_credential")
	if err != nil {
		return OAuthResult{Operation: operation, Account: &account}, err
	}
	return s.switchReauthorizedAccount(ctx, setup.CPAUpstreamURL, setup.ManagementKey, operation, account, replacement)
}

func (s *Service) SubmitReauthorizationCallback(ctx context.Context, accountID string, input OAuthCallbackInput) (OAuthResult, error) {
	s.reauthorizationMu.Lock()
	defer s.reauthorizationMu.Unlock()

	operation, account, err := s.reauthorizationOperation(ctx, accountID, input.OperationID)
	if err != nil {
		return OAuthResult{Operation: operation}, err
	}
	if operation.State != model.ProOperationStateProbed {
		return OAuthResult{Operation: operation, Account: &account}, ErrOperationState
	}
	callback, err := parseOAuthCallbackInput(input.CallbackText)
	if err != nil {
		return OAuthResult{Operation: operation, Account: &account}, err
	}
	expectedState := contextString(operation.Context, "oauthState")
	if callback.State == "" {
		callback.State = strings.TrimSpace(input.CallbackState)
	}
	if callback.State != "" && callback.State != expectedState {
		return OAuthResult{Operation: operation, Account: &account}, ErrOAuthStateMismatch
	}
	callback.State = expectedState
	setup, err := s.resolveSetup(ctx)
	if err != nil {
		return OAuthResult{Operation: operation, Account: &account}, err
	}
	err = s.gateway.SubmitOAuthCallback(ctx, setup.CPAUpstreamURL, setup.ManagementKey, proaccountgateway.OAuthCallbackInput{
		Platform: account.Platform, Code: callback.Code, State: callback.State, Error: callback.Error,
	})
	if err != nil {
		return OAuthResult{Operation: operation, Account: &account}, err
	}
	return OAuthResult{Operation: operation, Status: "wait", Account: &account}, nil
}

func (s *Service) CancelReauthorization(ctx context.Context, accountID string, operationID string) (OAuthResult, error) {
	s.reauthorizationMu.Lock()
	defer s.reauthorizationMu.Unlock()

	operation, account, err := s.reauthorizationOperation(ctx, accountID, operationID)
	if err != nil {
		return OAuthResult{Operation: operation}, err
	}
	if operation.State == model.ProOperationStateEnabled || operation.State == model.ProOperationStateFailed || operation.State == model.ProOperationStateCancelled || operation.State == model.ProOperationStateCompensating {
		return OAuthResult{Operation: operation, Account: &account}, ErrOperationState
	}
	setup, err := s.resolveSetup(ctx)
	if err != nil {
		return OAuthResult{Operation: operation, Account: &account}, err
	}
	if state := contextString(operation.Context, "oauthState"); state != "" {
		_ = s.gateway.CancelOAuth(ctx, setup.CPAUpstreamURL, setup.ManagementKey, state)
	}
	if replacementAuthIndex := contextString(operation.Context, "replacementAuthIndex"); replacementAuthIndex != "" {
		if deleteErr := s.deleteAccountByAuthIndex(ctx, setup, replacementAuthIndex); deleteErr != nil {
			operation, _ = s.transition(ctx, operation, model.ProOperationStateCompensating, account.ID, operation.Context,
				"oauth_cancelled", "正在清理重新授权草稿", "delete_replacement_credential")
			return OAuthResult{Operation: operation, Account: &account}, deleteErr
		}
	}
	operation, err = s.transition(ctx, operation, model.ProOperationStateCancelled, account.ID, operation.Context, "", "重新授权已取消，旧凭据保持不变", "")
	return OAuthResult{Operation: operation, Status: "cancelled", Account: &account}, err
}

func supportsTargetedReauthorization(account model.ProAccount) bool {
	return account.Platform == "openai" && account.AuthType == "oauth" && account.Binding != nil &&
		account.Binding.SourceType == proaccountgateway.SourceAuthFile && strings.TrimSpace(account.Binding.AuthIndex) != ""
}

func (s *Service) reauthorizationOperation(ctx context.Context, accountID string, operationID string) (model.ProAccountDraft, model.ProAccount, error) {
	operation, err := s.operations.Get(ctx, strings.TrimSpace(operationID))
	if err != nil {
		return model.ProAccountDraft{}, model.ProAccount{}, err
	}
	accountID = strings.TrimSpace(accountID)
	if operation.OperationType != "reauthorize" || operation.ProAccountID != accountID {
		return operation, model.ProAccount{}, ErrOperationConflict
	}
	account, err := s.accounts.Get(ctx, accountID)
	return operation, account, err
}

func (s *Service) findReauthorizationCandidate(ctx context.Context, baseURL string, managementKey string, operation model.ProAccountDraft) (proaccountgateway.AccountSnapshot, int, error) {
	snapshot, err := s.gateway.Snapshot(ctx, baseURL, managementKey)
	if err != nil {
		return proaccountgateway.AccountSnapshot{}, 0, err
	}
	known := contextStringSet(operation.Context["knownDraftLocators"])
	candidates := make([]proaccountgateway.AccountSnapshot, 0)
	for _, item := range snapshot.Accounts {
		if item.Platform != contextString(operation.Context, "platform") || !item.CredentialDraft || item.AuthIndex == contextString(operation.Context, "oldAuthIndex") {
			continue
		}
		if _, exists := known[item.SourceLocator]; exists {
			continue
		}
		candidates = append(candidates, item)
	}
	if len(candidates) != 1 {
		return proaccountgateway.AccountSnapshot{}, len(candidates), nil
	}
	return candidates[0], 1, nil
}

func matchesReauthorizationIdentity(contextValue map[string]any, replacement proaccountgateway.AccountSnapshot) bool {
	if !strings.EqualFold(contextString(contextValue, "oldProvider"), replacement.Provider) {
		return false
	}
	comparisons := [][2]string{
		{contextString(contextValue, "oldEmail"), replacement.Email},
		{contextString(contextValue, "oldUpstreamAccountID"), replacement.UpstreamAccountID},
		{contextString(contextValue, "oldFingerprint"), replacement.SourceFingerprint},
	}
	matched := false
	for _, comparison := range comparisons {
		oldValue := strings.TrimSpace(comparison[0])
		newValue := strings.TrimSpace(comparison[1])
		if oldValue == "" {
			continue
		}
		if newValue == "" || !strings.EqualFold(oldValue, newValue) {
			return false
		}
		matched = true
	}
	return matched
}

func (s *Service) failReauthorizationCandidate(ctx context.Context, baseURL string, managementKey string, operation model.ProAccountDraft, account model.ProAccount, authIndex string, code string, summary string, cause error) (OAuthResult, error) {
	_ = s.gateway.CancelOAuth(ctx, baseURL, managementKey, contextString(operation.Context, "oauthState"))
	if err := s.deleteAccountByAuthIndex(ctx, mustSetup(baseURL, managementKey), authIndex); err != nil {
		operation, _ = s.transition(ctx, operation, model.ProOperationStateCompensating, account.ID, operation.Context, code, summary, "delete_replacement_credential")
		return OAuthResult{Operation: operation, Status: "error", Account: &account}, cause
	}
	operation = s.fail(ctx, operation, code, summary)
	return OAuthResult{Operation: operation, Status: "error", Account: &account}, cause
}

func (s *Service) switchReauthorizedAccount(ctx context.Context, baseURL string, managementKey string, operation model.ProAccountDraft, account model.ProAccount, replacement proaccountgateway.AccountSnapshot) (OAuthResult, error) {
	contextValue := operation.Context
	contextValue["oldStatusChanged"] = contextBool(contextValue, "oldEnabled")
	contextValue["replacementProjectedLocator"] = replacement.SourceLocator
	operation, err := s.transition(ctx, operation, model.ProOperationStateCompensating, account.ID, contextValue,
		"replacement_switch_pending", "正在切换到已测试的新 OAuth 凭据", "rollback_replacement_switch")
	if err != nil {
		return OAuthResult{Operation: operation, Account: &account}, err
	}
	setup := mustSetup(baseURL, managementKey)
	finalizer, ok := s.gateway.(credentialDraftFinalizer)
	if !ok {
		operation, _ = s.rollbackReplacementSwitch(ctx, setup, operation)
		return OAuthResult{Operation: operation, Account: &account}, ErrGatewayCapability
	}
	// 草稿必须以原凭据的最终调度状态一次落盘，停用账号不能出现短暂启用窗口。
	finalSnapshot, err := finalizer.FinalizeCredentialDraft(ctx, baseURL, managementKey, replacement.SourceType, replacement.SourceLocator, contextBool(contextValue, "oldEnabled"))
	if err != nil {
		operation, _ = s.rollbackReplacementSwitch(ctx, setup, operation)
		return OAuthResult{Operation: operation, Account: &account}, err
	}
	if contextBool(contextValue, "oldEnabled") {
		oldSnapshot, findErr := s.gateway.FindAccountByAuthIndex(ctx, baseURL, managementKey, contextString(contextValue, "oldAuthIndex"))
		if findErr != nil {
			operation, _ = s.rollbackReplacementSwitch(ctx, setup, operation)
			return OAuthResult{Operation: operation, Account: &account}, findErr
		}
		if _, err = s.gateway.SetAccountEnabled(ctx, baseURL, managementKey, oldSnapshot.SourceType, oldSnapshot.SourceLocator, false); err != nil {
			operation, _ = s.rollbackReplacementSwitch(ctx, setup, operation)
			return OAuthResult{Operation: operation, Account: &account}, err
		}
	}
	discovery := discoveryFromSnapshot(finalSnapshot)
	discovery.Name = account.Name
	updated, err := s.repository.RebindManaged(ctx, account.ID, account.Version, discovery, s.now().UnixMilli())
	if err != nil {
		operation, _ = s.rollbackReplacementSwitch(ctx, setup, operation)
		return OAuthResult{Operation: operation, Account: &account}, err
	}
	if err = s.deleteAccountByAuthIndex(ctx, setup, contextString(contextValue, "oldAuthIndex")); err != nil {
		return OAuthResult{Operation: operation, Status: "wait", Account: &updated}, nil
	}
	_ = s.gateway.CancelOAuth(ctx, baseURL, managementKey, contextString(contextValue, "oauthState"))
	operation, err = s.transition(ctx, operation, model.ProOperationStateEnabled, account.ID, contextValue, "", "重新授权完成", "")
	return OAuthResult{Operation: operation, Status: "ok", Account: &updated}, err
}

func firstString(values []string) string {
	for _, value := range values {
		if value = strings.TrimSpace(value); value != "" {
			return value
		}
	}
	return ""
}

func mustSetup(baseURL string, managementKey string) store.Setup {
	return store.Setup{CPAUpstreamURL: baseURL, ManagementKey: managementKey}
}
