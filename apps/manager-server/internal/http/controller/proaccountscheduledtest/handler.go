package proaccountscheduledtest

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strconv"
	"strings"

	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/http/middleware"
	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/http/response"
	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/model"
	proaccountsvc "github.com/seakee/cpa-manager-plus/apps/manager-server/internal/service/proaccount"
	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/service/proaccountgateway"
	proaccountscheduledtestsvc "github.com/seakee/cpa-manager-plus/apps/manager-server/internal/service/proaccountscheduledtest"
)

type Handler struct {
	service  *proaccountscheduledtestsvc.Service
	verifier middleware.PanelVerifier
}

type planResponse struct {
	ID             int64  `json:"id"`
	ProAccountID   string `json:"proAccountId"`
	ModelID        string `json:"modelId"`
	CronExpression string `json:"cronExpression"`
	Enabled        bool   `json:"enabled"`
	MaxResults     int    `json:"maxResults"`
	AutoRecover    bool   `json:"autoRecover"`
	LastRunAtMS    int64  `json:"lastRunAtMs,omitempty"`
	NextRunAtMS    int64  `json:"nextRunAtMs,omitempty"`
	CreatedAtMS    int64  `json:"createdAtMs"`
	UpdatedAtMS    int64  `json:"updatedAtMs"`
}

func New(service *proaccountscheduledtestsvc.Service, verifier middleware.PanelVerifier) *Handler {
	return &Handler{service: service, verifier: verifier}
}

func MatchesPath(path string) bool {
	parts := strings.Split(strings.Trim(strings.TrimRight(path, "/"), "/"), "/")
	return len(parts) >= 5 && parts[0] == "v0" && parts[1] == "pro" &&
		parts[2] == "accounts" && parts[4] == "scheduled-test-plans"
}

func (h *Handler) Handle(w http.ResponseWriter, r *http.Request) {
	if h == nil || h.service == nil || h.verifier == nil {
		writeError(w, http.StatusServiceUnavailable, "scheduled_test_service_unavailable", "定时测试服务不可用", true)
		return
	}
	if !middleware.AuthorizePanel(w, r, h.verifier) {
		return
	}
	if !MatchesPath(r.URL.Path) {
		writeError(w, http.StatusNotFound, "scheduled_test_route_not_found", "定时测试接口不存在", false)
		return
	}
	parts := strings.Split(strings.Trim(strings.TrimRight(r.URL.Path, "/"), "/"), "/")
	accountID := strings.TrimSpace(parts[3])
	if accountID == "" {
		writeError(w, http.StatusNotFound, "pro_account_not_found", "统一账号不存在", false)
		return
	}
	switch len(parts) {
	case 5:
		h.handleCollection(w, r, accountID)
	case 6:
		planID, ok := parsePlanID(parts[5])
		if !ok {
			writeError(w, http.StatusNotFound, "scheduled_test_plan_not_found", "定时测试计划不存在", false)
			return
		}
		h.handleItem(w, r, accountID, planID)
	case 7:
		planID, ok := parsePlanID(parts[5])
		if !ok {
			writeError(w, http.StatusNotFound, "scheduled_test_plan_not_found", "定时测试计划不存在", false)
			return
		}
		switch parts[6] {
		case "results":
			h.handleResults(w, r, accountID, planID)
		case "run":
			h.handleRun(w, r, accountID, planID)
		default:
			writeError(w, http.StatusNotFound, "scheduled_test_route_not_found", "定时测试接口不存在", false)
		}
	default:
		writeError(w, http.StatusNotFound, "scheduled_test_route_not_found", "定时测试接口不存在", false)
	}
}

func (h *Handler) handleCollection(w http.ResponseWriter, r *http.Request, accountID string) {
	switch r.Method {
	case http.MethodGet:
		items, err := h.service.ListByAccount(r.Context(), accountID)
		if err != nil {
			h.writeServiceError(w, err)
			return
		}
		response.JSON(w, http.StatusOK, map[string]any{"items": mapPlans(items)})
	case http.MethodPost:
		var request struct {
			OperationID     string `json:"operation_id"`
			IdempotencyKey  string `json:"idempotency_key"`
			ExpectedVersion int64  `json:"expected_version"`
			ModelID         string `json:"model_id"`
			CronExpression  string `json:"cron_expression"`
			Enabled         *bool  `json:"enabled"`
			MaxResults      int    `json:"max_results"`
			AutoRecover     *bool  `json:"auto_recover"`
		}
		if err := decodeBody(w, r, &request); err != nil {
			writeError(w, http.StatusBadRequest, "invalid_scheduled_test_request", "定时测试计划请求无效", false)
			return
		}
		enabled := true
		if request.Enabled != nil {
			enabled = *request.Enabled
		}
		autoRecover := false
		if request.AutoRecover != nil {
			autoRecover = *request.AutoRecover
		}
		item, err := h.service.Create(r.Context(), proaccountscheduledtestsvc.CreateInput{
			AccountID: accountID, Model: request.ModelID, CronExpression: request.CronExpression,
			Enabled: enabled, MaxResults: request.MaxResults, AutoRecover: autoRecover,
			ExpectedVersion: request.ExpectedVersion,
		})
		if err != nil {
			h.writeServiceError(w, err)
			return
		}
		response.JSON(w, http.StatusCreated, mapPlan(item))
	default:
		writeError(w, http.StatusMethodNotAllowed, "method_not_allowed", "请求方法不受支持", false)
	}
}

