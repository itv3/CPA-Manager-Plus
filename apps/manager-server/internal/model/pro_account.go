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
