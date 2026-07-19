package proaccountprobe

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/service/proaccountgateway"
)

type builtInStub struct {
	models []string
	err    error
}

type probeRoundTripFunc func(*http.Request) (int, string)

func (f probeRoundTripFunc) RoundTrip(request *http.Request) (*http.Response, error) {
	status, body := f(request)
	return &http.Response{
		StatusCode: status,
		Header:     make(http.Header),
		Body:       io.NopCloser(strings.NewReader(body)),
		Request:    request,
	}, nil
}

func newProbeTestClient(handler probeRoundTripFunc) *http.Client {
	return &http.Client{Transport: handler}
}

func (s builtInStub) BuiltIn(context.Context, string, string) ([]string, error) {
	return s.models, s.err
}

func TestOpenAIChatCompletionsEndpointAcceptsFullAndVersionedBaseURL(t *testing.T) {
	for _, testCase := range []struct {
		name    string
		baseURL string
		want    string
	}{
		{
			name:    "完整端点保持不变",
			baseURL: "https://opencode.ai/zen/go/v1/chat/completions",
			want:    "https://opencode.ai/zen/go/v1/chat/completions",
		},
		{
			name:    "v1 基础地址追加相对端点",
			baseURL: "https://opencode.ai/zen/go/v1",
			want:    "https://opencode.ai/zen/go/v1/chat/completions",
		},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			base, err := url.Parse(testCase.baseURL)
			if err != nil {
				t.Fatalf("解析测试地址：%v", err)
			}
			if got := endpoint(base, "/v1/chat/completions"); got != testCase.want {
				t.Fatalf("Chat Completions 地址 = %q，期望 %q", got, testCase.want)
			}
		})
	}
}

func TestOpenAIProbeAcceptsFullChatCompletionsEndpoint(t *testing.T) {
	var chatCalls atomic.Int64
	client := newProbeTestClient(func(r *http.Request) (int, string) {
		if r.URL.Path == "/zen/go/v1/chat/completions" {
			chatCalls.Add(1)
			return http.StatusOK, `{"choices":[{"message":{"content":"OK"}}]}`
		}
		// 完整 Chat 端点无法推导模型列表和 Responses 兄弟端点，自动探测应继续回退到 Chat。
		return http.StatusNotFound, ""
	})

	result, err := New(client).ProbeCandidate(context.Background(), Input{
		Platform: "openai", AuthType: "api",
		BaseURL: "https://opencode.ai/zen/go/v1/chat/completions", APIKey: "candidate-secret",
		Model: "glm-5.2", AllowedModels: []string{"glm-5.2"},
	})
	if err != nil {
		t.Fatalf("探测完整 Chat Completions 端点：%v", err)
	}
	if result.SelectedProtocol != "chat_completions" || result.SourceType != proaccountgateway.SourceOpenAICompatibility {
		t.Fatalf("协议选择 = %#v", result)
	}
	if result.ChatCompletions.Status != CapabilitySupported || chatCalls.Load() != 1 {
		t.Fatalf("Chat Completions 探测结果 = %#v，调用次数 = %d", result.ChatCompletions, chatCalls.Load())
	}
}

func TestOpenAIProbePrefersResponsesAndRequiresFunctionCall(t *testing.T) {
	var chatCalls atomic.Int64
	client := newProbeTestClient(func(r *http.Request) (int, string) {
		if r.Header.Get("Authorization") != "Bearer candidate-secret" {
			return http.StatusUnauthorized, "unauthorized"
		}
		switch r.URL.Path {
		case "/v1/models":
			return http.StatusOK, `{"data":[{"id":"gpt-test"}]}`
		case "/v1/responses":
			return http.StatusOK, `{"output":[{"type":"function_call","name":"probe_account"}]}`
		case "/v1/chat/completions":
			chatCalls.Add(1)
			return http.StatusOK, `{"choices":[]}`
		default:
			return http.StatusNotFound, ""
		}
	})
	result, err := New(client).ProbeCandidate(context.Background(), Input{
		Platform: "openai", AuthType: "api", BaseURL: "https://probe.example", APIKey: "candidate-secret",
		AllowedModels: []string{"gpt-test"},
	})
	if err != nil {
		t.Fatalf("探测：%v", err)
	}
	if result.SelectedProtocol != "responses" || result.SourceType == "" || result.Responses.Status != CapabilitySupported {
		t.Fatalf("探测结果 = %#v", result)
	}
	if chatCalls.Load() != 0 {
		t.Fatalf("Responses 支持时不应探测 Chat，调用次数 = %d", chatCalls.Load())
	}
}

