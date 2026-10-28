package postgres

import (
	"context"
	"database/sql"
	"fmt"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"
)

func TestPostgreSQLJournalInvariants(t *testing.T) {
	db := openInvariantTestDB(t)
	ctx := context.Background()
	repository := NewRepository(db)

	source, err := repository.CreateAccount(ctx, "source", "KES")
	if err != nil {
		t.Fatal(err)
	}
	destination, err := repository.CreateAccount(ctx, "destination", "KES")
	if err != nil {
		t.Fatal(err)
	}
	other, err := repository.CreateAccount(ctx, "other", "KES")
	if err != nil {
		t.Fatal(err)
	}
	foreignCurrency, err := repository.CreateAccount(ctx, "foreign currency", "USD")
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := repository.Deposit(ctx, source.ID, "integration-deposit", 10_000); err != nil {
		t.Fatal(err)
	}
	transfer, replayed, err := repository.Transfer(ctx, source.ID, destination.ID, "integration-payment", 2_500)
	if err != nil {
		t.Fatal(err)
	}
	if replayed {
		t.Fatal("new payment was reported as an idempotent replay")
	}

	var entryCount int
	var netAmount int64
	if err := db.QueryRowContext(ctx, `
		SELECT count(*), COALESCE(sum(amount_minor), 0)
		FROM ledger_entries WHERE transfer_id = $1
	`, transfer.ID).Scan(&entryCount, &netAmount); err != nil {
		t.Fatal(err)
	}
	if entryCount != 2 || netAmount != 0 {
		t.Fatalf("payment entries = %d, net = %d; want 2 balanced entries", entryCount, netAmount)
	}

	var mismatchedBalances int
	if err := db.QueryRowContext(ctx, `
		SELECT count(*)
		FROM accounts a
		WHERE a.balance_minor <> COALESCE(
			(SELECT sum(e.amount_minor) FROM ledger_entries e WHERE e.account_id = a.id),
			0
		)
	`).Scan(&mismatchedBalances); err != nil {
		t.Fatal(err)
	}
	if mismatchedBalances != 0 {
		t.Fatalf("%d account balances differ from journal sums", mismatchedBalances)
	}

	t.Run("rejects_entries_after_posting", func(t *testing.T) {
		_, err := db.ExecContext(ctx, `
			INSERT INTO ledger_entries (id, transfer_id, account_id, amount_minor)
			VALUES ($1, $2, $3, 1)
		`, mustNewID(t), transfer.ID, other.ID)
		if err == nil {
			t.Fatal("expected journal append to a posted transfer to fail")
		}
	})

	t.Run("rejects_cross_currency_entry", func(t *testing.T) {
		_, err := db.ExecContext(ctx, `
			INSERT INTO ledger_entries (id, transfer_id, account_id, amount_minor)
			VALUES ($1, $2, $3, 1)
		`, mustNewID(t), transfer.ID, foreignCurrency.ID)
		if err == nil {
			t.Fatal("expected cross-currency journal entry to fail")
		}
	})

	t.Run("rejects_invalid_transfer_entry", func(t *testing.T) {
		tx, err := db.BeginTx(ctx, nil)
		if err != nil {
			t.Fatal(err)
		}
		id := mustNewID(t)
		_, err = tx.ExecContext(ctx, `
			INSERT INTO transfers (
				id, kind, idempotency_key, source_account_id,
				destination_account_id, amount_minor, currency
			) VALUES ($1, 'payment', $2, $3, $4, 100, 'KES')
		`, id, "invalid-entry-"+id, source.ID, destination.ID)
		if err != nil {
			t.Fatal(err)
		}
		_, err = tx.ExecContext(ctx, `
			INSERT INTO ledger_entries (id, transfer_id, account_id, amount_minor)
			VALUES ($1, $2, $3, 100)
		`, mustNewID(t), id, source.ID)
		if err == nil {
			if rollbackErr := tx.Rollback(); rollbackErr != nil {
				t.Errorf("rollback invalid entry transaction: %v", rollbackErr)
			}
			t.Fatal("expected wrong-side entry to fail")
		}
		if rollbackErr := tx.Rollback(); rollbackErr != nil {
			t.Errorf("rollback invalid entry transaction: %v", rollbackErr)
		}
	})

	t.Run("rejects_incomplete_transfer_at_commit", func(t *testing.T) {
		tx, err := db.BeginTx(ctx, nil)
		if err != nil {
			t.Fatal(err)
		}
		id := mustNewID(t)
		_, err = tx.ExecContext(ctx, `
			INSERT INTO transfers (
				id, kind, idempotency_key, source_account_id,
				destination_account_id, amount_minor, currency
			) VALUES ($1, 'payment', $2, $3, $4, 100, 'KES')
		`, id, "incomplete-"+id, source.ID, destination.ID)
		if err != nil {
			t.Fatal(err)
		}
		if err := tx.Commit(); err == nil {
			t.Fatal("expected unbalanced transfer to fail at commit")
		}
	})

	t.Run("rejects_balance_changes_without_entries", func(t *testing.T) {
		tx, err := db.BeginTx(ctx, nil)
		if err != nil {
			t.Fatal(err)
		}
		_, err = tx.ExecContext(ctx, `
			UPDATE accounts SET balance_minor = balance_minor + 1 WHERE id = $1
		`, source.ID)
		if err != nil {
			t.Fatal(err)
		}
		if err := tx.Commit(); err == nil {
			t.Fatal("expected balance projection mismatch to fail at commit")
		}
	})

	t.Run("prevents_journal_update_delete_and_truncate", func(t *testing.T) {
		var entryID string
		if err := db.QueryRowContext(ctx, `
			SELECT id::text FROM ledger_entries WHERE transfer_id = $1 LIMIT 1
		`, transfer.ID).Scan(&entryID); err != nil {
			t.Fatal(err)
		}
		for name, statement := range map[string]string{
			"update entry":       `UPDATE ledger_entries SET amount_minor = amount_minor WHERE id = $1`,
			"delete entry":       `DELETE FROM ledger_entries WHERE id = $1`,
			"update transfer":    `UPDATE transfers SET amount_minor = amount_minor WHERE id = $1`,
			"change currency":    `UPDATE accounts SET currency = 'USD' WHERE id = $1`,
			"truncate transfers": `TRUNCATE transfers`,
		} {
			t.Run(name, func(t *testing.T) {
				argument := any(entryID)
				if strings.Contains(statement, "transfers") {
					argument = transfer.ID
				} else if strings.Contains(statement, "accounts") {
					argument = source.ID
				}
				var err error
				if strings.Contains(statement, "TRUNCATE") {
					_, err = db.ExecContext(ctx, statement)
				} else {
					_, err = db.ExecContext(ctx, statement, argument)
				}
				if err == nil {
					t.Fatalf("expected %s to fail", name)
				}
			})
		}

		if _, err := db.ExecContext(ctx, `TRUNCATE ledger_entries`); err == nil {
			t.Fatal("expected journal truncate to fail")
		}
	})
}

