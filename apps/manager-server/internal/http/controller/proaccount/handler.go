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
	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/service/proaccountgateway"
	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/service/proaccountmodels"
	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/service/proaccountoperation"
	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/service/proaccountprobe"
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
	case path == "/v0/pro/accounts/operations":
		h.handleOperations(w, r)
	case strings.HasPrefix(path, "/v0/pro/accounts/operations/"):
		h.handleOperation(w, r, strings.TrimPrefix(path, "/v0/pro/accounts/operations/"))
	case path == "/v0/pro/accounts/probe":
		h.handleProbe(w, r)
	case path == "/v0/pro/accounts/sync":
		h.handleSync(w, r)
	case strings.HasPrefix(path, "/v0/pro/accounts/") && strings.HasSuffix(path, "/usage"):
		h.handleUsage(w, r, strings.TrimSuffix(strings.TrimPrefix(path, "/v0/pro/accounts/"), "/usage"))
	case strings.HasPrefix(path, "/v0/pro/accounts/") && strings.HasSuffix(path, "/models"):
		h.handleModels(w, r, strings.TrimSuffix(strings.TrimPrefix(path, "/v0/pro/accounts/"), "/models"))
	case strings.HasPrefix(path, "/v0/pro/accounts/") && strings.HasSuffix(path, "/test"):
		h.handleTest(w, r, strings.TrimSuffix(strings.TrimPrefix(path, "/v0/pro/accounts/"), "/test"))
	case strings.HasPrefix(path, "/v0/pro/accounts/"):
		h.handleItem(w, r, strings.TrimPrefix(path, "/v0/pro/accounts/"))
	default:
		h.writeError(w, http.StatusNotFound, "pro_route_not_found", "Pro route not found", false, nil)
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

func (h *Handler) handleModels(w http.ResponseWriter, r *http.Request, accountID string) {
	if r.Method != http.MethodPut || !validPathID(accountID) {
		h.writeError(w, http.StatusNotFound, "pro_account_not_found", "统一账号不存在", false, nil)
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
	if r.Method != http.MethodGet {
		h.writeError(w, http.StatusMethodNotAllowed, "method_not_allowed", "method not allowed", false, nil)
		return
	}
	id = strings.TrimSpace(id)
	if id == "" || strings.Contains(id, "/") {
		h.writeError(w, http.StatusNotFound, "pro_account_not_found", "Pro account not found", false, nil)
		return
	}
	item, err := h.App.ProAccountService.Get(r.Context(), id)
	if err != nil {
		if errors.Is(err, proaccountsvc.ErrAccountNotFound) {
			h.writeError(w, http.StatusNotFound, "pro_account_not_found", err.Error(), false, nil)
			return
		}
		h.writeError(w, http.StatusInternalServerError, "pro_account_get_failed", err.Error(), true, nil)
		return
	}
	response.JSON(w, http.StatusOK, map[string]any{"item": item})
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
