package postgres

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"math"
	"strings"
	"time"

	"github.com/0xKimutai/double-entry-ledger/internal/ledger"
	"github.com/jackc/pgx/v5/pgconn"
)

type Repository struct {
	db *sql.DB
}

const (
	transactionMaxAttempts = 3
	transactionRetryDelay  = 10 * time.Millisecond
	transactionRetryMax    = 100 * time.Millisecond
)

func NewRepository(db *sql.DB) *Repository {
	return &Repository{db: db}
}

func (r *Repository) CreateAccount(ctx context.Context, name, currency string) (ledger.Account, error) {
	id, err := newID()
	if err != nil {
		return ledger.Account{}, fmt.Errorf("generate account ID: %w", err)
	}

	account, err := scanAccount(r.db.QueryRowContext(ctx, `
		INSERT INTO accounts (id, name, currency)
		VALUES ($1, $2, $3)
		RETURNING id::text, name, currency, balance_minor, created_at
	`, id, name, currency))
	if err != nil {
		return ledger.Account{}, fmt.Errorf("create account: %w", err)
	}
	return account, nil
}

func (r *Repository) GetAccount(ctx context.Context, id string) (ledger.Account, error) {
	account, err := scanAccount(r.db.QueryRowContext(ctx, `
		SELECT id::text, name, currency, balance_minor, created_at
		FROM accounts
		WHERE id = $1 AND NOT is_system
	`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return ledger.Account{}, ledger.ErrNotFound
	}
	if err != nil {
		return ledger.Account{}, fmt.Errorf("get account: %w", err)
	}
	return account, nil
}

func (r *Repository) ListEntries(ctx context.Context, accountID string, page ledger.Page) ([]ledger.Entry, error) {
	var exists bool
	if err := r.db.QueryRowContext(ctx, `
		SELECT EXISTS (SELECT 1 FROM accounts WHERE id = $1 AND NOT is_system)
	`, accountID).Scan(&exists); err != nil {
		return nil, fmt.Errorf("check account existence: %w", err)
	}
	if !exists {
		return nil, ledger.ErrNotFound
	}

	rows, err := r.db.QueryContext(ctx, `
		SELECT id::text, transfer_id::text, amount_minor, created_at
		FROM ledger_entries
		WHERE account_id = $1
		ORDER BY created_at DESC, id DESC
		LIMIT $2 OFFSET $3
	`, accountID, page.Limit, page.Offset)
	if err != nil {
		return nil, fmt.Errorf("list ledger entries: %w", err)
	}
	defer rows.Close()

	entries := make([]ledger.Entry, 0)
	for rows.Next() {
		var entry ledger.Entry
		if err := rows.Scan(&entry.ID, &entry.TransferID, &entry.AmountMinor, &entry.CreatedAt); err != nil {
			return nil, fmt.Errorf("scan ledger entry: %w", err)
		}
		entries = append(entries, entry)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate ledger entries: %w", err)
	}
	return entries, nil
}

func (r *Repository) Deposit(ctx context.Context, accountID, idempotencyKey string, amountMinor int64) (ledger.Transfer, bool, error) {
	return r.executeTransfer(ctx, "deposit", "", accountID, idempotencyKey, amountMinor)
}

func (r *Repository) Transfer(ctx context.Context, sourceAccountID, destinationAccountID, idempotencyKey string, amountMinor int64) (ledger.Transfer, bool, error) {
	return r.executeTransfer(ctx, "payment", sourceAccountID, destinationAccountID, idempotencyKey, amountMinor)
}

func (r *Repository) executeTransfer(ctx context.Context, kind, sourceID, destinationID, key string, amount int64) (ledger.Transfer, bool, error) {
	for attempt := 1; ; attempt++ {
		transfer, replayed, err := r.executeTransferOnce(ctx, kind, sourceID, destinationID, key, amount)
		if err == nil || !isRetryableTransactionError(err) {
			return transfer, replayed, err
		}
		if attempt == transactionMaxAttempts {
			return ledger.Transfer{}, false, fmt.Errorf("transfer transaction failed after %d attempts: %w", attempt, err)
		}
		if waitErr := waitForTransactionRetry(ctx, transactionRetryDelayFor(attempt)); waitErr != nil {
			return ledger.Transfer{}, false, errors.Join(err, fmt.Errorf("wait to retry transfer transaction: %w", waitErr))
		}
	}
}

func (r *Repository) executeTransferOnce(ctx context.Context, kind, sourceID, destinationID, key string, amount int64) (result ledger.Transfer, replayed bool, resultErr error) {
	// Posting, projection updates, and journal entries commit atomically.
	tx, err := r.db.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelReadCommitted})
	if err != nil {
		return ledger.Transfer{}, false, fmt.Errorf("begin transfer transaction: %w", err)
	}
	defer func() {
		if rollbackErr := tx.Rollback(); rollbackErr != nil && !errors.Is(rollbackErr, sql.ErrTxDone) {
			resultErr = errors.Join(resultErr, fmt.Errorf("rollback transfer transaction: %w", rollbackErr))
		}
	}()

	if kind == "deposit" {
		var currency string
		err := tx.QueryRowContext(ctx, `
			SELECT currency FROM accounts WHERE id = $1 AND NOT is_system
		`, destinationID).Scan(&currency)
		if errors.Is(err, sql.ErrNoRows) {
			return ledger.Transfer{}, false, ledger.ErrNotFound
		}
		if err != nil {
			return ledger.Transfer{}, false, fmt.Errorf("read deposit account: %w", err)
		}
		sourceID, err = ensureExternalAccount(ctx, tx, currency)
		if err != nil {
			return ledger.Transfer{}, false, err
		}
	}

	// Lock rows in ID order so overlapping transfers acquire locks consistently.
	accounts, err := lockAccounts(ctx, tx, sourceID, destinationID)
	if err != nil {
		return ledger.Transfer{}, false, err
	}
	source, sourceExists := accounts[sourceID]
	destination, destinationExists := accounts[destinationID]
	if !sourceExists || !destinationExists || (kind == "payment" && (source.IsSystem || destination.IsSystem)) ||
		(kind == "deposit" && (source.IsSystem == false || destination.IsSystem)) {
		return ledger.Transfer{}, false, ledger.ErrNotFound
	}
	if source.Currency != destination.Currency {
		return ledger.Transfer{}, false, ledger.ErrCurrencyMismatch
	}
	// Resolve retries before checking funds already changed by the original debit.
	existing, found, err := findTransfer(ctx, tx, key)
	if err != nil {
		return ledger.Transfer{}, false, err
	}
	if found {
		if rollbackErr := tx.Rollback(); rollbackErr != nil && !errors.Is(rollbackErr, sql.ErrTxDone) {
			return ledger.Transfer{}, false, fmt.Errorf("rollback idempotent retry: %w", rollbackErr)
		}
		return compareReplay(existing, kind, sourceID, destinationID, amount)
	}
	transferID, err := newID()
	if err != nil {
		return ledger.Transfer{}, false, fmt.Errorf("generate transfer ID: %w", err)
	}
	var transfer ledger.Transfer
	err = tx.QueryRowContext(ctx, `
		INSERT INTO transfers (
			id, kind, idempotency_key, source_account_id,
			destination_account_id, amount_minor, currency
		)
		VALUES ($1, $2, $3, $4, $5, $6, $7)
		ON CONFLICT (idempotency_key) DO NOTHING
		RETURNING id::text, kind, source_account_id::text,
			destination_account_id::text, amount_minor, currency, created_at
	`, transferID, kind, key, sourceID, destinationID, amount, source.Currency).Scan(
		&transfer.ID, &transfer.Kind, &transfer.SourceAccountID,
		&transfer.DestinationAccountID, &transfer.AmountMinor, &transfer.Currency,
		&transfer.CreatedAt,
	)
	if errors.Is(err, sql.ErrNoRows) {
		if rollbackErr := tx.Rollback(); rollbackErr != nil && !errors.Is(rollbackErr, sql.ErrTxDone) {
			return ledger.Transfer{}, false, fmt.Errorf("rollback duplicate transfer: %w", rollbackErr)
		}
		return r.idempotentReplay(ctx, key, kind, sourceID, destinationID, amount)
	}
	if err != nil {
		return ledger.Transfer{}, false, translatePostgresError(err)
	}

	if kind == "payment" && source.BalanceMinor < amount {
		return ledger.Transfer{}, false, ledger.ErrInsufficientFunds
	}
	if destination.BalanceMinor > math.MaxInt64-amount {
		return ledger.Transfer{}, false, ledger.ErrBalanceOverflow
	}
	if source.IsSystem && source.BalanceMinor < math.MinInt64+amount {
		return ledger.Transfer{}, false, ledger.ErrBalanceOverflow
	}

	sourceBalance := source.BalanceMinor - amount
	destinationBalance := destination.BalanceMinor + amount
	if _, err := tx.ExecContext(ctx, `UPDATE accounts SET balance_minor = $1 WHERE id = $2`, sourceBalance, sourceID); err != nil {
		return ledger.Transfer{}, false, fmt.Errorf("debit source account: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `UPDATE accounts SET balance_minor = $1 WHERE id = $2`, destinationBalance, destinationID); err != nil {
		return ledger.Transfer{}, false, fmt.Errorf("credit destination account: %w", err)
	}

	if err := insertEntry(ctx, tx, transfer.ID, sourceID, -amount); err != nil {
		return ledger.Transfer{}, false, err
	}
	if err := insertEntry(ctx, tx, transfer.ID, destinationID, amount); err != nil {
		return ledger.Transfer{}, false, err
	}
	if err := tx.Commit(); err != nil {
		return ledger.Transfer{}, false, fmt.Errorf("commit transfer: %w", err)
	}
	return transfer, false, nil
}

