package api

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"runtime"
	"time"

	"github.com/0xKimutai/double-entry-ledger/internal/health"
	"github.com/0xKimutai/double-entry-ledger/internal/ledger"
	"go.uber.org/zap"
)

const maxRequestBody = 1 << 20
const readinessTimeout = 2 * time.Second

type errorResponse struct {
	Error apiError `json:"error"`
}

type apiError struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

func decodeJSON(w http.ResponseWriter, r *http.Request, destination any) error {
	r.Body = http.MaxBytesReader(w, r.Body, maxRequestBody)
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(destination); err != nil {
		return err
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return errors.New("request body must contain one JSON value")
	}
	return nil
}

func (h *handler) writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(value); err != nil {
		h.logger.Error("encode HTTP response", zap.Error(err))
	}
}

func (h *handler) writeError(w http.ResponseWriter, r *http.Request, err error) {
	switch {
	case errors.Is(err, ledger.ErrInvalidInput):
		h.writeJSON(w, http.StatusBadRequest, errorResponse{Error: apiError{Code: "invalid_request", Message: err.Error()}})
	case errors.Is(err, ledger.ErrNotFound):
		h.writeJSON(w, http.StatusNotFound, errorResponse{Error: apiError{Code: "not_found", Message: err.Error()}})
	case errors.Is(err, ledger.ErrInsufficientFunds),
		errors.Is(err, ledger.ErrCurrencyMismatch),
		errors.Is(err, ledger.ErrIdempotencyConflict):
		h.writeJSON(w, http.StatusConflict, errorResponse{Error: apiError{Code: "conflict", Message: err.Error()}})
	case errors.Is(err, ledger.ErrBalanceOverflow):
		h.writeJSON(w, http.StatusUnprocessableEntity, errorResponse{Error: apiError{Code: "balance_overflow", Message: err.Error()}})
	default:
		h.logger.Error("handle API request", zap.Error(err), zap.String("path", r.URL.Path))
		h.writeJSON(w, http.StatusInternalServerError, errorResponse{Error: apiError{Code: "internal_error", Message: "an internal error occurred"}})
	}
}

type healthResponse struct {
	Service       string                      `json:"service"`
	GoVersion     string                      `json:"go_version"`
	Status        string                      `json:"status"`
	CheckedAt     time.Time                   `json:"checked_at"`
	UptimeSeconds float64                     `json:"uptime_seconds"`
	Components    map[string]health.Component `json:"components"`
}

func (h *handler) healthReport(components map[string]health.Component) healthResponse {
	checkedAt := time.Now().UTC()
	report := health.NewReport(components, h.startedAt, checkedAt)
	return healthResponse{
		Service:       "double-entry-ledger",
		GoVersion:     runtime.Version(),
		Status:        report.Status,
		CheckedAt:     report.CheckedAt,
		UptimeSeconds: report.UptimeSeconds,
		Components:    report.Components,
	}
}

func (h *handler) health(w http.ResponseWriter, _ *http.Request) {
	response := h.healthReport(map[string]health.Component{
		"api": {Status: "healthy"},
	})
	h.writeJSON(w, http.StatusOK, response)
}

func (h *handler) ready(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), readinessTimeout)
	defer cancel()
	components := h.readiness.Check(ctx)
	components["api"] = health.Component{Status: "healthy"}
	response := h.healthReport(components)
	status := http.StatusOK
	if response.Status != "healthy" {
		status = http.StatusServiceUnavailable
		fields := make([]zap.Field, 0, len(components)*2)
		for name, component := range components {
			if component.Status == "healthy" {
				continue
			}
			fields = append(fields, zap.String(name+".status", component.Status))
			if component.Cause != nil {
				fields = append(fields, zap.NamedError(name+"_cause", component.Cause))
			}
		}
		h.logger.Warn("API is not ready", fields...)
	}
	h.writeJSON(w, status, response)
}
