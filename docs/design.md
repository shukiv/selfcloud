# Selfcloud design

Status: **approved 2026-10-28.** Items marked *proposed* were accepted with the design.
Section 15 records the owner's decisions and the questions still open.

## 1. Goals and non-goals

**Goals.** Selfcloud is a self-hosted drive written in Go. It is a self-contained product with
its own accounts, users, sign-in, storage and deployment, and it depends on no other system.
Any application can drive it through a documented HTTP API; none is assumed. It runs either as
a headless API server or as the same server with its own web interface, directly on a host or
in Docker. It handles files up to 100 GB, uploaded in resumable pieces whose size the operator
can cap (some proxies refuse any request over 100 MB), and it reaches the disk by the fastest
means the operating system offers.

**Non-goals for the first release.** Desktop sync clients, WebDAV or S3-compatible endpoints,
several servers sharing one data set (high availability), object-store back ends, end-to-end
encryption, and full-text search. The design leaves room for each of them, and section 15 asks
about public share links, file versions and a trash bin.

## 2. Architecture

One static binary, `selfcloud`, with subcommands:

| Command | Purpose |
|---|---|
| `serve` | Run the server. `ui.enabled` decides whether the web interface is served. |
| `migrate` | Apply database migrations (`serve` also does this on start). |
| `admin` | Bootstrap and recovery: create an installation admin key, create an account or an owner user. |
| `backup`, `fsck` | Consistent metadata snapshot; check database against disk. |
| `healthcheck` | Exit 0 if the local server is ready (for Docker, which has no curl in the image). |

Inside the server:

```
HTTP (net/http, Go 1.22+ routing patterns)
 ├─ middleware: request ID, client IP, CORS, auth, rate limits, JSON errors, metrics
 ├─ API handlers   /api/v1/...      (accounts, users, folders, files, uploads, tokens, ...)
 ├─ tus handler    /api/v1/uploads  (resumable uploads)
 ├─ WOPI host      /wopi/...        (office editors)
 └─ web UI         /                (embedded static files, only when ui.enabled)
        │
 ├─ store   metadata in SQLite (default) or PostgreSQL
 ├─ blob    file contents on local disk, fast-path IO
 └─ workers webhook delivery, upload expiry, blob garbage collection, mail
```

The web interface is a client of the public API like any other; it has no private endpoints.

**Repository layout** (*proposed*): `cmd/selfcloud`, `internal/{config,store,blob,auth,api,tus,mail,webhook,wopi,audit}`,
`web/` (UI source, built files embedded with `go:embed`), `api/openapi.yaml`, `client/` (Go
client, its own module), `docs/`.

**Dependencies** stay few: the standard library for HTTP and logging (`log/slog`),
`modernc.org/sqlite` (pure Go, so the binary stays static), `pgx` for PostgreSQL,
`golang.org/x/{crypto,sys,text}`, the Prometheus client, and one config-file parser.

## 3. Metadata store

All metadata lives in a database: accounts, users, folders, the file tree, uploads, tokens,
webhooks and the audit log. The disk holds only file contents. Listing a directory, computing
usage or resolving a path is an indexed query, never a walk of the disk.

- **SQLite by default**, in WAL mode with a busy timeout. One file, no extra service, and fast
  enough for a single server: metadata writes are small, and even a 100 GB upload in 64 MiB
  pieces is about 1,500 offset updates.
- **PostgreSQL as an option** (*proposed*: added after the first client can switch over, unless
  it is needed sooner). The store is one Go interface; every query is parameterised SQL, never
  built by string concatenation. Both back ends run the same test suite.
- **Never on a network file system.** SQLite needs working POSIX locks and `fsync`, which NFS
  and SMB do not reliably give. At start the server calls `statfs` on the database directory
  and refuses to run on NFS, CIFS/SMB or FUSE unless `store.allow_network_fs=true` is set. It
  also warns when the blob directory is on a network file system.

## 4. On-disk layout (*proposed*)

