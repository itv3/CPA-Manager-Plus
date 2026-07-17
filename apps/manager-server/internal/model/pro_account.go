package model

const (
	ProAccountHealthUnknown = "unknown"

	ProBindingStatusCurrent             = "current"
	ProBindingStatusPendingConfirmation = "pending_confirmation"
	ProBindingStatusConflict            = "conflict"

	ProBindingResolutionExact    = "exact"
	ProBindingResolutionCreated  = "created"
	ProBindingResolutionPending  = "pending_confirmation"
	ProBindingResolutionConflict = "conflict"

	ProAttributionQualityExact           = "exact"
	ProAttributionQualityRetainedHistory = "retained_history"
	ProAttributionQualityPartial         = "partial"
	ProAttributionQualityAmbiguous       = "ambiguous"
	ProAttributionQualityUnknown         = "unknown"

	ProOperationStateDraftCreated            = "draft_created"
	ProOperationStateCredentialSavedDisabled = "credential_saved_disabled"
	ProOperationStateProbed                  = "probed"
	ProOperationStateModelsConfigured        = "models_configured"
	ProOperationStateTested                  = "tested"
	ProOperationStateEnabled                 = "enabled"
	ProOperationStateCancelled               = "cancelled"
	ProOperationStateCompensating            = "compensating"
	ProOperationStateFailed                  = "failed"
)

// ProAccount 是管理端维护的统一账号视图，不改变 Gateway 的底层账号结构。
type ProAccount struct {
	ID               string             `json:"id"`
	Platform         string             `json:"platform"`
	AuthType         string             `json:"authType"`
	SourceType       string             `json:"sourceType"`
	Name             string             `json:"name,omitempty"`
	Email            string             `json:"email,omitempty"`
	Enabled          bool               `json:"enabled"`
	HealthStatus     string             `json:"healthStatus"`
	LastError        string             `json:"lastError,omitempty"`
	AllowedModels    []string           `json:"allowedModels"`
	ModelMapping     map[string]string  `json:"modelMapping"`
	ModelRuleVersion string             `json:"modelRuleVersion,omitempty"`
	LastUsedAtMS     int64              `json:"lastUsedAtMs,omitempty"`
	LastTestedAtMS   int64              `json:"lastTestedAtMs,omitempty"`
	ExpiresAtMS      int64              `json:"expiresAtMs,omitempty"`
	DeletedAtMS      int64              `json:"deletedAtMs,omitempty"`
	CreatedAtMS      int64              `json:"createdAtMs"`
	UpdatedAtMS      int64              `json:"updatedAtMs"`
	Version          int64              `json:"version"`
	Binding          *ProAccountBinding `json:"binding,omitempty"`
}

// ProAccountDraft 保存跨 Gateway Management API 调用的可恢复操作状态，不保存凭证明文。
type ProAccountDraft struct {
	OperationID        string         `json:"operationId"`
	IdempotencyKey     string         `json:"idempotencyKey"`
	OperationType      string         `json:"operationType"`
	ProAccountID       string         `json:"proAccountId,omitempty"`
	State              string         `json:"state"`
	Version            int64          `json:"version"`
	RetryCount         int            `json:"retryCount"`
	CleanupDeadlineMS  int64          `json:"cleanupDeadlineMs"`
	CompensationAction string         `json:"compensationAction,omitempty"`
	ErrorCode          string         `json:"errorCode,omitempty"`
	ErrorSummary       string         `json:"errorSummary,omitempty"`
	Context            map[string]any `json:"context,omitempty"`
	CreatedAtMS        int64          `json:"createdAtMs"`
	UpdatedAtMS        int64          `json:"updatedAtMs"`
}

type ProAccountDraftCreate struct {
	OperationID       string
	IdempotencyKey    string
	OperationType     string
	ProAccountID      string
	CleanupDeadlineMS int64
	Context           map[string]any
}

type ProAccountDraftUpdate struct {
	ProAccountID       string
	State              string
	RetryCount         int
	CleanupDeadlineMS  int64
	CompensationAction string
	ErrorCode          string
	ErrorSummary       string
	Context            map[string]any
}

// ProAccountBinding 保存统一账号与 Gateway 运行时凭证标识之间的历史关系。
type ProAccountBinding struct {
	ID                 int64  `json:"id"`
	ProAccountID       string `json:"proAccountId"`
	AuthIndex          string `json:"authIndex,omitempty"`
	SourceType         string `json:"sourceType"`
	SourceLocator      string `json:"sourceLocator"`
	SourceFingerprint  string `json:"sourceFingerprint,omitempty"`
	BindingStatus      string `json:"bindingStatus"`
	IsCurrent          bool   `json:"isCurrent"`
	ValidFromMS        int64  `json:"validFromMs"`
	ValidToMS          int64  `json:"validToMs,omitempty"`
	AttributionQuality string `json:"attributionQuality"`
	FirstSeenAtMS      int64  `json:"firstSeenAtMs"`
	LastSeenAtMS       int64  `json:"lastSeenAtMs"`
	CreatedAtMS        int64  `json:"createdAtMs"`
	UpdatedAtMS        int64  `json:"updatedAtMs"`
}

