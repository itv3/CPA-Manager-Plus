package model

const (
	ProAccountScheduledTestStatusSuccess = "success"
	ProAccountScheduledTestStatusFailed  = "failed"
)

// ProAccountScheduledTestPlan 描述统一账号的周期性连通性测试计划。
type ProAccountScheduledTestPlan struct {
	ID             int64  `json:"id"`
	AccountID      string `json:"proAccountId"`
	Model          string `json:"modelId"`
	CronExpression string `json:"cronExpression"`
	Enabled        bool   `json:"enabled"`
	MaxResults     int    `json:"maxResults"`
	AutoRecover    bool   `json:"autoRecover"`
	LastRunAtMS    int64  `json:"lastRunAtMs,omitempty"`
	NextRunAtMS    int64  `json:"nextRunAtMs,omitempty"`
	CreatedAtMS    int64  `json:"createdAtMs"`
	UpdatedAtMS    int64  `json:"updatedAtMs"`
}

// ProAccountScheduledTestResult 保存一次周期性或手动触发的测试结果。
type ProAccountScheduledTestResult struct {
	ID           int64  `json:"id"`
	PlanID       int64  `json:"planId"`
	Status       string `json:"status"`
	StatusCode   int    `json:"statusCode,omitempty"`
	ResponseText string `json:"responseText,omitempty"`
	ErrorCode    string `json:"errorCode,omitempty"`
	ErrorMessage string `json:"errorMessage,omitempty"`
	Retryable    bool   `json:"retryable"`
	LatencyMS    int64  `json:"latencyMs"`
	StartedAtMS  int64  `json:"startedAtMs"`
	FinishedAtMS int64  `json:"finishedAtMs"`
	CreatedAtMS  int64  `json:"createdAtMs"`
}