The user-visible tree (folders, directories, names) lives only in the database. On disk, each
file's contents is an immutable **blob** named by a random ID:

```
<data_dir>/
  selfcloud.db                         metadata (SQLite; absent when PostgreSQL is used)
  keys/                                signing and encryption keys, mode 0600 (unless given by config)
  blobs/<account-id>/<b1>/<b2>/<blob-id>     committed file contents, never modified in place
  uploads/<account-id>/<upload-id>           uploads in progress (same file system as blobs/)
```

Why this layout:

- **Rename, move and new directory are metadata-only.** Renaming a directory that holds 100,000
  files is one row update in one transaction, not a disk walk.
- **Path safety is structural.** Disk paths are built only from server-generated IDs, never from
  names a user typed, so `..`, absolute paths, symlinks and odd Unicode in names cannot reach the
  file system. As a second line of defence, blobs are opened with `O_NOFOLLOW`, and with
  `openat2(RESOLVE_BENEATH)` where the kernel has it.
- **Accounts are isolated on disk.** Each account has its own blob directory, which also makes
  per-account export and deletion simple.
- **Writes are copy-on-write.** Replacing a file's contents writes a new blob and then swaps the
  pointer in one database transaction. A download that already opened the old blob finishes
  reading it unharmed. File versions become cheap to add later: keep the old blob.
- **Backups are consistent** without stopping the server (section 12).

The cost is that the data directory is not browsable as ordinary files; `selfcloud` is the way
in. Section 15 asks whether a browsable layout is wanted instead.