func TestProbeMergesUpstreamBuiltInAndManualModels(t *testing.T) {
	client := newProbeTestClient(func(r *http.Request) (int, string) {
		switch r.URL.Path {
		case "/v1/models":
			return http.StatusOK, `{"data":[{"id":"upstream"},{"id":"shared"}]}`
		case "/v1/responses":
			return http.StatusOK, `{"output":[{"type":"function_call"}]}`
		default:
			return http.StatusNotFound, ""
		}
	})

	result, err := New(client, builtInStub{models: []string{"shared", "built-in"}}).ProbeCandidate(context.Background(), Input{
		Platform: "openai", AuthType: "api", BaseURL: "https://probe.example", APIKey: "candidate-secret", Model: "manual",
		AllowedModels: []string{"manual"}, ModelMapping: map[string]string{"alias": "target"},
	})
	if err != nil {
		t.Fatalf("探测：%v", err)
	}
	want := []string{"shared", "upstream", "built-in", "manual", "alias", "target"}
	if !reflect.DeepEqual(result.Models, want) {
		t.Fatalf("模型目录 = %#v，期望 %#v", result.Models, want)
	}
}

func TestOpenAIProbeFallsBackOnlyWhenResponsesIsUnsupported(t *testing.T) {
	for _, testCase := range []struct {
		name            string
		responsesStatus int
		wantProtocol    string
		wantSource      string
		wantChatCalls   int64
		wantResponses   string
		wantWarning     bool
	}{
		{
			name: "明确不支持", responsesStatus: http.StatusNotFound,
			wantProtocol: "chat_completions", wantSource: proaccountgateway.SourceOpenAICompatibility,
			wantChatCalls: 1, wantResponses: CapabilityUnsupported,
		},
		{
			name: "状态未知", responsesStatus: http.StatusInternalServerError,
			wantProtocol: "responses", wantSource: proaccountgateway.SourceCodexAPIKey,
			wantChatCalls: 0, wantResponses: CapabilityUnknown, wantWarning: true,
		},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			var chatCalls atomic.Int64
			client := newProbeTestClient(func(r *http.Request) (int, string) {
				switch r.URL.Path {
				case "/v1/models":
					return http.StatusOK, `{"data":[{"id":"gpt-test"}]}`
				case "/v1/responses":
					return testCase.responsesStatus, ""
				case "/v1/chat/completions":
					chatCalls.Add(1)
					return http.StatusOK, `{"choices":[{"message":{"content":"OK"}}]}`
				default:
					return http.StatusNotFound, ""
				}
			})
			result, err := New(client).ProbeCandidate(context.Background(), Input{
				Platform: "openai", AuthType: "api", BaseURL: "https://probe.example", APIKey: "candidate-secret",
			})
			if err != nil {
				t.Fatalf("探测：%v", err)
			}
			if result.SelectedProtocol != testCase.wantProtocol || result.SourceType != testCase.wantSource || result.Responses.Status != testCase.wantResponses || chatCalls.Load() != testCase.wantChatCalls {
				t.Fatalf("探测结果 = %#v chatCalls=%d", result, chatCalls.Load())
			}
			if testCase.wantWarning && len(result.Warnings) == 0 {
				t.Fatalf("状态未知时缺少诊断：%#v", result)
			}
		})
	}
}

