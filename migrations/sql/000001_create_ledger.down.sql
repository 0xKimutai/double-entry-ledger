DROP TRIGGER ledger_entries_append_only ON ledger_entries;
DROP TRIGGER transfers_append_only ON transfers;
DROP TRIGGER transfers_balanced ON transfers;
DROP FUNCTION prevent_ledger_mutation();
DROP FUNCTION ensure_transfer_balanced();
DROP TABLE ledger_entries;
DROP TABLE transfers;
DROP TABLE accounts;
