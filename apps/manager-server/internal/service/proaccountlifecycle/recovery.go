package proaccountlifecycle

import (
	"context"
	"errors"
	"strings"

	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/model"
	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/service/proaccountgateway"
)

func (s *Service) Recover(ctx context.Context, operation model.ProAccountDraft) error {
	if operation.State != model.ProOperationStateCompensating {
		return ErrOperationState
	}
	setup, err := s.resolveSetup(ctx)
	if err != nil {
		return err
	}
	switch operation.CompensationAction {
	case "cancel_oauth_session":
		if state := contextString(operation.Context, "oauthState"); state != "" {
			_ = s.gateway.CancelOAuth(ctx, setup.CPAUpstreamURL, setup.ManagementKey, state)
		}
		_, err = s.transition(ctx, operation, model.ProOperationStateCancelled, operation.ProAccountID, operation.Context, "", "OAuth 会话已取消", "cancel_oauth_session_completed")
		return err
	case "delete_new_credential", "delete_replacement_credential", "delete_credential", "resume_or_cleanup":
		sourceType, sourceLocator := recoveryCredentialLocator(operation)
		var account model.ProAccount
		if operation.ProAccountID != "" {
			account, _ = s.accounts.Get(ctx, operation.ProAccountID)
		}
		if sourceType == "" || sourceLocator == "" {
			if operation.CompensationAction == "delete_replacement_credential" {
				return ErrInvalidRequest
			}
			if account.Binding != nil {
				sourceType = account.Binding.SourceType
				sourceLocator = account.Binding.SourceLocator
			}
		}
		if sourceType != "" && sourceLocator != "" {
			if deleteErr := s.gateway.DeleteAccount(ctx, setup.CPAUpstreamURL, setup.ManagementKey, sourceType, sourceLocator); deleteErr != nil && !gatewayNotFound(deleteErr) {
				return deleteErr
			}
		}
		if account.ID != "" {
			if account.DeletedAtMS == 0 {
				if _, deleteErr := s.repository.SoftDelete(ctx, account.ID, account.Version, s.now().UnixMilli()); deleteErr != nil && !errors.Is(deleteErr, ErrResourceVersionConflict) {
					return deleteErr
				}
			}
		}
		terminal := model.ProOperationStateFailed
		if operation.OperationType == "delete" || strings.Contains(operation.ErrorCode, "cancel") {
			terminal = model.ProOperationStateCancelled
		}
		_, err = s.transition(ctx, operation, terminal, operation.ProAccountID, operation.Context, operation.ErrorCode, "补偿清理已完成", operation.CompensationAction+"_completed")
		return err
	case "enable_replacement_credential":
		authIndex := contextString(operation.Context, "replacementAuthIndex")
		snapshot, findErr := s.gateway.FindAccountByAuthIndex(ctx, setup.CPAUpstreamURL, setup.ManagementKey, authIndex)
		if findErr != nil {
			return findErr
		}
		snapshot, err = s.gateway.SetAccountEnabled(ctx, setup.CPAUpstreamURL, setup.ManagementKey, snapshot.SourceType, snapshot.SourceLocator, true)
		if err != nil {
			return err
		}
		account, err := s.accounts.Get(ctx, operation.ProAccountID)
		if err != nil {
			return err
		}
		discovery := discoveryFromSnapshot(snapshot)
		discovery.Name = account.Name
		if _, err = s.repository.RebindManaged(ctx, account.ID, account.Version, discovery, s.now().UnixMilli()); err != nil {
			return err
		}
		_, err = s.transition(ctx, operation, model.ProOperationStateEnabled, operation.ProAccountID, operation.Context, "", "替换凭证已恢复并启用", "")
		return err
	case "restore_model_rules":
		account, err := s.accounts.Get(ctx, operation.ProAccountID)
		if err != nil {
			return err
		}
		if account.Binding == nil {
			return ErrInvalidRequest
		}
		previous := proaccountgateway.ModelRules{
			AllowedModels: stringSlice(operation.Context["previousAllowedModels"]),
			ModelMapping:  stringMap(operation.Context["previousModelMapping"]),
		}
		if err := s.gateway.RestoreModelRules(ctx, setup.CPAUpstreamURL, setup.ManagementKey, account.Binding.SourceType, account.Binding.SourceLocator, previous); err != nil {
			return err
		}
		_, err = s.transition(ctx, operation, model.ProOperationStateFailed, operation.ProAccountID, operation.Context, operation.ErrorCode, "旧模型规则已恢复", "restore_model_rules_completed")
		return err
	default:
		return ErrOperationState
	}
}

func recoveryCredentialLocator(operation model.ProAccountDraft) (string, string) {
	if operation.CompensationAction == "delete_replacement_credential" {
		return contextString(operation.Context, "replacementSourceType"), contextString(operation.Context, "replacementSourceLocator")
	}
	return contextString(operation.Context, "sourceType"), contextString(operation.Context, "sourceLocator")
}

func gatewayNotFound(err error) bool {
	var gatewayErr *proaccountgateway.GatewayError
	return errors.As(err, &gatewayErr) && gatewayErr.StatusCode == 404
}

func stringSlice(value any) []string {
	result := make([]string, 0)
	switch values := value.(type) {
	case []any:
		for _, item := range values {
			if text, ok := item.(string); ok {
				result = append(result, text)
			}
		}
	case []string:
		result = append(result, values...)
	}
	return result
}

func stringMap(value any) map[string]string {
	result := map[string]string{}
	switch values := value.(type) {
	case map[string]any:
		for key, item := range values {
			if text, ok := item.(string); ok {
				result[key] = text
			}
		}
	case map[string]string:
		for key, item := range values {
			result[key] = item
		}
	}
	return result
}