func TestOpenAIProbeRetriesAfterModelSpecific404(t *testing.T) {
	var chatCalls atomic.Int64
	probedModels := make([]string, 0, 2)
	client := newProbeTestClient(func(r *http.Request) (int, string) {
		switch r.URL.Path {
		case "/v1/models":
			return http.StatusOK, `{"data":[{"id":"gpt-5-codex"},{"id":"gpt-5.6-sol"}]}`
		case "/v1/responses":
			var payload struct {
				Model string `json:"model"`
			}
			if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
				t.Errorf("解析 Responses 探测请求：%v", err)
				return http.StatusBadRequest, ""
			}
			probedModels = append(probedModels, payload.Model)
			if payload.Model == "gpt-5-codex" {
				return http.StatusNotFound, `{"error":"当前 API 不支持所选模型 gpt-5-codex","type":"error"}`
			}
			return http.StatusInternalServerError, `{"error":{"message":"当前模型 gpt-5.6-sol 负载已经达到上限","code":"get_channel_failed"}}`
		case "/v1/chat/completions":
			chatCalls.Add(1)
			return http.StatusOK, `{"choices":[]}`
		default:
			return http.StatusNotFound, ""
		}
	})

	result, err := New(client).ProbeCandidate(context.Background(), Input{
		Platform: "openai", AuthType: "api", BaseURL: "https://probe.example/v1", APIKey: "candidate-secret",
		Model: "gpt-5-codex", AllowedModels: []string{"gpt-5-codex", "gpt-5.6-sol"},
	})
	if err != nil {
		t.Fatalf("探测：%v", err)
	}
	wantModels := []string{"gpt-5-codex", "gpt-5.6-sol"}
	if !reflect.DeepEqual(probedModels, wantModels) {
		t.Fatalf("Responses 探测模型 = %#v，期望 %#v", probedModels, wantModels)
	}
	if result.SelectedProtocol != "responses" || result.SourceType != proaccountgateway.SourceCodexAPIKey || result.TestModel != "gpt-5.6-sol" {
		t.Fatalf("模型级 404 后的协议选择 = %#v", result)
	}
	if result.Responses.Status != CapabilityUnknown || result.Responses.StatusCode != http.StatusInternalServerError || result.Responses.ErrorCode != "upstream_unavailable" {
		t.Fatalf("第二个候选模型的探测结果 = %#v", result.Responses)
	}
	if chatCalls.Load() != 0 {
		t.Fatalf("模型级 404 不应回退 Chat Completions，调用次数 = %d", chatCalls.Load())
	}
}

func TestOpenAIProbeSelectsChatEvenWhenBothProtocolsAreUnsupported(t *testing.T) {
	const secret = "candidate-secret-must-not-leak"
	client := newProbeTestClient(func(r *http.Request) (int, string) {
		switch r.URL.Path {
		case "/v1/models":
			return http.StatusOK, `{"data":[{"id":"gpt-test"}]}`
		case "/v1/responses", "/v1/chat/completions":
			return http.StatusNotFound, ""
		default:
			return http.StatusNotFound, ""
		}
	})

	result, err := New(client).ProbeCandidate(context.Background(), Input{
		Platform: "openai", AuthType: "api", BaseURL: "https://probe.example", APIKey: secret,
	})
	if err != nil {
		t.Fatalf("探测：%v", err)
	}
	if result.SelectedProtocol != "chat_completions" || result.SourceType != proaccountgateway.SourceOpenAICompatibility || result.ChatCompletions.Status != CapabilityUnsupported || result.ErrorMessage == "" {
		t.Fatalf("双协议不支持时的选择 = %#v", result)
	}
	if len(result.Warnings) == 0 || !strings.Contains(strings.Join(result.Warnings, "；"), "上游未提供 Chat Completions 协议端点") {
		t.Fatalf("双协议不支持时缺少中文诊断：%#v", result.Warnings)
	}
	raw, err := json.Marshal(result)
	if err != nil {
		t.Fatalf("序列化探测结果：%v", err)
	}
	if strings.Contains(string(raw), secret) || strings.Contains(strings.Join(result.Warnings, "；"), secret) {
		t.Fatalf("探测结果泄露 API Key：%s", raw)
	}
}

