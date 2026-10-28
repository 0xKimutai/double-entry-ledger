-- Reject legacy data that would violate the stricter checks below.
DO $$
BEGIN
    IF EXISTS (
        SELECT 1
        FROM transfers t
        LEFT JOIN ledger_entries e ON e.transfer_id = t.id
        GROUP BY t.id, t.source_account_id, t.destination_account_id, t.amount_minor, t.kind
        HAVING count(e.id) <> 2
            OR COALESCE(sum(e.amount_minor), 0) <> 0
            OR count(e.id) FILTER (
                WHERE e.account_id = t.source_account_id
                    AND e.amount_minor = -t.amount_minor
            ) <> 1
            OR count(e.id) FILTER (
                WHERE e.account_id = t.destination_account_id
                    AND e.amount_minor = t.amount_minor
            ) <> 1
    ) THEN
        RAISE EXCEPTION 'existing journal contains an unbalanced transfer';
    END IF;

    IF EXISTS (
        SELECT 1
        FROM accounts a
        LEFT JOIN ledger_entries e ON e.account_id = a.id
        GROUP BY a.id, a.balance_minor
        HAVING COALESCE(sum(e.amount_minor), 0) <> a.balance_minor
    ) THEN
        RAISE EXCEPTION 'existing account balance differs from its journal';
    END IF;

    IF EXISTS (
        SELECT 1
        FROM transfers t
        JOIN ledger_entries e ON e.transfer_id = t.id
        JOIN accounts a ON a.id = e.account_id
        WHERE a.currency <> t.currency
            OR (
                e.account_id = t.source_account_id
                AND a.is_system IS DISTINCT FROM (t.kind = 'deposit')
            )
            OR (
                e.account_id = t.destination_account_id
                AND a.is_system
            )
            OR e.account_id NOT IN (t.source_account_id, t.destination_account_id)
    ) THEN
        RAISE EXCEPTION 'existing journal contains an invalid account or currency';
    END IF;
END;
$$;

CREATE FUNCTION validate_new_ledger_entry()
RETURNS TRIGGER
LANGUAGE plpgsql
AS $$
DECLARE
    transfer_row transfers%ROWTYPE;
    account_currency TEXT;
    account_is_system BOOLEAN;
    existing_entries BIGINT;
BEGIN
    SELECT * INTO transfer_row
    FROM transfers
    WHERE id = NEW.transfer_id
    FOR UPDATE;

    IF NOT FOUND THEN
        RAISE EXCEPTION 'transfer % does not exist', NEW.transfer_id
            USING ERRCODE = '23503';
    END IF;

    SELECT currency, is_system
    INTO account_currency, account_is_system
    FROM accounts
    WHERE id = NEW.account_id;

    IF NOT FOUND THEN
        RAISE EXCEPTION 'account % does not exist', NEW.account_id
            USING ERRCODE = '23503';
    END IF;

    SELECT count(*) INTO existing_entries
    FROM ledger_entries
    WHERE transfer_id = NEW.transfer_id;

    IF existing_entries >= 2 THEN
        RAISE EXCEPTION 'transfer % is already posted', NEW.transfer_id
            USING ERRCODE = '23514';
    END IF;

    IF account_currency <> transfer_row.currency THEN
        RAISE EXCEPTION 'entry currency must match transfer currency'
            USING ERRCODE = '23514';
    END IF;

    IF NEW.account_id = transfer_row.source_account_id
        AND NEW.amount_minor = -transfer_row.amount_minor
        AND account_is_system = (transfer_row.kind = 'deposit') THEN
        RETURN NEW;
    END IF;

    IF NEW.account_id = transfer_row.destination_account_id
        AND NEW.amount_minor = transfer_row.amount_minor
        AND NOT account_is_system THEN
        RETURN NEW;
    END IF;

    RAISE EXCEPTION 'entry must match a transfer debit or credit'
        USING ERRCODE = '23514';
END;
$$;

CREATE TRIGGER ledger_entries_validate_insert
    BEFORE INSERT ON ledger_entries
    FOR EACH ROW
    EXECUTE FUNCTION validate_new_ledger_entry();

CREATE FUNCTION ensure_account_balance_matches_journal()
RETURNS TRIGGER
LANGUAGE plpgsql
AS $$
DECLARE
    target_account_id UUID;
    account_balance BIGINT;
    journal_balance NUMERIC;
BEGIN
    IF TG_TABLE_NAME = 'accounts' THEN
        target_account_id := NEW.id;
    ELSE
        target_account_id := NEW.account_id;
    END IF;

    SELECT balance_minor INTO account_balance
    FROM accounts
    WHERE id = target_account_id;

    IF NOT FOUND THEN
        RETURN NULL;
    END IF;

    SELECT COALESCE(sum(amount_minor), 0)
    INTO journal_balance
    FROM ledger_entries
    WHERE ledger_entries.account_id = target_account_id;

    IF journal_balance <> account_balance THEN
        RAISE EXCEPTION 'account % balance does not match its journal', target_account_id
            USING ERRCODE = '23514';
    END IF;
    RETURN NULL;
END;
$$;

-- Defer these checks so posting can write both projections in one transaction.
CREATE CONSTRAINT TRIGGER accounts_balance_matches_journal
    AFTER INSERT OR UPDATE ON accounts
    DEFERRABLE INITIALLY DEFERRED
    FOR EACH ROW
    EXECUTE FUNCTION ensure_account_balance_matches_journal();

CREATE CONSTRAINT TRIGGER entries_balance_matches_account
    AFTER INSERT ON ledger_entries
    DEFERRABLE INITIALLY DEFERRED
    FOR EACH ROW
    EXECUTE FUNCTION ensure_account_balance_matches_journal();

CREATE TRIGGER ledger_entries_truncate_immutable
    BEFORE TRUNCATE ON ledger_entries
    FOR EACH STATEMENT
    EXECUTE FUNCTION prevent_ledger_mutation();

CREATE TRIGGER transfers_truncate_immutable
    BEFORE TRUNCATE ON transfers
    FOR EACH STATEMENT
    EXECUTE FUNCTION prevent_ledger_mutation();

CREATE FUNCTION prevent_account_identity_mutation()
RETURNS TRIGGER
LANGUAGE plpgsql
AS $$
BEGIN
    IF NEW.currency IS DISTINCT FROM OLD.currency
        OR NEW.is_system IS DISTINCT FROM OLD.is_system
        OR NEW.system_key IS DISTINCT FROM OLD.system_key THEN
        RAISE EXCEPTION 'account currency and system identity are immutable'
            USING ERRCODE = '55000';
    END IF;
    RETURN NEW;
END;
$$;

CREATE TRIGGER accounts_identity_immutable
    BEFORE UPDATE OF currency, is_system, system_key ON accounts
    FOR EACH ROW
    EXECUTE FUNCTION prevent_account_identity_mutation();
