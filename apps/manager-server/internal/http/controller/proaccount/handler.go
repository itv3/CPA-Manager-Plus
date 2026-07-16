package proaccount

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strconv"
	"strings"

	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/app"
	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/http/middleware"
	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/http/response"
	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/model"
	proaccountsvc "github.com/seakee/cpa-manager-plus/apps/manager-server/internal/service/proaccount"
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
	case path == "/v0/pro/accounts/sync":
		h.handleSync(w, r)
	case strings.HasPrefix(path, "/v0/pro/accounts/"):
		h.handleItem(w, r, strings.TrimPrefix(path, "/v0/pro/accounts/"))
	default:
		h.writeError(w, http.StatusNotFound, "pro_route_not_found", "Pro route not found", false, nil)
	}
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
