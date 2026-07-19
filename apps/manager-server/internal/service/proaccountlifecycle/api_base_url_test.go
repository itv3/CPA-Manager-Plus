package proaccountlifecycle

import (
	"testing"

	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/service/proaccountgateway"
)

func TestNormalizeAPIBaseURLForSource(t *testing.T) {
	for _, testCase := range []struct {
		name       string
		sourceType string
		baseURL    string
		want       string
	}{
		{
			name:       "OpenAI 兼容完整端点转换为 v1 基础地址",
			sourceType: proaccountgateway.SourceOpenAICompatibility,
			baseURL:    " https://opencode.ai/zen/go/v1/chat/completions/ ",
			want:       "https://opencode.ai/zen/go/v1",
		},
		{
			name:       "OpenAI 兼容 v1 基础地址保持不变",
			sourceType: proaccountgateway.SourceOpenAICompatibility,
			baseURL:    "https://opencode.ai/zen/go/v1",
			want:       "https://opencode.ai/zen/go/v1",
		},
		{
			name:       "Responses 来源不改变输入",
			sourceType: proaccountgateway.SourceCodexAPIKey,
			baseURL:    "https://example.test/v1/chat/completions",
			want:       "https://example.test/v1/chat/completions",
		},
		{
			name:       "其他平台不改变输入",
			sourceType: proaccountgateway.SourceClaudeAPIKey,
			baseURL:    "https://example.test/v1/chat/completions",
			want:       "https://example.test/v1/chat/completions",
		},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			if got := normalizeAPIBaseURLForSource(testCase.sourceType, testCase.baseURL); got != testCase.want {
				t.Fatalf("保存地址 = %q，期望 %q", got, testCase.want)
			}
		})
	}
}
