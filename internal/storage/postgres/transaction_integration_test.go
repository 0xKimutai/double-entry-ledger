package postgres

import (
	"context"
	"fmt"
	"testing"
	"time"
)

func TestTransferRetriesSerializationFailure(t *testing.T) {
	db := openInvariantTestDB(t)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	repository := NewRepository(db)

	source, err := repository.CreateAccount(ctx, "retry source", "KES")
	if err != nil {
		t.Fatal(err)
	}
	destination, err := repository.CreateAccount(ctx, "retry destination", "KES")
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := repository.Deposit(ctx, source.ID, "retry-funding", 1_000); err != nil {
		t.Fatal(err)
	}

	for _, statement := range []string{
		`CREATE SEQUENCE transaction_retry_test_sequence`,
		`CREATE FUNCTION fail_first_transfer_insert() RETURNS trigger AS $$
			BEGIN
				IF current_setting('transaction_isolation') <> 'read committed' THEN
					RAISE EXCEPTION 'unexpected transaction isolation: %', current_setting('transaction_isolation');
				END IF;
				IF nextval('transaction_retry_test_sequence') = 1 THEN
					RAISE EXCEPTION 'injected serialization failure' USING ERRCODE = '40001';
				END IF;
				RETURN NEW;
			END;
		$$ LANGUAGE plpgsql`,
		`CREATE TRIGGER transaction_retry_test
			BEFORE INSERT ON transfers
			FOR EACH ROW EXECUTE FUNCTION fail_first_transfer_insert()`,
	} {
		if _, err := db.ExecContext(ctx, statement); err != nil {
			t.Fatalf("install serialization failure test trigger: %v", err)
		}
	}

	transfer, replayed, err := repository.Transfer(ctx, source.ID, destination.ID, "retry-payment", 250)
	if err != nil {
		t.Fatalf("transfer after serialization failure: %v", err)
	}
	if replayed {
		t.Fatal("new transfer was reported as replayed")
	}

	var sequenceValue int64
	if err := db.QueryRowContext(ctx, `SELECT last_value FROM transaction_retry_test_sequence`).Scan(&sequenceValue); err != nil {
		t.Fatal(err)
	}
	if sequenceValue != 2 {
		t.Fatalf("sequence value = %d, want 2 (one failed attempt and one successful retry)", sequenceValue)
	}

	var transferCount, entryCount int
	if err := db.QueryRowContext(ctx, `
		SELECT count(*) FROM transfers WHERE idempotency_key = 'retry-payment'
	`).Scan(&transferCount); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRowContext(ctx, `
		SELECT count(*) FROM ledger_entries WHERE transfer_id = $1
	`, transfer.ID).Scan(&entryCount); err != nil {
		t.Fatal(err)
	}
	if transferCount != 1 || entryCount != 2 {
		t.Fatalf("committed transfer rows = %d and journal entries = %d; want 1 and 2", transferCount, entryCount)
	}
}

func TestTransferTransactionUsesReadCommitted(t *testing.T) {
	db := openInvariantTestDB(t)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	repository := NewRepository(db)

	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := tx.Rollback(); err != nil {
			t.Errorf("rollback isolation test transaction: %v", err)
		}
	}()

	var isolation string
	if err := tx.QueryRowContext(ctx, `SHOW transaction_isolation`).Scan(&isolation); err != nil {
		t.Fatal(err)
	}
	if isolation != "read committed" {
		t.Fatalf("transaction isolation = %q, want read committed", isolation)
	}

	var countBefore int
	if err := tx.QueryRowContext(ctx, `SELECT count(*) FROM accounts`).Scan(&countBefore); err != nil {
		t.Fatal(err)
	}
	if _, err := repository.CreateAccount(ctx, fmt.Sprintf("isolation account %d", time.Now().UnixNano()), "KES"); err != nil {
		t.Fatal(err)
	}
	var countAfter int
	if err := tx.QueryRowContext(ctx, `SELECT count(*) FROM accounts`).Scan(&countAfter); err != nil {
		t.Fatal(err)
	}
	if countAfter != countBefore+1 {
		t.Fatalf("second statement saw %d accounts; before=%d, want the newly committed account", countAfter, countBefore)
	}
}
