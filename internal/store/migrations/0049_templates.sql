-- The account's templates folder (decision 105, slice nt1). One row per account names one directory node: the folder whose
-- files are the account's templates. A row goes with its node (a purge removes it) and with its account. A trashed
-- folder keeps its row, since trashing takes a node out of every route at once; the reads treat a trashed or unreadable
-- folder as none, so nothing is rewritten when a folder is trashed or restored.
CREATE TABLE account_templates (
    account_id TEXT PRIMARY KEY REFERENCES accounts (id) ON DELETE CASCADE,
    node_id    TEXT NOT NULL REFERENCES nodes (id) ON DELETE CASCADE
) STRICT;
