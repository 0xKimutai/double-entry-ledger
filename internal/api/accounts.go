package api

import (
	"net/http"
	"strconv"

	"github.com/0xKimutai/double-entry-ledger/internal/ledger"
	"github.com/go-chi/chi/v5"
)

type createAccountRequest struct {
	Name     string `json:"name"`
	Currency string `json:"currency"`
}

func (h *handler) createAccount(w http.ResponseWriter, r *http.Request) {
	var request createAccountRequest
	if err := decodeJSON(w, r, &request); err != nil {
		h.writeError(w, r, ledger.ErrInvalidInput)
		return
	}
	account, err := h.ledger.CreateAccount(r.Context(), request.Name, request.Currency)
	if err != nil {
		h.writeError(w, r, err)
		return
	}
	h.writeJSON(w, http.StatusCreated, account)
}

func (h *handler) getAccount(w http.ResponseWriter, r *http.Request) {
	account, err := h.ledger.GetAccount(r.Context(), chi.URLParam(r, "accountID"))
	if err != nil {
		h.writeError(w, r, err)
		return
	}
	h.writeJSON(w, http.StatusOK, account)
}

func (h *handler) listEntries(w http.ResponseWriter, r *http.Request) {
	limit := 50
	offset := 0
	var err error
	if value := r.URL.Query().Get("limit"); value != "" {
		limit, err = strconv.Atoi(value)
		if err != nil {
			h.writeError(w, r, ledger.ErrInvalidInput)
			return
		}
	}
	if value := r.URL.Query().Get("offset"); value != "" {
		offset, err = strconv.Atoi(value)
		if err != nil {
			h.writeError(w, r, ledger.ErrInvalidInput)
			return
		}
	}
	entries, err := h.ledger.ListEntries(r.Context(), chi.URLParam(r, "accountID"), ledger.Page{
		Limit:  limit,
		Offset: offset,
	})
	if err != nil {
		h.writeError(w, r, err)
		return
	}
	h.writeJSON(w, http.StatusOK, entries)
}

type depositRequest struct {
	AmountMinor int64 `json:"amount_minor"`
}

func (h *handler) deposit(w http.ResponseWriter, r *http.Request) {
	var request depositRequest
	if err := decodeJSON(w, r, &request); err != nil {
		h.writeError(w, r, ledger.ErrInvalidInput)
		return
	}
	transfer, replayed, err := h.ledger.Deposit(
		r.Context(),
		chi.URLParam(r, "accountID"),
		r.Header.Get("Idempotency-Key"),
		request.AmountMinor,
	)
	if err != nil {
		h.writeError(w, r, err)
		return
	}
	status := http.StatusCreated
	if replayed {
		status = http.StatusOK
	}
	h.writeJSON(w, status, transfer)
}
