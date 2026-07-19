package proaccount

import (
	"testing"

	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/service/cpaauthfiles"
)

func TestDiscoveryFromAuthFileNormalizesIdentityWithoutSecrets(t *testing.T) {
	file := cpaauthfiles.File{
		Name: "codex-alpha.json", AuthIndex: "runtime-index", Provider: "codex",
		AccountSnapshot: "alpha@example.com", AccountID: "account-1",
		Raw: map[string]any{
			"email":                   "alpha@example.com",
			"status":                  "active",
			"subscription_expires_at": "1800000000",
			"allowed_models":          []any{"gpt-5", "gpt-5-codex"},
			"model_mapping":           map[string]any{"codex": "gpt-5-codex"},
			"access_token":            "secret-must-not-be-stored",
		},
	}
	discovery, ok := discoveryFromAuthFile(file)
	if !ok {
		t.Fatal("认证文件应被识别")
	}
	if discovery.Platform != "openai" || discovery.AuthType != "oauth" || discovery.Email != "alpha@example.com" {
		t.Fatalf("归一化结果 = %#v", discovery)
	}
	if discovery.SourceFingerprint == "" || discovery.SourceFingerprint == "secret-must-not-be-stored" {
		t.Fatalf("指纹不正确：%q", discovery.SourceFingerprint)
	}
	if len(discovery.AllowedModels) != 2 || discovery.ModelMapping["codex"] != "gpt-5-codex" {
		t.Fatalf("模型规则 = %#v %#v", discovery.AllowedModels, discovery.ModelMapping)
	}
	if discovery.ExpiresAtMS != 1_800_000_000_000 {
		t.Fatalf("订阅到期时间 = %d", discovery.ExpiresAtMS)
	}
}
