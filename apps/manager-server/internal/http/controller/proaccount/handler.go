package proaccount

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/app"
	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/http/middleware"
	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/http/response"
	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/model"
	proaccountrepo "github.com/seakee/cpa-manager-plus/apps/manager-server/internal/repository/proaccount"
	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/repository/proaccountdraft"
	proaccountsvc "github.com/seakee/cpa-manager-plus/apps/manager-server/internal/service/proaccount"
	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/service/proaccountbatch"
	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/service/proaccountgateway"
	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/service/proaccountlifecycle"
	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/service/proaccountmodelcatalog"
	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/service/proaccountmodels"
	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/service/proaccountoperation"
	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/service/proaccountprobe"
	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/service/proaccountrebind"
	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/service/proaccountreset"
	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/service/proaccounttest"
)

type Handler struct {
	App *app.Context
}

func (h *Handler) Handle(w http.ResponseWriter, r *http.Request) {
	if !middleware.AuthorizePanel(w, r, h.App.AdminAuthService) {
		return
	}
	path := strings.TrimRight(r.URL.Path, "/")
	switch {
	case path == "/v0/pro/accounts":
		h.handleCollection(w, r)
	case path == "/v0/pro/accounts/capabilities":
		h.handleCapabilities(w, r)
	case path == "/v0/pro/accounts/model-catalog":
		h.handleStaticModelCatalog(w, r)
	case path == "/v0/pro/accounts/batch":
		h.handleBatch(w, r)
	case path == "/v0/pro/accounts/binding-reviews":
		h.handleBindingReviews(w, r)
	case path == "/v0/pro/accounts/rebind":
		h.handleRebind(w, r)
	case path == "/v0/pro/accounts/operations":
		h.handleOperations(w, r)
	case strings.HasPrefix(path, "/v0/pro/accounts/operations/"):
		h.handleOperation(w, r, strings.TrimPrefix(path, "/v0/pro/accounts/operations/"))
	case path == "/v0/pro/accounts/probe":
		h.handleProbe(w, r)
	case path == "/v0/pro/accounts/vertex":
		h.handleCreateVertex(w, r)
	case path == "/v0/pro/accounts/oauth/start":
		h.handleOAuthStart(w, r)
	case path == "/v0/pro/accounts/oauth/status":
		h.handleOAuthStatus(w, r)
	case path == "/v0/pro/accounts/oauth/cancel":
		h.handleOAuthCancel(w, r)
	case path == "/v0/pro/accounts/drafts/cancel":
		h.handleDraftCancel(w, r)
	case path == "/v0/pro/accounts/sync":
		h.handleSync(w, r)
	case strings.HasPrefix(path, "/v0/pro/accounts/") && strings.HasSuffix(path, "/usage"):
		h.handleUsage(w, r, strings.TrimSuffix(strings.TrimPrefix(path, "/v0/pro/accounts/"), "/usage"))
	case strings.HasPrefix(path, "/v0/pro/accounts/") && strings.HasSuffix(path, "/openai-reset-credits"):
		h.handleResetCredits(w, r, strings.TrimSuffix(strings.TrimPrefix(path, "/v0/pro/accounts/"), "/openai-reset-credits"))
	case strings.HasPrefix(path, "/v0/pro/accounts/") && strings.HasSuffix(path, "/openai-reset"):
		h.handleOpenAIReset(w, r, strings.TrimSuffix(strings.TrimPrefix(path, "/v0/pro/accounts/"), "/openai-reset"))
	case strings.HasPrefix(path, "/v0/pro/accounts/") && strings.HasSuffix(path, "/models"):
		h.handleModels(w, r, strings.TrimSuffix(strings.TrimPrefix(path, "/v0/pro/accounts/"), "/models"))
	case strings.HasPrefix(path, "/v0/pro/accounts/") && strings.HasSuffix(path, "/test"):
		h.handleTest(w, r, strings.TrimSuffix(strings.TrimPrefix(path, "/v0/pro/accounts/"), "/test"))
	case strings.HasPrefix(path, "/v0/pro/accounts/") && strings.HasSuffix(path, "/complete"):
		h.handleComplete(w, r, strings.TrimSuffix(strings.TrimPrefix(path, "/v0/pro/accounts/"), "/complete"))
	case strings.HasPrefix(path, "/v0/pro/accounts/"):
		h.handleItem(w, r, strings.TrimPrefix(path, "/v0/pro/accounts/"))
	default:
		h.writeError(w, http.StatusNotFound, "pro_route_not_found", "Pro route not found", false, nil)
	}
}

func (h *Handler) handleStaticModelCatalog(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		h.writeError(w, http.StatusMethodNotAllowed, "method_not_allowed", "请求方法不受支持", false, nil)
		return
	}
	platform := strings.ToLower(strings.TrimSpace(r.URL.Query().Get("platform")))
	authType := strings.ToLower(strings.TrimSpace(r.URL.Query().Get("auth_type")))
	if platform == "" || authType == "" {
		h.writeError(w, http.StatusBadRequest, "invalid_model_catalog_request", "平台和认证方式不能为空", false, nil)
		return
	}
	result, err := h.App.ProAccountModelCatalogService.StaticCatalog(r.Context(), platform, authType)
	if err != nil {
		h.writeError(w, http.StatusBadGateway, "model_catalog_unavailable", "模型目录同步失败", true, nil)
		return
	}
	response.JSON(w, http.StatusOK, result)
}

