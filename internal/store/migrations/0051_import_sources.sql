-- The archive source can now be a file already in the drive, not only an upload (design section 3.2).
--
-- source_kind says where the archive came from: 'upload' for an upload made for the import, as every existing row is,
-- or 'node' for a drive file, named by source_node_id. For a node source the drive file's blob is held in
-- source_blob_id, and archive_blob_id stays empty. Old binaries remove only archive_blob_id, so they remove nothing
-- for a node source, and the drive file is never in a field an old binary removes.
--
-- Additive only: the columns are added with defaults, so the rows that exist read as uploads.

ALTER TABLE import_jobs ADD COLUMN source_kind TEXT NOT NULL DEFAULT 'upload' CHECK (source_kind IN ('upload', 'node'));
ALTER TABLE import_jobs ADD COLUMN source_node_id TEXT NOT NULL DEFAULT '' CHECK (length(source_node_id) <= 64);
ALTER TABLE import_jobs ADD COLUMN source_blob_id TEXT NOT NULL DEFAULT '' CHECK (length(source_blob_id) <= 64);
