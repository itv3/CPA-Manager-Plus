package proaccountlifecycle

import (
	"context"
	"strings"

	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/model"
)

func (s *Service) StartOAuth(ctx context.Context, input OAuthStartInput) (OAuthResult, error) {
	input.Platform = strings.ToLower(strings.TrimSpace(input.Platform))
	if input.Platform != "openai" && input.Platform != "anthropic" && input.Platform != "gemini" && input.Platform != "antigravity" && input.Platform != "xai" {
		return OAuthResult{}, ErrUnsupportedAccountType
	}
	if strings.TrimSpace(input.IdempotencyKey) == "" {
		return OAuthResult{}, ErrInvalidRequest
	}
	setup, err := s.resolveSetup(ctx)
	if err != nil {
		return OAuthResult{}, err
	}
	capabilities, err := s.gateway.Capabilities(ctx, setup.CPAUpstreamURL, setup.ManagementKey)
	if err != nil || !capabilities.CredentialDraft {
		return OAuthResult{}, ErrGatewayCapability
	}
	snapshot, err := s.gateway.Snapshot(ctx, setup.CPAUpstreamURL, setup.ManagementKey)
	if err != nil {
		return OAuthResult{}, err
	}
	knownDrafts := make([]string, 0)
	for _, account := range snapshot.Accounts {
		if account.Platform == input.Platform && account.CredentialDraft {
			knownDrafts = append(knownDrafts, account.SourceLocator)
		}
	}
	operation, created, err := s.start(ctx, input.OperationID, input.IdempotencyKey, "add", "", map[string]any{
		"platform": input.Platform, "authType": "oauth", "knownDraftLocators": knownDrafts,
	})
	if err != nil {
		return OAuthResult{Operation: operation}, err
	}
	if !created {
		return OAuthResult{Operation: operation}, ErrOperationState
	}
	oauth, err := s.gateway.StartOAuth(ctx, setup.CPAUpstreamURL, setup.ManagementKey, input.Platform)
	if err != nil {
		operation = s.fail(ctx, operation, "oauth_start_failed", "OAuth 授权启动失败")
		return OAuthResult{Operation: operation}, err
	}
	contextValue := operation.Context
	contextValue["oauthState"] = oauth.State
	operation, err = s.transition(ctx, operation, model.ProOperationStateProbed, "", contextValue, "", "", "cancel_oauth_session")
	if err != nil {
		_ = s.gateway.CancelOAuth(ctx, setup.CPAUpstreamURL, setup.ManagementKey, oauth.State)
		return OAuthResult{Operation: operation}, err
	}
	return OAuthResult{Operation: operation, OAuth: &oauth, Status: "wait"}, nil
}

func (s *Service) OAuthStatus(ctx context.Context, operationID string) (OAuthResult, error) {
	operation, err := s.operations.Get(ctx, strings.TrimSpace(operationID))
	if err != nil {
		return OAuthResult{}, err
	}
	if operation.OperationType != "add" {
		return OAuthResult{Operation: operation}, ErrOperationConflict
	}
	if operation.State == model.ProOperationStateCredentialSavedDisabled && operation.ProAccountID != "" {
		account, getErr := s.accounts.Get(ctx, operation.ProAccountID)
		return OAuthResult{Operation: operation, Status: "ok", Account: &account}, getErr
	}
	if operation.State != model.ProOperationStateProbed {
		return OAuthResult{Operation: operation}, ErrOperationState
	}
	state := contextString(operation.Context, "oauthState")
	platform := contextString(operation.Context, "platform")
	if state == "" || platform == "" {
		return OAuthResult{Operation: operation}, ErrInvalidRequest
	}
	setup, err := s.resolveSetup(ctx)
	if err != nil {
		return OAuthResult{Operation: operation}, err
	}
	status, err := s.gateway.OAuthStatus(ctx, setup.CPAUpstreamURL, setup.ManagementKey, state)
	if err != nil {
		return OAuthResult{Operation: operation}, err
	}
	if status.Status == "wait" {
		return OAuthResult{Operation: operation, Status: "wait"}, nil
	}
	if status.Status != "ok" {
		_ = s.gateway.CancelOAuth(ctx, setup.CPAUpstreamURL, setup.ManagementKey, state)
		operation = s.fail(ctx, operation, "oauth_failed", "OAuth 授权失败或已经超时")
		return OAuthResult{Operation: operation, Status: "error"}, nil
	}
	snapshot, err := s.gateway.Snapshot(ctx, setup.CPAUpstreamURL, setup.ManagementKey)
	if err != nil {
		return OAuthResult{Operation: operation}, err
	}
	known := contextStringSet(operation.Context["knownDraftLocators"])
	candidates := make([]model.ProAccountDiscovery, 0)
	for _, account := range snapshot.Accounts {
		if account.Platform != platform || !account.CredentialDraft {
			continue
		}
		if _, exists := known[account.SourceLocator]; exists {
			continue
		}
		candidates = append(candidates, discoveryFromSnapshot(account))
	}
	if len(candidates) == 0 {
		return OAuthResult{Operation: operation, Status: "credential_pending"}, nil
	}
	if len(candidates) > 1 {
		return OAuthResult{Operation: operation, Status: "ambiguous"}, ErrOAuthCredentialAmbiguous
	}
	synced, err := s.repository.Sync(ctx, candidates, s.now().UnixMilli(), false)
	if err != nil || len(synced.Items) != 1 || synced.Items[0].ProAccountID == "" {
		if err == nil {
			err = ErrOAuthCredentialMissing
		}
		return OAuthResult{Operation: operation}, err
	}
	account, err := s.accounts.Get(ctx, synced.Items[0].ProAccountID)
	if err != nil {
		return OAuthResult{Operation: operation}, err
	}
	contextValue := operation.Context
	contextValue["sourceType"] = account.Binding.SourceType
	contextValue["sourceLocator"] = account.Binding.SourceLocator
	contextValue["authIndex"] = account.Binding.AuthIndex
	operation, err = s.transition(ctx, operation, model.ProOperationStateCredentialSavedDisabled, account.ID, contextValue, "", "", "delete_new_credential")
	if err != nil {
		return OAuthResult{Operation: operation, Account: &account}, err
	}
	return OAuthResult{Operation: operation, Status: "ok", Account: &account}, nil
}