func TestOpenAIForcedProtocolDeterminesSourceBeforeProbe(t *testing.T) {
	for _, testCase := range []struct {
		name         string
		protocolMode string
		wantProtocol string
		wantSource   string
		wantPath     string
	}{
		{
			name: "强制 Responses", protocolMode: "responses", wantProtocol: "responses",
			wantSource: proaccountgateway.SourceCodexAPIKey, wantPath: "/v1/responses",
		},
		{
			name: "强制 Chat Completions", protocolMode: "chat_completions", wantProtocol: "chat_completions",
			wantSource: proaccountgateway.SourceOpenAICompatibility, wantPath: "/v1/chat/completions",
		},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			var responsesCalls atomic.Int64
			var chatCalls atomic.Int64
			client := newProbeTestClient(func(r *http.Request) (int, string) {
				switch r.URL.Path {
				case "/v1/models":
					return http.StatusOK, `{"data":[{"id":"gpt-test"}]}`
				case "/v1/responses":
					responsesCalls.Add(1)
					return http.StatusInternalServerError, ""
				case "/v1/chat/completions":
					chatCalls.Add(1)
					return http.StatusInternalServerError, ""
				default:
					return http.StatusNotFound, ""
				}
			})

			result, err := New(client).ProbeCandidate(context.Background(), Input{
				Platform: "openai", AuthType: "api", BaseURL: "https://probe.example", APIKey: "candidate-secret",
				ProtocolMode: testCase.protocolMode,
			})
			if err != nil {
				t.Fatalf("探测：%v", err)
			}
			if result.SelectedProtocol != testCase.wantProtocol || result.SourceType != testCase.wantSource || len(result.Warnings) == 0 {
				t.Fatalf("强制协议结果 = %#v", result)
			}
			if testCase.wantPath == "/v1/responses" && (responsesCalls.Load() != 1 || chatCalls.Load() != 0) {
				t.Fatalf("强制 Responses 调用次数 responses=%d chat=%d", responsesCalls.Load(), chatCalls.Load())
			}
			if testCase.wantPath == "/v1/chat/completions" && (responsesCalls.Load() != 0 || chatCalls.Load() != 1) {
				t.Fatalf("强制 Chat 调用次数 responses=%d chat=%d", responsesCalls.Load(), chatCalls.Load())
			}
		})
	}
}

func TestFixedProtocolPlatformsKeepSourceWhenConnectivityIsUnknown(t *testing.T) {
	for _, testCase := range []struct {
		platform   string
		wantSource string
	}{
		{platform: "anthropic", wantSource: proaccountgateway.SourceClaudeAPIKey},
		{platform: "gemini", wantSource: proaccountgateway.SourceGeminiAPIKey},
	} {
		t.Run(testCase.platform, func(t *testing.T) {
			client := newProbeTestClient(func(r *http.Request) (int, string) {
				switch r.URL.Path {
				case "/v1/models", "/v1beta/models":
					return http.StatusInternalServerError, ""
				case "/v1/messages", "/v1beta/models/gemini-2.0-flash:generateContent":
					return http.StatusServiceUnavailable, ""
				default:
					return http.StatusNotFound, ""
				}
			})

			result, err := New(client).ProbeCandidate(context.Background(), Input{
				Platform: testCase.platform, AuthType: "api", BaseURL: "https://probe.example", APIKey: "candidate-secret",
			})
			if err != nil {
				t.Fatalf("探测：%v", err)
			}
			if result.SourceType != testCase.wantSource || result.BasicConnectivity.Status != CapabilityUnknown || result.BasicConnectivity.ErrorMessage == "" || result.ErrorMessage == "" || len(result.Warnings) == 0 {
				t.Fatalf("固定协议平台探测结果 = %#v", result)
			}
		})
	}
}
