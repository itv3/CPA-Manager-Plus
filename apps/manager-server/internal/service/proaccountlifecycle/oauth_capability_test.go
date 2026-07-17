package proaccountlifecycle

import (
	"context"
	"errors"
	"testing"

	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/service/proaccountgateway"
)

type geminiCapabilityGateway struct {
	Gateway
	status string
}

func (g geminiCapabilityGateway) Capabilities(context.Context, string, string) (proaccountgateway.Capabilities, error) {
	return proaccountgateway.Capabilities{CredentialDraft: true, AllowedModels: true}, nil
}

func (g geminiCapabilityGateway) PlatformCapabilities(context.Context, string, string) (proaccountgateway.PlatformCapabilities, error) {
	return proaccountgateway.PlatformCapabilities{
		GeminiOAuth: proaccountgateway.AuthCapability{Status: g.status},
	}, nil
}

func TestStartOAuthRejectsGeminiWhenPluginIsUnavailable(t *testing.T) {
	service := New(nil, nil, recoverySetupStub{}, geminiCapabilityGateway{status: proaccountgateway.CapabilityUnsupported}, nil, nil)
	_, err := service.StartOAuth(context.Background(), OAuthStartInput{
		OperationID: "gemini-oauth", IdempotencyKey: "gemini-key", Platform: "gemini",
	})
	if !errors.Is(err, ErrGatewayCapability) {
		t.Fatalf("错误 = %v，期望 Gateway 能力不足", err)
	}
}
