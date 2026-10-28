DROP TRIGGER transfers_truncate_immutable ON transfers;
DROP TRIGGER ledger_entries_truncate_immutable ON ledger_entries;
DROP TRIGGER accounts_identity_immutable ON accounts;
DROP FUNCTION prevent_account_identity_mutation();
DROP TRIGGER entries_balance_matches_account ON ledger_entries;
DROP TRIGGER accounts_balance_matches_journal ON accounts;
DROP FUNCTION ensure_account_balance_matches_journal();
DROP TRIGGER ledger_entries_validate_insert ON ledger_entries;
DROP FUNCTION validate_new_ledger_entry();
