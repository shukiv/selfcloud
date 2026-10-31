-- Labels: typed fields the account owner defines, and one value per field on each node (slice p4). Times
-- are Unix milliseconds.
--
-- A field has one type: text, number, date, user or choice. A choice field offers its choices; a choice is
-- archived rather than deleted, so the values that use it keep their meaning. Each node holds at most one
-- value per field. The value columns are checked to hold at most one value; that the value matches the
-- field's type is checked by the API, because it needs a join.
--
-- Deleting a field removes its choices and values, a node's removal removes its values, and a deleted user
-- removes the values that name it (value_user). Labels are not inherited, and nothing here reads or writes
-- a node's own row.

CREATE TABLE label_fields (
    id         TEXT    PRIMARY KEY,
    account_id TEXT    NOT NULL REFERENCES accounts (id) ON DELETE CASCADE,
    name       TEXT    NOT NULL,
    name_key   TEXT    NOT NULL,
    type       TEXT    NOT NULL CHECK (type IN ('text', 'number', 'date', 'user', 'choice')),
    restricted INTEGER NOT NULL DEFAULT 0 CHECK (restricted IN (0, 1)),
    position   INTEGER NOT NULL DEFAULT 0,
    created_by TEXT    NOT NULL DEFAULT '',
    created_at INTEGER NOT NULL,
    updated_by TEXT    NOT NULL DEFAULT '',
    updated_at INTEGER NOT NULL,
    UNIQUE (account_id, name_key)
) STRICT;

CREATE TABLE label_choices (
    id       TEXT    PRIMARY KEY,
    field_id TEXT    NOT NULL REFERENCES label_fields (id) ON DELETE CASCADE,
    name     TEXT    NOT NULL,
    name_key TEXT    NOT NULL,
    position INTEGER NOT NULL DEFAULT 0,
    archived INTEGER NOT NULL DEFAULT 0 CHECK (archived IN (0, 1)),
    UNIQUE (field_id, name_key)
) STRICT;

CREATE TABLE node_labels (
    node_id      TEXT    NOT NULL REFERENCES nodes (id) ON DELETE CASCADE,
    field_id     TEXT    NOT NULL REFERENCES label_fields (id) ON DELETE CASCADE,
    account_id   TEXT    NOT NULL REFERENCES accounts (id) ON DELETE CASCADE,
    value_text   TEXT    CHECK (value_text IS NULL OR length(value_text) <= 500),
    value_num    REAL,
    value_date   TEXT    CHECK (value_date IS NULL OR value_date GLOB '[0-9][0-9][0-9][0-9]-[0-9][0-9]-[0-9][0-9]'),
    value_user   TEXT    REFERENCES users (id) ON DELETE CASCADE,
    value_choice TEXT    REFERENCES label_choices (id) ON DELETE CASCADE,
    set_by       TEXT    NOT NULL DEFAULT '',
    set_at       INTEGER NOT NULL,
    PRIMARY KEY (node_id, field_id),
    CHECK ((value_text IS NOT NULL) + (value_num IS NOT NULL) + (value_date IS NOT NULL)
        + (value_user IS NOT NULL) + (value_choice IS NOT NULL) <= 1)
) STRICT;

CREATE INDEX label_fields_account ON label_fields (account_id, position);
CREATE INDEX label_choices_field ON label_choices (field_id, position);
CREATE INDEX node_labels_field ON node_labels (field_id);
CREATE INDEX node_labels_user ON node_labels (value_user) WHERE value_user IS NOT NULL;
