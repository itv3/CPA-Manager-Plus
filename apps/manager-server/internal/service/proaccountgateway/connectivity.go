package proaccountgateway

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
)

const (
	ConnectivityModeDefault = "default"
	ConnectivityModeCompact = "compact"
)

type accountTestResponse struct {
	Success         bool   `json:"success"`
	StatusCode      int    `json:"status_code"`
	Protocol        string `json:"protocol"`
	Mode            string `json:"mode"`
	Model           string `json:"model"`
	UpstreamModel   string `json:"upstream_model"`
	DurationMS      int64  `json:"duration_ms"`
	ResponsePreview string `json:"response_preview"`
	ErrorCode       string `json:"error_code"`
	ErrorMessage    string `json:"error_message"`
	Retryable       bool   `json:"retryable"`
}

func (c *Client) TestAccount(ctx context.Context, gatewayBaseURL string, managementKey string, account AccountReference, modelName string) (ConnectivityResult, error) {
	return c.TestAccountWithMode(ctx, gatewayBaseURL, managementKey, account, modelName, ConnectivityModeDefault)
}

func (c *Client) TestAccountWithMode(ctx context.Context, gatewayBaseURL string, managementKey string, account AccountReference, modelName string, mode string) (ConnectivityResult, error) {
	modelName = strings.TrimSpace(modelName)
	mode = strings.ToLower(strings.TrimSpace(mode))
	if mode == "" {
		mode = ConnectivityModeDefault
	}
	if modelName == "" || (mode != ConnectivityModeDefault && mode != ConnectivityModeCompact) {
		return ConnectivityResult{}, ErrInvalidModelRule
	}
	protocol, err := connectivityProtocol(account, mode)
	if err != nil {
		return ConnectivityResult{}, err
	}

	raw, _, err := c.requestJSON(ctx, gatewayBaseURL, managementKey, "POST", "/v0/management/account-test", map[string]any{
		"auth_index": strings.TrimSpace(account.AuthIndex),
		"model":      modelName,
		"protocol":   protocol,
		"mode":       mode,
	})
	if err != nil {
		return ConnectivityResult{}, err
	}
	var response accountTestResponse
	if err := json.Unmarshal(raw, &response); err != nil {
		return ConnectivityResult{}, err
	}
	if response.Protocol == "" {
		response.Protocol = protocol
	}
	if response.Mode == "" {
		response.Mode = mode
	}
	if response.Model == "" {
		response.Model = modelName
	}
	result := ConnectivityResult{
		Success: response.Success, StatusCode: response.StatusCode, Protocol: response.Protocol,
		Mode: response.Mode, Model: response.Model, UpstreamModel: response.UpstreamModel,
		DurationMS: response.DurationMS, ResponsePreview: response.ResponsePreview,
		ErrorCode: response.ErrorCode, ErrorMessage: response.ErrorMessage, Retryable: response.Retryable,
	}
	if !result.Success {
		// 上游原始报文可能是 JSON 或长文本,归一为人类可读提示;原文降级为 responsePreview 供排查
		friendly, upstreamPreview := normalizeConnectivityError(result.ErrorCode, result.StatusCode, response.Model, result.ErrorMessage)
		result.ErrorMessage = friendly
		if result.ResponsePreview == "" && upstreamPreview != "" {
			result.ResponsePreview = upstreamPreview
		}
	}
	return result, nil
}

// normalizeConnectivityError 将 Gateway 返回的错误码/状态码翻译为面向管理员的友好提示。
// 返回 (友好提示, 上游原文预览)。
func normalizeConnectivityError(code string, statusCode int, model string, upstream string) (string, string) {
	upstream = strings.TrimSpace(upstream)
	model = strings.TrimSpace(model)
	switch code {
	case "authentication_failed":
		return "认证失败，请检查账号凭证或密钥是否有效", upstream
	case "model_not_allowed":
		return "测试模型不在账号有效白名单内", upstream
	case "model_unavailable":
		if model != "" {
			return "当前模型 " + model + " 不可用或账号无权访问", upstream
		}
		return "所选模型不可用或账号无权访问", upstream
	case "protocol_not_supported":
		return "上游不支持当前账号协议，请确认协议模式或改用其他添加入口", upstream
	case "rate_limited":
		if model != "" {
			return "当前模型 " + model + " 负载已达上限，请稍后重试", upstream
		}
		return "上游负载已达上限或触发限流，请稍后重试", upstream
	case "quota_exhausted":
		return "账号配额已用尽", upstream
	case "network_error", "connectivity_request_failed":
		return "连接上游失败，请检查网络或代理设置", upstream
	case "tls_error":
		return "TLS 握手失败，请检查证书与代理配置", upstream
	case "upstream_unavailable":
		return "上游服务暂时不可用，请稍后重试", upstream
	}
	// 未知错误码:按 HTTP 状态兜底
	switch {
	case statusCode == 401 || statusCode == 403:
		return "认证失败，请检查账号凭证或密钥是否有效", upstream
	case statusCode == 404 || statusCode == 405:
		return "上游未提供该模型或协议端点，请确认模型名与协议", upstream
	case statusCode == 429:
		return "上游负载已达上限或触发限流，请稍后重试", upstream
	case statusCode >= 500:
		return "上游服务异常，请稍后重试", upstream
	}
	if upstream != "" {
		return "账号连通性测试失败，请查看上游返回详情", upstream
	}
	return "账号连通性测试失败", upstream
}

func connectivityProtocol(account AccountReference, mode string) (string, error) {
	platform := strings.ToLower(strings.TrimSpace(account.Platform))
	if mode == ConnectivityModeCompact {
		// Compact 探测走 /v1/responses/compact,仅 Responses(codex)账号具备该端点;
		// Chat Completions 中转站没有该端点会返回 404,故在此拒绝。
		if platform != "openai" || account.SourceType != SourceCodexAPIKey {
			return "", errors.New("compact connectivity test is only available for OpenAI Responses accounts")
		}
		return "responses", nil
	}
	switch platform {
	case "openai":
		if account.SourceType == SourceOpenAICompatibility {
			return "chat_completions", nil
		}
		return "responses", nil
	case "anthropic":
		return "messages", nil
	case "gemini", "antigravity":
		return "generate_content", nil
	case "xai":
		return "chat_completions", nil
	default:
		return "", ErrUnsupportedSource
	}
}
