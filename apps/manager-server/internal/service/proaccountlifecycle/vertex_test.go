package proaccountlifecycle

import (
	"context"
	"testing"

	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/model"
	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/service/proaccountgateway"
	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/service/proaccountoperation"
)

type vertexDraftRepository struct {
	AccountRepository
	state *migrationAccountState
}

func (r *vertexDraftRepository) Sync(_ context.Context, discoveries []model.ProAccountDiscovery, nowMS int64, _ bool) (model.ProAccountSyncResult, error) {
	discovery := discoveries[0]
	r.state.account = model.ProAccount{
		ID: "vertex-account", Platform: discovery.Platform, AuthType: discovery.AuthType,
		SourceType: discovery.SourceType, Name: discovery.Name, Enabled: discovery.Enabled,
		HealthStatus: discovery.HealthStatus, AllowedModels: discovery.AllowedModels,
		ModelMapping: discovery.ModelMapping, Version: 1, CreatedAtMS: nowMS, UpdatedAtMS: nowMS,
		Binding: &model.ProAccountBinding{
			ProAccountID: "vertex-account", AuthIndex: discovery.AuthIndex,
			SourceType: discovery.SourceType, SourceLocator: discovery.SourceLocator,
			BindingStatus: model.ProBindingStatusCurrent, IsCurrent: true,
		},
	}
	return model.ProAccountSyncResult{Items: []model.ProAccountSyncItem{{ProAccountID: "vertex-account"}}}, nil
}

func (r *vertexDraftRepository) UpdateMetadata(_ context.Context, accountID string, expectedVersion int64, name string, notes string, nowMS int64) (model.ProAccount, error) {
	if r.state.account.ID != accountID || r.state.account.Version != expectedVersion {
		return model.ProAccount{}, ErrResourceVersionConflict
	}
	if name != "" {
		r.state.account.Name = name
	}
	r.state.account.Notes = notes
	r.state.account.UpdatedAtMS = nowMS
	r.state.account.Version++
	return r.state.account, nil
}

type vertexDraftGateway struct {
	Gateway
	imported bool
}

type vertexOperations struct {
	migrationOperations
}

func (o *vertexOperations) Start(ctx context.Context, input proaccountoperation.StartInput) (model.ProAccountDraft, bool, error) {
	if o.current.OperationID != "" {
		return o.current, false, nil
	}
	return o.migrationOperations.Start(ctx, input)
}

func (g *vertexDraftGateway) Capabilities(context.Context, string, string) (proaccountgateway.Capabilities, error) {
	return proaccountgateway.Capabilities{CredentialDraft: true, AllowedModels: true}, nil
}

func (g *vertexDraftGateway) ImportVertexDraft(context.Context, string, string, proaccountgateway.ImportVertexInput) (proaccountgateway.AccountSnapshot, error) {
	g.imported = true
	return proaccountgateway.AccountSnapshot{
		Platform: "gemini", AuthType: "vertex", SourceType: proaccountgateway.SourceAuthFile,
		SourceLocator: "vertex.json", AuthIndex: "vertex-auth", Name: "vertex-project", Enabled: false,
	}, nil
}

func TestCreateVertexDraftStopsBeforeModelWriteAndConnectivityTest(t *testing.T) {
	state := &migrationAccountState{}
	repository := &vertexDraftRepository{state: state}
	gateway := &vertexDraftGateway{}
	operations := &vertexOperations{}
	service := New(migrationAccountReader{state: state}, repository, recoverySetupStub{}, gateway, migrationProbe{}, operations)

	result, err := service.CreateVertex(context.Background(), CreateVertexInput{
		OperationID: "vertex-operation", IdempotencyKey: "vertex-key",
		FileName: "service-account.json", ServiceAccount: []byte(`{"project_id":"project-a"}`),
		Location: "us-central1", DraftOnly: true,
	})
	if err != nil {
		t.Fatalf("创建 Vertex 草稿：%v", err)
	}
	if !gateway.imported || result.Account.ID != "vertex-account" || result.Account.Enabled {
		t.Fatalf("Vertex 草稿结果 = %#v", result)
	}
	if result.Operation.State != model.ProOperationStateCredentialSavedDisabled {
		t.Fatalf("草稿状态 = %s", result.Operation.State)
	}
	replayed, err := service.CreateVertex(context.Background(), CreateVertexInput{
		OperationID: "vertex-operation", IdempotencyKey: "vertex-key",
		FileName: "service-account.json", ServiceAccount: []byte(`{"project_id":"project-a"}`),
		Location: "us-central1", DraftOnly: true,
	})
	if err != nil || replayed.Account.ID != result.Account.ID || replayed.Operation.State != model.ProOperationStateCredentialSavedDisabled {
		t.Fatalf("重放 Vertex 草稿 = %#v, err=%v", replayed, err)
	}
}

func TestCreateVertexReplaysSavedDisabledTerminalState(t *testing.T) {
	state := &migrationAccountState{account: model.ProAccount{ID: "vertex-account", Enabled: false}}
	operations := &vertexOperations{migrationOperations: migrationOperations{current: model.ProAccountDraft{
		OperationID: "vertex-operation", IdempotencyKey: "vertex-key", OperationType: "add",
		ProAccountID: "vertex-account", State: model.ProOperationStateSavedDisabled, Version: 5,
	}}}
	service := New(migrationAccountReader{state: state}, nil, nil, nil, nil, operations)

	result, err := service.CreateVertex(context.Background(), CreateVertexInput{
		OperationID: "vertex-operation", IdempotencyKey: "vertex-key",
		ServiceAccount: []byte(`{"project_id":"project-a"}`),
	})
	if err != nil || result.Account.ID != "vertex-account" || !result.SavedDisabled || result.Operation.State != model.ProOperationStateSavedDisabled {
		t.Fatalf("重放 Vertex 停用终态 = %#v, err=%v", result, err)
	}
}