**Names.** A name is 1 to 255 bytes of UTF-8, normalised to NFC, and may not contain `/`, `\`,
NUL or other control characters, or be `.` or `..`. Names are case-preserving, and *proposed*
case-insensitive for uniqueness within a directory, so a folder cannot hold both `Report.pdf`
and `report.pdf` (which would collide when downloaded to Windows or macOS).

## 5. Uploads

Uploads use **tus 1.0** with the `creation`, `creation-with-upload`, `expiration`, `checksum`
and `termination` extensions, so existing clients such as tus-js-client and Uppy work as they
are. The protocol is implemented inside Selfcloud (*proposed*) rather than through the tusd
library, because quota reservation, authorisation, preallocation and error codes have to be
woven into each step.

1. **Create.** `POST /api/v1/uploads` with `Upload-Length` and `Upload-Metadata` (target
   folder, parent path, file name, conflict policy, and `parents=true` to create missing
   directories, so a client can upload a whole tree with a relative path per file). The reply's
   `Location` is an absolute URL built from `public_url`, because browsers may talk to
   Selfcloud directly from another origin. The server checks the caller's permission,
   the name, `upload.max_file_size` (default 100 GiB) and the quota: *used + reserved + length*
   must fit, or the reply is `quota_exceeded`. It then reserves `length` bytes against the
   account, creates the upload file and preallocates it with `fallocate`, so a full disk fails
   now with `storage_full`, not after 80 GB have been sent. Deferred length is not supported;
   the size must be declared.
2. **Send pieces.** `PATCH` with `Upload-Offset`. The body is streamed straight into the file
   at that offset (`pwrite`, through a fixed 1 MiB buffer, so memory per upload stays flat
   whatever the file size). A body larger than `upload.max_chunk_size` is refused with
   `chunk_too_large`; the default is 64 MiB, and behind a proxy with a 100 MB request cap it
   should stay at or under 90 MiB (about 94 MB), leaving room for headers. Clients read the
   limit from `GET /api/v1/info`; the file-size limit is also sent as `Tus-Max-Size` on
   `OPTIONS /api/v1/uploads`, as tus clients expect. A write can never go past the declared
   length, so the quota holds while writing.
3. **Durability.** After each piece the server calls `fdatasync` on the file and only then
   records the new offset in the database. Syncing only once at the end would be faster, but
   after a power cut the database could claim bytes that never reached the disk and the client
   would resume past a hole, corrupting the file silently. If benchmarks show the per-piece
   sync costs too much, the fallback is to sync every N bytes and report the last synced offset
   after a crash. If the connection drops mid-piece, the bytes received so far are synced and
   kept, and the client resumes from there (`HEAD` returns the offset).
4. **Integrity.** A SHA-256 of the whole file is computed while writing (pieces arrive in
   order, and the hash state is saved with each durable offset). It is stored and returned as
   `sha256`. Clients may also send `Upload-Checksum` per piece.
5. **Complete.** When the offset reaches the length: sync, `rename(2)` the file into `blobs/`,
   sync the directory, then in one transaction create the file entry, turn the reservation into
   usage, and queue the `upload.completed` webhook. The conflict policy is `rename` (save as
   "name (1).ext"), `replace` or `fail`. With `fail`, a name taken after the upload began
   leaves the completed upload waiting, until it expires, to be committed under another name.
6. **Expire.** Unfinished uploads expire after `upload.expiry` (default 24 h, sent as
   `Upload-Expires`); the file is deleted and the reservation released.

An upload belongs to its account, user and target, not to the credential that created it: any
later token for the same user with `write` on the target may continue it, so an upload can
outlive many short-lived tokens and survive a page reload.

Only one `PATCH` per upload runs at a time; a second gets `409`. Parallel pieces of one file
(the tus `concatenation` extension) can come later if a single stream proves too slow.
Small files can also be written in one request (section 9).

## 6. Fast-path IO

| Operation | Method | Effect |
|---|---|---|
| Download | `http.ServeContent` over an `*os.File`; Go hands the copy to `sendfile(2)` | Bytes go from page cache to socket without passing through the process. Holds for plain HTTP/1.1 and single ranges; TLS in the process or HTTP/2 means a copy in user space, so TLS is best terminated by the proxy in front. |
| Upload | `pwrite` at the offset into a file preallocated with `fallocate` | No append races, no fragmentation, early disk-full error. The request body is a stream inside `net/http`, so one copy in user space is unavoidable; the network is the limit. |
| Rename, move | Database row update | Instant, whatever the size. Only completing an upload uses `rename(2)`. |
| Copy | `ioctl(FICLONE)` reflink where supported (XFS with reflink, Btrfs), else `copy_file_range(2)` | A 100 GB copy is near-instant with reflinks, and stays in the kernel otherwise. |
| Delete | Database delete; blob removed later by a worker | Deleting a large tree returns at once. |
| List, usage | Indexed query; usage counters updated in the same transaction as the change | No disk walk, no `du`. |

Recommended file system: XFS (with reflink) or Btrfs on local SSD; ext4 works without
reflinks. A benchmark suite ships with the upload and download slices and measures throughput,
listing latency and the per-piece sync cost, so these choices are checked, not assumed.

## 7. Authentication and authorisation

Four kinds of caller:

| Caller | How it authenticates | What it can do |
|---|---|---|
| **Installation admin** | `Authorization: Bearer sc_admin_...` | Manage accounts and their keys. |
| **Account key** | `Authorization: Bearer sc_acct_...` | Everything inside one account: users, folders, grants, files, delegated tokens, webhooks, audit. |
| **User session** | Email and password (argon2id), optional TOTP; `__Host-` session cookie, `HttpOnly`, `Secure`, `SameSite=Lax` | What the user's grants allow. Account *owners* (a user role) also manage users, folders and grants in the web interface. |
| **Delegated token** | `Authorization: Bearer sct_...` | A short-lived, narrowed slice of an account key's or a user's rights, for a browser. |

**API keys** are 256-bit random secrets with a visible prefix. Only a SHA-256 hash is stored (a
slow hash adds nothing for secrets this long), each key is shown once at creation, and keys can
carry an expiry and be revoked. The first admin key comes from `selfcloud admin create-key` or
from a secret file named in the config.

**Delegated tokens** (*proposed*) let a client's back end hand a browser direct access, so file
bytes never pass through the client's server. The back end calls
`POST /api/v1/accounts/{account}/tokens` with its account key and gets back a token scoped by:

- `user`: act as this user; rights are then the intersection of the token and the user's grants,
  and the audit log names the user;
- `folders` and optionally a `path` prefix;
- `permissions`: `read`, `write`;
- `ttl`: default 15 minutes, capped by `tokens.max_ttl` (default 1 hour, configurable);
- `audience`: `api` or `wopi`.

The reply carries the token and its `expires_at`, so the back end can cache a token for most of
its lifetime instead of minting one per request.

Tokens are signed with HMAC-SHA256 by a server key with a key ID, so checking one needs no
database lookup and keys can rotate. To revoke, an account can invalidate every token issued
before a given moment, and disabling a user ends that user's tokens. An expired token gets
`401 token_expired`, distinct from `401 unauthenticated`, so a client refreshes silently in the
first case and asks the person to sign in only in the second. A long upload that outlives its
token keeps going: tus-js-client fetches a fresh token from the client's back end when the
current one nears expiry (section 5).

*Alternative:* the client signs tokens itself with a secret shared with Selfcloud, which saves
one round trip per token but brings key distribution, rotation and clock-skew problems to every
client. Section 15 asks which one to build.

**Tokens stay out of URLs**, with one exception: signed download links (section 9), which have
short lifetimes and are scrubbed from logs.

**CORS.** `cors.allowed_origins` lists the client origins. Allowed request headers:
`Authorization`, `Content-Type`, `If-Match`, `Tus-Resumable`, `Upload-*`. Exposed response
headers: `Location`, `Upload-Offset`, `Upload-Length`, `Upload-Expires`, `Tus-Resumable`,
`Tus-Max-Size`, `ETag`, `Content-Range`, `Retry-After`. Cookies are never accepted on
cross-origin requests; browsers from other origins use delegated tokens.

**Web sessions** are stored server-side (hashed ID), rotated at sign-in, and protected against
CSRF by `SameSite`, an `Origin` check and a token on state-changing requests. Sign-in,
verification and password reset are rate-limited per IP and per email, and answer the same way
whether or not an address exists. argon2id runs with RFC 9106 parameters, and the number of
hashes running at once is capped so a burst of sign-ins cannot exhaust memory.

## 8. Accounts, users, folders and grants

- **Account**: a tenant. Has a name, a status (`active`, `suspended`), a storage quota in bytes
  (`0` = unlimited) and usage counters (bytes, files, bytes reserved by uploads). Suspending
  blocks all access but keeps the data. The quota may be lowered below current usage: existing
  files stay, and new writes get `quota_exceeded` until usage drops. Deleting an account runs
  as a job (`202`), since a large tree takes time to remove.
- **External IDs**: accounts, users and folders may carry an `external_id`, the client's own
  reference, unique within the parent (installation for accounts, account for users and folders). Clients can look records up by it, and a create call repeated with
  the same `external_id` and the same fields returns the existing record instead of a
  duplicate (different fields get `409 conflict`), so retrying after a timeout is safe.
- **User**: belongs to one account; the email is unique within it. Role `owner` or `member`.
  Status `pending` until verified, then `active`, or `disabled`. A user may have no password at
  all, when only delegated tokens act for them. *Proposed:* the same email in two accounts is
  two separate users; at sign-in, an address found in several accounts gets an account picker.
- **Verification**: the API call that creates a user chooses how:
  - `email`: Selfcloud sends a link by SMTP, from templates in the user's language;
  - `link`: Selfcloud returns the link or token and the client delivers it;
  - `none`: created already verified. Only an account or admin key may do this, for clients that
    have verified the person themselves.

  Opening the link lets the user set a password and makes them `active`. Tokens are 256-bit,
  single-use, stored hashed, and expire after 72 hours by default. In headless mode the link
  points to a page the client hosts (`verify_url_template`), which calls
  `POST /api/v1/auth/verify`.
- **Folder**: a named top-level root inside an account, holding its own tree.
- **Grant**: attaches a folder to a user at a level: `read` (list, download, preview) or `write`
  (also upload, create, rename, move, copy, delete, edit). Owners reach every folder in their
  account without grants.

## 9. API outline

Everything below is under `/api/v1`. The full contract will be an OpenAPI 3 document at
`api/openapi.yaml`. IDs are opaque strings with a type prefix (`acc_`, `usr_`, `fld_`, `nod_`,
...). Times are RFC 3339 in UTC. Lists use cursor paging: `?limit=&cursor=` in, `next_cursor`
out.

```
System
  GET  /healthz /readyz /metrics           (outside /api/v1)
  GET  /info                               version, features, public_url, limits (max file size, max_chunk_size)