func isRetryableTransactionError(err error) bool {
	var pgErr *pgconn.PgError
	if !errors.As(err, &pgErr) {
		return false
	}
	return pgErr.Code == "40001" || pgErr.Code == "40P01"
}

func transactionRetryDelayFor(retry int) time.Duration {
	delay := transactionRetryDelay
	for range retry - 1 {
		delay *= 2
		if delay >= transactionRetryMax {
			return transactionRetryMax
		}
	}
	return delay
}

func waitForTransactionRetry(ctx context.Context, delay time.Duration) error {
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

func (r *Repository) idempotentReplay(ctx context.Context, key, kind, sourceID, destinationID string, amount int64) (ledger.Transfer, bool, error) {
	transfer, found, err := findTransfer(ctx, r.db, key)
	if err != nil {
		return ledger.Transfer{}, false, err
	}
	if !found {
		return ledger.Transfer{}, false, errors.New("transfer conflict disappeared before replay lookup")
	}
	return compareReplay(transfer, kind, sourceID, destinationID, amount)
}

type rowQueryer interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}

func findTransfer(ctx context.Context, queryer rowQueryer, key string) (ledger.Transfer, bool, error) {
	var transfer ledger.Transfer
	err := queryer.QueryRowContext(ctx, `
		SELECT id::text, kind, source_account_id::text, destination_account_id::text,
			amount_minor, currency, created_at
		FROM transfers WHERE idempotency_key = $1
	`, key).Scan(
		&transfer.ID, &transfer.Kind, &transfer.SourceAccountID,
		&transfer.DestinationAccountID, &transfer.AmountMinor, &transfer.Currency,
		&transfer.CreatedAt,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return ledger.Transfer{}, false, nil
	}
	if err != nil {
		return ledger.Transfer{}, false, fmt.Errorf("look up idempotent transfer: %w", err)
	}
	return transfer, true, nil
}