func TestMigrationRejectsLegacyJournalDrift(t *testing.T) {
	db := openInvariantTestDB(t)
	ctx := context.Background()
	repository := NewRepository(db)

	source, err := repository.CreateAccount(ctx, "legacy source", "KES")
	if err != nil {
		t.Fatal(err)
	}
	destination, err := repository.CreateAccount(ctx, "legacy destination", "KES")
	if err != nil {
		t.Fatal(err)
	}
	other, err := repository.CreateAccount(ctx, "legacy extra account", "KES")
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := repository.Deposit(ctx, source.ID, "legacy-deposit", 1000); err != nil {
		t.Fatal(err)
	}
	transfer, _, err := repository.Transfer(ctx, source.ID, destination.ID, "legacy-payment", 100)
	if err != nil {
		t.Fatal(err)
	}

	migrator, err := newMigrator(db)
	if err != nil {
		t.Fatal(err)
	}
	if err := migrator.Steps(-1); err != nil {
		t.Fatalf("roll back invariant migration: %v", err)
	}
	if _, err := db.ExecContext(ctx, `
		INSERT INTO ledger_entries (id, transfer_id, account_id, amount_minor)
		VALUES ($1, $2, $3, 1)
	`, mustNewID(t), transfer.ID, other.ID); err != nil {
		t.Fatalf("simulate legacy journal drift: %v", err)
	}

	if err := Migrate(db); err == nil || !strings.Contains(err.Error(), "existing journal contains an unbalanced transfer") {
		t.Fatalf("migration error = %v, want legacy journal drift rejection", err)
	}
	_, dirty, err := MigrationStatus(db)
	if err != nil {
		t.Fatal(err)
	}
	if !dirty {
		t.Fatal("migration should remain dirty after rejecting invalid legacy data")
	}
}

func openInvariantTestDB(t *testing.T) *sql.DB {
	t.Helper()
	dsn := os.Getenv("LEDGER_TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("set LEDGER_TEST_DATABASE_URL to run PostgreSQL invariant tests")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	admin, err := sql.Open("pgx", dsn)
	if err != nil {
		t.Fatal(err)
	}
	if err := admin.PingContext(ctx); err != nil {
		_ = admin.Close()
		t.Fatalf("connect to LEDGER_TEST_DATABASE_URL: %v", err)
	}
	t.Cleanup(func() {
		if err := admin.Close(); err != nil {
			t.Errorf("close test database admin connection: %v", err)
		}
	})

	schema := "ledger_test_" + strings.ReplaceAll(mustNewID(t), "-", "")
	if _, err := admin.ExecContext(ctx, fmt.Sprintf(`CREATE SCHEMA "%s"`, schema)); err != nil {
		t.Fatalf("create isolated test schema: %v", err)
	}
	t.Cleanup(func() {
		if _, err := admin.ExecContext(context.Background(), fmt.Sprintf(`DROP SCHEMA "%s" CASCADE`, schema)); err != nil {
			t.Errorf("drop isolated test schema: %v", err)
		}
	})

	parsed, err := url.Parse(dsn)
	if err != nil {
		t.Fatalf("parse test database URL: %v", err)
	}
	query := parsed.Query()
	query.Set("search_path", schema)
	parsed.RawQuery = query.Encode()
	db, err := sql.Open("pgx", parsed.String())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := db.Close(); err != nil {
			t.Errorf("close isolated test connection: %v", err)
		}
	})
	if err := db.PingContext(ctx); err != nil {
		t.Fatalf("connect to isolated test schema: %v", err)
	}
	if err := Migrate(db); err != nil {
		t.Fatalf("apply migrations to isolated test schema: %v", err)
	}
	return db
}

func mustNewID(t *testing.T) string {
	t.Helper()
	id, err := newID()
	if err != nil {
		t.Fatal(err)
	}
	return id
}