Accounts                                   admin key
  POST|GET              /accounts                           ?external_id= to look one up
  GET|PATCH|DELETE      /accounts/{acc}                     name, quota_bytes, status; DELETE answers 202 and a job
  POST|GET              /accounts/{acc}/keys                secret shown once
  DELETE                /accounts/{acc}/keys/{key}

Inside an account                          account key, admin key, or owner session
  GET                   /accounts/{acc}/usage
  POST|GET              /accounts/{acc}/users               verification: email | link | none; ?external_id=
  GET|PATCH|DELETE      /accounts/{acc}/users/{usr}
  POST                  /accounts/{acc}/users/{usr}/verification     send again, or a new link
  POST|GET              /accounts/{acc}/folders             ?external_id=
  PATCH|DELETE          /accounts/{acc}/folders/{fld}
  GET                   /accounts/{acc}/folders/{fld}/grants
  PUT|DELETE            /accounts/{acc}/folders/{fld}/grants/{usr}   {level}
  POST                  /accounts/{acc}/tokens              mint a delegated token
  POST                  /accounts/{acc}/tokens/revoke       revoke all issued before now
  GET                   /accounts/{acc}/audit
  POST|GET              /accounts/{acc}/webhooks
  PATCH|DELETE          /accounts/{acc}/webhooks/{hook}