func (h *Handler) handleBatch(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		h.writeError(w, http.StatusMethodNotAllowed, "method_not_allowed", "请求方法不受支持", false, nil)
		return
	}
	var request struct {
		OperationID    string `json:"operation_id"`
		IdempotencyKey string `json:"idempotency_key"`
		Action         string `json:"action"`
		Items          []struct {
			ProAccountID    string `json:"pro_account_id"`
			ExpectedVersion int64  `json:"expected_version"`
			Model           string `json:"model"`
		} `json:"items"`
	}
	if err := decodeBody(w, r, &request); err != nil {
		h.writeError(w, http.StatusBadRequest, "invalid_request", "批量账号操作请求无效", false, nil)
		return
	}
	items := make([]proaccountbatch.Item, 0, len(request.Items))
	for _, item := range request.Items {
		items = append(items, proaccountbatch.Item{
			ProAccountID: item.ProAccountID, ExpectedVersion: item.ExpectedVersion, Model: item.Model,
		})
	}
	result, err := h.App.ProAccountBatchService.Execute(r.Context(), proaccountbatch.Input{
		OperationID: request.OperationID, IdempotencyKey: idempotencyKey(r, request.IdempotencyKey),
		Action: request.Action, Items: items,
	})
	if err != nil {
		if errors.Is(err, proaccountbatch.ErrInvalidRequest) {
			h.writeError(w, http.StatusBadRequest, "invalid_batch_request", "批量账号操作请求无效", false, nil)
			return
		}
		h.writeError(w, http.StatusInternalServerError, "batch_operation_failed", "批量账号操作失败", true, nil)
		return
	}
	response.JSON(w, http.StatusOK, result)
}

func (h *Handler) handleBindingReviews(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		h.writeError(w, http.StatusMethodNotAllowed, "method_not_allowed", "请求方法不受支持", false, nil)
		return
	}
	limit := 100
	if raw := strings.TrimSpace(r.URL.Query().Get("limit")); raw != "" {
		value, err := strconv.Atoi(raw)
		if err != nil || value <= 0 || value > 500 {
			h.writeError(w, http.StatusBadRequest, "invalid_limit", "limit 必须在 1 到 500 之间", false, nil)
			return
		}
		limit = value
	}
	items, err := h.App.ProAccountRebindService.List(r.Context(), limit)
	if err != nil {
		h.writeError(w, http.StatusInternalServerError, "binding_reviews_failed", "无法读取待确认绑定", true, nil)
		return
	}
	response.JSON(w, http.StatusOK, map[string]any{"items": items, "total": len(items)})
}

func (h *Handler) handleRebind(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		h.writeError(w, http.StatusMethodNotAllowed, "method_not_allowed", "请求方法不受支持", false, nil)
		return
	}
	var request struct {
		OperationID    string `json:"operation_id"`
		IdempotencyKey string `json:"idempotency_key"`
		Items          []struct {
			ReviewID        int64  `json:"review_id"`
			ProAccountID    string `json:"pro_account_id"`
			ExpectedVersion int64  `json:"expected_version"`
		} `json:"items"`
	}
	if err := decodeBody(w, r, &request); err != nil {
		h.writeError(w, http.StatusBadRequest, "invalid_request", "批量重绑请求无效", false, nil)
		return
	}
	items := make([]proaccountrebind.ConfirmItem, 0, len(request.Items))
	for _, item := range request.Items {
		items = append(items, proaccountrebind.ConfirmItem{
			ReviewID: item.ReviewID, ProAccountID: item.ProAccountID, ExpectedVersion: item.ExpectedVersion,
		})
	}
	result, err := h.App.ProAccountRebindService.Confirm(r.Context(), proaccountrebind.ConfirmInput{
		OperationID: request.OperationID, IdempotencyKey: idempotencyKey(r, request.IdempotencyKey), Items: items,
	})
	if err != nil {
		if errors.Is(err, proaccountrebind.ErrInvalidRequest) {
			h.writeError(w, http.StatusBadRequest, "invalid_rebind_request", "批量重绑请求无效", false, nil)
			return
		}
		h.writeError(w, http.StatusBadGateway, "binding_rebind_failed", "批量重绑无法执行", true, nil)
		return
	}
	response.JSON(w, http.StatusOK, result)
}

func (h *Handler) handleResetCredits(w http.ResponseWriter, r *http.Request, accountID string) {
	if r.Method != http.MethodGet || !validPathID(accountID) {
		h.writeError(w, http.StatusNotFound, "pro_account_not_found", "统一账号不存在", false, nil)
		return
	}
	result, err := h.App.ProAccountResetService.Credits(r.Context(), accountID)
	if err != nil {
		if errors.Is(err, proaccountsvc.ErrAccountNotFound) {
			h.writeError(w, http.StatusNotFound, "pro_account_not_found", "统一账号不存在", false, nil)
			return
		}
		h.writeError(w, http.StatusInternalServerError, "reset_credits_query_failed", "无法查询重置次数", true, nil)
		return
	}
	response.JSON(w, http.StatusOK, result)
}

func (h *Handler) handleOpenAIReset(w http.ResponseWriter, r *http.Request, accountID string) {
	if r.Method != http.MethodPost || !validPathID(accountID) {
		h.writeError(w, http.StatusNotFound, "pro_account_not_found", "统一账号不存在", false, nil)
		return
	}
	var request struct {
		OperationID     string `json:"operation_id"`
		IdempotencyKey  string `json:"idempotency_key"`
		ExpectedVersion int64  `json:"expected_version"`
		Confirmed       bool   `json:"confirmed"`
	}
	if err := decodeBody(w, r, &request); err != nil {
		h.writeError(w, http.StatusBadRequest, "invalid_request", "OpenAI 重置请求无效", false, nil)
		return
	}
	result, err := h.App.ProAccountResetService.Reset(r.Context(), proaccountreset.ResetInput{
		AccountID: accountID, OperationID: request.OperationID,
		IdempotencyKey: idempotencyKey(r, request.IdempotencyKey), ExpectedVersion: request.ExpectedVersion,
		Confirmed: request.Confirmed,
	})
	if err == nil {
		response.JSON(w, http.StatusOK, result)
		return
	}
	switch {
	case errors.Is(err, proaccountsvc.ErrAccountNotFound):
		h.writeError(w, http.StatusNotFound, "pro_account_not_found", "统一账号不存在", false, nil)
	case errors.Is(err, proaccountreset.ErrInvalidRequest):
		h.writeError(w, http.StatusBadRequest, "reset_confirmation_required", "重置请求必须确认并携带操作标识", false, nil)
	case errors.Is(err, proaccountreset.ErrCapabilityUnavailable):
		h.writeError(w, http.StatusConflict, "reset_credits_unavailable", "该账号未确认支持 reset credits", false, result.Credits)
	case errors.Is(err, proaccountreset.ErrNoCredits):
		h.writeError(w, http.StatusConflict, "reset_credits_exhausted", "当前没有可用重置次数", false, result.Credits)
	case errors.Is(err, proaccountrepo.ErrVersionConflict):
		h.writeError(w, http.StatusConflict, "resource_version_conflict", "账号版本已变化", false, nil)
	case errors.Is(err, proaccountreset.ErrResetFailed):
		h.writeError(w, http.StatusBadGateway, "openai_reset_failed", "官方重置请求失败", true, nil)
	default:
		h.writeError(w, http.StatusInternalServerError, "openai_reset_failed", "OpenAI 重置失败", true, nil)
	}
}

