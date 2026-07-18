package proaccountlifecycle

import (
	"context"
	"errors"
	"strings"

	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/model"
	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/service/proaccountgateway"
)

type credentialRefresher interface {
	RefreshCredential(ctx context.Context, baseURL string, managementKey string, authIndex string, id string) (proaccountgateway.CredentialRefreshResult, error)
}

func (s *Service) RefreshToken(ctx context.Context, input MutationInput) (Result, error) {
	account, err := s.accounts.Get(ctx, strings.TrimSpace(input.AccountID))
	if err != nil {
		return Result{}, err
	}
	operation, created, err := s.start(ctx, input.OperationID, input.IdempotencyKey, "refresh_token", account.ID, map[string]any{})
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
	if account.AuthType != "oauth" || account.Binding == nil || account.Binding.SourceType != proaccountgateway.SourceAuthFile || strings.TrimSpace(account.Binding.AuthIndex) == "" {
		operation = s.fail(ctx, operation, "credential_refresh_unsupported", "账号不具备可安全刷新的 OAuth 绑定")
		return Result{Account: account, Operation: operation}, ErrUnsupportedAccountType
	}
	refresher, ok := s.gateway.(credentialRefresher)
	if !ok {
		operation = s.fail(ctx, operation, "gateway_capability_missing", "Gateway 不支持安全刷新 OAuth 凭据")
		return Result{Account: account, Operation: operation}, ErrGatewayCapability
	}
	setup, err := s.resolveSetup(ctx)
	if err != nil {
		operation = s.fail(ctx, operation, "gateway_connection_unavailable", "Gateway 连接不可用")
		return Result{Account: account, Operation: operation}, err
	}
	refresh, err := refresher.RefreshCredential(ctx, setup.CPAUpstreamURL, setup.ManagementKey, account.Binding.AuthIndex, "")
	if err != nil {
		var gatewayErr *proaccountgateway.GatewayError
		if errors.As(err, &gatewayErr) && gatewayErr.Code == "reauthorization_required" {
			operation = s.fail(ctx, operation, "reauthorization_required", "刷新令牌已失效，请重新授权")
			return Result{Account: account, Operation: operation}, ErrReauthorizationRequired
		}
		operation = s.fail(ctx, operation, "credential_refresh_failed", "OAuth 凭据刷新失败，旧凭据保持不变")
		return Result{Account: account, Operation: operation}, err
	}
	operation, err = s.transition(ctx, operation, model.ProOperationStateTested, account.ID, operation.Context, "", "OAuth 凭据已刷新并持久化", "")
	if err != nil {
		return Result{Account: account, Operation: operation, CredentialRefresh: &refresh}, err
	}
	operation, err = s.transition(ctx, operation, model.ProOperationStateEnabled, account.ID, operation.Context, "", "OAuth 凭据刷新完成", "")
	return Result{Account: account, Operation: operation, CredentialRefresh: &refresh}, err
}
