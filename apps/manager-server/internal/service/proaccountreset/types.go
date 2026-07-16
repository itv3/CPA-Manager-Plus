package proaccountreset

import (
	"context"

	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/model"
	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/service/managerconfig"
	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/service/proaccountgateway"
	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/service/proaccountoperation"
	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/store"
)

const (
	CapabilitySupported   = "supported"
	CapabilityUnsupported = "unsupported"
	CapabilityUnknown     = "unknown"
)

type Credit struct {
	ID          string `json:"id,omitempty"`
	ExpiresAtMS int64  `json:"expiresAtMs,omitempty"`
}

type CreditsResult struct {
	Capability     string   `json:"capability"`
	AvailableCount *int     `json:"availableCount,omitempty"`
	Credits        []Credit `json:"credits"`
	UpdatedAtMS    int64    `json:"updatedAtMs"`
	ErrorCode      string   `json:"errorCode,omitempty"`
	Retryable      bool     `json:"retryable"`
}

type ResetInput struct {
	AccountID       string
	OperationID     string
	IdempotencyKey  string
	ExpectedVersion int64
	Confirmed       bool
}

type ResetResult struct {
	Credits   CreditsResult         `json:"credits"`
	Operation model.ProAccountDraft `json:"operation"`
}

type AccountReader interface {
	Get(ctx context.Context, id string) (model.ProAccount, error)
}

type SetupResolver interface {
	ResolveSetupWithSource(ctx context.Context) (store.Setup, managerconfig.Source, bool, error)
}

type Gateway interface {
	Snapshot(ctx context.Context, baseURL string, managementKey string) (proaccountgateway.SnapshotResult, error)
	APICall(ctx context.Context, baseURL string, managementKey string, input proaccountgateway.APICallRequest) (proaccountgateway.APICallResult, error)
}

type OperationService interface {
	Start(ctx context.Context, input proaccountoperation.StartInput) (model.ProAccountDraft, bool, error)
	Transition(ctx context.Context, operationID string, input proaccountoperation.TransitionInput) (model.ProAccountDraft, error)
}