func (h *Handler) handleCapabilities(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		h.writeError(w, http.StatusMethodNotAllowed, "method_not_allowed", "请求方法不受支持", false, nil)
		return
	}
	setup, _, ok, err := h.App.ManagerConfigService.ResolveSetupWithSource(r.Context())
	if err != nil || !ok {
		h.writeError(w, http.StatusPreconditionRequired, "cpa_connection_required", "尚未配置 Gateway 连接", false, nil)
		return
	}
	capabilities, err := h.App.ProAccountGateway.Capabilities(r.Context(), setup.CPAUpstreamURL, setup.ManagementKey)
	if err != nil {
		h.writeError(w, http.StatusBadGateway, "gateway_capabilities_failed", "无法读取 Gateway 能力", true, nil)
		return
	}
	response.JSON(w, http.StatusOK, map[string]any{
		"credentialDraft": capabilities.CredentialDraft,
		"allowedModels":   capabilities.AllowedModels,
		"stores":          map[string]string{"file": "supported", "object": "supported", "postgresql": "supported", "git": "supported"},
	})
}

func (h *Handler) handleOperations(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		h.writeError(w, http.StatusMethodNotAllowed, "method_not_allowed", "请求方法不受支持", false, nil)
		return
	}
	var request struct {
		OperationID       string         `json:"operation_id"`
		IdempotencyKey    string         `json:"idempotency_key"`
		OperationType     string         `json:"operation_type"`
		ProAccountID      string         `json:"pro_account_id"`
		CleanupDeadlineMS int64          `json:"cleanup_deadline_ms"`
		Context           map[string]any `json:"context"`
	}
	if err := decodeBody(w, r, &request); err != nil {
		h.writeError(w, http.StatusBadRequest, "invalid_request", "操作请求无效", false, nil)
		return
	}
	request.IdempotencyKey = idempotencyKey(r, request.IdempotencyKey)
	operation, created, err := h.App.ProAccountOperationService.Start(r.Context(), proaccountoperation.StartInput{
		OperationID: request.OperationID, IdempotencyKey: request.IdempotencyKey,
		OperationType: request.OperationType, ProAccountID: request.ProAccountID,
		CleanupDeadlineMS: request.CleanupDeadlineMS, Context: request.Context,
	})
	if err != nil {
		h.writeOperationError(w, err)
		return
	}
	status := http.StatusOK
	if created {
		status = http.StatusCreated
	}
	response.JSON(w, status, map[string]any{"operation": operation, "created": created})
}

func (h *Handler) handleOperation(w http.ResponseWriter, r *http.Request, operationID string) {
	if r.Method != http.MethodGet || strings.TrimSpace(operationID) == "" || strings.Contains(operationID, "/") {
		h.writeError(w, http.StatusNotFound, "operation_not_found", "账号操作不存在", false, nil)
		return
	}
	operation, err := h.App.ProAccountOperationService.Get(r.Context(), operationID)
	if err != nil {
		if errors.Is(err, proaccountoperation.ErrOperationNotFound) {
			h.writeError(w, http.StatusNotFound, "operation_not_found", "账号操作不存在", false, nil)
			return
		}
		h.writeError(w, http.StatusInternalServerError, "operation_read_failed", "无法读取账号操作", true, nil)
		return
	}
	response.JSON(w, http.StatusOK, map[string]any{"operation": operation})
}

func (h *Handler) handleProbe(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		h.writeError(w, http.StatusMethodNotAllowed, "method_not_allowed", "请求方法不受支持", false, nil)
		return
	}
	var request struct {
		OperationID    string            `json:"operation_id"`
		IdempotencyKey string            `json:"idempotency_key"`
		Platform       string            `json:"platform"`
		AuthType       string            `json:"auth_type"`
		BaseURL        string            `json:"base_url"`
		APIKey         string            `json:"api_key"`
		ProtocolMode   string            `json:"protocol_mode"`
		Model          string            `json:"model"`
		AllowedModels  []string          `json:"allowed_models"`
		ModelMapping   map[string]string `json:"model_mapping"`
		Headers        map[string]string `json:"headers"`
	}
	if err := decodeBody(w, r, &request); err != nil {
		h.writeError(w, http.StatusBadRequest, "invalid_request", "探测请求无效", false, nil)
		return
	}
	request.IdempotencyKey = idempotencyKey(r, request.IdempotencyKey)
	operation, created, err := h.App.ProAccountOperationService.Start(r.Context(), proaccountoperation.StartInput{
		OperationID: request.OperationID, IdempotencyKey: request.IdempotencyKey, OperationType: "add",
		Context: map[string]any{"platform": request.Platform, "protocolMode": request.ProtocolMode, "baseOrigin": safeOrigin(request.BaseURL)},
	})
	if err != nil {
		h.writeOperationError(w, err)
		return
	}
	if !created {
		response.JSON(w, http.StatusOK, map[string]any{"operation": operation, "replayed": true})
		return
	}
	result, err := h.App.ProAccountProbeService.ProbeCandidate(r.Context(), proaccountprobe.Input{
		Platform: request.Platform, AuthType: request.AuthType, BaseURL: request.BaseURL,
		APIKey: request.APIKey, ProtocolMode: request.ProtocolMode, Model: request.Model,
		AllowedModels: request.AllowedModels, ModelMapping: request.ModelMapping, Headers: request.Headers,
	})
	if err != nil {
		operation, _ = h.App.ProAccountOperationService.Transition(r.Context(), operation.OperationID, proaccountoperation.TransitionInput{
			ExpectedVersion: operation.Version, State: model.ProOperationStateFailed,
			ErrorCode: "candidate_probe_failed", ErrorSummary: "候选账号探测失败", Context: operation.Context,
		})
		h.writeError(w, http.StatusBadRequest, "candidate_probe_failed", "候选账号探测失败", false, map[string]any{"operation": operation})
		return
	}
	operation, err = h.App.ProAccountOperationService.Transition(r.Context(), operation.OperationID, proaccountoperation.TransitionInput{
		ExpectedVersion: operation.Version, State: model.ProOperationStateProbed, Context: operation.Context,
	})
	if err != nil {
		h.writeOperationError(w, err)
		return
	}
	response.JSON(w, http.StatusOK, map[string]any{"probe": result, "operation": operation})
}

