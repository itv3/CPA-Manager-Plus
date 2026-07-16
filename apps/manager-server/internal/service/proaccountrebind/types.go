package proaccountrebind

import (
	"context"

	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/model"
	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/service/managerconfig"
	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/service/proaccountgateway"
	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/service/proaccountoperation"
	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/store"
)

type Review struct {
	Review     model.ProAccountBindingReview `json:"review"`
	Candidates []model.ProAccount            `json:"candidates"`
}

type ConfirmItem struct {
	ReviewID        int64
	ProAccountID    string
	ExpectedVersion int64
}

type ConfirmInput struct {
	OperationID    string
	IdempotencyKey string
	Items          []ConfirmItem
}

type ItemResult struct {
	ReviewID     int64             `json:"reviewId"`
	ProAccountID string            `json:"proAccountId"`
	Success      bool              `json:"success"`
	Code         string            `json:"code,omitempty"`
	Message      string            `json:"message,omitempty"`
	Retryable    bool              `json:"retryable"`
	Account      *model.ProAccount `json:"account,omitempty"`
}

type Result struct {
	Total     int          `json:"total"`
	Succeeded int          `json:"succeeded"`
	Failed    int          `json:"failed"`
	Items     []ItemResult `json:"items"`
}

type AccountReader interface {
	Get(ctx context.Context, id string) (model.ProAccount, error)
}

type Repository interface {
	ListBindingReviews(ctx context.Context, statuses []string, limit int) ([]model.ProAccountBindingReview, error)
	GetBindingReview(ctx context.Context, reviewID int64) (model.ProAccountBindingReview, bool, error)
	RebindFromReview(ctx context.Context, reviewID int64, accountID string, expectedVersion int64, discovery model.ProAccountDiscovery, nowMS int64) (model.ProAccount, error)
}

type SetupResolver interface {
	ResolveSetupWithSource(ctx context.Context) (store.Setup, managerconfig.Source, bool, error)
}

type Gateway interface {
	Snapshot(ctx context.Context, baseURL string, managementKey string) (proaccountgateway.SnapshotResult, error)
}

type OperationService interface {
	Start(ctx context.Context, input proaccountoperation.StartInput) (model.ProAccountDraft, bool, error)
	Transition(ctx context.Context, operationID string, input proaccountoperation.TransitionInput) (model.ProAccountDraft, error)
}