Files                                      any caller, limited to what it may reach
  GET    /folders                          folders the caller can reach
  GET    /folders/{fld}/children?path=/a/b list a directory by path
  GET    /nodes/{nod}                      stat: name, size, mime_type, etag, modified_at, sha256
  GET    /nodes/{nod}/children             list a directory by ID (sort, paging)
  POST   /folders/{fld}/directories        {path, parents: true}
  POST   /folders/{fld}/files              small file in one request, or a blank document from a template
  POST   /nodes/{nod}/move                 {folder, parent | path, name, on_conflict}
  POST   /nodes/{nod}/copy                 same; 202 and a job for large trees
  DELETE /nodes/{nod}                      a directory is always deleted with its contents
  GET    /nodes/{nod}/content              download: Range, If-Range, ETag, Last-Modified, ?disposition=inline
  PUT    /nodes/{nod}/content              replace a small file; If-Match required
  POST   /nodes/{nod}/links                signed download link {ttl, disposition}
  GET    /links/{signed}                   follow a signed link (no other auth)
  GET    /jobs/{job}

Uploads (tus 1.0)
  OPTIONS /uploads   POST /uploads   HEAD|PATCH|DELETE /uploads/{upl}

Users                                      session
  POST /auth/login /auth/logout /auth/verify /auth/password/forgot /auth/password/reset
  POST /auth/totp/{enrol,confirm,disable}
  GET  /me

Office editing
  POST /nodes/{nod}/office                 editor URL and WOPI token for the client to open
  /wopi/files/{nod}...                     WOPI host (section 10)
