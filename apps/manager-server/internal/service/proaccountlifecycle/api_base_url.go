package proaccountlifecycle

import (
	"strings"

	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/service/proaccountgateway"
)

// normalizeAPIBaseURLForSource 将控制面允许输入的完整 Chat Completions 地址
// 转换为 CLIProxy OpenAI 兼容执行器需要的 base URL。
// 仅处理已探测为 OpenAI Compatibility 的来源，避免改变其他平台和 Responses 账号语义。
func normalizeAPIBaseURLForSource(sourceType string, baseURL string) string {
	if sourceType != proaccountgateway.SourceOpenAICompatibility {
		return baseURL
	}
	normalized := strings.TrimRight(strings.TrimSpace(baseURL), "/")
	return strings.TrimSuffix(normalized, "/chat/completions")
}