func (h *Handler) handleOAuthStart(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		h.writeError(w, http.StatusMethodNotAllowed, "method_not_allowed", "请求方法不受支持", false, nil)
		return
	}
	var request struct {
		OperationID    string `json:"operation_id"`
		IdempotencyKey string `json:"idempotency_key"`
		Platform       string `json:"platform"`
	}
	if err := decodeBody(w, r, &request); err != nil {
		h.writeError(w, http.StatusBadRequest, "invalid_request", "OAuth 启动请求无效", false, nil)
		return
	}
	result, err := h.App.ProAccountLifecycleService.StartOAuth(r.Context(), proaccountlifecycle.OAuthStartInput{
		OperationID: request.OperationID, IdempotencyKey: idempotencyKey(r, request.IdempotencyKey), Platform: request.Platform,
	})
	if err != nil {
		h.writeLifecycleOAuthError(w, err, result)
		return
	}
	response.JSON(w, http.StatusCreated, result)
}

func (h *Handler) handleOAuthStatus(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		h.writeError(w, http.StatusMethodNotAllowed, "method_not_allowed", "请求方法不受支持", false, nil)
		return
	}
	result, err := h.App.ProAccountLifecycleService.OAuthStatus(r.Context(), r.URL.Query().Get("operation_id"))
	if err != nil {
		h.writeLifecycleOAuthError(w, err, result)
		return
	}
	response.JSON(w, http.StatusOK, result)
}

func (h *Handler) handleOAuthCancel(w http.ResponseWriter, r *http.Request) {
	h.handleDraftCancel(w, r)
}

func (h *Handler) handleDraftCancel(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		h.writeError(w, http.StatusMethodNotAllowed, "method_not_allowed", "请求方法不受支持", false, nil)
		return
	}
	var request struct {
		OperationID string `json:"operation_id"`
	}
	if err := decodeBody(w, r, &request); err != nil {
		h.writeError(w, http.StatusBadRequest, "invalid_request", "OAuth 取消请求无效", false, nil)
		return
	}
	result, err := h.App.ProAccountLifecycleService.CancelDraft(r.Context(), request.OperationID)
	if err != nil {
		h.writeLifecycleOAuthError(w, err, result)
		return
	}
	response.JSON(w, http.StatusOK, result)
}

func (h *Handler) handleComplete(w http.ResponseWriter, r *http.Request, accountID string) {
	if r.Method != http.MethodPost || !validPathID(accountID) {
		h.writeError(w, http.StatusNotFound, "pro_account_not_found", "统一账号不存在", false, nil)
		return
	}
	var request struct {
		OperationID     string            `json:"operation_id"`
		ExpectedVersion int64             `json:"expected_version"`
		AllowedModels   []string          `json:"allowed_models"`
		ModelMapping    map[string]string `json:"model_mapping"`
		TestModel       string            `json:"test_model"`
		SaveDisabled    bool              `json:"save_disabled_on_test_failure"`
	}
	if err := decodeBody(w, r, &request); err != nil {
		h.writeError(w, http.StatusBadRequest, "invalid_request", "草稿完成请求无效", false, nil)
		return
	}
	result, err := h.App.ProAccountLifecycleService.CompleteDraft(r.Context(), proaccountlifecycle.CompleteDraftInput{
		OperationID: request.OperationID, AccountID: accountID, ExpectedVersion: request.ExpectedVersion,
		AllowedModels: request.AllowedModels, ModelMapping: request.ModelMapping,
		TestModel: request.TestModel, SaveDisabled: request.SaveDisabled,
	})
	if err != nil {
		h.writeLifecycleError(w, err, result)
		return
	}
	response.JSON(w, http.StatusOK, result)
}

func (h *Handler) handleModels(w http.ResponseWriter, r *http.Request, accountID string) {
	if !validPathID(accountID) {
		h.writeError(w, http.StatusNotFound, "pro_account_not_found", "统一账号不存在", false, nil)
		return
	}
	if r.Method == http.MethodGet {
		result, err := h.App.ProAccountModelCatalogService.SyncAccount(r.Context(), accountID)
		if err != nil {
			switch {
			case errors.Is(err, proaccountsvc.ErrAccountNotFound):
				h.writeError(w, http.StatusNotFound, "pro_account_not_found", "统一账号不存在", false, nil)
			case errors.Is(err, proaccountmodelcatalog.ErrAccountBindingMissing):
				h.writeError(w, http.StatusConflict, "account_binding_missing", "账号缺少当前底层绑定", false, nil)
			default:
				h.writeError(w, http.StatusBadGateway, "model_catalog_unavailable", "模型目录同步失败", true, nil)
			}
			return
		}
		response.JSON(w, http.StatusOK, result)
		return
	}
	if r.Method != http.MethodPut {
		h.writeError(w, http.StatusMethodNotAllowed, "method_not_allowed", "请求方法不受支持", false, nil)
		return
	}
	var request struct {
		OperationID     string            `json:"operation_id"`
		IdempotencyKey  string            `json:"idempotency_key"`
		ExpectedVersion int64             `json:"expected_version"`
		AllowedModels   []string          `json:"allowed_models"`
		ModelMapping    map[string]string `json:"model_mapping"`
	}
	if err := decodeBody(w, r, &request); err != nil {
		h.writeError(w, http.StatusBadRequest, "invalid_request", "模型规则请求无效", false, nil)
		return
	}
	result, err := h.App.ProAccountModelsService.Update(r.Context(), proaccountmodels.Input{
		AccountID: accountID, OperationID: request.OperationID,
		IdempotencyKey: idempotencyKey(r, request.IdempotencyKey), ExpectedVersion: request.ExpectedVersion,
		AllowedModels: request.AllowedModels, ModelMapping: request.ModelMapping,
	})
	if err != nil {
		h.writeModelsError(w, err, result)
		return
	}
	response.JSON(w, http.StatusOK, result)
}

