package proaccountlifecycle

import (
	"context"
	"strings"

	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/model"
	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/service/proaccountgateway"
)

func (s *Service) CreateVertex(ctx context.Context, input CreateVertexInput) (Result, error) {
	if len(input.ServiceAccount) == 0 || strings.TrimSpace(input.IdempotencyKey) == "" {
		return Result{}, ErrInvalidRequest
	}
	operation, created, err := s.start(ctx, input.OperationID, input.IdempotencyKey, "add", "", map[string]any{
		"platform": "gemini", "authType": "vertex", "location": strings.TrimSpace(input.Location),
	})
	if err != nil {
		return Result{Operation: operation}, err
	}
	if !created {
		if operation.State == model.ProOperationStateEnabled && operation.ProAccountID != "" {
			account, getErr := s.accounts.Get(ctx, operation.ProAccountID)
			return Result{Account: account, Operation: operation}, getErr
		}
		return Result{Operation: operation}, ErrOperationState
	}
	setup, err := s.resolveSetup(ctx)
	if err != nil {
		return Result{Operation: operation}, err
	}
	capabilities, err := s.gateway.Capabilities(ctx, setup.CPAUpstreamURL, setup.ManagementKey)
	if err != nil || !capabilities.CredentialDraft || !capabilities.AllowedModels {
		return Result{Operation: operation}, ErrGatewayCapability
	}
	operation, err = s.transition(ctx, operation, model.ProOperationStateProbed, "", operation.Context, "", "", "delete_new_credential")
	if err != nil {
		return Result{Operation: operation}, err
	}
	snapshot, err := s.gateway.ImportVertexDraft(ctx, setup.CPAUpstreamURL, setup.ManagementKey, proaccountgateway.ImportVertexInput{
		FileName: input.FileName, ServiceAccount: input.ServiceAccount, Location: input.Location,
	})
	if err != nil {
		operation = s.fail(ctx, operation, "vertex_import_failed", "Vertex Service Account 导入失败")
		return Result{Operation: operation}, err
	}
	synced, err := s.repository.Sync(ctx, []model.ProAccountDiscovery{discoveryFromSnapshot(snapshot)}, s.now().UnixMilli(), false)
	if err != nil || len(synced.Items) != 1 || synced.Items[0].ProAccountID == "" {
		_ = s.gateway.DeleteAccount(ctx, setup.CPAUpstreamURL, setup.ManagementKey, snapshot.SourceType, snapshot.SourceLocator)
		operation = s.fail(ctx, operation, "manager_account_create_failed", "统一账号创建失败，已清理 Vertex 草稿")
		if err == nil {
			err = ErrInvalidRequest
		}
		return Result{Operation: operation}, err
	}
	account, err := s.accounts.Get(ctx, synced.Items[0].ProAccountID)
	if err != nil {
		return Result{Operation: operation}, err
	}
	contextValue := operation.Context
	contextValue["sourceType"] = snapshot.SourceType
	contextValue["sourceLocator"] = snapshot.SourceLocator
	contextValue["authIndex"] = snapshot.AuthIndex
	operation, err = s.transition(ctx, operation, model.ProOperationStateCredentialSavedDisabled, account.ID, contextValue, "", "", "delete_new_credential")
	if err != nil {
		return Result{Account: account, Operation: operation}, err
	}
	return s.completeCredential(ctx, setup, operation, account, input.AllowedModels, input.ModelMapping, input.TestModel, input.SaveDisabled)
}
