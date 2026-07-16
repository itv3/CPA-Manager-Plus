package proaccountprobe

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
)

func TestOpenAIProbePrefersResponsesAndRequiresFunctionCall(t *testing.T) {
	var chatCalls atomic.Int64
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer candidate-secret" {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		switch r.URL.Path {
		case "/v1/models":
			_, _ = w.Write([]byte(`{"data":[{"id":"gpt-test"}]}`))
		case "/v1/responses":
			_, _ = w.Write([]byte(`{"output":[{"type":"function_call","name":"probe_account"}]}`))
		case "/v1/chat/completions":
			chatCalls.Add(1)
			_, _ = w.Write([]byte(`{"choices":[]}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	result, err := New(nil).ProbeCandidate(context.Background(), Input{
		Platform: "openai", AuthType: "api", BaseURL: server.URL, APIKey: "candidate-secret",
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

func TestOpenAIProbeFallsBackOnlyWhenResponsesIsUnsupported(t *testing.T) {
	for _, testCase := range []struct {
		name            string
		responsesStatus int
		wantProtocol    string
		wantChatCalls   int64
		wantResponses   string
	}{
		{name: "明确不支持", responsesStatus: http.StatusNotFound, wantProtocol: "chat_completions", wantChatCalls: 1, wantResponses: CapabilityUnsupported},
		{name: "状态未知", responsesStatus: http.StatusInternalServerError, wantProtocol: "", wantChatCalls: 0, wantResponses: CapabilityUnknown},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			var chatCalls atomic.Int64
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch r.URL.Path {
				case "/v1/models":
					_, _ = w.Write([]byte(`{"data":[{"id":"gpt-test"}]}`))
				case "/v1/responses":
					w.WriteHeader(testCase.responsesStatus)
				case "/v1/chat/completions":
					chatCalls.Add(1)
					_, _ = w.Write([]byte(`{"choices":[{"message":{"content":"OK"}}]}`))
				default:
					http.NotFound(w, r)
				}
			}))
			defer server.Close()
			result, err := New(nil).ProbeCandidate(context.Background(), Input{
				Platform: "openai", AuthType: "api", BaseURL: server.URL, APIKey: "candidate-secret",
			})
			if err != nil {
				t.Fatalf("探测：%v", err)
			}
			if result.SelectedProtocol != testCase.wantProtocol || result.Responses.Status != testCase.wantResponses || chatCalls.Load() != testCase.wantChatCalls {
				t.Fatalf("探测结果 = %#v chatCalls=%d", result, chatCalls.Load())
			}
		})
	}
}