func (h *Handler) handleTest(w http.ResponseWriter, r *http.Request, accountID string) {
	if r.Method != http.MethodPost || !validPathID(accountID) {
		h.writeError(w, http.StatusNotFound, "pro_account_not_found", "统一账号不存在", false, nil)
		return
	}
	var request struct {
		OperationID     string `json:"operation_id"`
		IdempotencyKey  string `json:"idempotency_key"`
		ExpectedVersion int64  `json:"expected_version"`
		Model           string `json:"model"`
	}
	if err := decodeBody(w, r, &request); err != nil {
		h.writeError(w, http.StatusBadRequest, "invalid_request", "连通性测试请求无效", false, nil)
		return
	}
	result, err := h.App.ProAccountTestService.Test(r.Context(), proaccounttest.Input{
		AccountID: accountID, OperationID: request.OperationID,
		IdempotencyKey: idempotencyKey(r, request.IdempotencyKey), ExpectedVersion: request.ExpectedVersion,
		Model: request.Model,
	})
	if err != nil {
		h.writeTestError(w, err, result)
		return
	}
	response.JSON(w, http.StatusOK, result)
}

func (h *Handler) handleUsage(w http.ResponseWriter, r *http.Request, id string) {
	if r.Method != http.MethodGet {
		h.writeError(w, http.StatusMethodNotAllowed, "method_not_allowed", "method not allowed", false, nil)
		return
	}
	id = strings.TrimSpace(id)
	if id == "" || strings.Contains(id, "/") {
		h.writeError(w, http.StatusNotFound, "pro_account_not_found", "Pro account not found", false, nil)
		return
	}
	source := strings.ToLower(strings.TrimSpace(r.URL.Query().Get("source")))
	if source == "" {
		source = "passive"
	}
	if source != "passive" && source != "active" {
		h.writeError(w, http.StatusBadRequest, "invalid_usage_source", "source must be passive or active", false, nil)
		return
	}
	force := false
	if raw := strings.TrimSpace(r.URL.Query().Get("force")); raw != "" {
		value, err := strconv.ParseBool(raw)
		if err != nil {
			h.writeError(w, http.StatusBadRequest, "invalid_force", "force must be true or false", false, nil)
			return
		}
		force = value
	}
	result, err := h.App.ProAccountUsageService.Get(r.Context(), id, source, force)
	if err != nil {
		if errors.Is(err, proaccountsvc.ErrAccountNotFound) {
			h.writeError(w, http.StatusNotFound, "pro_account_not_found", err.Error(), false, nil)
			return
		}
		h.writeError(w, http.StatusInternalServerError, "pro_account_usage_failed", err.Error(), true, nil)
		return
	}
	response.JSON(w, http.StatusOK, result)
}

func (h *Handler) handleCollection(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodPost {
		h.handleCreateAPI(w, r)
		return
	}
	if r.Method != http.MethodGet {
		h.writeError(w, http.StatusMethodNotAllowed, "method_not_allowed", "method not allowed", false, nil)
		return
	}
	filter, err := parseListFilter(r)
	if err != nil {
		h.writeError(w, http.StatusBadRequest, "invalid_filter", err.Error(), false, nil)
		return
	}
	result, err := h.App.ProAccountService.List(r.Context(), filter)
	if err != nil {
		h.writeError(w, http.StatusInternalServerError, "pro_account_list_failed", err.Error(), true, nil)
		return
	}
	response.JSON(w, http.StatusOK, result)
}

func parseListFilter(r *http.Request) (model.ProAccountListFilter, error) {
	query := r.URL.Query()
	filter := model.ProAccountListFilter{
		Cursor: query.Get("cursor"), Search: query.Get("search"), Platform: query.Get("platform"),
		AuthType: query.Get("auth_type"), HealthStatus: query.Get("health_status"), Limit: 50,
	}
	if raw := strings.TrimSpace(query.Get("limit")); raw != "" {
		value, err := strconv.Atoi(raw)
		if err != nil || value <= 0 || value > 200 {
			return filter, errors.New("limit must be between 1 and 200")
		}
		filter.Limit = value
	}
	if raw := strings.TrimSpace(query.Get("enabled")); raw != "" {
		value, err := strconv.ParseBool(raw)
		if err != nil {
			return filter, errors.New("enabled must be true or false")
		}
		filter.Enabled = &value
	}
	return filter, nil
}

func (h *Handler) handleItem(w http.ResponseWriter, r *http.Request, id string) {
	id = strings.TrimSpace(id)
	if id == "" || strings.Contains(id, "/") {
		h.writeError(w, http.StatusNotFound, "pro_account_not_found", "Pro account not found", false, nil)
		return
	}
	switch r.Method {
	case http.MethodGet:
		h.handleGetItem(w, r, id)
	case http.MethodPut:
		h.handleUpdateItem(w, r, id)
	case http.MethodDelete:
		h.handleDeleteItem(w, r, id)
	default:
		h.writeError(w, http.StatusMethodNotAllowed, "method_not_allowed", "请求方法不受支持", false, nil)
	}
}

func (h *Handler) handleGetItem(w http.ResponseWriter, r *http.Request, id string) {
	item, editable, err := h.App.ProAccountLifecycleService.Details(r.Context(), id)
	if err != nil {
		if errors.Is(err, proaccountsvc.ErrAccountNotFound) {
			h.writeError(w, http.StatusNotFound, "pro_account_not_found", err.Error(), false, nil)
			return
		}
		h.writeError(w, http.StatusInternalServerError, "pro_account_get_failed", err.Error(), true, nil)
		return
	}
	response.JSON(w, http.StatusOK, map[string]any{"item": item, "editable": editable})
}

