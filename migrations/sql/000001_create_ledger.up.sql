CREATE TABLE accounts (
    id UUID PRIMARY KEY,
    name TEXT NOT NULL CHECK (length(name) BETWEEN 1 AND 120),
    currency TEXT NOT NULL CHECK (currency ~ '^[A-Z]{3}$'),
    balance_minor BIGINT NOT NULL DEFAULT 0,
    is_system BOOLEAN NOT NULL DEFAULT FALSE,
    system_key TEXT UNIQUE,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (id, currency),
    CHECK ((is_system AND system_key IS NOT NULL) OR (NOT is_system AND system_key IS NULL))
);

CREATE TABLE transfers (
    id UUID PRIMARY KEY,
    kind TEXT NOT NULL CHECK (kind IN ('deposit', 'payment')),
    idempotency_key TEXT NOT NULL UNIQUE CHECK (length(idempotency_key) BETWEEN 1 AND 255),
    source_account_id UUID NOT NULL,
    destination_account_id UUID NOT NULL,
    amount_minor BIGINT NOT NULL CHECK (amount_minor > 0),
    currency TEXT NOT NULL CHECK (currency ~ '^[A-Z]{3}$'),
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    CHECK (source_account_id <> destination_account_id),
    FOREIGN KEY (source_account_id, currency) REFERENCES accounts (id, currency),
    FOREIGN KEY (destination_account_id, currency) REFERENCES accounts (id, currency)
);

CREATE TABLE ledger_entries (
    id UUID PRIMARY KEY,
    transfer_id UUID NOT NULL REFERENCES transfers (id),
    account_id UUID NOT NULL REFERENCES accounts (id),
    amount_minor BIGINT NOT NULL CHECK (amount_minor <> 0),
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (transfer_id, account_id)
);

CREATE INDEX ledger_entries_account_created_idx
    ON ledger_entries (account_id, created_at DESC, id DESC);

CREATE FUNCTION ensure_transfer_balanced()
RETURNS TRIGGER
LANGUAGE plpgsql
AS $$
DECLARE
    entry_count BIGINT;
    net_amount NUMERIC;
    source_entry_count BIGINT;
    destination_entry_count BIGINT;
    source_is_system BOOLEAN;
    destination_is_system BOOLEAN;
BEGIN
    SELECT count(*),
        COALESCE(sum(amount_minor), 0),
        count(*) FILTER (
            WHERE account_id = NEW.source_account_id
                AND amount_minor = -NEW.amount_minor
        ),
        count(*) FILTER (
            WHERE account_id = NEW.destination_account_id
                AND amount_minor = NEW.amount_minor
        )
    INTO entry_count, net_amount, source_entry_count, destination_entry_count
    FROM ledger_entries
    WHERE transfer_id = NEW.id;

    SELECT is_system INTO source_is_system
    FROM accounts WHERE id = NEW.source_account_id;
    SELECT is_system INTO destination_is_system
    FROM accounts WHERE id = NEW.destination_account_id;

    IF entry_count <> 2 OR net_amount <> 0
        OR source_entry_count <> 1 OR destination_entry_count <> 1
        OR source_is_system IS DISTINCT FROM (NEW.kind = 'deposit')
        OR destination_is_system IS DISTINCT FROM FALSE THEN
        RAISE EXCEPTION 'transfer % must have one debit and one credit entry', NEW.id
            USING ERRCODE = '23514';
    END IF;
    RETURN NULL;
END;
$$;

CREATE CONSTRAINT TRIGGER transfers_balanced
    AFTER INSERT ON transfers
    DEFERRABLE INITIALLY DEFERRED
    FOR EACH ROW
    EXECUTE FUNCTION ensure_transfer_balanced();

CREATE FUNCTION prevent_ledger_mutation()
RETURNS TRIGGER
LANGUAGE plpgsql
AS $$
BEGIN
    RAISE EXCEPTION '% rows are immutable', TG_TABLE_NAME
        USING ERRCODE = '55000';
    RETURN OLD;
END;
$$;

CREATE TRIGGER transfers_append_only
    BEFORE UPDATE OR DELETE ON transfers
    FOR EACH ROW
    EXECUTE FUNCTION prevent_ledger_mutation();

CREATE TRIGGER ledger_entries_append_only
    BEFORE UPDATE OR DELETE ON ledger_entries
    FOR EACH ROW
    EXECUTE FUNCTION prevent_ledger_mutation();