```

**Downloads.** `GET /nodes/{nod}/content` supports single and multiple ranges, `If-Range`,
`If-None-Match` and `If-Modified-Since`, so interrupted downloads resume. The ETag is strong and
changes with every write. `?disposition=inline` is honoured only for types safe to preview
(common images, PDF, audio, video, plain text). HTML, SVG and everything else is always an
attachment. Content responses carry `X-Content-Type-Options: nosniff` and a sandboxing
`Content-Security-Policy`. An operator can also serve file contents from a separate origin
(`content_url`) so a hostile file can never run script next to the interface's cookies. The
same setting lets bulk downloads take a different route from the API, for example around a
CDN whose terms limit large-file traffic.

**Signed links** are HMAC-signed URLs naming one file, a disposition and an expiry (default 5
minutes, capped by config). They exist for `<img>`, `<video>` and plain downloads, where the
browser cannot send an `Authorization` header.

**Small-file edit.** `PUT /nodes/{nod}/content` requires `If-Match` with the ETag the editor
read; a stale ETag gets `412 precondition_failed`, so two editors cannot overwrite each other.

**Errors.** Every error is JSON with a stable code. Clients switch on `code`, not on the HTTP
status:

```json
{"error": {"code": "quota_exceeded", "message": "The file needs 2.5 GB; the account has 1.1 GB free.",
           "details": {"required_bytes": 2684354560, "available_bytes": 1181116006},
           "request_id": "req_..."}}
