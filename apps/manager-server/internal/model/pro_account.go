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
)

// ProAccount 是管理端维护的统一账号视图，不改变 Gateway 的底层账号结构。
type ProAccount struct {
	ID             string             `json:"id"`
	Platform       string             `json:"platform"`
	AuthType       string             `json:"authType"`
	SourceType     string             `json:"sourceType"`
	Name           string             `json:"name,omitempty"`
	Email          string             `json:"email,omitempty"`
	Enabled        bool               `json:"enabled"`
	HealthStatus   string             `json:"healthStatus"`
	LastError      string             `json:"lastError,omitempty"`
	AllowedModels  []string           `json:"allowedModels"`
	ModelMapping   map[string]string  `json:"modelMapping"`
	LastUsedAtMS   int64              `json:"lastUsedAtMs,omitempty"`
	LastTestedAtMS int64              `json:"lastTestedAtMs,omitempty"`
	ExpiresAtMS    int64              `json:"expiresAtMs,omitempty"`
	CreatedAtMS    int64              `json:"createdAtMs"`
	UpdatedAtMS    int64              `json:"updatedAtMs"`
	Binding        *ProAccountBinding `json:"binding,omitempty"`
}

// ProAccountBinding 保存统一账号与 Gateway 运行时凭证标识之间的历史关系。
type ProAccountBinding struct {
	ID                int64  `json:"id"`
	ProAccountID      string `json:"proAccountId"`
	AuthIndex         string `json:"authIndex,omitempty"`
	SourceType        string `json:"sourceType"`
	SourceLocator     string `json:"sourceLocator"`
	SourceFingerprint string `json:"sourceFingerprint,omitempty"`
	BindingStatus     string `json:"bindingStatus"`
	IsCurrent         bool   `json:"isCurrent"`
	ValidFromMS       int64  `json:"validFromMs"`
	ValidToMS         int64  `json:"validToMs,omitempty"`
	FirstSeenAtMS     int64  `json:"firstSeenAtMs"`
	LastSeenAtMS      int64  `json:"lastSeenAtMs"`
	CreatedAtMS       int64  `json:"createdAtMs"`
	UpdatedAtMS       int64  `json:"updatedAtMs"`
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
	DryRun     bool                 `json:"dryRun"`
	Discovered int                  `json:"discovered"`
	Created    int                  `json:"created"`
	Updated    int                  `json:"updated"`
	Pending    int                  `json:"pending"`
	Conflicts  int                  `json:"conflicts"`
	Items      []ProAccountSyncItem `json:"items"`
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