func (h *Handler) handleCreateAPI(w http.ResponseWriter, r *http.Request) {
	var request struct {
		OperationID    string            `json:"operation_id"`
		IdempotencyKey string            `json:"idempotency_key"`
		Platform       string            `json:"platform"`
		AuthType       string            `json:"auth_type"`
		Name           string            `json:"name"`
		BaseURL        string            `json:"base_url"`
		APIKey         string            `json:"api_key"`
		ProtocolMode   string            `json:"protocol_mode"`
		Headers        map[string]string `json:"headers"`
		AllowedModels  []string          `json:"allowed_models"`
		ModelMapping   map[string]string `json:"model_mapping"`
		TestModel      string            `json:"test_model"`
		SaveDisabled   bool              `json:"save_disabled_on_test_failure"`
	}
	if err := decodeBody(w, r, &request); err != nil || (request.AuthType != "" && request.AuthType != "api") {
		h.writeError(w, http.StatusBadRequest, "invalid_request", "API 账号添加请求无效", false, nil)
		return
	}
	result, err := h.App.ProAccountLifecycleService.CreateAPI(r.Context(), proaccountlifecycle.CreateAPIInput{
		OperationID: request.OperationID, IdempotencyKey: idempotencyKey(r, request.IdempotencyKey),
		Platform: request.Platform, Name: request.Name, BaseURL: request.BaseURL, APIKey: request.APIKey,
		ProtocolMode: request.ProtocolMode, Headers: request.Headers,
		AllowedModels: request.AllowedModels, ModelMapping: request.ModelMapping,
		TestModel: request.TestModel, SaveDisabled: request.SaveDisabled,
	})
	if err != nil {
		h.writeLifecycleError(w, err, result)
		return
	}
	response.JSON(w, http.StatusCreated, result)
}

func (h *Handler) handleCreateVertex(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		h.writeError(w, http.StatusMethodNotAllowed, "method_not_allowed", "请求方法不受支持", false, nil)
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 3*1024*1024)
	if err := r.ParseMultipartForm(3 * 1024 * 1024); err != nil {
		h.writeError(w, http.StatusBadRequest, "invalid_request", "Vertex 上传请求无效", false, nil)
		return
	}
	file, header, err := r.FormFile("file")
	if err != nil {
		h.writeError(w, http.StatusBadRequest, "vertex_file_required", "请选择 Service Account JSON 文件", false, nil)
		return
	}
	defer file.Close()
	raw, err := io.ReadAll(io.LimitReader(file, 2*1024*1024+1))
	if err != nil || len(raw) == 0 || len(raw) > 2*1024*1024 {
		h.writeError(w, http.StatusBadRequest, "invalid_vertex_file", "Service Account 文件无效或超过 2 MiB", false, nil)
		return
	}
	allowedModels := []string{}
	modelMapping := map[string]string{}
	if value := strings.TrimSpace(r.FormValue("allowed_models")); value != "" {
		if json.Unmarshal([]byte(value), &allowedModels) != nil {
			h.writeError(w, http.StatusBadRequest, "invalid_model_rules", "允许模型格式无效", false, nil)
			return
		}
	}
	if value := strings.TrimSpace(r.FormValue("model_mapping")); value != "" {
		if json.Unmarshal([]byte(value), &modelMapping) != nil {
			h.writeError(w, http.StatusBadRequest, "invalid_model_rules", "模型映射格式无效", false, nil)
			return
		}
	}
	saveDisabled, _ := strconv.ParseBool(strings.TrimSpace(r.FormValue("save_disabled_on_test_failure")))
	draftOnly, _ := strconv.ParseBool(strings.TrimSpace(r.FormValue("draft_only")))
	result, err := h.App.ProAccountLifecycleService.CreateVertex(r.Context(), proaccountlifecycle.CreateVertexInput{
		OperationID: r.FormValue("operation_id"), IdempotencyKey: idempotencyKey(r, r.FormValue("idempotency_key")),
		FileName: header.Filename, ServiceAccount: raw, Location: r.FormValue("location"),
		AllowedModels: allowedModels, ModelMapping: modelMapping,
		TestModel: r.FormValue("test_model"), SaveDisabled: saveDisabled, DraftOnly: draftOnly,
	})
	if err != nil {
		h.writeLifecycleError(w, err, result)
		return
	}
	response.JSON(w, http.StatusCreated, result)
}

func (h *Handler) handleUpdateItem(w http.ResponseWriter, r *http.Request, accountID string) {
	var request struct {
		OperationID     string             `json:"operation_id"`
		IdempotencyKey  string             `json:"idempotency_key"`
		ExpectedVersion int64              `json:"expected_version"`
		Enabled         *bool              `json:"enabled"`
		Name            *string            `json:"name"`
		BaseURL         *string            `json:"base_url"`
		APIKey          string             `json:"api_key"`
		ProtocolMode    string             `json:"protocol_mode"`
		Headers         *map[string]string `json:"headers"`
		AllowedModels   []string           `json:"allowed_models"`
		ModelMapping    map[string]string  `json:"model_mapping"`
		TestModel       string             `json:"test_model"`
	}
	if err := decodeBody(w, r, &request); err != nil {
		h.writeError(w, http.StatusBadRequest, "invalid_request", "账号更新请求无效", false, nil)
		return
	}
	mutation := proaccountlifecycle.MutationInput{
		AccountID: accountID, OperationID: request.OperationID,
		IdempotencyKey: idempotencyKey(r, request.IdempotencyKey), ExpectedVersion: request.ExpectedVersion,
	}
	var result proaccountlifecycle.Result
	var err error
	if request.Enabled != nil {
		result, err = h.App.ProAccountLifecycleService.SetEnabled(r.Context(), mutation, *request.Enabled)
	} else {
		result, err = h.App.ProAccountLifecycleService.Update(r.Context(), proaccountlifecycle.UpdateInput{
			MutationInput: mutation, Name: request.Name, BaseURL: request.BaseURL, APIKey: request.APIKey,
			ProtocolMode: request.ProtocolMode, Headers: request.Headers,
			AllowedModels: request.AllowedModels, ModelMapping: request.ModelMapping, TestModel: request.TestModel,
		})
	}
	if err != nil {
		h.writeLifecycleError(w, err, result)
		return
	}
	response.JSON(w, http.StatusOK, result)
}

