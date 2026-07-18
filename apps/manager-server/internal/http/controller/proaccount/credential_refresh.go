package proaccount

import (
	"net/http"

	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/http/response"
	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/service/proaccountlifecycle"
)

func (h *Handler) handleRefreshToken(w http.ResponseWriter, r *http.Request, accountID string) {
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
		h.writeError(w, http.StatusBadRequest, "invalid_request", "刷新令牌请求无效", false, nil)
		return
	}
	result, err := h.App.ProAccountLifecycleService.RefreshToken(r.Context(), proaccountlifecycle.MutationInput{
		AccountID:       accountID,
		OperationID:     request.OperationID,
		IdempotencyKey:  idempotencyKey(r, request.IdempotencyKey),
		ExpectedVersion: request.ExpectedVersion,
	})
	if err != nil {
		h.writeLifecycleError(w, err, result)
		return
	}
	response.JSON(w, http.StatusOK, result)
}