```

| Code | Status | Meaning, and `details` where useful |
|---|---|---|
| `invalid_request`, `invalid_path`, `invalid_name` | 400 | Malformed request, path or name. |
| `unauthenticated` | 401 | No valid credential; sign in again. |
| `token_expired` | 401 | Delegated token expired; refresh it and retry. |
| `forbidden` | 403 | The caller may not do this. |
| `account_suspended`, `user_not_verified`, `user_disabled` | 403 | Account or user state blocks access. |
| `not_found` | 404 | No such resource, or the caller may not know it exists. |
| `conflict` | 409 | Name taken, duplicate `external_id`, or a concurrent upload. |
| `upload_offset_mismatch` | 409 | `Upload-Offset` differs from the server's. |
| `locked` | 409 | Locked by an office editor (WOPI). |
| `upload_expired` | 410 | The upload expired; start again. |
| `precondition_failed` | 412 | Stale ETag in `If-Match`. |
| `too_large` | 413 | File over the size limit. `limit_bytes`. |
| `chunk_too_large` | 413 | Piece over the limit. `max_chunk_size`. |
| `range_not_satisfiable` | 416 | Requested range outside the file. |
| `rate_limited` | 429 | Slow down; `Retry-After` says when. |
| `checksum_mismatch` | 460 | Piece failed its `Upload-Checksum` (status as tus defines). |
| `internal` | 500 | Server fault; the `request_id` finds it in the log. |
| `unavailable` | 503 | Shutting down or in maintenance; retry after `Retry-After`. |
| `quota_exceeded` | 507 | Account quota. `required_bytes`, `available_bytes`. |
| `storage_full` | 507 | The server's disk is full. |

Messages are English. Clients translate by code; `details` gives them the numbers to fill in.

## 10. Webhooks, office editing and the web interface

These are the last slices, so this is an outline.

**Webhooks.** An account registers endpoints with a URL, a secret and the events it wants:
`user.created`, `user.verified`, `file.created`, `file.updated`, `file.deleted`, `file.moved`,
`upload.completed`, `quota.warning` (at a configurable share of the quota), `quota.exceeded`.
Events go to an outbox table in the same transaction as the change, and a worker delivers them
at least once, retrying with exponential backoff for up to 24 hours. Payloads name the account
and user by ID and by `external_id`. Each delivery carries an event ID for de-duplication and a
signature header,
`Selfcloud-Signature: t=<unix time>,v1=<hex HMAC-SHA256 of "<t>.<body>">`. Webhook URLs that
resolve to loopback, link-local or private addresses are refused unless the operator allows
them, to prevent server-side request forgery.

**Office editing (WOPI).** Selfcloud is a WOPI host for Collabora Online, OnlyOffice or any
other WOPI client: `CheckFileInfo`, `GetFile`, `PutFile`, `Lock`, `Unlock`, `RefreshLock`,
`GetLock` and `RenameFile`. The editor's discovery URL comes from config. Requests are checked
against the editor's proof keys. Locks expire after 30 minutes, as WOPI specifies. `PutFile` is
copy-on-write and honours the lock. The access token is a delegated token with audience `wopi`,
bound to one file. A client calls `POST /nodes/{nod}/office` and embeds the editor URL it gets
back.

**Web interface.** Served at `/` when `ui.enabled`. Screens: sign-in, verification and password
set-up, password reset, TOTP set-up, the file browser (upload with progress and resume,
download, preview of images, PDF, video and text, new folder, rename, move, copy, delete, text
editing, opening in the office editor), and owner screens for users, folders and grants.
Branding comes from config: product name, logo, favicon and colours. Light, dark and
follow-the-system themes. All strings are translated from catalogues; right-to-left languages
are laid out with CSS logical properties and `dir`. It works at phone width. *Proposed* stack:
TypeScript, Svelte and Vite, compiled into the binary, so Node is needed only to build it.

## 11. Data model

| Table | Main fields |
|---|---|
| `accounts` | id, external_id, name, status, quota_bytes, used_bytes, used_files, reserved_bytes, tokens_not_before |
| `api_keys` | id, account_id (null for admin keys), name, prefix, hash, expires_at, revoked_at, last_used_at |
| `users` | id, account_id, external_id, email, display_name, locale, role, status, password_hash, totp_secret (encrypted), verified_at |
| `verifications` | id, user_id, purpose (verify, reset), token_hash, expires_at, used_at |
| `sessions` | id_hash, user_id, created_at, last_seen_at, expires_at, ip, user_agent |
| `folders` | id, account_id, external_id, name, root_node_id |
| `grants` | folder_id, user_id, level, created_by, created_at |
| `nodes` | id, account_id, folder_id, parent_id, name, name_key (for uniqueness), kind, size, mime_type, blob_id, etag, created/modified at and by |
| `blobs` | id, account_id, size, sha256, state, delete_after |
| `uploads` | id, account_id, target (folder, parent, name, on_conflict), length, durable_offset, hash_state, created_by, expires_at |
| `wopi_locks` | node_id, lock_id, expires_at |
| `webhooks`, `webhook_deliveries` | endpoint (url, secret encrypted, events); outbox (event, payload, attempts, next_attempt_at, status, last_error) |
| `jobs` | id, account_id, kind, status, progress, error |
| `audit_log` | id, time, account_id, actor kind and id, action, target, ip, user_agent, outcome |

Every store method takes the account ID and every query filters on it, so one account's data
cannot leak into another's results. Tests check this for each endpoint.

## 12. Operations

- **Config**: a YAML file, overridable by environment variables (`SELFCLOUD_UPLOAD_MAX_CHUNK_SIZE`
  and so on); environment variables alone are enough in Docker. Secrets can be read from files.
  The server refuses to start with an empty or example secret.
- **Docker**: multi-stage build into a distroless image running as a non-root user; `/data` is
  the volume, which should be a local disk. `HEALTHCHECK` runs `selfcloud healthcheck`.
- **Health**: `/healthz` answers while the process is alive; `/readyz` also checks the database,
  that the data directory is writable, and that free disk is above `storage.min_free`.
- **Metrics**: Prometheus at `/metrics`, optionally on its own address: requests by route,
  status and duration, bytes in and out, uploads active, completed and failed, webhook queue
  depth, blob garbage-collection backlog. Per-account figures come from the usage API, not from
  metric labels.
- **Logs**: JSON through `log/slog`, one line per request with its request ID. Passwords, keys,
  tokens, signed-link tokens and file contents never reach the log.
- **Client IP**: taken from `X-Forwarded-For` or a named header such as `CF-Connecting-IP` only
  when the connection comes from a CIDR in `trusted_proxies`; otherwise the peer address.
- **Graceful shutdown**: on SIGTERM the server stops accepting connections, `/readyz` fails,
  requests that still arrive get `503 unavailable` with `Retry-After`, and in-flight requests
  get `shutdown_timeout` (default 30 s). An upload piece still running at the
  deadline is synced and its offset recorded, so the client resumes; nothing half-written is
  ever committed.
- **Backup**: `selfcloud backup --to DIR` writes an online snapshot of the metadata (SQLite
  `VACUUM INTO`, or `pg_dump`), after which `blobs/` is copied with any tool (rsync, restic,
  ...). This is consistent because committed blobs never change, and a deleted blob is removed
  from disk only after `blobs.delete_grace` (default 7 days, to be set longer than the gap
  between backups), so every blob the snapshot refers to is still there while the copy runs.
  Uploads in progress are not backed up. After a restore, `selfcloud fsck` reports missing blobs
  and removes orphans.

## 13. Security

- **Access control** (OWASP A01): one authoriser decides every request from the caller, the
  account and the grants; tests try cross-account and cross-folder access on each endpoint.
- **Cryptography** (A02): argon2id for passwords; SHA-256 for high-entropy keys; HMAC-SHA256
  for tokens, links and webhooks; AES-GCM for TOTP and webhook secrets at rest; constant-time
  comparisons. TLS is expected at the proxy; built-in TLS is available.
- **Injection** (A03): parameterised SQL only; no shell commands; names validated and NFC
  normalised at the API edge; paths parsed into segments, with `.`, `..` and empty segments
  rejected rather than resolved.
- **Design and configuration** (A04, A05): rate limits, enumeration-safe auth answers, secure
  defaults, refusal to start with weak secrets, user content kept from running script.
- **Authentication** (A07): session rotation, optional TOTP with recovery codes, sign-in backoff.
- **Integrity** (A08): signed webhooks, upload checksums, signed release images.
- **Logging** (A09): audit log of who did what, when and from where; no secrets in logs.
- **Server-side request forgery** (A10): webhook URL guard; the editor's discovery URL only
  comes from config.
- **Repository hygiene**: no secrets, hostnames or customer data are committed; a secret scan
  runs in CI.

## 14. Build order

Each slice ships with tests and leaves the server runnable.

| Slice | Contents |
|---|---|
| a | Server skeleton, config, logging, `/healthz`, `/readyz`, `/metrics`, graceful shutdown, Dockerfile, CI |
| b | Accounts, admin and account keys, `admin` bootstrap command, JSON errors, audit log |
| c | Users, verification (`email`, `link`, `none`), SMTP and templates, passwords, sessions, rate limits |
| d | Folders and grants |
| e | File operations: list, stat, mkdir, rename, move, copy, delete, small-file write with `If-Match` |
| f | Resumable uploads (tus), with crash and resume tests and the upload benchmark |
| g | Ranged downloads and signed links, with the download benchmark |
| h | Quotas and usage, enforced at upload creation and while writing |
| i | Delegated tokens and CORS |
| j | Webhooks |
| k | Web interface |
| l | WOPI host |

An API client can switch over once slices a to i work; j, k and l can follow. The OpenAPI file
grows with each slice, and a test checks that every route is in it. The Go client in `client/`
is generated from it.

## 15. Decisions

Decided on approval:

1. **Permission levels**: `read` and `write`. No upload-only or grant-managing level for now.
2. **Grants** attach to whole top-level folders only.
3. **Verification**: an email link, or a link the client delivers. No numeric code.
4. **Public share links, file versions, trash bin**: not in the first release.
5. **On-disk layout**: blobs by ID (section 4).
6. **Delegated tokens**: Selfcloud mints them through the API.
7. **PostgreSQL**: added after slice i.
8. **Web interface stack**: Svelte.

Still open, and not blocking slices a to i:

9. **Licence.** Options: AGPL-3.0 for the server with Apache-2.0 for the Go client (hosted forks
   must publish their changes), or Apache-2.0 or MIT for everything (widest adoption).
10. **Public repository.** Where it lives and when it opens. Until then the Go module path is
    the placeholder `selfcloud`.
