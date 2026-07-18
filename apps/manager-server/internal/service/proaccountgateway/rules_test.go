package proaccountgateway

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"reflect"
	"sync"
	"testing"
)

type rulesRoundTripFunc func(*http.Request) (*http.Response, error)

func (f rulesRoundTripFunc) RoundTrip(request *http.Request) (*http.Response, error) {
	return f(request)
}

func TestModelListForRulesMaterializesCatalogAndExactAllowlist(t *testing.T) {
	models := modelListForRules(nil, ModelRules{
		AllowedModels: []string{"manual-model", "family-*"},
		ModelMapping:  map[string]string{"client-alias": "upstream-model"},
	}, []string{"catalog-model", "manual-model"})

	want := []map[string]any{
		{"name": "catalog-model", "alias": "catalog-model"},
		{"name": "upstream-model", "alias": "client-alias"},
		{"name": "manual-model", "alias": "manual-model"},
	}
	if !reflect.DeepEqual(models, want) {
		t.Fatalf("物化模型 = %#v，期望 %#v", models, want)
	}
	if mapping := modelMappingFromList(models); !reflect.DeepEqual(mapping, map[string]string{"client-alias": "upstream-model"}) {
		t.Fatalf("用户映射 = %#v", mapping)
	}
}

func TestWriteAndVerifyOpenAICompatibilityRulesPreservesCatalogAndHidesIdentities(t *testing.T) {
	var mu sync.Mutex
	allowed := []string{"old-model"}
	models := []map[string]any{
		{"name": "catalog-model", "alias": "catalog-model", "display-name": "Catalog Model"},
		{"name": "old-upstream", "alias": "old-alias"},
	}
	version := "rule-old"

	client := &http.Client{Transport: rulesRoundTripFunc(func(r *http.Request) (*http.Response, error) {
		mu.Lock()
		defer mu.Unlock()
		status := http.StatusOK
		payload := any(map[string]any{})
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/v0/management/openai-compatibility":
			payload = map[string]any{"openai-compatibility": []map[string]any{{
				"name": "provider", "models": models,
				"api-key-entries": []map[string]any{{"allowed-models": allowed, "model-rule-version": version}},
			}}}
		case r.Method == http.MethodPatch && r.URL.Path == "/v0/management/openai-compatibility":
			var requestPayload struct {
				Value struct {
					Allowed []string         `json:"allowed-models"`
					Models  []map[string]any `json:"models"`
				} `json:"value"`
			}
			if err := json.NewDecoder(r.Body).Decode(&requestPayload); err != nil {
				t.Fatalf("解析规则写入请求：%v", err)
			}
			allowed = requestPayload.Value.Allowed
			models = requestPayload.Value.Models
			version = "rule-new"
		default:
			status = http.StatusNotFound
		}
		raw, err := json.Marshal(payload)
		if err != nil {
			t.Fatalf("编码模拟响应：%v", err)
		}
		return &http.Response{
			StatusCode: status,
			Header:     make(http.Header),
			Body:       io.NopCloser(bytes.NewReader(raw)),
			Request:    r,
		}, nil
	})}

	desired := ModelRules{
		AllowedModels: []string{"catalog-model", "manual-model"},
		ModelMapping:  map[string]string{"client-alias": "upstream-model"},
	}
	previous, applied, err := New(client).WriteAndVerifyModelRules(
		context.Background(), "http://gateway.test", "management-key", SourceOpenAICompatibility, "provider:0:key:0", desired,
	)
	if err != nil {
		t.Fatalf("写入并回读 OpenAI Compatibility 规则：%v", err)
	}
	if previous.ModelMapping["old-alias"] != "old-upstream" || applied.ModelRuleVersion != "rule-new" {
		t.Fatalf("规则版本或旧映射异常：previous=%#v applied=%#v", previous, applied)
	}
	if !reflect.DeepEqual(applied.ModelMapping, desired.ModelMapping) {
		t.Fatalf("identity 条目泄漏到用户映射：%#v", applied.ModelMapping)
	}

	mu.Lock()
	writtenModels := append([]map[string]any(nil), models...)
	mu.Unlock()
	byAlias := make(map[string]map[string]any, len(writtenModels))
	for _, item := range writtenModels {
		alias := mapString(item, "alias")
		if alias == "" {
			alias = mapString(item, "name")
		}
		byAlias[alias] = item
	}
	if len(byAlias) != 3 || mapString(byAlias["manual-model"], "name") != "manual-model" || mapString(byAlias["client-alias"], "name") != "upstream-model" {
		t.Fatalf("写入模型目录 = %#v", writtenModels)
	}
	if mapString(byAlias["catalog-model"], "display-name") != "Catalog Model" {
		t.Fatalf("已有目录元数据未保留：%#v", byAlias["catalog-model"])
	}
	if _, exists := byAlias["old-alias"]; exists {
		t.Fatalf("已删除的显式映射仍存在：%#v", writtenModels)
	}
}