func (s *Service) CancelOAuth(ctx context.Context, operationID string) (OAuthResult, error) {
	return s.CancelDraft(ctx, operationID)
}

func (s *Service) CancelDraft(ctx context.Context, operationID string) (OAuthResult, error) {
	operation, err := s.operations.Get(ctx, strings.TrimSpace(operationID))
	if err != nil {
		return OAuthResult{}, err
	}
	if operation.State == model.ProOperationStateEnabled || operation.State == model.ProOperationStateCancelled || operation.State == model.ProOperationStateFailed {
		return OAuthResult{Operation: operation}, ErrOperationState
	}
	setup, err := s.resolveSetup(ctx)
	if err != nil {
		return OAuthResult{Operation: operation}, err
	}
	if state := contextString(operation.Context, "oauthState"); state != "" {
		_ = s.gateway.CancelOAuth(ctx, setup.CPAUpstreamURL, setup.ManagementKey, state)
	}
	if operation.ProAccountID == "" {
		operation, err = s.transition(ctx, operation, model.ProOperationStateCancelled, "", operation.Context, "", "", "cancel_oauth_session")
		return OAuthResult{Operation: operation, Status: "cancelled"}, err
	}
	account, err := s.accounts.Get(ctx, operation.ProAccountID)
	if err != nil {
		return OAuthResult{Operation: operation}, err
	}
	operation, err = s.transition(ctx, operation, model.ProOperationStateCompensating, account.ID, operation.Context, "oauth_cancelled", "正在清理已保存的 OAuth 草稿", "delete_new_credential")
	if err != nil {
		return OAuthResult{Operation: operation, Account: &account}, err
	}
	if account.Binding != nil {
		if err := s.gateway.DeleteAccount(ctx, setup.CPAUpstreamURL, setup.ManagementKey, account.Binding.SourceType, account.Binding.SourceLocator); err != nil {
			return OAuthResult{Operation: operation, Account: &account}, err
		}
	}
	deleted, err := s.repository.SoftDelete(ctx, account.ID, account.Version, s.now().UnixMilli())
	if err == nil {
		account = deleted
	}
	operation, transitionErr := s.transition(ctx, operation, model.ProOperationStateCancelled, account.ID, operation.Context, "", "OAuth 草稿已清理", "delete_new_credential_completed")
	if err != nil {
		return OAuthResult{Operation: operation, Account: &account}, err
	}
	return OAuthResult{Operation: operation, Status: "cancelled", Account: &account}, transitionErr
}

func contextStringSet(value any) map[string]struct{} {
	result := map[string]struct{}{}
	switch typed := value.(type) {
	case []any:
		for _, item := range typed {
			if text := strings.TrimSpace(toString(item)); text != "" {
				result[text] = struct{}{}
			}
		}
	case []string:
		for _, item := range typed {
			if text := strings.TrimSpace(item); text != "" {
				result[text] = struct{}{}
			}
		}
	}
	return result
}

func toString(value any) string {
	text, _ := value.(string)
	return text
}
