package proaccountgateway

import (
	"errors"
	"sort"
	"strings"
)

const (
	SourceAuthFile            = "auth_file"
	SourceGeminiAPIKey        = "config_gemini_api_key"
	SourceInteractionsAPIKey  = "config_interactions_api_key"
	SourceClaudeAPIKey        = "config_claude_api_key"
	SourceCodexAPIKey         = "config_codex_api_key"
	SourceXAIAPIKey           = "config_xai_api_key"
	SourceVertexAPIKey        = "config_vertex_api_key"
	SourceOpenAICompatibility = "config_openai_compatibility"
)

var (
	ErrUnsupportedSource    = errors.New("unsupported gateway account source")
	ErrInvalidSourceLocator = errors.New("invalid gateway account source locator")
	ErrInvalidModelRule     = errors.New("invalid account model rule")
	ErrRuleReadbackMismatch = errors.New("gateway model rule readback mismatch")
)

type Capabilities struct {
	CredentialDraft bool `json:"credentialDraft"`
	AllowedModels   bool `json:"allowedModels"`
}

type AccountSnapshot struct {
	Platform          string
	AuthType          string
	SourceType        string
	SourceLocator     string
	Name              string
	Email             string
	Enabled           bool
	HealthStatus      string
	LastError         string
	AuthIndex         string
	SourceFingerprint string
	AllowedModels     []string
	ModelMapping      map[string]string
	ModelRuleVersion  string
	ExpiresAtMS       int64
	SharedProvider    bool
	BaseURL           string
	Headers           map[string]string
	CredentialDraft   bool
	UpstreamAccountID string
}

type SnapshotResult struct {
	Accounts     []AccountSnapshot
	Capabilities Capabilities
	Warnings     []string
}

type ModelRules struct {
	AllowedModels    []string          `json:"allowedModels"`
	ModelMapping     map[string]string `json:"modelMapping"`
	ModelRuleVersion string            `json:"modelRuleVersion,omitempty"`
}

type APICallRequest struct {
	AuthIndex string
	Method    string
	URL       string
	Headers   map[string]string
	Body      any
}

type APICallResult struct {
	StatusCode int                 `json:"statusCode"`
	Headers    map[string][]string `json:"headers,omitempty"`
	Body       string              `json:"body,omitempty"`
}

type AccountRuntime struct {
	Platform string
	BaseURL  string
	Headers  map[string]string
}

type AccountReference struct {
	Platform      string
	AuthType      string
	SourceType    string
	SourceLocator string
	AuthIndex     string
}

type ConnectivityResult struct {
	Success    bool   `json:"success"`
	StatusCode int    `json:"statusCode,omitempty"`
	Protocol   string `json:"protocol"`
	Model      string `json:"model"`
	ErrorCode  string `json:"errorCode,omitempty"`
	Retryable  bool   `json:"retryable"`
}

type CreateAPIInput struct {
	Platform      string
	SourceType    string
	Name          string
	BaseURL       string
	APIKey        string
	Headers       map[string]string
	AllowedModels []string
	ModelMapping  map[string]string
}

type UpdateAPIInput struct {
	APIKey  string
	BaseURL *string
	Headers *map[string]string
}

type EditableAccount struct {
	BaseURL        string            `json:"baseUrl,omitempty"`
	Headers        map[string]string `json:"headers"`
	SharedProvider bool              `json:"sharedProvider"`
}

type OAuthStartResult struct {
	URL   string `json:"url"`
	State string `json:"state"`
}

type OAuthStatusResult struct {
	Status string `json:"status"`
	Error  string `json:"error,omitempty"`
}

type ImportVertexInput struct {
	FileName       string
	ServiceAccount []byte
	Location       string
}

func NormalizeModelRules(input ModelRules) (ModelRules, error) {
	allowed := make([]string, 0, len(input.AllowedModels))
	seen := make(map[string]struct{}, len(input.AllowedModels))
	for _, raw := range input.AllowedModels {
		value := strings.TrimSpace(raw)
		if value == "" {
			continue
		}
		if !validWildcard(value) {
			return ModelRules{}, ErrInvalidModelRule
		}
		key := strings.ToLower(value)
		if _, exists := seen[key]; exists {
			continue
		}
		seen[key] = struct{}{}
		allowed = append(allowed, value)
	}
	mapping := make(map[string]string, len(input.ModelMapping))
	for rawAlias, rawTarget := range input.ModelMapping {
		alias := strings.TrimSpace(rawAlias)
		target := strings.TrimSpace(rawTarget)
		if alias == "" || target == "" || !validWildcard(alias) || strings.Contains(target, "*") {
			return ModelRules{}, ErrInvalidModelRule
		}
		mapping[alias] = target
	}
	return ModelRules{AllowedModels: allowed, ModelMapping: mapping, ModelRuleVersion: strings.TrimSpace(input.ModelRuleVersion)}, nil
}

func RulesEqual(left ModelRules, right ModelRules) bool {
	left, leftErr := NormalizeModelRules(left)
	right, rightErr := NormalizeModelRules(right)
	if leftErr != nil || rightErr != nil {
		return false
	}
	if len(left.AllowedModels) != len(right.AllowedModels) || len(left.ModelMapping) != len(right.ModelMapping) {
		return false
	}
	leftModels := append([]string(nil), left.AllowedModels...)
	rightModels := append([]string(nil), right.AllowedModels...)
	sort.Slice(leftModels, func(i, j int) bool { return strings.ToLower(leftModels[i]) < strings.ToLower(leftModels[j]) })
	sort.Slice(rightModels, func(i, j int) bool { return strings.ToLower(rightModels[i]) < strings.ToLower(rightModels[j]) })
	for i := range leftModels {
		if !strings.EqualFold(leftModels[i], rightModels[i]) {
			return false
		}
	}
	for alias, target := range left.ModelMapping {
		if right.ModelMapping[alias] != target {
			return false
		}
	}
	return true
}

func ModelAllowed(modelName string, rules ModelRules) bool {
	modelName = strings.ToLower(strings.TrimSpace(modelName))
	if modelName == "" {
		return false
	}
	if len(rules.AllowedModels) == 0 {
		return true
	}
	patterns := append([]string(nil), rules.AllowedModels...)
	for alias := range rules.ModelMapping {
		patterns = append(patterns, alias)
	}
	for _, raw := range patterns {
		pattern := strings.ToLower(strings.TrimSpace(raw))
		if !validWildcard(pattern) {
			continue
		}
		if strings.HasSuffix(pattern, "*") {
			if strings.HasPrefix(modelName, strings.TrimSuffix(pattern, "*")) {
				return true
			}
			continue
		}
		if modelName == pattern {
			return true
		}
	}
	return false
}

func ResolveMappedModel(modelName string, rules ModelRules) string {
	modelName = strings.TrimSpace(modelName)
	if target := strings.TrimSpace(rules.ModelMapping[modelName]); target != "" {
		return target
	}
	for alias, target := range rules.ModelMapping {
		if !strings.HasSuffix(alias, "*") {
			continue
		}
		prefix := strings.TrimSuffix(alias, "*")
		if strings.HasPrefix(modelName, prefix) {
			return strings.TrimSpace(target)
		}
	}
	return modelName
}

func validWildcard(value string) bool {
	count := strings.Count(value, "*")
	return value != "" && (count == 0 || (count == 1 && strings.HasSuffix(value, "*")))
}
