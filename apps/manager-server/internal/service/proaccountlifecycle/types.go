package proaccountlifecycle

import (
	"context"

	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/model"
	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/service/managerconfig"
	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/service/proaccountgateway"
	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/service/proaccountoperation"
	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/service/proaccountprobe"
	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/store"
)

type CreateAPIInput struct {
	OperationID    string
	IdempotencyKey string
	Platform       string
	Name           string
	BaseURL        string
	APIKey         string
	ProtocolMode   string
	Headers        map[string]string
	AllowedModels  []string
	ModelMapping   map[string]string
	TestModel      string
	SaveDisabled   bool
}

type CompleteDraftInput struct {
	OperationID     string
	AccountID       string
	ExpectedVersion int64
	AllowedModels   []string
	ModelMapping    map[string]string
	TestModel       string
	SaveDisabled    bool
}

type CreateVertexInput struct {
	OperationID    string
	IdempotencyKey string
	FileName       string
	ServiceAccount []byte
	Location       string
	AllowedModels  []string
	ModelMapping   map[string]string
	TestModel      string
	SaveDisabled   bool
	DraftOnly      bool
}

type MutationInput struct {
	AccountID       string
	OperationID     string
	IdempotencyKey  string
	ExpectedVersion int64
}

type UpdateInput struct {
	MutationInput
	Name          *string
	BaseURL       *string
	APIKey        string
	ProtocolMode  string
	Headers       *map[string]string
	AllowedModels []string
	ModelMapping  map[string]string
	TestModel     string
}

type OAuthStartInput struct {
	OperationID    string
	IdempotencyKey string
	Platform       string
}

type Result struct {
	Account       model.ProAccount                      `json:"account"`
	Operation     model.ProAccountDraft                 `json:"operation"`
	Probe         *proaccountprobe.Result               `json:"probe,omitempty"`
	Connectivity  *proaccountgateway.ConnectivityResult `json:"connectivity,omitempty"`
	SavedDisabled bool                                  `json:"savedDisabled,omitempty"`
}

type OAuthResult struct {
	Operation model.ProAccountDraft               `json:"operation"`
	OAuth     *proaccountgateway.OAuthStartResult `json:"oauth,omitempty"`
	Status    string                              `json:"status,omitempty"`
	Account   *model.ProAccount                   `json:"account,omitempty"`
}

type AccountReader interface {
	Get(ctx context.Context, id string) (model.ProAccount, error)
}

type AccountRepository interface {
	Sync(ctx context.Context, discoveries []model.ProAccountDiscovery, nowMS int64, dryRun bool) (model.ProAccountSyncResult, error)
	UpdateModelRules(ctx context.Context, accountID string, expectedVersion int64, allowedModels []string, modelMapping map[string]string, modelRuleVersion string, nowMS int64) (model.ProAccount, error)
	RecordTestResult(ctx context.Context, accountID string, success bool, errorCode string, nowMS int64) (model.ProAccount, error)
	RebindManaged(ctx context.Context, accountID string, expectedVersion int64, discovery model.ProAccountDiscovery, nowMS int64) (model.ProAccount, error)
	SoftDelete(ctx context.Context, accountID string, expectedVersion int64, nowMS int64) (model.ProAccount, error)
}

type SetupResolver interface {
	ResolveSetupWithSource(ctx context.Context) (store.Setup, managerconfig.Source, bool, error)
}

type Gateway interface {
	Capabilities(ctx context.Context, baseURL string, managementKey string) (proaccountgateway.Capabilities, error)
	Snapshot(ctx context.Context, baseURL string, managementKey string) (proaccountgateway.SnapshotResult, error)
	CreateDisabledAPI(ctx context.Context, baseURL string, managementKey string, input proaccountgateway.CreateAPIInput) (proaccountgateway.AccountSnapshot, error)
	SetAccountEnabled(ctx context.Context, baseURL string, managementKey string, sourceType string, sourceLocator string, enabled bool) (proaccountgateway.AccountSnapshot, error)
	DeleteAccount(ctx context.Context, baseURL string, managementKey string, sourceType string, sourceLocator string) error
	EditableAccount(ctx context.Context, baseURL string, managementKey string, sourceType string, sourceLocator string) (proaccountgateway.EditableAccount, error)
	FindAccountByAuthIndex(ctx context.Context, baseURL string, managementKey string, authIndex string) (proaccountgateway.AccountSnapshot, error)
	WriteAndVerifyModelRules(ctx context.Context, baseURL string, managementKey string, sourceType string, sourceLocator string, desired proaccountgateway.ModelRules) (proaccountgateway.ModelRules, proaccountgateway.ModelRules, error)
	RestoreModelRules(ctx context.Context, baseURL string, managementKey string, sourceType string, sourceLocator string, previous proaccountgateway.ModelRules) error
	TestAccount(ctx context.Context, gatewayBaseURL string, managementKey string, account proaccountgateway.AccountReference, modelName string) (proaccountgateway.ConnectivityResult, error)
	StartOAuth(ctx context.Context, baseURL string, managementKey string, platform string) (proaccountgateway.OAuthStartResult, error)
	OAuthStatus(ctx context.Context, baseURL string, managementKey string, state string) (proaccountgateway.OAuthStatusResult, error)
	CancelOAuth(ctx context.Context, baseURL string, managementKey string, state string) error
	ImportVertexDraft(ctx context.Context, baseURL string, managementKey string, input proaccountgateway.ImportVertexInput) (proaccountgateway.AccountSnapshot, error)
}

type ProbeService interface {
	ProbeCandidate(ctx context.Context, input proaccountprobe.Input) (proaccountprobe.Result, error)
}

type OperationService interface {
	Start(ctx context.Context, input proaccountoperation.StartInput) (model.ProAccountDraft, bool, error)
	Get(ctx context.Context, operationID string) (model.ProAccountDraft, error)
	Transition(ctx context.Context, operationID string, input proaccountoperation.TransitionInput) (model.ProAccountDraft, error)
}
