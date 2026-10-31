-- Why a stored zip failed, as a code the interface can translate (design section 14, slice s4a, decision 38).
--
-- error_code is the API error code of the refusal (too_large, storage_full, download_restricted, ...), and
-- error_reason its details' reason where it has one (zip_disk, zip_account_disk). Both are '' for a failure
-- with no code, such as a fault of the host. error stays the server's own text, for the logs.

ALTER TABLE zips ADD COLUMN error_code TEXT NOT NULL DEFAULT '';
ALTER TABLE zips ADD COLUMN error_reason TEXT NOT NULL DEFAULT '';