type ProAccountDiscovery struct {
	Platform          string
	AuthType          string
	SourceType        string
	Name              string
	Email             string
	Enabled           bool
	HealthStatus      string
	LastError         string
	AllowedModels     []string
	ModelMapping      map[string]string
	ModelRuleVersion  string
	ExpiresAtMS       int64
	AuthIndex         string
	SourceLocator     string
	SourceFingerprint string
}

type ProAccountListFilter struct {
	Cursor       string
	Limit        int
	Search       string
	Platform     string
	AuthType     string
	Enabled      *bool
	HealthStatus string
}

type ProAccountListResult struct {
	Items      []ProAccount `json:"items"`
	NextCursor string       `json:"nextCursor,omitempty"`
	Total      int64        `json:"total"`
}

type ProAccountSyncItem struct {
	Resolution    string   `json:"resolution"`
	ProAccountID  string   `json:"proAccountId,omitempty"`
	SourceLocator string   `json:"sourceLocator"`
	AuthIndex     string   `json:"authIndex,omitempty"`
	CandidateIDs  []string `json:"candidateIds,omitempty"`
	ReasonCode    string   `json:"reasonCode,omitempty"`
}

type ProAccountSyncResult struct {
	DryRun       bool                   `json:"dryRun"`
	Discovered   int                    `json:"discovered"`
	Created      int                    `json:"created"`
	Updated      int                    `json:"updated"`
	Pending      int                    `json:"pending"`
	Conflicts    int                    `json:"conflicts"`
	Items        []ProAccountSyncItem   `json:"items"`
	Capabilities ProAccountCapabilities `json:"capabilities"`
	Warnings     []string               `json:"warnings,omitempty"`
}

// ProAccountBindingReview 保存无法自动确认的底层绑定漂移，必须由管理员选择候选账号。
type ProAccountBindingReview struct {
	ID                int64    `json:"id"`
	DiscoveryKey      string   `json:"discoveryKey"`
	SourceType        string   `json:"sourceType"`
	SourceLocator     string   `json:"sourceLocator"`
	AuthIndex         string   `json:"authIndex,omitempty"`
	SourceFingerprint string   `json:"sourceFingerprint,omitempty"`
	ResolutionStatus  string   `json:"resolutionStatus"`
	CandidateIDs      []string `json:"candidateIds"`
	ReasonCode        string   `json:"reasonCode"`
	DriftType         string   `json:"driftType"`
	ResolvedAccountID string   `json:"resolvedAccountId,omitempty"`
	ResolvedAtMS      int64    `json:"resolvedAtMs,omitempty"`
	FirstSeenAtMS     int64    `json:"firstSeenAtMs"`
	LastSeenAtMS      int64    `json:"lastSeenAtMs"`
	CreatedAtMS       int64    `json:"createdAtMs"`
	UpdatedAtMS       int64    `json:"updatedAtMs"`
}

// ProAccountCapabilities 描述当前 Gateway 是否具备 Pro 流程依赖的通用能力。
type ProAccountCapabilities struct {
	CredentialDraft bool `json:"credentialDraft"`
	AllowedModels   bool `json:"allowedModels"`
}

type ProAccountUsageWindow struct {
	ID               string   `json:"id"`
	Label            string   `json:"label"`
	UsedPercent      *float64 `json:"usedPercent,omitempty"`
	RemainingPercent *float64 `json:"remainingPercent,omitempty"`
	ResetAtMS        int64    `json:"resetAtMs,omitempty"`
	Source           string   `json:"source"`
}

type ProAccountLocalUsage struct {
	FromMS              int64    `json:"fromMs"`
	ToMS                int64    `json:"toMs"`
	Requests            int64    `json:"requests"`
	Successes           int64    `json:"successes"`
	Failures            int64    `json:"failures"`
	InputTokens         int64    `json:"inputTokens"`
	OutputTokens        int64    `json:"outputTokens"`
	CachedTokens        int64    `json:"cachedTokens"`
	CacheReadTokens     int64    `json:"cacheReadTokens"`
	CacheCreationTokens int64    `json:"cacheCreationTokens"`
	ReasoningTokens     int64    `json:"reasoningTokens"`
	TotalTokens         int64    `json:"totalTokens"`
	EstimatedCost       *float64 `json:"estimatedCost,omitempty"`
	CostKnown           bool     `json:"costKnown"`
	LastActivityAtMS    int64    `json:"lastActivityAtMs,omitempty"`
}

type ProAccountUsageResult struct {
	Source          string                  `json:"source"`
	UpdatedAtMS     int64                   `json:"updatedAtMs"`
	OfficialWindows []ProAccountUsageWindow `json:"officialWindows"`
	Local           ProAccountLocalUsage    `json:"local"`
	ErrorCode       string                  `json:"errorCode,omitempty"`
	ErrorMessage    string                  `json:"errorMessage,omitempty"`
	Retryable       bool                    `json:"retryable"`
}