func compareReplay(transfer ledger.Transfer, kind, sourceID, destinationID string, amount int64) (ledger.Transfer, bool, error) {
	if transfer.Kind != kind || transfer.SourceAccountID != sourceID ||
		transfer.DestinationAccountID != destinationID || transfer.AmountMinor != amount {
		return ledger.Transfer{}, false, ledger.ErrIdempotencyConflict
	}
	return transfer, true, nil
}

type lockedAccount struct {
	Currency     string
	BalanceMinor int64
	IsSystem     bool
}

func lockAccounts(ctx context.Context, tx *sql.Tx, sourceID, destinationID string) (map[string]lockedAccount, error) {
	rows, err := tx.QueryContext(ctx, `
		SELECT id::text, currency, balance_minor, is_system
		FROM accounts
		WHERE id IN ($1, $2)
		ORDER BY id
		FOR UPDATE
	`, sourceID, destinationID)
	if err != nil {
		return nil, translatePostgresError(err)
	}
	defer rows.Close()

	accounts := make(map[string]lockedAccount, 2)
	for rows.Next() {
		var id string
		var account lockedAccount
		if err := rows.Scan(&id, &account.Currency, &account.BalanceMinor, &account.IsSystem); err != nil {
			return nil, fmt.Errorf("scan locked account: %w", err)
		}
		accounts[id] = account
	}
	if err := rows.Err(); err != nil {
		return nil, translatePostgresError(err)
	}
	return accounts, nil
}