func (h *Handler) handleDeleteItem(w http.ResponseWriter, r *http.Request, accountID string) {
	var request struct {
		OperationID     string `json:"operation_id"`
		IdempotencyKey  string `json:"idempotency_key"`
		ExpectedVersion int64  `json:"expected_version"`
	}
	if err := decodeBody(w, r, &request); err != nil {
		h.writeError(w, http.StatusBadRequest, "invalid_request", "账号删除请求无效", false, nil)
		return
	}
	result, err := h.App.ProAccountLifecycleService.Delete(r.Context(), proaccountlifecycle.MutationInput{
		AccountID: accountID, OperationID: request.OperationID,
		IdempotencyKey: idempotencyKey(r, request.IdempotencyKey), ExpectedVersion: request.ExpectedVersion,
	})
	if err != nil {
		h.writeLifecycleError(w, err, result)
		return
	}
	response.JSON(w, http.StatusOK, result)
}

func (h *Handler) handleSync(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		h.writeError(w, http.StatusMethodNotAllowed, "method_not_allowed", "method not allowed", false, nil)
		return
	}
	var request struct {
		DryRun bool `json:"dry_run"`
	}
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1024*1024))
	if err := decoder.Decode(&request); err != nil && !errors.Is(err, io.EOF) {
		h.writeError(w, http.StatusBadRequest, "invalid_request", "invalid sync request", false, nil)
		return
	}
	result, err := h.App.ProAccountService.Sync(r.Context(), request.DryRun)
	if err != nil {
		status := http.StatusBadGateway
		code := "pro_account_sync_failed"
		if strings.Contains(err.Error(), "usage service is not configured") {
			status = http.StatusPreconditionRequired
			code = "cpa_connection_required"
		}
		h.writeError(w, status, code, err.Error(), true, nil)
		return
	}
	response.JSON(w, http.StatusOK, result)
}

func (h *Handler) writeError(w http.ResponseWriter, status int, code string, message string, retryable bool, details any) {
	payload := map[string]any{"code": code, "message": message, "retryable": retryable}
	if details != nil {
		payload["details"] = details
	}
	response.JSON(w, status, payload)
}

func (h *Handler) writeOperationError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, proaccountdraft.ErrIdempotencyConflict), errors.Is(err, proaccountdraft.ErrOperationIDConflict):
		h.writeError(w, http.StatusConflict, "operation_conflict", "操作 ID 或幂等键发生冲突", false, nil)
	case errors.Is(err, proaccountoperation.ErrSensitiveOperationData):
		h.writeError(w, http.StatusBadRequest, "sensitive_operation_context", "操作上下文不得包含凭证或鉴权信息", false, nil)
	case errors.Is(err, proaccountoperation.ErrInvalidOperation):
		h.writeError(w, http.StatusBadRequest, "invalid_operation", "账号操作参数无效", false, nil)
	case errors.Is(err, proaccountoperation.ErrOperationVersionConflict):
		h.writeError(w, http.StatusConflict, "operation_version_conflict", "操作状态已被其他请求更新", true, nil)
	default:
		h.writeError(w, http.StatusInternalServerError, "operation_failed", "账号操作处理失败", true, nil)
	}
}

func (h *Handler) writeModelsError(w http.ResponseWriter, err error, result proaccountmodels.Result) {
	details := map[string]any{"operation": result.Operation}
	switch {
	case errors.Is(err, proaccountsvc.ErrAccountNotFound), errors.Is(err, proaccountrepo.ErrAccountNotFound):
		h.writeError(w, http.StatusNotFound, "pro_account_not_found", "统一账号不存在", false, details)
	case errors.Is(err, proaccountmodels.ErrResourceVersionConflict):
		h.writeError(w, http.StatusConflict, "version_conflict", "账号版本已变化，请刷新后重试", true, details)
	case errors.Is(err, proaccountgateway.ErrInvalidModelRule):
		h.writeError(w, http.StatusBadRequest, "invalid_model_rules", "模型白名单或映射规则无效", false, details)
	case errors.Is(err, proaccountmodels.ErrAccountBindingMissing):
		h.writeError(w, http.StatusConflict, "binding_missing", "账号缺少当前底层绑定", false, details)
	case errors.Is(err, proaccountmodels.ErrOperationInProgress):
		h.writeError(w, http.StatusConflict, "operation_in_progress", "相同幂等操作仍在处理中", true, details)
	case errors.Is(err, proaccountmodels.ErrOperationAlreadyFailed):
		h.writeError(w, http.StatusConflict, "operation_terminal", "相同幂等操作已经结束", false, details)
	case errors.Is(err, proaccountdraft.ErrIdempotencyConflict), errors.Is(err, proaccountdraft.ErrOperationIDConflict):
		h.writeError(w, http.StatusConflict, "operation_conflict", "操作 ID 或幂等键发生冲突", false, details)
	case errors.Is(err, proaccountgateway.ErrRuleReadbackMismatch):
		h.writeError(w, http.StatusBadGateway, "model_rule_readback_mismatch", "Gateway 模型规则回读不一致，已尝试恢复旧规则", true, details)
	default:
		h.writeError(w, http.StatusBadGateway, "model_rule_update_failed", "模型规则更新失败，旧规则保持可用或等待补偿", true, details)
	}
}

