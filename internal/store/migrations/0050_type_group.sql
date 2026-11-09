-- The file type group of each node (decision 110, slice ff1 part 1). The column is set by the store on every write of a name
-- or a media type (groupOf in filetype.go); this backfill gives the same answer for every existing row. The CASE below is
-- groupOf written in SQL, in the same order: a folder or shortcut has no group; a known extension decides; otherwise the
-- media type (parameters dropped, trimmed, lower case) decides, first by an exact member, then by the video and audio
-- prefixes. A test compares this result with groupOf on a fixture of names and types that disagree.
-- The five list and trash indexes are added by a later step of this slice (see the decision entry); they are not here.
ALTER TABLE nodes ADD COLUMN type_group TEXT NOT NULL DEFAULT '';

UPDATE nodes SET type_group = CASE
    WHEN kind <> 'file' THEN ''
    WHEN lower(CASE WHEN instr(name, '.') > 0 THEN substr(name, length(rtrim(name, replace(name, '.', ''))) + 1) ELSE '' END)
        IN ('docx', 'doc', 'odt', 'rtf', 'txt', 'md') THEN 'wordprocessing'
    WHEN lower(CASE WHEN instr(name, '.') > 0 THEN substr(name, length(rtrim(name, replace(name, '.', ''))) + 1) ELSE '' END)
        IN ('xlsx', 'xls', 'ods', 'csv') THEN 'spreadsheet'
    WHEN lower(CASE WHEN instr(name, '.') > 0 THEN substr(name, length(rtrim(name, replace(name, '.', ''))) + 1) ELSE '' END)
        IN ('pptx', 'ppt', 'odp') THEN 'presentation'
    WHEN lower(CASE WHEN instr(name, '.') > 0 THEN substr(name, length(rtrim(name, replace(name, '.', ''))) + 1) ELSE '' END)
        IN ('odg') THEN 'drawing'
    WHEN lower(CASE WHEN instr(name, '.') > 0 THEN substr(name, length(rtrim(name, replace(name, '.', ''))) + 1) ELSE '' END)
        IN ('pdf') THEN 'pdf'
    WHEN lower(CASE WHEN instr(name, '.') > 0 THEN substr(name, length(rtrim(name, replace(name, '.', ''))) + 1) ELSE '' END)
        IN ('jpg', 'jpeg', 'png', 'gif', 'webp', 'avif', 'svg', 'heic', 'heif', 'bmp', 'tif', 'tiff') THEN 'image'
    WHEN lower(CASE WHEN instr(name, '.') > 0 THEN substr(name, length(rtrim(name, replace(name, '.', ''))) + 1) ELSE '' END)
        IN ('mp4', 'mov', 'webm', 'mkv', 'avi', 'ogv') THEN 'video'
    WHEN lower(CASE WHEN instr(name, '.') > 0 THEN substr(name, length(rtrim(name, replace(name, '.', ''))) + 1) ELSE '' END)
        IN ('mp3', 'm4a', 'ogg', 'oga', 'flac', 'wav') THEN 'audio'
    WHEN lower(CASE WHEN instr(name, '.') > 0 THEN substr(name, length(rtrim(name, replace(name, '.', ''))) + 1) ELSE '' END)
        IN ('zip', 'tar', 'gz', '7z', 'rar') THEN 'archive'
    WHEN lower(trim(CASE WHEN instr(mime_type, ';') > 0 THEN substr(mime_type, 1, instr(mime_type, ';') - 1) ELSE mime_type END))
        IN ('application/vnd.openxmlformats-officedocument.wordprocessingml.document', 'application/msword',
            'application/vnd.oasis.opendocument.text', 'application/rtf', 'text/plain', 'text/markdown') THEN 'wordprocessing'
    WHEN lower(trim(CASE WHEN instr(mime_type, ';') > 0 THEN substr(mime_type, 1, instr(mime_type, ';') - 1) ELSE mime_type END))
        IN ('application/vnd.openxmlformats-officedocument.spreadsheetml.sheet', 'application/vnd.ms-excel',
            'application/vnd.oasis.opendocument.spreadsheet', 'text/csv') THEN 'spreadsheet'
    WHEN lower(trim(CASE WHEN instr(mime_type, ';') > 0 THEN substr(mime_type, 1, instr(mime_type, ';') - 1) ELSE mime_type END))
        IN ('application/vnd.openxmlformats-officedocument.presentationml.presentation',
            'application/vnd.ms-powerpoint', 'application/vnd.oasis.opendocument.presentation') THEN 'presentation'
    WHEN lower(trim(CASE WHEN instr(mime_type, ';') > 0 THEN substr(mime_type, 1, instr(mime_type, ';') - 1) ELSE mime_type END))
        IN ('application/vnd.oasis.opendocument.graphics') THEN 'drawing'
    WHEN lower(trim(CASE WHEN instr(mime_type, ';') > 0 THEN substr(mime_type, 1, instr(mime_type, ';') - 1) ELSE mime_type END))
        IN ('application/pdf') THEN 'pdf'
    WHEN lower(trim(CASE WHEN instr(mime_type, ';') > 0 THEN substr(mime_type, 1, instr(mime_type, ';') - 1) ELSE mime_type END))
        IN ('image/jpeg', 'image/png', 'image/gif', 'image/webp', 'image/avif', 'image/svg+xml', 'image/heic',
            'image/heif', 'image/bmp', 'image/tiff') THEN 'image'
    WHEN lower(trim(CASE WHEN instr(mime_type, ';') > 0 THEN substr(mime_type, 1, instr(mime_type, ';') - 1) ELSE mime_type END))
        LIKE 'video/%' THEN 'video'
    WHEN lower(trim(CASE WHEN instr(mime_type, ';') > 0 THEN substr(mime_type, 1, instr(mime_type, ';') - 1) ELSE mime_type END))
        LIKE 'audio/%' THEN 'audio'
    WHEN lower(trim(CASE WHEN instr(mime_type, ';') > 0 THEN substr(mime_type, 1, instr(mime_type, ';') - 1) ELSE mime_type END))
        IN ('application/zip', 'application/x-zip-compressed', 'application/x-tar', 'application/gzip',
            'application/x-7z-compressed', 'application/x-rar-compressed') THEN 'archive'
    ELSE '' END;