func ensureExternalAccount(ctx context.Context, tx *sql.Tx, currency string) (string, error) {
	id, err := newID()
	if err != nil {
		return "", fmt.Errorf("generate external account ID: %w", err)
	}
	var accountID string
	err = tx.QueryRowContext(ctx, `
		INSERT INTO accounts (id, name, currency, is_system, system_key)
		VALUES ($1, $2, $3, TRUE, $4)
		ON CONFLICT (system_key) DO UPDATE SET system_key = EXCLUDED.system_key
		RETURNING id::text
	`, id, "External clearing "+currency, currency, "external:"+currency).Scan(&accountID)
	if err != nil {
		return "", fmt.Errorf("ensure external clearing account: %w", err)
	}
	return accountID, nil
}

func insertEntry(ctx context.Context, tx *sql.Tx, transferID, accountID string, amount int64) error {
	id, err := newID()
	if err != nil {
		return fmt.Errorf("generate ledger entry ID: %w", err)
	}
	_, err = tx.ExecContext(ctx, `
		INSERT INTO ledger_entries (id, transfer_id, account_id, amount_minor)
		VALUES ($1, $2, $3, $4)
	`, id, transferID, accountID, amount)
	if err != nil {
		return fmt.Errorf("insert ledger entry: %w", err)
	}
	return nil
}

func scanAccount(row *sql.Row) (ledger.Account, error) {
	var account ledger.Account
	err := row.Scan(&account.ID, &account.Name, &account.Currency, &account.BalanceMinor, &account.CreatedAt)
	return account, err
}

func translatePostgresError(err error) error {
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) && pgErr.Code == "23503" {
		return ledger.ErrNotFound
	}
	return fmt.Errorf("postgres operation: %w", err)
}

func newID() (string, error) {
	var bytes [16]byte
	if _, err := rand.Read(bytes[:]); err != nil {
		return "", err
	}
	bytes[6] = (bytes[6] & 0x0f) | 0x40
	bytes[8] = (bytes[8] & 0x3f) | 0x80
	encoded := hex.EncodeToString(bytes[:])
	return strings.Join([]string{
		encoded[:8], encoded[8:12], encoded[12:16], encoded[16:20], encoded[20:],
	}, "-"), nil
}
