package api

import (
	"net/http"

	"github.com/0xKimutai/double-entry-ledger/internal/ledger"
)

type transferRequest struct {
	SourceAccountID      string `json:"source_account_id"`
	DestinationAccountID string `json:"destination_account_id"`
	AmountMinor          int64  `json:"amount_minor"`
}

func (h *handler) transfer(w http.ResponseWriter, r *http.Request) {
	var request transferRequest
	if err := decodeJSON(w, r, &request); err != nil {
		h.writeError(w, r, ledger.ErrInvalidInput)
		return
	}
	transfer, replayed, err := h.ledger.Transfer(
		r.Context(),
		request.SourceAccountID,
		request.DestinationAccountID,
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
