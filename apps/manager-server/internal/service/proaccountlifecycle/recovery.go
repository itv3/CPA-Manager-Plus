package proaccountlifecycle

import (
	"context"
	"errors"
	"strings"

	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/model"
	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/service/proaccountgateway"
	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/store"
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
	case "resume_or_cleanup":
		_, err = s.transition(ctx, operation, model.ProOperationStateFailed, operation.ProAccountID, operation.Context,
			valueOr(operation.ErrorCode, "manual_recovery_required"), "操作缺少明确补偿动作，已停止自动恢复并保留记录", "manual_recovery_required")
		return err
	case "delete_new_credential", "delete_replacement_credential", "delete_credential":
		sourceType, sourceLocator := recoveryCredentialLocator(operation)
		var account model.ProAccount
		if operation.ProAccountID != "" {
			account, _ = s.accounts.Get(ctx, operation.ProAccountID)
		}
		if sourceType == "" || sourceLocator == "" {
			if operation.CompensationAction == "delete_replacement_credential" {
				_, err = s.transition(ctx, operation, model.ProOperationStateFailed, operation.ProAccountID, operation.Context,
					valueOr(operation.ErrorCode, "replacement_not_created"), "替换凭证尚未创建，旧配置保持不变", "replacement_cleanup_completed")
				return err
			}
			if account.Binding != nil {
				sourceType = account.Binding.SourceType
				sourceLocator = account.Binding.SourceLocator
			}
		}
		// 优先按稳定的 auth_index 定位后删除:配置数组索引会随其它条目删除而前移,
		// 直接使用记录中的旧 index 可能命中其它账号或返回 HTTP 400 导致恢复循环卡死。
		authIndex := recoveryAuthIndex(operation, account)
		if authIndex != "" {
			if deleteErr := s.deleteAccountByAuthIndex(ctx, setup, authIndex); deleteErr != nil {
				return deleteErr
			}
		} else if sourceType != "" && sourceLocator != "" {
			if deleteErr := s.gateway.DeleteAccount(ctx, setup.CPAUpstreamURL, setup.ManagementKey, sourceType, sourceLocator); deleteErr != nil && !gatewayGone(deleteErr) {
				return deleteErr
			}
		}
		if account.ID != "" && operation.CompensationAction != "delete_replacement_credential" {
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
	case "rollback_replacement_switch":
		account, getErr := s.accounts.Get(ctx, operation.ProAccountID)
		if getErr != nil {
			return getErr
		}
		if account.Binding != nil && account.Binding.AuthIndex == contextString(operation.Context, "replacementAuthIndex") {
			if err = s.deleteAccountByAuthIndex(ctx, setup, contextString(operation.Context, "oldAuthIndex")); err != nil {
				return err
			}
			_, err = s.transition(ctx, operation, model.ProOperationStateEnabled, operation.ProAccountID, operation.Context,
				"", "替换凭证切换已完成", "")
			return err
		}
		_, err = s.rollbackReplacementSwitch(ctx, setup, operation)
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

func (s *Service) rollbackReplacementSwitch(ctx context.Context, setup store.Setup, operation model.ProAccountDraft) (model.ProAccountDraft, error) {
	replacementAuthIndex := contextString(operation.Context, "replacementAuthIndex")
	if replacement, err := s.gateway.FindAccountByAuthIndex(ctx, setup.CPAUpstreamURL, setup.ManagementKey, replacementAuthIndex); err == nil {
		_, _ = s.gateway.SetAccountEnabled(ctx, setup.CPAUpstreamURL, setup.ManagementKey, replacement.SourceType, replacement.SourceLocator, false)
	} else if !errors.Is(err, proaccountgateway.ErrGatewayAccountNotFound) {
		return operation, err
	}
	oldAuthIndex := contextString(operation.Context, "oldAuthIndex")
	if contextBool(operation.Context, "oldStatusChanged") {
		oldAccount, err := s.gateway.FindAccountByAuthIndex(ctx, setup.CPAUpstreamURL, setup.ManagementKey, oldAuthIndex)
		if err != nil {
			return operation, err
		}
		if _, err = s.gateway.SetAccountEnabled(ctx, setup.CPAUpstreamURL, setup.ManagementKey,
			oldAccount.SourceType, oldAccount.SourceLocator, contextBool(operation.Context, "oldEnabled")); err != nil {
			return operation, err
		}
	}
	if deleteErr := s.deleteAccountByAuthIndex(ctx, setup, replacementAuthIndex); deleteErr != nil {
		return operation, deleteErr
	}
	updated, err := s.transition(ctx, operation, model.ProOperationStateFailed, operation.ProAccountID, operation.Context,
		valueOr(operation.ErrorCode, "replacement_switch_rolled_back"), "替换凭证切换失败，旧配置已恢复", "rollback_replacement_switch_completed")
	if err != nil {
		return operation, err
	}
	return updated, nil
}

func (s *Service) deleteAccountByAuthIndex(ctx context.Context, setup store.Setup, authIndex string) error {
	authIndex = strings.TrimSpace(authIndex)
	if authIndex == "" {
		return ErrInvalidRequest
	}
	account, err := s.gateway.FindAccountByAuthIndex(ctx, setup.CPAUpstreamURL, setup.ManagementKey, authIndex)
	if errors.Is(err, proaccountgateway.ErrGatewayAccountNotFound) {
		return nil
	}
	if err != nil {
		return err
	}
	return s.gateway.DeleteAccount(ctx, setup.CPAUpstreamURL, setup.ManagementKey, account.SourceType, account.SourceLocator)
}

func contextBool(value map[string]any, key string) bool {
	result, _ := value[key].(bool)
	return result
}

func valueOr(value string, fallback string) string {
	if strings.TrimSpace(value) != "" {
		return value
	}
	return fallback
}

func recoveryCredentialLocator(operation model.ProAccountDraft) (string, string) {
	if operation.CompensationAction == "delete_replacement_credential" {
		return contextString(operation.Context, "replacementSourceType"), contextString(operation.Context, "replacementSourceLocator")
	}
	return contextString(operation.Context, "sourceType"), contextString(operation.Context, "sourceLocator")
}

// recoveryAuthIndex 返回补偿删除应使用的稳定凭证标识。
func recoveryAuthIndex(operation model.ProAccountDraft, account model.ProAccount) string {
	if operation.CompensationAction == "delete_replacement_credential" {
		return strings.TrimSpace(contextString(operation.Context, "replacementAuthIndex"))
	}
	if value := strings.TrimSpace(contextString(operation.Context, "authIndex")); value != "" {
		return value
	}
	if account.Binding != nil {
		return strings.TrimSpace(account.Binding.AuthIndex)
	}
	return ""
}

// gatewayGone 判断 Gateway 已无法按记录定位到目标凭证:
// 404 表示不存在,400 通常为配置数组索引已越界(条目已被删除)。
func gatewayGone(err error) bool {
	var gatewayErr *proaccountgateway.GatewayError
	return errors.As(err, &gatewayErr) && (gatewayErr.StatusCode == 404 || gatewayErr.StatusCode == 400)
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
