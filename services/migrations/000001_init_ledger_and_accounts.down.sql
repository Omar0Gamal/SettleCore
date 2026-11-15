DROP TRIGGER IF EXISTS trg_ledger_entries_balanced ON ledger_entries;
DROP FUNCTION IF EXISTS fn_ledger_entries_balanced();
DROP TRIGGER IF EXISTS trg_ledger_entries_immutable ON ledger_entries;
DROP FUNCTION IF EXISTS fn_ledger_entries_immutable();
DROP TABLE IF EXISTS ledger_entries;
DROP TABLE IF EXISTS accounts;
