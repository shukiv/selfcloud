-- Jobs carry a payload: the arguments a job needs that are not its target, as JSON. Empty when
-- the job needs none.
ALTER TABLE jobs ADD COLUMN payload TEXT NOT NULL DEFAULT '';
