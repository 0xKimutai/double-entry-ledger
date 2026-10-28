package ledger

import "errors"

var (
	ErrInvalidInput        = errors.New("invalid input")
	ErrNotFound            = errors.New("account not found")
	ErrInsufficientFunds   = errors.New("insufficient funds")
	ErrCurrencyMismatch    = errors.New("account currencies do not match")
	ErrBalanceOverflow     = errors.New("account balance would overflow")
	ErrIdempotencyConflict = errors.New("idempotency key was already used for a different request")
)
