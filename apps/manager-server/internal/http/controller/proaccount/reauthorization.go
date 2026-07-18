package proaccount

import (
	"net/http"
	"strings"

	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/http/response"
	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/service/proaccountlifecycle"
)

func (h *Handler) handleReauthorizationStart(w http.ResponseWriter, r *http.Request, accountID string) {
	if r.Method != http.MethodPost || !validPathID(accountID) {
		h.writeError(w, http.StatusNotFound, "pro_account_not_found", "统一账号不存在", false, nil)
		return
	}
	var request struct {
		OperationID     string `json:"operation_id"`
		IdempotencyKey  string `json:"idempotency_key"`
		ExpectedVersion int64  `json:"expected_version"`
	}
	if err := decodeBody(w, r, &request); err != nil {
		h.writeError(w, http.StatusBadRequest, "invalid_request", "重新授权请求无效", false, nil)
		return
	}
	result, err := h.App.ProAccountLifecycleService.StartReauthorization(r.Context(), proaccountlifecycle.ReauthorizationStartInput{
		MutationInput: proaccountlifecycle.MutationInput{
			AccountID: accountID, OperationID: request.OperationID,
			IdempotencyKey: idempotencyKey(r, request.IdempotencyKey), ExpectedVersion: request.ExpectedVersion,
		},
	})
	if err != nil {
		h.writeLifecycleOAuthError(w, err, result)
		return
	}
	response.JSON(w, http.StatusOK, result)
}

func (h *Handler) handleReauthorizationStatus(w http.ResponseWriter, r *http.Request, accountID string) {
	if r.Method != http.MethodGet || !validPathID(accountID) {
		h.writeError(w, http.StatusNotFound, "pro_account_not_found", "统一账号不存在", false, nil)
		return
	}
	operationID := strings.TrimSpace(r.URL.Query().Get("operation_id"))
	if operationID == "" {
		h.writeError(w, http.StatusBadRequest, "invalid_request", "operation_id 不能为空", false, nil)
		return
	}
	result, err := h.App.ProAccountLifecycleService.ReauthorizationStatus(r.Context(), accountID, operationID)
	if err != nil {
		h.writeLifecycleOAuthError(w, err, result)
		return
	}
	response.JSON(w, http.StatusOK, result)
}

func (h *Handler) handleReauthorizationCallback(w http.ResponseWriter, r *http.Request, accountID string) {
	if r.Method != http.MethodPost || !validPathID(accountID) {
		h.writeError(w, http.StatusNotFound, "pro_account_not_found", "统一账号不存在", false, nil)
		return
	}
	var request struct {
		OperationID   string `json:"operation_id"`
		CallbackInput string `json:"callback_input"`
		CallbackState string `json:"callback_state"`
	}
	if err := decodeBody(w, r, &request); err != nil {
		h.writeError(w, http.StatusBadRequest, "invalid_request", "OAuth 回调请求无效", false, nil)
		return
	}
	result, err := h.App.ProAccountLifecycleService.SubmitReauthorizationCallback(r.Context(), accountID, proaccountlifecycle.OAuthCallbackInput{
		OperationID: request.OperationID, CallbackText: request.CallbackInput, CallbackState: request.CallbackState,
	})
	if err != nil {
		h.writeLifecycleOAuthError(w, err, result)
		return
	}
	response.JSON(w, http.StatusOK, result)
}

func (h *Handler) handleReauthorizationCancel(w http.ResponseWriter, r *http.Request, accountID string) {
	if r.Method != http.MethodPost || !validPathID(accountID) {
		h.writeError(w, http.StatusNotFound, "pro_account_not_found", "统一账号不存在", false, nil)
		return
	}
	var request struct {
		OperationID string `json:"operation_id"`
	}
	if err := decodeBody(w, r, &request); err != nil || strings.TrimSpace(request.OperationID) == "" {
		h.writeError(w, http.StatusBadRequest, "invalid_request", "重新授权取消请求无效", false, nil)
		return
	}
	result, err := h.App.ProAccountLifecycleService.CancelReauthorization(r.Context(), accountID, request.OperationID)
	if err != nil {
		h.writeLifecycleOAuthError(w, err, result)
		return
	}
	response.JSON(w, http.StatusOK, result)
}
