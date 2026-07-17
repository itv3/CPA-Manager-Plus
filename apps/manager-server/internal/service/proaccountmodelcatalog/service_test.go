package proaccountmodelcatalog

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/model"
	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/service/managerconfig"
	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/store"
)

type accountStub struct {
	account model.ProAccount
}

func (s accountStub) Get(context.Context, string) (model.ProAccount, error) {
	return s.account, nil
}

type setupStub struct{}

func (setupStub) ResolveSetupWithSource(context.Context) (store.Setup, managerconfig.Source, bool, error) {
	return store.Setup{CPAUpstreamURL: "http://gateway.test", ManagementKey: "management-key"}, managerconfig.Source(""), true, nil
}

type gatewayStub struct {
	runtime    []string
	builtIn    []string
	runtimeErr error
	channel    string
}

func (s *gatewayStub) ListRuntimeModels(context.Context, string, string, string, string) ([]string, error) {
	return s.runtime, s.runtimeErr
}

func (s *gatewayStub) ListBuiltInModels(_ context.Context, _ string, _ string, channel string) ([]string, error) {
	s.channel = channel
	return s.builtIn, nil
}

func TestSyncAccountMergesModelsInRequiredOrder(t *testing.T) {
	gateway := &gatewayStub{
		runtime: []string{"upstream-a", "shared"},
		builtIn: []string{"shared", "built-in-b"},
	}
	service := New(accountStub{account: model.ProAccount{
		ID: "account-1", Platform: "gemini", AuthType: "vertex",
		AllowedModels: []string{"manual-c", "prefix-*"},
		ModelMapping:  map[string]string{"alias-d": "target-d"},
		Binding:       &model.ProAccountBinding{AuthIndex: "auth-1", SourceLocator: "vertex.json"},
	}}, setupStub{}, gateway)

	result, err := service.SyncAccount(context.Background(), "account-1")
	if err != nil {
		t.Fatalf("同步模型目录：%v", err)
	}
	want := []string{"upstream-a", "shared", "built-in-b", "manual-c", "alias-d", "target-d"}
	if !reflect.DeepEqual(result.Models, want) {
		t.Fatalf("模型合并顺序 = %#v，期望 %#v", result.Models, want)
	}
	if result.UpstreamStatus != UpstreamSupported || gateway.channel != "vertex" {
		t.Fatalf("同步状态 = %#v，channel=%q", result, gateway.channel)
	}
}

func TestSyncAccountKeepsBuiltInAndManualWhenUpstreamFails(t *testing.T) {
	gateway := &gatewayStub{builtIn: []string{"built-in"}, runtimeErr: errors.New("unavailable")}
	service := New(accountStub{account: model.ProAccount{
		ID: "account-1", Platform: "anthropic", AuthType: "oauth",
		AllowedModels: []string{"manual"}, ModelMapping: map[string]string{},
		Binding: &model.ProAccountBinding{AuthIndex: "auth-1", SourceLocator: "claude.json"},
	}}, setupStub{}, gateway)

	result, err := service.SyncAccount(context.Background(), "account-1")
	if err != nil {
		t.Fatalf("同步模型目录：%v", err)
	}
	if !reflect.DeepEqual(result.Models, []string{"built-in", "manual"}) || result.UpstreamStatus != UpstreamUnknown {
		t.Fatalf("降级结果 = %#v", result)
	}
	if !reflect.DeepEqual(result.Warnings, []string{"upstream_models_unavailable"}) {
		t.Fatalf("警告 = %#v", result.Warnings)
	}
}