func (h *Handler) writeTestError(w http.ResponseWriter, err error, result proaccounttest.Result) {
	details := map[string]any{"operation": result.Operation}
	switch {
	case errors.Is(err, proaccountsvc.ErrAccountNotFound), errors.Is(err, proaccountrepo.ErrAccountNotFound):
		h.writeError(w, http.StatusNotFound, "pro_account_not_found", "统一账号不存在", false, details)
	case errors.Is(err, proaccounttest.ErrResourceVersionConflict):
		h.writeError(w, http.StatusConflict, "version_conflict", "账号版本已变化，请刷新后重试", true, details)
	case errors.Is(err, proaccounttest.ErrModelNotAllowed):
		h.writeError(w, http.StatusBadRequest, "model_not_allowed", "测试模型不在账号有效白名单内", false, details)
	case errors.Is(err, proaccounttest.ErrAccountBindingMissing):
		h.writeError(w, http.StatusConflict, "binding_missing", "账号缺少可测试的运行时绑定", false, details)
	case errors.Is(err, proaccountdraft.ErrIdempotencyConflict), errors.Is(err, proaccountdraft.ErrOperationIDConflict):
		h.writeError(w, http.StatusConflict, "operation_conflict", "操作 ID 或幂等键发生冲突", false, details)
	default:
		h.writeError(w, http.StatusBadGateway, "connectivity_test_failed", "账号连通性测试失败", true, details)
	}
}

func (h *Handler) writeLifecycleError(w http.ResponseWriter, err error, result proaccountlifecycle.Result) {
	details := map[string]any{"operation": result.Operation}
	switch {
	case errors.Is(err, proaccountsvc.ErrAccountNotFound), errors.Is(err, proaccountrepo.ErrAccountNotFound):
		h.writeError(w, http.StatusNotFound, "pro_account_not_found", "统一账号不存在", false, details)
	case errors.Is(err, proaccountlifecycle.ErrResourceVersionConflict):
		h.writeError(w, http.StatusConflict, "version_conflict", "账号版本已变化，请刷新后重试", true, details)
	case errors.Is(err, proaccountlifecycle.ErrUnsupportedAccountType):
		h.writeError(w, http.StatusBadRequest, "unsupported_account_type", "该平台不支持所选认证方式", false, details)
	case errors.Is(err, proaccountlifecycle.ErrGatewayCapability):
		h.writeError(w, http.StatusPreconditionFailed, "gateway_capability_required", "当前 Gateway 不具备安全添加账号所需能力", false, details)
	case errors.Is(err, proaccountlifecycle.ErrInvalidRequest), errors.Is(err, proaccountgateway.ErrInvalidModelRule):
		h.writeError(w, http.StatusBadRequest, "invalid_request", "账号生命周期请求无效", false, details)
	case errors.Is(err, proaccountlifecycle.ErrOperationState), errors.Is(err, proaccountlifecycle.ErrOperationConflict):
		h.writeError(w, http.StatusConflict, "operation_state_conflict", "账号操作状态不允许执行当前步骤", false, details)
	case errors.Is(err, proaccountdraft.ErrIdempotencyConflict), errors.Is(err, proaccountdraft.ErrOperationIDConflict):
		h.writeError(w, http.StatusConflict, "operation_conflict", "操作 ID 或幂等键发生冲突", false, details)
	case errors.Is(err, proaccountlifecycle.ErrConnectivityFailed):
		h.writeError(w, http.StatusUnprocessableEntity, "connectivity_test_failed", "账号连通性测试失败，未启用凭证", false, details)
	case errors.Is(err, proaccountgateway.ErrCredentialNotReady):
		h.writeError(w, http.StatusGatewayTimeout, "credential_not_ready", "Gateway 尚未完成凭证重载", true, details)
	default:
		h.writeError(w, http.StatusBadGateway, "account_lifecycle_failed", "账号生命周期操作失败", true, details)
	}
}

func (h *Handler) writeLifecycleOAuthError(w http.ResponseWriter, err error, result proaccountlifecycle.OAuthResult) {
	details := map[string]any{"operation": result.Operation}
	switch {
	case errors.Is(err, proaccountlifecycle.ErrUnsupportedAccountType), errors.Is(err, proaccountlifecycle.ErrInvalidRequest):
		h.writeError(w, http.StatusBadRequest, "invalid_oauth_request", "OAuth 请求无效", false, details)
	case errors.Is(err, proaccountlifecycle.ErrGatewayCapability):
		h.writeError(w, http.StatusPreconditionFailed, "oauth_draft_unsupported", "当前 Gateway 或 Token Store 不支持 OAuth 草稿首次持久化", false, details)
	case errors.Is(err, proaccountlifecycle.ErrOAuthCredentialAmbiguous):
		h.writeError(w, http.StatusConflict, "oauth_credential_ambiguous", "同时发现多个 OAuth 草稿，需人工确认", false, details)
	case errors.Is(err, proaccountlifecycle.ErrOAuthCredentialMissing):
		h.writeError(w, http.StatusNotFound, "oauth_credential_missing", "未找到本次 OAuth 授权保存的草稿凭证", true, details)
	case errors.Is(err, proaccountlifecycle.ErrOperationState), errors.Is(err, proaccountlifecycle.ErrOperationConflict):
		h.writeError(w, http.StatusConflict, "operation_state_conflict", "OAuth 操作状态不允许执行当前步骤", false, details)
	case errors.Is(err, proaccountoperation.ErrOperationNotFound):
		h.writeError(w, http.StatusNotFound, "operation_not_found", "账号操作不存在", false, details)
	case errors.Is(err, proaccountdraft.ErrIdempotencyConflict), errors.Is(err, proaccountdraft.ErrOperationIDConflict):
		h.writeError(w, http.StatusConflict, "operation_conflict", "操作 ID 或幂等键发生冲突", false, details)
	default:
		h.writeError(w, http.StatusBadGateway, "oauth_lifecycle_failed", "OAuth 生命周期操作失败", true, details)
	}
}

func decodeBody(w http.ResponseWriter, r *http.Request, target any) error {
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1024*1024))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return err
	}
	var extra any
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		return errors.New("request body must contain one JSON value")
	}
	return nil
}

func idempotencyKey(r *http.Request, bodyValue string) string {
	if value := strings.TrimSpace(r.Header.Get("Idempotency-Key")); value != "" {
		return value
	}
	return strings.TrimSpace(bodyValue)
}

func safeOrigin(raw string) string {
	parsed, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || parsed.Scheme == "" || parsed.Host == "" {
		return ""
	}
	return strings.ToLower(parsed.Scheme) + "://" + strings.ToLower(parsed.Host)
}

func validPathID(value string) bool {
	value = strings.TrimSpace(value)
	return value != "" && !strings.Contains(value, "/")
}
