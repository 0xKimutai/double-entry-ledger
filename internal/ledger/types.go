package ledger

import (
	"context"
	"time"
)

type Account struct {
	ID           string    `json:"id"`
	Name         string    `json:"name"`
	Currency     string    `json:"currency"`
	BalanceMinor int64     `json:"balance_minor"`
	CreatedAt    time.Time `json:"created_at"`
}

type Entry struct {
	ID          string    `json:"id"`
	TransferID  string    `json:"transfer_id"`
	AmountMinor int64     `json:"amount_minor"`
	CreatedAt   time.Time `json:"created_at"`
}

type Transfer struct {
	ID                   string    `json:"id"`
	Kind                 string    `json:"kind"`
	SourceAccountID      string    `json:"source_account_id"`
	DestinationAccountID string    `json:"destination_account_id"`
	AmountMinor          int64     `json:"amount_minor"`
	Currency             string    `json:"currency"`
	CreatedAt            time.Time `json:"created_at"`
}

type Page struct {
	Limit  int
	Offset int
}

type Repository interface {
	CreateAccount(ctx context.Context, name, currency string) (Account, error)
	GetAccount(ctx context.Context, id string) (Account, error)
	ListEntries(ctx context.Context, accountID string, page Page) ([]Entry, error)
	Deposit(ctx context.Context, accountID, idempotencyKey string, amountMinor int64) (Transfer, bool, error)
	Transfer(ctx context.Context, sourceAccountID, destinationAccountID, idempotencyKey string, amountMinor int64) (Transfer, bool, error)
}