func (h *Handler) handleItem(w http.ResponseWriter, r *http.Request, accountID string, planID int64) {
	if _, err := h.ownedPlan(r, accountID, planID); err != nil {
		h.writeServiceError(w, err)
		return
	}
	switch r.Method {
	case http.MethodPut:
		var request struct {
			ModelID        *string `json:"model_id"`
			CronExpression *string `json:"cron_expression"`
			Enabled        *bool   `json:"enabled"`
			MaxResults     *int    `json:"max_results"`
			AutoRecover    *bool   `json:"auto_recover"`
		}
		if err := decodeBody(w, r, &request); err != nil {
			writeError(w, http.StatusBadRequest, "invalid_scheduled_test_request", "定时测试计划请求无效", false)
			return
		}
		item, err := h.service.Update(r.Context(), planID, proaccountscheduledtestsvc.UpdateInput{
			Model: request.ModelID, CronExpression: request.CronExpression, Enabled: request.Enabled,
			MaxResults: request.MaxResults, AutoRecover: request.AutoRecover,
		})
		if err != nil {
			h.writeServiceError(w, err)
			return
		}
		response.JSON(w, http.StatusOK, mapPlan(item))
	case http.MethodDelete:
		if err := h.service.Delete(r.Context(), planID); err != nil {
			h.writeServiceError(w, err)
			return
		}
		response.JSON(w, http.StatusOK, map[string]any{"deleted": true})
	default:
		writeError(w, http.StatusMethodNotAllowed, "method_not_allowed", "请求方法不受支持", false)
	}
}

func (h *Handler) handleResults(w http.ResponseWriter, r *http.Request, accountID string, planID int64) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "method_not_allowed", "请求方法不受支持", false)
		return
	}
	if _, err := h.ownedPlan(r, accountID, planID); err != nil {
		h.writeServiceError(w, err)
		return
	}
	limit := 50
	if raw := strings.TrimSpace(r.URL.Query().Get("limit")); raw != "" {
		parsed, err := strconv.Atoi(raw)
		if err != nil || parsed <= 0 || parsed > 500 {
			writeError(w, http.StatusBadRequest, "invalid_limit", "limit 必须在 1 到 500 之间", false)
			return
		}
		limit = parsed
	}
	items, err := h.service.ListResults(r.Context(), planID, limit)
	if err != nil {
		h.writeServiceError(w, err)
		return
	}
	response.JSON(w, http.StatusOK, map[string]any{"items": items})
}

func (h *Handler) handleRun(w http.ResponseWriter, r *http.Request, accountID string, planID int64) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "method_not_allowed", "请求方法不受支持", false)
		return
	}
	if _, err := h.ownedPlan(r, accountID, planID); err != nil {
		h.writeServiceError(w, err)
		return
	}
	item, err := h.service.RunNow(r.Context(), planID)
	if err != nil {
		h.writeServiceError(w, err)
		return
	}
	response.JSON(w, http.StatusOK, item)
}

func (h *Handler) ownedPlan(r *http.Request, accountID string, planID int64) (model.ProAccountScheduledTestPlan, error) {
	plan, err := h.service.Get(r.Context(), planID)
	if err != nil {
		return model.ProAccountScheduledTestPlan{}, err
	}
	if plan.AccountID != accountID {
		return model.ProAccountScheduledTestPlan{}, proaccountscheduledtestsvc.ErrPlanNotFound
	}
	return plan, nil
}

func mapPlans(items []model.ProAccountScheduledTestPlan) []planResponse {
	result := make([]planResponse, 0, len(items))
	for _, item := range items {
		result = append(result, mapPlan(item))
	}
	return result
}

func mapPlan(item model.ProAccountScheduledTestPlan) planResponse {
	return planResponse{
		ID: item.ID, ProAccountID: item.AccountID, ModelID: item.Model,
		CronExpression: item.CronExpression, Enabled: item.Enabled, MaxResults: item.MaxResults,
		AutoRecover: item.AutoRecover, LastRunAtMS: item.LastRunAtMS, NextRunAtMS: item.NextRunAtMS,
		CreatedAtMS: item.CreatedAtMS, UpdatedAtMS: item.UpdatedAtMS,
	}
}

func (h *Handler) writeServiceError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, proaccountsvc.ErrAccountNotFound):
		writeError(w, http.StatusNotFound, "pro_account_not_found", "统一账号不存在", false)
	case errors.Is(err, proaccountscheduledtestsvc.ErrPlanNotFound):
		writeError(w, http.StatusNotFound, "scheduled_test_plan_not_found", "定时测试计划不存在", false)
	case errors.Is(err, proaccountscheduledtestsvc.ErrVersionConflict):
		writeError(w, http.StatusConflict, "version_conflict", "账号版本已变化，请刷新后重试", true)
	case errors.Is(err, proaccountscheduledtestsvc.ErrInvalidPlan),
		errors.Is(err, proaccountscheduledtestsvc.ErrModelNotAllowed),
		errors.Is(err, proaccountgateway.ErrInvalidModelRule):
		writeError(w, http.StatusBadRequest, "invalid_scheduled_test_plan", err.Error(), false)
	default:
		writeError(w, http.StatusInternalServerError, "scheduled_test_operation_failed", "定时测试操作失败", true)
	}
}

func parsePlanID(raw string) (int64, bool) {
	id, err := strconv.ParseInt(strings.TrimSpace(raw), 10, 64)
	return id, err == nil && id > 0
}

func decodeBody(w http.ResponseWriter, r *http.Request, target any) error {
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1024*1024))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return err
	}
	var extra any
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		return errors.New("请求体只能包含一个 JSON 值")
	}
	return nil
}

func writeError(w http.ResponseWriter, status int, code string, message string, retryable bool) {
	response.JSON(w, status, map[string]any{
		"code": code, "message": message, "retryable": retryable,
	})
}
