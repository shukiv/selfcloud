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

**Languages.** The web interface and every email Selfcloud sends are available in seven
languages: English (`en`, the default and fallback), Spanish (`es`), French (`fr`), European
Portuguese (`pt`, written for Portugal, not Brazil), Russian (`ru`), and Arabic (`ar`) and
Hebrew (`he`), which are laid out right to left. API error messages stay English; clients
translate them by `code` (section 9). Section 10 says how a language is chosen.

**Non-goals for the first release.** Native desktop sync clients, S3-compatible endpoints,
several servers sharing one data set (high availability), object-store back ends and end-to-end
encryption. The design leaves room for each of them. WebDAV, full-text search, public links,
file versions and a trash bin were non-goals at first; decision 11 (section 15) brings them in,
following `feature-parity.md`.

## 2. Architecture

One static binary, `selfcloud`, with subcommands:

| Command | Purpose |
|---|---|
| `serve` | Run the server. `ui.enabled` decides whether the web interface is served. |
| `migrate` | Apply database migrations (`serve` also does this on start). |
| `admin` | Bootstrap and recovery: create an installation admin key, create an account or an owner user, `recount-usage` to rebuild usage counters from the files and uploads. |
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

**Repository layout** (*proposed*): `cmd/selfcloud`, `internal/{config,store,blob,auth,api,tus,mail,webhook,wopi,audit,web}`,
`web/` (UI source in React 18 with Vite; `web/embed.go` embeds the built files, since Go's `embed` cannot
reach outside a package's directory; `internal/web` serves them), `api/openapi.yaml`, `client/` (Go
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
  keys/                                signing and encryption keys: directory 0700, files 0600
                                       (signing.key, signing.prev.key), unless given by config
  blobs/<account-id>/<b1>/<b2>/<blob-id>     committed file contents, never modified in place
  uploads/<account-id>/<upload-id>           uploads in progress (same file system as blobs/)
  uploads/<account-id>/tmp-*                 small writes before they become blobs; removed after a day
```

A crash can still leave a file under `blobs/` that no row names. The periodic sweep does not walk
the blob tree; a `selfcloud fsck` command (planned) finds and removes such files.

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
- **Blobs are shared and collected.** A copy points at the same blob as its original. A blob no
  file refers to is removed by a background sweep after `blobs.delete_grace`.
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
library, because quota reservation, authorisation, disk checks and error codes have to be
woven into each step.

1. **Create.** `POST /api/v1/uploads` with `Upload-Length` and `Upload-Metadata` (target
   folder, parent path, file name, conflict policy, and `parents=true` to create missing
   directories, so a client can upload a whole tree with a relative path per file). The reply's
   `Location` is an absolute URL built from `public_url`, because browsers may talk to
   Selfcloud directly from another origin. The server checks the caller's permission,
   the name, `upload.max_file_size` (default 100 GiB) and the quota: *used + reserved + length*
   must fit, or the reply is `quota_exceeded`, with its numbers only to the callers section 8
   names. It then reserves `length` bytes against the
   account and creates the upload file. The disk is checked at the same moment, so a full disk
   fails now with `storage_full`, not after 80 GB have been sent: every open upload's unwritten
   bytes must fit in the usable space (free space less `storage.min_free`), and one account's
   unwritten bytes may take at most `upload.max_disk_share_percent` of it (default 50), so one
   account cannot refuse every other account's uploads. An account with no quota may hold at
   most `upload.max_reserved_per_account` (default 1 TiB) in unfinished uploads. Files are not
   preallocated: `fallocate` is not on every file system, and these checks give the early
   refusal it was meant to give. Deferred length is not supported; the size must be declared.
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
   usage, and queue the `upload.completed` webhook (from slice j2; slice j1 adds only the endpoint routes,
   so no event is written yet). The conflict policy is `rename` (save as
   "name (1).ext"), `replace` or `fail`. With `fail`, a name taken after the upload began
   leaves the completed upload waiting, until it expires, to be committed under another name:
   the last `PATCH` answers `409 conflict` with `Selfcloud-Upload-State: awaiting_commit`, and
   `POST /uploads/{upl}/commit` saves it.
   Each principal may hold at most `upload.max_open_per_principal` open uploads (default 100)
   and each account `upload.max_open_per_account` (default 1000); the free-space check counts
   the bytes every open upload has yet to write.
6. **Expire.** Unfinished uploads expire after `upload.expiry` (default 24 h, sent as
   `Upload-Expires`) without a write, and in any case `upload.max_lifetime` (default 168 h)
   after creation, so a trickle of writes cannot hold a reservation for ever. The file is
   deleted and the reservation released.

An upload belongs to its account, user and target, not to the credential that created it: any
later token for the same user with `write` on the target may continue it, so an upload can
outlive many short-lived tokens and survive a page reload.

Only one `PATCH` per upload runs at a time; a second gets `409`. Parallel pieces of one file
(the tus `concatenation` extension) can come later if a single stream proves too slow.
Small files can also be written in one request (section 9).

**Uploads through a drop link (o3).** A visitor of a drop link (section 8) uploads with the same protocol,
under its own bearer credential. The visitor names no folder and no directory: the server places the file in
the link's directory, ignores `folder`, `parent`, `on_conflict` and `mime_type`, refuses `path` and `parents`, and
renames a clash, so the visitor learns no name. The content type comes from the file name. The visitor's
`uploader_name` and `uploader_email` are optional unless the link asks for them; they are stored on the upload, and
the completed file's `node.create` entry names them with `via: share_link`. The limits are the account's quota, the user's own limit when the drop lands in a personal folder (p1),
`upload.max_file_size` and the rate limits of section 8, and at most 20 unfinished uploads per link.

**Storage limits (p1).** An upload's reservation, and each byte it writes, count against the folder its upload names
(the folder of the directory it is aimed at). The account quota checks them as before; a personal folder's user's limit
checks them too (section 8, Storage limits). Completion moves the reservation into usage in the same folder, so it is
not checked again.

A drop upload holds only the bytes it has received (owner's decision of 2026-10-29, "Reserve as received"). Creation
reserves nothing: `upload.max_file_size` still caps the declared length, but no bytes are reserved until they arrive.
Each piece checks the quota and the disk for the bytes it brings, charges them, and is refused when they do not fit,
keeping the bytes already received, so the visitor can resume once space frees. Other uploads keep their up-front
reservation. The refusal a visitor gets is the one in section 8 (`quota_exceeded`, no numbers).

Every write of such an upload checks the link as it is then. A link that is truly gone for the visitor's credential
(deleted, expired, or reset after the credential was issued) ends the upload at once, and its bytes are released. A
link that is only paused (its switch off, a members-only space, or the node trashed) answers `not_found` and keeps the
upload until its normal expiry, so turning the switch back on lets the visitor resume. A directory moved to another
folder takes the finished transfer where it now is.

## 6. Fast-path IO

| Operation | Method | Effect |
|---|---|---|
| Download | `http.ServeContent` over an `*os.File`; Go hands the copy to `sendfile(2)` | Bytes go from page cache to socket without passing through the process. Holds for plain HTTP/1.1 and single ranges; TLS in the process or HTTP/2 means a copy in user space, so TLS is best terminated by the proxy in front. |
| Upload | `pwrite` at the offset into the upload file | No append races; the disk check at creation gives the early disk-full error. The request body is a stream inside `net/http`, so one copy in user space is unavoidable; the network is the limit. |
| Rename, move | Database row update | Instant, whatever the size. Only completing an upload uses `rename(2)`. |
| Copy | The new file entry points at the same blob | Blobs never change, so copies can share one: a 100 GB copy is a database row on any file system. Quota counts each copy in full, as users see them. |
| Delete | Database delete; blob removed later by a worker | Deleting a large tree returns at once. |
| List, usage | Indexed query; usage counters updated in the same transaction as the change | No disk walk, no `du`. |

Recommended file system: XFS (with reflink) or Btrfs on local SSD; ext4 works without
reflinks. A benchmark suite ships with the upload and download slices and measures throughput,
listing latency and the per-piece sync cost, so these choices are checked, not assumed.

## 7. Authentication and authorisation

Four kinds of caller:

| Caller | How it authenticates | What it can do |
|---|---|---|
| **Installation admin** | `Authorization: Bearer sc_admin_...`, or an admin user signed in with a password | Manage accounts and their keys. |
| **Account key** | `Authorization: Bearer sc_acct_...` | Everything inside one account: users, folders, grants, files, delegated tokens, webhooks, audit. |
| **User session** | Email and password (argon2id), optional TOTP; `__Host-` session cookie, `HttpOnly`, `Secure`, `SameSite=Lax` | What the user's roles allow: permissions on nodes, its groups' permissions, and its own personal folder. Account *owners* (a user role) also manage users, folders, groups and permissions in the web interface, and hold manager on every team space. |
| **Delegated token** | `Authorization: Bearer sct_...` | A short-lived, narrowed slice of an account key's or a user's rights, for a browser. |
| **Sign-in link** | A single-use token from `POST /accounts/{account}/users/{user}/sign-in-links`, redeemed at `POST /api/v1/auth/sign-in-link` | A normal user session for one user, with no password and no TOTP. The account's own key makes the link; nothing else can. A browser already signed in as another user is asked before it switches (section 8). |

An installation admin has no direct path to a tenant's files. It reaches them only through an
account key it creates, and that creation is audited.

**API keys** are 256-bit random secrets with a visible prefix. Only a SHA-256 hash is stored (a
slow hash adds nothing for secrets this long), each key is shown once at creation, and keys can
carry an expiry and be revoked. The first admin key comes from `selfcloud admin create-key` or
from a secret file named in the config.

**Delegated tokens** let a client's back end hand a browser direct access, so file bytes never pass through the client's server. The back end calls `POST /api/v1/accounts/{account}/tokens` with its account key and gets back a token scoped by:

- `user`: optional. The token acts as this user, and its rights are the intersection of the token and the user's grants. Without `user`, the token acts as the account key that minted it, with write on the folders it names. The audit log names the acting user or key and records `via: delegated_token` with the minting key.
- `folders`: 1 to 32 folders of the account. A folder outside the list is `not_found`, even where the user may reach it. With `"all": true` and `user` (and no `folders`), the token is *all-reachable*: it reaches every folder the user reaches (team spaces the user holds through permissions, groups or ownership, and the user's My files), and nothing else. The user's own access decides each request, and the token adds only its role cap. All-reachable is asked for, never inferred: a user with no folders and no `all` is refused, and `all` without `user`, or with `folders`, is refused too. Without `all`, `folders` is required.
- `path` or `node`: optional, and at most one. `path` names a directory under the root of the one folder given, so it needs exactly one folder; `node` names a directory ID in one of the folders. The directory is resolved at mint time, inside the mint transaction, and stored in the token as its node ID. The token then reaches that directory and everything below it, and nothing else in its folders. Anything outside that subtree is `not_found`, and a move that would carry an item out of it, or bring one in from outside, is refused.
- `permissions`: `read` (the default) or `write`; write implies read. A token can narrow a role, never widen it. The role a token carries is capped: `read` at viewer, `write` at editor. A token never manages anything, so it never reaches manager (section 8).
- `ttl`: default 15 minutes, at least 1 minute. A longer lifetime than `tokens.max_ttl` (default 1 hour) is refused with the limit in `details.max_ttl_seconds`, not cut short, as for signed links.
- `audience`: `api`. `wopi` is the audience of the editing token of decision 31; only the `/wopi` routes accept it, and the API refuses it.

Only an account key of the same account may mint. An installation admin, a signed-in session and a token itself get `forbidden`, so a token never widens itself. Tokens manage nothing: the management routes (users, folders, grants, groups, permissions, keys, usage, audit, webhooks, minting and revocation) refuse a token as they refuse any caller outside the account. The reply carries the token and its `expires_at`, so the back end can cache a token for most of its lifetime instead of minting one per request. Minting is rate-limited per account key.

*Format.* `sct_` followed by the unpadded base64url of a compact JSON payload, a `.`, and the unpadded base64url of HMAC-SHA256 over the payload bytes. The payload holds the format version, the signing key ID (`kid`), the account, the user (empty when none), the minting key, the folders, the permission, the audience, and the issue and expiry times in Unix milliseconds. An all-reachable token carries `"all": true` with no folders; the server refuses a payload that has `all` without a user, or with folders. A token without the field is a folder-list token, as every token minted before the field existed. A token is at most 4096 bytes; a longer one is refused before anything is decoded.

*Keys.* The MAC key is derived from the signing key of section 9 (`keys/signing.key`, with `signing.prev.key` also accepted) by HKDF-SHA256 with the label `selfcloud token v1`, so a token and a signed link never share a MAC key. `kid` is the first 8 bytes of the SHA-256 of the derived key, in base64url. A token is checked against both keys, so it survives one rotation; once the previous key file is removed, the tokens it signed fail.

*Verification.* In order: the shape and length; the MAC; the payload, parsed strictly (an unknown field is refused), whose `kid` must be the key that verified the MAC; the expiry, which is the one distinct answer, `token_expired`; then one read of the database. The account must be `active`; the minting key must exist, not be revoked and not have expired; a user must exist and be `active`; and the token must have been issued after the account's cut-off. A token that acts as a user must also have been issued after that user's own cut-off. The user's cut-off is read in the same query, so it costs no extra round trip. Every other failure is `unauthenticated` with no detail. So checking a token's signature needs no database read, but revocation needs one. A client refreshes an expired token silently, and asks the person to sign in only when a token is refused. A long upload that outlives its token keeps going: tus-js-client fetches a fresh token from the client's back end when the current one nears expiry (section 5).

*Revocation.* `POST /api/v1/accounts/{account}/tokens/revoke` moves the account's cut-off to now, so every token issued before it stops working; a token issued in the same millisecond is void too. The cut-off only moves forward, so two revocations racing each other cannot reopen tokens. `POST /api/v1/accounts/{account}/users/{user}/tokens/revoke` does the same for one user: it moves that user's own cut-off (`users.tokens_not_before`) to now, so every token acting as that user stops, while the account's other tokens, the user's sessions and the tokens of other users keep working. Revoking the minting key ends its tokens, disabling the user ends the user's tokens, and suspending the account ends all of them until it is reactivated. The account key or a person who manages the account may revoke, and the call is audited (`tokens.revoke`, or `user.tokens_revoke` naming the user). A delegated token is refused both routes, as every management route refuses it. A revocation does not end a signed link that a token made earlier: such a link is a capability that no revocation reaches, and it lives to its own expiry, which is at most the token's remaining life when the link was made, as *Signed links* above already says.

*Signed links.* A signed link is a capability that no revocation reaches, so a link made with a token may not outlive the token: its default and longest lifetime are cut to what the token has left, and a longer `ttl` is refused with that limit in `details.max_ttl_seconds`.

*Visitor credentials (share links, slice o2).* A visitor who opens a link that anyone can open holds a credential, `scl_` followed by base64url(payload JSON), a `.`, and base64url(HMAC-SHA256 of the payload bytes). The payload holds the version, the signing key ID, the account, the link, the node, and the issue and expiry times in Unix milliseconds. Its MAC key is derived from the signing key by HKDF-SHA256 under the label `selfcloud share credential v1`, so it is never a token's or a link's. It lives 15 minutes; `POST /share/renew` swaps a live one for a fresh one. A credential is not a user, a key or a token: it reaches only the routes that allow it (the table in section 8), and every other route refuses it with `forbidden`, `reason: link_route`. Its role and its link's state are read from the database on every request, in one read: a credential issued before the link's `credentials_at` (a reset or a password change), a deleted, paused, trashed or expired link, a team space that pauses it, or a suspended account is refused at once with `not_found`. A forged, malformed or expired credential is `unauthenticated`.
A drop link's credential also carries a visitor ID (`vid`), made at open and kept across renewals; its uploads carry
it, and no other visitor of the link reaches them. Its role is read as none, and its `upload_only` flag is read from
the link's row, never from the payload. A drop credential reaches only the drop routes of section 8, and an access
credential reaches none of them.

*Known gaps, all-reachable tokens.* `GET /folders` leaves out the user's hidden team spaces, as the user's own session does by default, and `?hidden=true` answers an empty list for every token. A hidden space is still reachable by its ID, since hiding changes nothing about access. A signed link made with a token is not ended by a revocation (see *Revocation*).

*Shared limits.* A token without `user` acts as its minting key, so it shares that key's limits: open uploads, upload and link rate buckets, and upload ownership. A client that hands tokens to many people's browsers should mint them with `user`, so each person has their own limits.

*Queued work.* A copy queued with a token checks its caller again when it runs (section 9). The caller it runs for may read the job with `GET /jobs/{job}`, under any credential that is the same user or key, without managing the account. If the account, the minting key, the user or the cut-off says the caller may no longer act, the job fails and writes nothing. Expiry alone does not cancel a job, because the action was allowed when it was asked for.

*Alternative, not built:* the client signs tokens itself with a secret shared with Selfcloud. Selfcloud mints them instead (section 15, decision 6), which keeps key distribution and rotation on the server.

**Tokens stay out of URLs**, with one exception: signed download links (section 9), which have
short lifetimes and are scrubbed from logs.

**CORS.** `cors.allowed_origins` lists the origins whose pages may call the API, as exact origins: `https://app.example.com`, optionally with a port, and never a wildcard or a path. Empty turns CORS off. An allowed origin gets its own origin back in `Access-Control-Allow-Origin`, `Vary: Origin`, and the exposed headers below. Credentials are never allowed.

A preflight (an `OPTIONS` with `Access-Control-Request-Method`) from an allowed origin is answered `204` before routing and authentication, with `Access-Control-Allow-Methods: GET, HEAD, POST, PUT, PATCH, DELETE, OPTIONS` and `Access-Control-Max-Age: 600`. A preflight from any other origin gets `403` and no CORS headers. A plain `OPTIONS` without the request-method header, such as the tus capability probe on `/uploads`, is answered as before. The server's own origin is not a CORS origin.

Allowed request headers: `Authorization`, `Content-Type`, `If-Match`, `If-None-Match`, `If-Range`, `Range`, `Tus-Resumable`, `Upload-Length`, `Upload-Offset`, `Upload-Metadata`, `Upload-Checksum`. Exposed response headers: `Location`, `Upload-Offset`, `Upload-Length`, `Upload-Expires`, `Upload-Metadata`, `Tus-Resumable`, `Tus-Max-Size`, `Tus-Version`, `Tus-Extension`, `Tus-Checksum-Algorithm`, `Selfcloud-Max-Chunk-Size`, `Selfcloud-Node-ID`, `Selfcloud-Upload-State`, `ETag`, `Content-Range`, `Accept-Ranges`, `Content-Disposition`, `Retry-After`, `X-Request-Id`.

Signed links and the content origin get no CORS headers: a link is already a capability for anyone who holds it, and nothing in a cross-origin page needs one. CORS does not weaken the other protections. The `sameSite` sign-in checks, the content headers and the signed-link routes are as they were.

**Cookies never authenticate across sites.** A request whose `Origin` is not the server's own `public_url` origin is refused a session cookie on every route, read or write. Such a page uses a delegated token instead. A request with no `Origin`, from a server or a same-site read, is unaffected.

**Web sessions** are stored server-side (hashed ID), rotated at sign-in, and protected against
CSRF by `SameSite`, an `Origin` check and a token on state-changing requests. Sign-in,
verification and password reset are rate-limited per IP and per email, and answer the same way
whether or not an address exists. argon2id runs with RFC 9106 parameters, and the number of
hashes running at once is capped so a burst of sign-ins cannot exhaust memory.

## 8. Accounts, users, folders and grants

The hierarchy has three levels:

1. An **installation admin** creates tenants (accounts). Admins are admin users who sign in, or
   admin API keys for automation.
2. A **tenant** (an account) creates its own users, its sub-accounts, and shares folders with
   them, read-only or read-write. The tenant acts through its owner users in the web
   interface, or through an account key from its own application.
3. A **user** (a sub-account) must be verified before signing in. It reaches its own personal
   folder, and whatever is shared with it, at the role granted.

- **Account**: a tenant. Has a name, a status (`active`, `suspended`), a storage quota in bytes
  (`0` = unlimited) and usage counters (bytes, files, bytes reserved by uploads). Every write that
  grows usage checks *used + reserved + growth* against the quota in the same transaction, and
  an upload reserves its declared length when it is created, so the counters always match the
  rows. Each folder carries its share of the counters, and the account's are the sum of its
  folders' (slice p1). Growth that lands in a personal folder is also checked against its user's
  limit (`section 8, Storage limits`). A copy counts in full even though it shares the blob. Suspending
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
- **Sign-in links.** A way in for a user with or without a password. An application that holds the
  account's own key (not a delegated token) calls `POST /accounts/{account}/users/{user}/sign-in-links`
  and gets a single-use link, valid for `users.sign_in_link_ttl` (60 seconds by default, at most 10
  minutes). The browser opens it at `users.sign_in_url_template` (default
  `{public_url}/sign-in#token={token}`; the start-up check refuses a template that puts `{token}`
  before a `#`, since a token outside the fragment reaches servers and logs). `POST
  /api/v1/auth/sign-in-link` redeems it: it starts a
  normal session and answers the path to open. The token is 256-bit, stored hashed in
  `sign_in_links`, and consumed in the redeem transaction. The path (`next`) is stored with the token,
  so the link carries only the token. `next` is a path on this site: one leading `/`, no backslash,
  scheme, host, control character or space, no `%2e`, `%2f` or `%5c` (a browser reads `%2e` as a dot,
  so `/%2e%2e//host` would become `//host`), at most 2048 bytes. A replay of a used or expired link
  answers `invalid_request` and writes nothing. **A sign-in link skips TOTP**: the client vouches for the user
  (decided 2026-10-29). The TOTP slice must keep that rule: a password sign-in asks for a TOTP code
  when the user has one, and a sign-in link never does.
  **A browser already signed in as another user is asked first.** When the request carries a live
  session cookie whose user differs from the link's user, and `confirm` is not `true`, the redeem
  answers `409 conflict` with `details.reason` `signed_in_as_other` and `details.current` and
  `details.target`, each `{name, email}` (the display name, or the email when it is empty). Nothing
  is consumed, nothing is deleted and nothing is audited. The page shows the two names and offers
  Switch, which redeems again with `confirm: true`, or Stay, which does not redeem. Staying leaves the
  link unused until its 60 seconds run out; it is not revoked. A browser with no cookie, a cookie for
  no live session, or a session of the link's own user is signed in at once, as before.
- **Folder**: a named top-level root inside an account, holding its own tree. Two kinds:
  - a **team space** (`kind: team`, the default, and every folder made before slice n1). Its name is
    unique in the account. Account owners manage it, and everyone else reaches it through
    permissions.
  - a **personal folder** (`kind: personal`, "My files"). Each user has exactly one, made in the
    same transaction as the user, and at most one per user. Its name key is `/personal/` and the
    user's ID, which a folder name cannot contain (names refuse `/`), so it never collides with a
    team space. The personal folder carries `owner_user_id`.

The rest of this section is the sharing model. Slice n1 built the model (section 15, decisions 17
to 19). Slice n2 adds the controls that sit on it (decision 20): access expiry, no download, editors
who can share, ownership transfer, team-space settings, hidden spaces, access requests and share
emails.

### Roles and permissions

Five roles, one ordered ladder. Each includes everything below it:

| Role | Adds to the role below |
|---|---|
| `viewer` | list, stat, download, signed links |
| `commenter` | comments (slice r; the role is stored and accepted now) |
| `contributor` | create files and directories, upload into a directory, replace a file's content. Nothing is renamed, moved, deleted or copied away. |
| `editor` | every content operation: rename, move, delete, copy |
| `manager` | change the permissions on the node and below it |

On a team space the docs may call `editor` Drive's "content manager"; the API value stays `editor`.

A **permission** gives a user or a group one role on one node (table `permissions`, key node, subject). A permission on a directory reaches the directory and everything below it. Nodes have no stored path: the ancestors come from a recursive walk up `parent_id`.

**Effective role.** A subject's role on a node is the highest role of its permissions over the node and every ancestor, root included. The subject is the user itself, or any group the user is in (a group permission reaches each member at once, and removing a member revokes it at once). Two rules are not stored, and the access code adds them:

- on a team space, an account owner is `manager`;
- on a personal folder, its user is `manager`.

Nobody else gets a role on a personal folder by rule. So an account owner does not reach another user's personal folder unless it is shared with them, but the account key reaches everything, personal folders included.

**Inheritance only adds.** A deeper permission never lowers the role above it. A viewer on the root and an editor on a subdirectory give editor below that directory and viewer everywhere else. There are no "limited access" subfolders.

**Reach.** A node the caller cannot reach at all is `not_found`. A node it reaches at a role too low for the operation is `forbidden`. A folder is reached through its root, so a permission deep in a folder does not reach the folder's root: the root stays `not_found` for that user, and the user sees its shared subtree and nothing above it, with no names of ancestors in the answers.

**Paths.** A path that does not lead to a node the caller reaches is `not_found`, and a missing name and an unreachable one give the same code and message at the same place in the path. A file standing on the path answers `invalid_path` only to a caller who reaches the directory the walk stopped in, since only then does the caller already know what that directory holds; anyone else gets the `not_found` of a missing name. The account key reaches everything, so it is told the path is invalid. A destination (`parent`, `path` or `folder`) the caller does not reach is `not_found` with the same label a missing one gets.

**Operations and the role they need** (each checked again inside the transaction that writes):

| Operation | Needs |
|---|---|
| stat, list, download, signed link | viewer on the node |
| mkdir, create file, upload (create, patch, complete, commit) | contributor on the target directory (the deepest existing directory of the path when `parents` creates the rest) |
| replace a file's content | contributor on the file |
| move or copy with `on_conflict=replace`, and upload completion that replaces | editor on the displaced file, checked inside the transaction (a contributor may place a file but not overwrite one) |
| rename, delete | editor on the node |
| move | editor on the item and contributor on the destination directory |
| copy | viewer on the source and contributor on the destination |
| set or remove permissions | manager on the node, or an account key |

A move into a directory needs only contributor there, since nothing is removed from the destination. The destination is resolved and checked before any name is checked, so a refused role is `forbidden` whatever the name. A queued copy checks the source and the destination again when it runs.

**Tokens.** The role a delegated token carries is capped at editor (write) or viewer (read), and a token never reaches manager. Its node scope (section 7) confines it further. A token does not list permissions at all, since that is a management view. A queued copy job is visible to the token that queued it and to tokens of the same user with the same node scope; another token of the same user, scoped to a different node, gets `not_found` for the job. An all-reachable token reads a job only when the job was queued by an all-reachable token of the same user with the same node scope; a folder-list token of that user does not read such a job, and an all-reachable token does not read a job queued by a folder-list token.

**Permissions are not copied.** A move keeps the permissions of the node it moves, since the node keeps its ID. A copy is a new node and has none.

**Expiry.** A permission may carry `expires_at`. Every read filters it: an expired permission grants nothing. The API sets it (see Access expiry below). **Ownership.** Each node has an `owner_id`: its creator, or the folder's user inside a personal folder. Slice n2 uses it for ownership transfer (see Transferring ownership below).

### Groups

An account has named groups of its users (`grp_` IDs; name unique in the account; optional
`external_id`, with the same idempotent create as folders). A group member may hold any role, and a
group permission is a permission on a node held by every member. A user is removed from a group,
or a group is deleted, and its permissions stop at once. A group's members and permissions are
removed in the same transaction as the group. Only users of the same account may join a group, and
an installation admin may not join one. Group management refuses delegated tokens, as user
management does.

### Sharing a file or folder

`PUT /nodes/{nod}/permissions/{usr|grp}` gives a subject a role on a node. The caller needs manager
on the node, or to be an account key. A caller may not grant a role above its own. The subject must
be a user or group of the same account, and an installation admin cannot hold a permission.
Every change is audited in the same transaction. `GET /nodes/{nod}/permissions` lists the
permissions on the node and above it, each saying where it sits (`inherited_from`). A manager on the
node and the account key see the full chain. A user below manager sees only the rows that name it or
a group it is in, and inherited rows only among those; the other subjects' rows stay hidden, so a
viewer cannot learn who else holds the node. A delegated token gets `forbidden`. `GET /shared`
lists, for a user, the nodes shared with it, each with its role. It leaves out what the user
reaches already: its own personal folder, and any node under a team space whose root it reaches.

### Team spaces and personal folders

Account owners manage team spaces and their members, but not personal folders. `GET /folders` lists,
for a user, the team spaces it reaches at the root, plus its own personal folder. For an owner it
lists every team space. The account key lists every folder, personal folders included, each with
role manager. The management list (`GET /accounts/{acc}/folders`) lists team spaces only.

**Audit privacy.** An audit entry about a node or folder in a personal folder is private to the folder's
user. The store records that user with the entry when it is written (`audit_log.private_user_id`), taken
from the folder, so a move or copy into or out of a personal folder is private too. The account owner's
audit listing and any user's hide such entries unless the viewer is that user; the account key sees every
entry. The filter runs in the query, so a hidden entry never appears in a page.

Deleting a user removes its permissions and group memberships, and its personal folder. A personal
folder that holds any file or directory refuses the delete with `conflict` unless the caller says
`delete_files=true`, since the files are then gone for good. The folder goes through the normal
folder delete, so the usage counters and reservations are adjusted in the same transaction.

### Access expiry

A permission may end on a set date. `PUT /nodes/{nod}/permissions/{subject}` takes `expires_at`,
an RFC 3339 time. It must lie in the future, and at most `sharing.max_expiry` from now. The default
is 365 days, and `0` means no cap. A permission without `expires_at` never ends. Listings return the
time, so a manager can see when access will end.

Reads already filter expired permissions, so access stops at the moment it expires, before any
cleanup. The sweep (section 12) removes an expired row once it has been expired for more than 24
hours, and audits each removal as `permission.expired`. The grace period keeps the row long enough
for the audit trail to show when the access ended.

### No download

An editor sets `no_download: true` on a node with `PATCH /nodes/{nod}`. The flag applies to the node
and everything below it, and a flag on an ancestor applies too. On a team space,
`no_download_for_viewers` applies the same rule to the whole space (see Team-space settings below).

The rule binds only callers whose role on the node is viewer or commenter, the roles that only read.
Contributors and above are unaffected. For the callers it binds, the API refuses:

- a download of the content with attachment disposition, `HEAD` included (`download_restricted`, 403);
- a signed link with attachment disposition;
- a copy of the node, or of a subtree that contains a flagged node.

Inline viewing still works wherever the file type allows inline display (section 9, Downloads). A copy
keeps the flag, so copying a file does not remove the restriction from it.

**This rule deters; it does not secure.** Anyone who may view a file can still see its contents in the
browser, and can copy what they see. The rule closes the easy routes, a download button, a signed link
and a copy. It does not stop a determined viewer. Use it to reduce casual copying. Do not rely on it to
keep a file secret: a file that must stay secret should not be shared with a viewer at all.

### Editors can share

`editors_can_share` lets editors share. A manager sets it with `PATCH /nodes/{nod}`. It is stored on
the node and inherited from its ancestors, and it is off by default, so only managers share unless a
manager decides otherwise.

When it is on, an editor may set and remove permissions on the node and below it, up to the editor
role. An editor may not grant manager. An editor also may not change or remove a row above its own
role, so a manager's row stays with the manager. The default is off because a share made by an editor
is a share the manager did not choose.

A team space's `sharing_managers_only` overrides this switch: when it is on, only managers share, even
where a node says otherwise. A delegated token never sets permissions, so the switch gives no power to a
token.

### Transferring ownership

`POST /nodes/{nod}/transfer-ownership` takes `{to}`, a user of the same account. The caller must be the
item's owner (`owner_id`) or an account key, and must hold editor on the item. The recipient must be an
active user of the same account.

- In a **personal folder**, the item moves, with its subtree and its direct permissions, into the
  recipient's My files. A name clash is resolved by renaming, as a move is. The previous owner gets
  editor on the item, so it keeps its access.
- In a **team space**, only `owner_id` changes, on the item and its subtree. The item stays where it is,
  and the space's permissions do not change.

Usage does not change, since both users belong to one account. An audit entry about an item in a
personal folder is private to that folder's user, so after a transfer the recipient sees the new
entries.

### Team-space settings

A manager of the space root changes these with `PATCH /folders/{fld}/settings`. Managers of a team space
are the account's owners and an account key. A personal folder refuses the call with `invalid_request`,
since its owner is its only manager.

| Setting | Effect |
|---|---|
| `members_only` | A permission below the root may name only a user or group that already holds a permission on the root, directly or through a group the user is in. Anything else is `conflict` (409). |
| `sharing_managers_only` | Only managers may set or remove permissions below the root. It overrides `editors_can_share`. |
| `no_download_for_viewers` | The no-download rule applies to every node in the space, whatever each node's own flag says. |
| `allow_links` | Allows organisation links in the space (slice o1, see Share links). Off by default. Turning it off pauses the space's links at once, and turning it on revives them. |

### Hidden spaces

A user may hide a team space from its own list with `PUT /folders/{fld}/hidden`, and show it again with
`DELETE` on the same path. `GET /folders` leaves hidden spaces out. `?hidden=true` lists only the hidden
ones. Each folder in a listing carries `hidden`. A personal folder cannot be hidden.

Hiding changes nothing about access. The user keeps its role on the space, and the space still works
through its ID. The setting is per user, so one user hiding a space does not change what another user
sees. It only tidies one user's list.

### Access requests

A user who lacks a role on a node can ask for one. `POST /nodes/{nod}/access-requests` takes `{role,
message}`: the role is `viewer`, `commenter` or `editor`, and the message is at most 500 characters.
Only a user may ask. An account key holds every role already, and a delegated token never reaches the
node it would ask about, so both are answered as the endpoint answers everything else: a silent `202`.

The endpoint always answers `202` with no body. It gives the same answer whether the node exists or
the caller can reach it, so it cannot be used to find out which nodes exist. A request is recorded only
when the node exists in the caller's account, the caller is an active user, and the caller's current role
there is below the role asked for. A user with no role at all is exactly who asks, so "no role" counts as
below. A request on a node in another user's My files is recorded too, and mails that folder's owner.

A repeat from the same user while the first is pending collapses into it and changes nothing: the role and
message stay as first made, and no audit entry is written. To ask for more, the user waits for a decision.
A new request within 24 hours of the user's previous one on the same node (pending or decided) is recorded
but not mailed, so a user who asks again in a loop does not mail the managers each time. Requests are rate
limited per user, at 20 an hour (limiter `access.request`).

Access-request mail draws on its own buckets, not the verification buckets: `mail.requests.account`, 20 an
hour per account, and `mail.requests.target`, 5 an hour per address it goes to. When a bucket is empty the
request is still recorded and listed, and only its mail is skipped. A burst of requests therefore cannot
use up the mail a user needs to sign in.

Each requesting user also has an allowance of its own, `mail.requests.requester`: three requests an hour, charged
once per request, before any of its mail goes out. A request to a node with five managers mails all five. A request
over the allowance is recorded and listed, and mails nobody. One user asking over and over therefore uses up only its
own allowance. It takes several users to use up `mail.requests.account`, and a user's request is never silenced by
another user's flood.

A decision stores the role it granted, so a listing shows what was actually given. A denial mail says
that an item was involved and does not name it.

- `GET /nodes/{nod}/access-requests` lists the requests on a node. It needs manager, or editor where
  `editors_can_share` allows sharing.
- `GET /access-requests` lists the pending requests the caller may decide. It reads the oldest 5,000
  pending requests in the account, so a request beyond that point is not listed until the older ones are
  decided. The limit bounds the work of one call.
- `POST /access-requests/{arq}/approve` takes an optional `{role}` and grants that role on the node. The
  approver may not grant a role above its own. An approval only adds access, since the effective role is
  always the highest one held.
- `POST /access-requests/{arq}/deny` closes the request without a grant.

A decided request cannot be decided again: the second decision is `conflict` (409). Every request and
decision is audited. Managers are mailed about a new request: for a team space, the owners and the users
holding manager on the space root; for a personal folder, its owner. The requester is mailed the
decision, and an approval names the role granted.

### Share emails

Three mail purposes exist: `share` (something was shared with you), `access_request` (to managers) and
`access_decision` (to the requester). Each is sent in the recipient's language, in the seven languages
of the verification mail.

`PUT /nodes/{nod}/permissions/{subject}` takes `notify` (default `true`) and `message`, a note of at most
500 characters. The reply carries `notified` and `reason`, so the caller can tell whether mail went out
and, if not, why:

| `reason` | Meaning |
|---|---|
| `not_requested` | The caller sent `notify: false`. |
| `no_recipients` | The subject is not a user of the account, or a group has no active member. |
| `recipient_not_active` | The user is pending or disabled. Only active users get mail. |
| `too_many_recipients` | The group has more than 50 members, so nobody is mailed. |
| `mail_budget` | The send would exceed the mail budget. |
| `mail_failed` | The mail server refused or failed the send. |
| `mail_not_configured` | No mail server is configured. |

A group of 50 members or fewer mails each of its active members. A larger group mails nobody, which keeps
one share from sending a flood of mail. The person who makes the change is never mailed about it. Each
send takes one unit from its own buckets, `mail.share.account` (60 an hour per account) and `mail.share.target`
(10 an hour per address), so sharing cannot use up the mail a user needs to sign in: verification and reset mail
draw on `mail.account` and `mail.target` alone. When a bucket is empty the share is still made; only its mail is
skipped, and the reply says `notified: false` with `reason: mail_budget`. The `/grants` alias routes never
send mail. Mail never includes file contents, and a
name is stripped of line breaks before it reaches a mail header, so a name cannot add headers.

### Grants: the older routes

`/accounts/{acc}/folders/{fld}/grants` and `/accounts/{acc}/users/{usr}/grants` keep their request and
response shapes and status codes. They read and write user permissions on the folder's root: `read`
is viewer and `write` is editor. Listing shows viewer and commenter as `read`, and contributor,
editor and manager as `write`. Only a member can hold a grant; an owner is refused. A personal
folder has no grants and answers `not_found`. Migration 0009 turned every grant into such a
permission, so existing shares keep working.

### Trash

A delete moves the node to the trash. `DELETE /nodes/{nod}` still needs editor. The node keeps its contents,
its permissions and its usage. Only the node itself is marked, with `trashed_at` and `trashed_by`. Its subtree
is trashed through the ancestor walk, so it carries no marks of its own. Its name key becomes a reserved value
that a name can never produce, so a new item may take the name at once; the original key is kept, for a restore.

A trashed node, and everything below it, answers `not_found` on every route a normal caller uses: stat, list,
content, uploads into it, moves, copies, links (including signed links made before the trash), permissions,
`/shared`, jobs, transfers and delegated tokens. The rule sits in the one ancestor walk that access reads
(`nodeAccessWith`), and in each recheck inside a transaction, so no route can forget it. A copy never carries a
trashed node, and an access request for one is not recorded.

Trashed items count toward usage and quota. Only a purge takes them off. Open uploads aimed at a directory in a
trashed subtree are deleted and their reservations released, as deleting that directory did before. A file that a
move or copy replaces with `on_conflict=replace` goes to the trash too, rather than being deleted: it is a
different node from the one that takes its place, and a restore can still reach it.

**Restore.** `POST /nodes/{nod}/restore` brings a trashed node back. The caller must be the one that trashed it, or
hold the manager role on it; the account key may restore anything. The role is worked out as if the node were not
trashed. The caller must also hold editor on the destination, the original parent. If that parent is gone or is
itself in the trash, the node goes to the root of its folder, which needs editor too; without it the answer is
`conflict`. A name the destination now has is taken by renaming, as a copy is: "name (1).ext". Audited as
`node.restore`, which reports `file.restored` for each file it brings back (decision 27).

**Permanent delete.** `DELETE /nodes/{nod}?permanent=true` deletes a live node at once, and needs manager.
`DELETE /trash/{nod}` purges a node already in the trash, with the same role. `POST /trash/empty`, with an optional
`folder`, purges every trashed item the caller manages. It purges at most 1000 items per call, and examines at most
5000 trash roots, the ones it cannot purge included. It replies `{"purged": n, "more": bool}`, with `more` true when it
stopped early, and the client calls again until `more` is false. Each purge is audited as `node.purge`, and takes the item's bytes off usage, its
versions included. The blobs are left for the collector.

**Listing.** `GET /trash` lists the explicit trash roots the caller may restore: those it trashed, and those it
manages. An account key sees all of them. A delegated token sees only what lies inside its folders and node scope.
An owner does not see another user's personal-folder trash. Each item is the node, with `trashed_at`,
`trashed_by`, `original_parent_id` and `purge_at`.

**Retention.** `trash.retention` (default 30 days) is how long an item waits in the trash. The sweep purges the
roots trashed longer ago than that, in bounded batches, under the system actor. `purge_at` is `trashed_at` plus
the retention. The purge checks that inside its own transaction, against the row as it is then, so an item restored and
trashed again after the listing is not purged.

### Versions

Whenever a file's content is replaced, the content it loses is kept as a version. That covers `PUT` content, an
upload completing with `on_conflict=replace` onto an existing file, and a restore of a version. A version keeps the
blob, size, hash and ETag of that content, when it was first written and by whom, and when and by whom it was
replaced.

Versions count toward usage and quota. A replacement therefore grows usage by the full size of the new content, and
the quota check uses that size, not the difference. A restore of a version grows usage by that version's size: the
chosen version stays in the history with its row, label and keep_forever, and its bytes then count as the file's
content as well. The quota is checked for that growth. A completed upload that replaces a file is counted the same way,
once: its reservation becomes the new bytes in one step.

Pruning keeps the number of versions bounded. `versions.max_count` (default 100) limits the versions a file keeps
that are not kept forever; the oldest go first, and a replacement that adds a version applies the limit at once.
`versions.max_age` (default 30 days) removes a version that has been replaced longer ago than that, and the sweep
applies it. A version kept forever (`keep_forever`) is never pruned, and does not count toward `max_count`. Each
pruned version comes off usage, and its blob is collected later, like any other unreferenced blob.

`versions.max_kept` (default 200) caps how many versions of one file may be kept forever. Marking one more answers
`conflict`, with the limit in `details`; unmarking one frees a place. A restore does not add to the count, since the
version it makes current keeps whatever mark it had.

Copies do not copy versions. Deleting a file for good deletes its versions and takes their bytes off usage.

The routes are: the list, `GET /nodes/{nod}/versions`, which starts with the current content (`current: true`,
with the id `current`) and then the earlier versions, newest first (viewer); the download,
`GET /nodes/{nod}/versions/{ver}/content`, which follows the content route in its ranges, validators and
disposition, and honours the no-download rule (viewer); `PATCH` on a version, which sets `label` (at most 100
characters) and `keep_forever` (editor); the restore, `POST /nodes/{nod}/versions/{ver}/restore`, which makes the
version current with a new ETag, keeps the content it replaces as a version, keeps the chosen version in the history,
and needs `If-Match` with the current ETag as a content replace does (contributor); and `DELETE` on a version, which takes its bytes off usage and cannot
be used on the current content, answered `conflict` (editor). Every version route is refused on a trashed node,
checks its role again inside its transaction, and is audited with the folder's user for a personal folder.

A version's blob is referenced by its row, so the collector keeps it while the version exists.

### Undo

The server has no undo API. A client undoes a delete with a restore, which the trash makes possible until the
retention ends. It undoes a move or a rename with the inverse call, sent with `If-Match` set to the `etag` the item got from the
first call. The rename and move routes take an optional `If-Match`: a mismatch is `precondition_failed` and changes
nothing, and the check runs inside the write transaction. Every write changes the node's `etag`, a rename and a move
included, so the token catches a change of name or place as well as of contents. A directory has no contents to tag,
so its `etag` comes from its record (its ID and `modified_at`); a directory's token therefore changes with a rename, a
move or a flag change, but not when something is added inside it. The undo toast of the web interface (slice k2) is
built on these calls.

### Share links

A share link gives access to a file or a folder without a permission row. Slice o1 builds organisation
links, slice o2 builds links that anyone can open, and slice o3 builds drop links (below). The table and the
routes carry every column the links need.

- **The link.** A row of `share_links` (section 11): one access link per node, so `UNIQUE (node_id,
  upload_only)`; an audience (`organisation` or `anyone`); a role (viewer, commenter, contributor or
  editor, never manager, default viewer); an optional expiry; the SHA-256 hash of a 256-bit secret; and
  `credentials_at`, the time the current secret was issued. The URL is `{public_url}/s#{secret}`. The
  secret sits in the fragment, so no server, proxy or `Referer` sees it. It is shown once, in the answer
  that makes the link and in the answer to a reset. Listings and the audit log never hold it. Reset gives the
  same row a new secret, and `credentials_at` moves forward.
- **Not a permission.** A link never appears in `GET /nodes/{nod}/permissions`. Opening it takes a signed-in
  session of the link's own account (the cookie's Origin and CSRF rules apply; a token, an admin, a key and
  a signed-out visitor are refused as dead). `POST /share/open` then records a visit in `share_link_visits`
  and answers the node. The visit gives the link's role on that node and everything below, through the rank
  queries that the access code already runs (`EffectiveRank`, `ChildRanks` and `SharedNodes`). A visitor keeps
  a higher role of its own, since inheritance only adds. The visitor's access is read on every request: a
  deleted link has no visit left, and the other ways of ending a link are checked in the same query.
  A role held through a link opens the content; it never shares, decides requests or makes links. The
  rights to share, decide and grant are read from permissions, groups and the implicit rule only (`nodeSharingAccess`):
  a link editor gets `forbidden` on a permission change, a link, a link change or delete (unless it created
  the link), and on approving or denying a request.
- **The expiry of a link.** An organisation link may have no end date even when the space sets
  `sharing.max_expiry`: the expiry check allows an empty expiry, so that setting does not reach links in o1.
- **When a visit stops counting.** At once, when the link is expired; when it is reset after the visit (a
  visit before `credentials_at` no longer counts, so members open the new URL again); when its node or an
  ancestor is trashed (the access walk, section 8, Trash); and when the team space's policy pauses it. The
  visit is kept, so a link that is turned back on revives its members (an accepted limit, below). A visit
  counts only after the credentials it used were made: a visit at the same instant as a reset does not count.
- **Who may.** Creating, changing, resetting and deleting a link needs the right the caller has to share the
  node (`mayShare`: a manager, or an editor where `editors_can_share` allows and the space does not keep
  sharing for managers). The role may not be above the caller's own, and a change may not touch a link above
  the caller's role. The link's creator may always delete it, while it still reaches the node. A delegated
  token is refused on every link route. A caller that does not reach the node gets `not_found`.
- **Policy.** A team space needs `allow_links`, and with `members_only` a link is allowed on the space's root
  only, both at creation and at each use. A personal folder (My files) always allows an organisation link. The
  refusals are `forbidden` with `reason: links_off` and `conflict` with `reason: members_only`. A second link on
  a node is `conflict` with `reason: exists`.
- **The account switch.** `accounts.public_links` (off by default) is set by the account's owner or key, with
  `GET` and `PATCH /accounts/{acc}/sharing-policy`. It is enforced for links that anyone can open at each use, so
  turning it off pauses them at once and turning it back on revives them. Organisation links do not need it. It is
  audited as `account.sharing_policy`. `GET /me` answers `public_links`. `GET` and `PATCH` also answer
  `anyone_links`: how many of the account's links that anyone can open have not ended, live or paused by the switch.
  The owner's switch shows that count beside it.
- **Listing.** `GET /nodes/{nod}/share-links` lists the links on the node and above it, the node's own first,
  each with `inherited_from`. A sharer (a manager, or an account key) sees every live link with its creator,
  its visitor count and whether policy pauses it. Anyone else sees only the live links on nodes it reaches.
  Expired links are not listed.
- **Account list and bulk delete (o4).** `GET /accounts/{acc}/share-links` lists every live link of the account, oldest
  first, with its audience, role or drop, end, `paused`, `password_set`, creator, visitor count for organisation links,
  folder (a team space's name, or a personal folder's owner), and the item's name and path. It takes `?audience=` and
  `?kind=access|drop`, pages with a cursor, and is open to the owner's session, the account key and an installation admin
  (managed-account rules: a member and a token get `not_found`). Decision 11 of the owner (2026-10-29) applies: an owner
  sees the items in other members' My files without their name or path, and may still delete those links; an admin sees
  no item name or path at all; the account key sees every item. `POST /accounts/{acc}/share-links/delete-public` deletes
  every anyone link of the account, access and drop links alike, releases their open uploads, audits each link as
  `link.delete` in its folder and one `link.delete_public` entry with the count, and answers `{deleted}`. The web asks for
  confirmation first; the route does not.
  - `DELETE /share-links/{shl}` now also lets the owner's session and the account key delete any link of the account,
    including one in a member's My files and one on a trashed item. Members keep the old rule, and an admin uses the bulk
    delete only.
  - A child of a folder listing carries `has_link` when it has a link the caller may know of: every live link for a sharer
    (a manager or the account key), and for anyone else the access links the team space's policy lets through and the anyone
    links while the switch is on. A visitor never gets it. One query reads a listing's links; no query is made per row.
- **Links that anyone can open (o2).** The audience `anyone` is a link whose secret opens it for whoever holds
  it, with no session. It gives the viewer role only, and it always ends: 30 days by default, 365 days at most,
  and `sharing.max_expiry` lowers both when it is set. Making, changing, resetting or deleting one needs the
  node's manager (an editor with `editors_can_share` is refused, and so is a link-only role); a delegated token is
  refused. It needs the account's `public_links` switch on, and `allow_links` on a team space; in My files only
  the switch. Its password, when set, is hashed with argon2id like a user's, may be 8 to 256 characters, and is
  never shown back: a listing says only `password_set`. A new or removed password, and a change of audience, end
  the visitors' credentials issued before it.
  `POST /share/open` takes `{secret, password}` and answers `{token, expires_at, link, node}`. A missing password
  is `unauthenticated` with `reason: password_required`, and a wrong one `password_wrong`; neither the missing nor
  the wrong one counts toward a lock, but a wrong one does count toward the limits below. An open is audited as
  `link.open` with the actor `share_link` and the client address, at most once per 15 minutes for each link (see Audit), and it names the link, never its secret.
  - Ten wrong passwords from one address on one link in 15 minutes lock that address out of that link for 15
    minutes (`rate_limited`, audited once as `link.throttled`). Other addresses are not affected.
  - Thirty wrong passwords on a link in an hour make every attempt on it wait two seconds, the right ones too. Nothing
    is refused, so a holder of the secret cannot shut the link for its visitors.
  - `share.open.ip` (30 a minute per address) is charged before any read, as for organisation links.
  - An address is its IPv4 address, or for an IPv6 client its /64, in the lock and in `share.open.ip`, so one host
    cannot shed a lock by changing its low bits. `clientKey` is unchanged for the other limits.
  - One password check runs at a time per link. The check's turn is held through the lockout check, the slowdown,
    the verify and the recording of the outcome, so no two guesses race each other. Once the link's budget is spent,
    that gives one guess per two seconds per link.
  - The turn is bounded. A locked address is refused before it takes or waits for the turn. One request per address
    may hold or wait for a link's turn; a second from the same address is `rate_limited` at once. A waiter gives up
    after 10 seconds with `rate_limited` and `Retry-After`. A holder of the secret who floods a link therefore slows
    its real visitors for at most those bounds, and cannot queue them behind an unbounded line. Under a distributed
    attack, real visitors of a password link may be asked to retry; resetting the link ends the attack.
  - Each per-address map holds at most 100000 keys, and the per-link timestamps at most thirty. A full map is swept
    at once; if it is still full, the new address is not recorded. The link's budget and the turn still apply.
  - The limits are kept in memory: a restart clears them, which only shortens a lock.
  - A link under a trashed ancestor is dead, as a trashed node is. The check is made before the password, so every
    guess gets the same `not_found`.
  - A switch from an organisation link to anyone pulls an end set earlier back to the anyone cap, and audits the
    end in `link.update`. An end no later than the cap is kept.
  - A visitor's signed-link budget (`links.create`) is per visitor and client address, not per link, so one visitor
    cannot use it up for everyone on the link. A visitor's stat of the link's node has `folder_id` blank and
    `editors_can_share` false.
  `POST /share/renew` takes the credential as its bearer and answers a new one, if the link is still live. The
  renewal reads the link and writes the new credential in one transaction, and refuses (`not_found`) a credential
  issued at or before the link's current `credentials_at`. It never moves the issue time forward, so a reset that
  lands between the credential's check and the renewal ends the credential. The page renews two minutes before the credential ends, and reopens with the secret (and the password) when renewal
  fails.
  A visitor reaches only these routes, marked with the route flag `link` in the route table, and nothing else:

  | Route | Visitor may |
  |---|---|
  | `GET /nodes/{nod}` | stat a node the link reaches, with the link's role; no `created_by`, `owner_id`, or the link root's `parent_id` |
  | `GET /nodes/{nod}/children` | list a directory the link reaches |
  | `GET` and `HEAD /nodes/{nod}/content` | read a file the link reaches; the no-download rules apply, as to a viewer |
  | `POST /nodes/{nod}/links` | make a signed link to a file, capped at the credential's remaining life; not audited one by one |
  | `POST /share/renew` | renew its own credential |

  The route table decides it: a route without the flag refuses a visitor, so a route added later is closed to visitors
  until it says otherwise. The test enumerates the table and fails on any mismatch.
- **Open answers.** Every dead link gives one answer: `not_found`. That covers a missing link, an expired,
  deleted, paused, trashed or reset one, another account's, and every caller that is not a signed-in member of
  the link's account. The route checks its own session, so its answer comes from the handler and not from the
  router's 401. `POST /share/open` is limited per client address (30 a minute, `share.open.ip`) and per member
  (60 a minute, `share.open.user`), after the session is read. The open re-reads the link inside its write, so a
  link deleted or reset between the two reads is the same `not_found`.
- **Audit.** `link.create`, `link.update`, `link.reset`, `link.delete`, `link.open` (a member's first visit, or
  one more than 24 hours after its last) and `account.sharing_policy`. Each names the link's ID, never its secret or URL.
  An anyone link's `link.open` is written at most once per 15 minutes for each link (`throttleAuditGap`); the open
  itself always runs, and re-reads the link. An entry about an item in My
  files is private to that folder's user, as every entry about a personal folder is.
- **Web.** The share dialog's "General access" section makes and changes the link, and shows its URL once
  (with "Get a new link" to replace it). A member who opens `/s#secret` is taken to the item, which replaces the address
  and the secret in it. The page reads the secret from the fragment and holds it in memory. A signed-out visitor
  of a link that needs sign-in goes to sign-in: the page then drops the fragment from the address and keeps the
  secret in this tab's session storage, which it clears once the open is made or fails, or ten minutes after it
  was stored, and comes back to the page with the secret still waiting. A dead link's fragment is dropped too. A
  link that anyone can open keeps its fragment in the address, so a reload opens it again. The team-space switch "Allow links" is live.

- **Drop links (o3).** A drop link (`upload_only`) lets whoever holds its secret upload files into one directory,
  and nothing else. It is an anyone link with no role, one per node beside the node's access link (`UNIQUE (node_id,
  upload_only)`). Its rules are those of an anyone link, with these differences:
  - **Who and where.** Only the node's manager may make, change, reset or delete it: never an editor through
    `editors_can_share`, never a role held through a link, never a delegated token. It is for a directory only.
    It needs the account's `public_links` switch, and `allow_links` on a team space; a members-only space admits it
    on its root only; in My files the switch alone decides.
  - **It must end (decision 9 of the owner, 2026-10-29).** Its end is 30 days from creation by default, at most 365
    days, and lower when `sharing.max_expiry` is lower. `expires_at: null` is refused. Reset and password changes
    work as for anyone links.
  - **Anonymous only (decision 10).** Audience `anyone`. There is no members-only drop link.
  - **Limits (decision 3).** No per-link size or count caps. The account quota, `upload.max_file_size`, the rate
    limits (per address 30 uploads an hour, `drop.ip`; per link 300 an hour, `drop.link`) and at most 20 unfinished
    uploads per link (across its visitors, `too_many_uploads` with `scope: link`) are the only limits.
  - **Bytes held (owner's decision, 2026-10-29).** A drop upload holds only the bytes it has received (section 5).
  - **Refusals (item 2).** When the account's quota or the disk cannot take a drop visitor's bytes, the visitor gets
    one generic answer: `quota_exceeded`, with the message "this link cannot take the file now". It carries no
    numbers and no scope, and it is the same for a full quota and a full disk. The owner keeps the detail in the
    audit log.
  - **Visitor.** `POST /share/open` answers `{token, expires_at, upload_only: true}` and names no node or link. The
    visitor's credential is marked upload only; it reaches `POST /uploads`, `HEAD`, `PATCH` and `DELETE /uploads/{upl}`,
    `GET /share/info` (`{title, message, ask_name, expires_at, max_file_size}`) and `POST /share/renew`. It reaches no
    node: `GET`, listings, content, signed links, permissions and links answer `link_route`, and the node access walk
    answers `not_found` for it. A commit is never needed, since a drop upload takes a free name.
  - **Visitors are kept apart.** Every visitor of one link shares the link's principal, so each upload records its
    visitor's ID, and `HEAD`, `PATCH` and `DELETE` of another visitor's upload answer `not_found`.
  - **Owner.** A file a visitor sends is owned as the creator's files are: by the creator user, or by the account key
    when the key made the link (the same owner a key-made node gets). A file in a personal folder belongs to the
    folder's user, as the store gives every node there. The upload's `created_by` is the link's ID. The completing
    piece does not send the new node's ID to the visitor (`Selfcloud-Node-ID` is for the owner's requests only).
  - **Notification.** When `notify` is on, the creator user (never an account key) is mailed at most once an hour
    per link: "N files arrived in {folder}", in the seven languages, from the `mail.drop.account` and
    `mail.drop.target` budgets. Files that arrive inside the hour are counted, and mailed once the hour ends, by a
    loop that runs every minute. The creator gets no mail if it is no longer an active user or no longer a manager of
    the item; its files are then not reported again. A mail that does not go (a send error or a refused budget) gives
    its files back to the count, and the link is not tried again for an hour, so a failing mail server is not hit
    every minute.
  - **Audit.** `link.create`, `link.update`, `link.reset` and `link.delete` carry `upload_only` and the drop fields
    that changed; never the secret, the password or an uploader's email. `upload.create` and `node.create` carry
    `via: share_link`; `node.create` also carries the uploader's name and email. A drop link's opens use the same
    throttle as an anyone link's.
  - **Uploader fields.** The name and email a visitor types are visitor-supplied and unverified. Before they are
    stored or audited, every control character (Unicode category Cc, including line breaks and NUL) and the Unicode
    line and paragraph separators (U+2028, U+2029) become a space (a CR LF pair becomes one space), and the ends are
    trimmed. The email must then be a
    bare address (`net/mail`). They are kept in the audit log as long as that log is kept, as the entries are.
  - **Route allowlist.** A route admits a visitor by kind: `link` for an access link, `drop` for a drop link. A route
    with neither refuses both, so a route added later is closed to both. The route-table walk checks both allowlists.
  - **A drop link's `role` is empty** in the API; `upload_only` says what it is.
  - **Deleting or resetting a link ends its open uploads.** Both release the uploads' rows, bytes and files in the
    transaction that changes the link, not at the visitor's next request.

### Storage limits (p1)

Each user's My files may have a limit of its own (`users.quota_bytes`). A null limit follows the account's default
(`accounts.user_quota_bytes`, 0 by default, which means no limit), and 0 means no limit. The owner, the account key and
an installation admin set both: `PATCH /accounts/{acc}/users/{usr}` with `quota_bytes`, and
`PATCH /accounts/{acc}/storage-policy` with `user_quota_bytes`.

- **Charged by folder.** A byte counts against the folder it lands in. Bytes in a personal folder count against its
  user's limit as well as the account quota. Bytes in a team space count against the account only, until slice u.
  A drop upload counts where it lands.
- **Checked first, and only growth.** The account quota is checked as it always was. Then, for a personal folder that
  grows, `folder used + folder reserved + growth` must fit the user's limit. A change that shrinks usage, or that moves
  bytes between two folders of the account, always passes the account check; a move is refused only when the destination
  user's limit would be passed.
- **Who learns the numbers.** A `quota_exceeded` refusal with `details.scope` `user` carries `required_bytes` and
  `available_bytes` only to the user the folder belongs to, and to the account key. Anyone else (a contributor in a
  member's My files) gets the refusal with no numbers. A drop visitor gets the one generic refusal of section 8. The
  account's own refusal (scope `quota` or `account_reservations`) carries its numbers and its scope only to an account
  owner in a session, an installation admin and the account key. A member, a link or drop visitor and every delegated
  token (including one acting for an owner) get the same code with no details.
- **Moves and transfers.** A move across folders moves the subtree's bytes, files and reservations with it, and the
  destination user's limit is checked. An ownership transfer out of one My files into another is checked the same way.
  Deleting a user with `transfer_to` moves its files and is never refused.
- **Lowering a limit** below current use keeps the files; new growth is refused until usage drops.
- **Reservations** belong to the folder of the directory their upload is aimed at. A move of a directory moves the
  uploads aimed into it, so their reservations move with it.
- **Storage routes.** `GET /me` carries `storage` for the caller's own My files, against its limit. `GET /me/storage/largest`
  lists the caller's largest files. `GET /accounts/{acc}/storage` gives the account's totals, its folders and, for an
  owner or the key, the users paged by use. `GET /accounts/{acc}/storage/largest` lists the largest files of team spaces
  (owners) or of any folder (the key); an owner never sees a file of a member's My files, and an admin sees no names.
  Tokens are refused on every account route (`ManagesAccount`), and link visitors are refused by the route table.

### Stars, recent and Home (p2)

Stars and recent items belong to one user, and no route reads another user's (the account key, the installation admin
and the owner included). Only a signed-in user, and a delegated token acting as its user, reach them; the key and an
admin get `forbidden`, as `/shared` gives it, and a link visitor is refused by the route table. Nothing here is audited.

- **Stars.** `PUT /nodes/{nod}/star` needs the caller to reach the node at any role, checked again inside the write, and
  a delegated token needs write as well. A star past 5000 is `conflict` with `details.reason` `limit`. `DELETE` removes
  the signed-in user's own star with no reach check, and answers 204 for any well-formed node ID, so a star on a trashed
  or unreachable node can be removed and stops counting toward the limit; a token acting for the user needs write and
  reach. A read-only token is refused on star and unstar. A star on a trashed or unreachable node stays in the database
  and is not listed; it shows again when the node is restored or the grant returns. `starred`
  is a field of the node (stat, folder listings and `/shared`), set for the caller's own star and absent for a key,
  an admin and a visitor.
- **Reach in one query.** Every list that shows nodes to a user reads their caller's role on the whole page in one
  recursive query (`store.ReachRows`). It applies the rules of the per-node check (`nodeRoleIn`): the permissions and
  groups of the node and its ancestors, live organisation links the user has visited, the implicit manager rule, the
  trash of the node and its ancestors, a token's folder list, its node scope and its role cap. `reachRoles` applies
  them, and a property test (`TestReachRowsMatchPerNodeCheck`) checks the two over random trees, grants, trash, links
  and tokens, so they cannot drift apart. `/shared` reads its roles this way too.
- **Recent.** An entry records the caller's last view (`viewed_at`) and last edit (`edited_at`); `last_at` is the later
  one and orders the list. Views are recorded at `GET /nodes/{nod}/content` and `POST /nodes/{nod}/links`, for a
  signed-in user acting for itself, never at `HEAD`, never for a token, a key, an admin or a visitor, and never for a
  directory. Edits are recorded in the transaction of each write that changes a file's contents: a file made, an
  upload completed (its replacement or its new file), a `PUT` of the content, and a version restored. They are
  recorded for any user principal, a token acting as its user included. A rename, a move and a label change record
  nothing. A copy made by a user is an edit too: the new copy lands in the copier's Recent, in the request and when a
  queued copy job runs, by user principals only, in the copy's transaction (owner decision, 2026-10-30). `DELETE /recent`
  clears the list, and `DELETE /recent/{nod}` removes one entry (204, whatever the reach). Neither is open to a token,
  of any scope or permission: the list is the signed-in user's own.
- **View recorder.** A view is taken off the request path. An in-memory gate lets one user see one file at most once in
  10 minutes, and holds at most 100000 keys; a full gate drops the view. A buffered channel of 4096 holds the views
  waiting to be written, and a view that finds it full is dropped, with its gate mark undone. A writer goroutine writes
  them every 2 seconds or every 256, in one transaction of upserts whose times only move forward, so a batch that
  arrives out of order keeps the later times. Serve flushes the last batch before it returns, while the store is open.
- **Sweep.** The sweep removes recent entries last used more than 30 days ago, and then all but the 500 latest of each
  user.
- **Home** (`/`, decision 9) is built on the public API alone: Recent, Starred and Recently shared (`/shared?order=recent`,
  8 each), the access requests the caller may decide, and a storage notice at 80% of its limit from `GET /me`.
  `/files` stays My files.
- **Pages of stars and recent items.** A page is read at the requested size and then listed after the reach check, so a
  page can come back shorter than its limit, as `/shared` does, and `next_cursor` still follows the rows read.

### Shortcuts, colours and descriptions (p3)

**Shortcuts.** A shortcut is a node of kind `shortcut`, with size 0. It points to a file or a directory of the same
account through `target_id`. It grants nothing: opening it reads its target under the caller's own access, and the
access rules never read `target_id`. A shortcut has no content and no children, and it cannot be a parent: nothing is
made, uploaded or moved into it. It takes no quota, since the counters count `kind = 'file'` only.

- A target the caller cannot reach, whether missing, trashed, moved out of reach, purged, or in another account, gives
  the same answer: `not_found` on creation, and on a read `unavailable: true` with no `target`. The target's ID is
  never shown to such a caller.
- A target that is a shortcut, or a folder's root, is `invalid_request`.
- A target trashed or purged changes what the shortcut shows, and a purge sets `target_id` to NULL.
- A copy of a shortcut is a new shortcut to the same target. A copy of a tree copies its shortcuts, never their targets.
- A permission on a shortcut shows it to the person it names, and grants no reach to the target.
- A shortcut cannot be shared by link. Its target can.
- A link visitor's stat of a shortcut is `not_found`, as for an item outside the link. Its listing leaves shortcuts out.

**Colours and descriptions.** A directory's colour is one of the twelve palette names (`store.Colors`). A description is
plain text of at most 1000 characters; control characters other than line breaks are removed, and it is stored and sent
as text. Editors set both with `PATCH /nodes/{nod}`, and every signed-in reader sees them. A link visitor never does: a
description may hold notes the sharer did not mean to publish. A copy
keeps both.

**Rebuild and operators.** Migration 0019 rebuilds the `nodes` table, because the kind CHECK must change. The runner
runs that migration with foreign keys off (the directive on its first line), so the rebuild does not cascade into
permissions, uploads, versions, share links, access requests, stars or recent items. The migration runs in one
transaction, runs `PRAGMA foreign_key_check` before the commit, and rolls back on any violation. Foreign keys are on
again afterwards. The rebuild takes time in proportion to the number of nodes. **An operator must back up the database
file before starting a version with migration 0019**: the server does not make a backup, and a failed migration rolls
back, but a backup is still the only recovery from a bad disk or an interrupted copy.

**Kind comparisons.** Every comparison of a node's kind in `internal/api` and `internal/store` is listed in
`TestNodeKindComparisonsAreReviewed` (`internal/api/kind_guard_test.go`), with its count per file. A new comparison
fails that test until a reviewer decides it. Where a check accepts only a file or a directory, a shortcut is refused
(fail closed), as the decision list in section 15 (decision 24, p3) records.

### Labels (p4)

**Fields.** The account owner, or the account key, defines a field with a name, a type (`text`, `number`, `date`,
`user` or `choice`) and a restriction. The names are checked as file names are. An account holds at most 50 fields,
and a choice field at most 100 choices, archived ones included. Choices are archived, never deleted, so a value that
uses one keeps its meaning. The type of a field that holds values cannot change (`conflict`, `reason` `type_locked`).
Every user of the account, the account key and a delegated token read the field list, restricted fields included.

**Values.** Each node holds at most one value per field. Editors set and clear values with
`PUT` and `DELETE /nodes/{nod}/labels/{lbf}`. A restricted field is set only by a manager of the node, and read only by
a manager. A token has editor at most, so it never sets or reads a restricted value. A link visitor reaches no label
at all: the route table refuses it on each label route, as it refuses any route that does not admit links.

**Copies and moves.** A copy takes the values of its source, in one statement inside the copy's transaction. A copy
carries a restricted value only when its caller is a manager of the source. Any other caller's copy keeps the
unrestricted values and drops the restricted ones, without an error. The rule reads the caller's role on the source
root, inside the write, for a copy made in the request and for a queued copy alike. A move and the trash keep the
values, because a node keeps its ID. A move may not take an item that holds a restricted value anywhere in its subtree
out of its folder, or to a place where the caller would manage it, unless the caller manages the item. The refusal is
`forbidden`, `reason` `restricted_label`, and it says that a restricted value is there. That is the one fact it
reveals. A move that stays in its folder with no higher role, and a move of an item with no restricted value in its
subtree, are unaffected. A purge removes the values with the node.

**What a label change does not do.** It does not write the node's own row, so the modification time and the ETag do
not change. It is not a webhook event (design-j-proposal section 10), and each of its audit actions is classified in
`webhookAuditNoEvent`. Nor is it an edit in Recent, a rename or a move.

**Measured.** `BenchmarkCopyLabelValues` (`internal/store/labels_bench_test.go`) times the one statement that copies
the values: 10,000 nodes with two values each took about 355 ms of writer hold on the shared machine (the average of
three runs with `-benchtime 3x`). The rest of the copy is unchanged, and a copy with no labelled field in the account skips the
statement. A copy above 10,000 nodes runs as a job in phases (decision 34): each batch is one write, and the publish is one write.

**Measured publish of a staged copy, bare move (`BenchmarkCopyPublish`, `internal/store/copy_publish_bench_test.go`).**
The benchmark builds the database the way a real copy leaves it: the source tree is in it too (its nodes and labels),
and the staged rows carry derived IDs and are written in a shuffled order. The timed write is the one `UPDATE` of
`folder_id` that moves the staged rows, with the root update, the reservation and the audit. Three runs per size, under
`/var/tmp`, on a shared machine:

| nodes   | hold, run 1 | run 2   | run 3   | median  |
|---------|-------------|---------|---------|---------|
| 20,000  | 268 ms      | 341 ms  | 370 ms  | 341 ms  |
| 50,000  | 1,405 ms    | 1,268 ms| 1,256 ms| 1,268 ms|
| 100,000 | 2,826 ms    | 3,380 ms| 5,671 ms| 3,380 ms|

The earlier version of this benchmark (sorted IDs, no source tree in the database) held 1.4 to 2.1 s at 100,000. It
understated the move, which is why it is replaced. Which of the two differences (the source tree, or the order of the
IDs) costs the most was not isolated.

**Measured publish of a real chunked copy, end to end (`TestMeasureChunkedCopyAt100k`, decision 34).** The longest write
of each phase, with three runs per size (`MEASURE` lines, `internal/api/copy_measure_test.go`). The publish is one write;
`batch` is the longest of a copy's batch writes; the rest is the snapshot and the cleanup.

| nodes   | publish, runs 1 / 2 / 3       | worst publish | longest batch, worst run | overall worst |
|---------|-------------------------------|---------------|--------------------------|---------------|
| 20,000  | 0.46 s / 0.90 s / 0.88 s      | 0.90 s        | 0.71 s                   | 0.90 s        |
| 50,000  | 1.22 s / 1.44 s / 1.56 s      | 1.56 s        | 1.03 s                   | 1.56 s        |
| 100,000 | 7.06 s / 7.87 s / 4.16 s      | 7.87 s        | 1.31 s                   | 7.87 s        |

The machine's load varied between the runs (load average 4.5 to 16), and the spread is large at 100,000, so three runs are
the least a rule can rest on, not a precise figure. The publish is the longest write at every size; the batches stay
between 0.3 s and 1.3 s, under the 5 s rule, though above the 400 ms the batch sizer aims at (a batch holds the writer
for its whole write, and the sizer reacts after the fact). Before the review fixes the same measurement gave 2.39 s,
2.84 s and 3.01 s at 50,000 (worst 3.01 s) and 3.22 s, 4.96 s and 6.54 s at 100,000 (worst 6.54 s). The load was not the
same in the two matrices, and the cause of the difference was not isolated; the 100,000 worst case is over 5 s in both.

The rule (the largest measured size whose worst longest write is at most 5 s) gives 50,000 as the default. The bare move
above is 1.3 to 3.4 s (median) at 100,000, so the checks and reads around the move account for part of the 4 to 8 s of
the real publish; the reads before the move were measured at about 0.4 s in one run, and the snapshot check added in
the review fixes (`CopySnapshotChanged`, one join over the snapshot rows) was not timed on its own.

**Measured for the default of 20,000 before chunking (`BenchmarkCopyHold`, `internal/store/copy_hold_bench_test.go`). The default is now 50,000 (decision 34).**
The benchmark times one `store.Write` that does what `copyTree` does: it reads the subtree, inserts every node, and,
with labels, copies one text value per node (`CopyLabelValues`). It ran on a database under `/var/tmp`, ext4 on the
NVMe disk (not tmpfs), on the shared machine (8 cores, load average 5 to 7). Eight runs without labels held the writer
for 2,273 to 4,546 ms (median 2,842 ms). Eight runs with a text label on every node held it for 2,690 to 4,927 ms
(median 4,378 ms). The label difference is not isolated: three labelled runs were taken earlier, under load near 10, and
held 2.7 to 3.2 s; the five slower ones came later, so run order and load are mixed in with the label cost. The worst measured hold, 4.9 s, is about half the 10 s busy timeout. The owner said "comfortably
under": we read that as a 2x margin, which this meets, but the owner decides. The default later became 50,000, with chunking (decision 34).
The 200,000-node numbers below are kept for history.

**Measured after the review (`copy.max_nodes`, 200,000 nodes, one write).** A copy of a folder of 200,000 nodes inside
one write took 39.5 s with no label field in the account, and 75.4 s with a labelled tree (a text field on every node).
The writer hold is above the 10 s busy timeout in both cases, so another writer waits out the copy and can fail with
`SQLITE_BUSY`. The difference of 36 s is the label statement and the work around it, measured as two whole runs and
not isolated; the benchmark above suggests a smaller share, so this number is to be confirmed. This was measured before the copy was chunked (decision 34), so these figures are history. The move guard's subtree walk
(`SubtreeHasRestricted`) took 161 ms over 200,000 nodes in its worst case (no restricted value, full walk). These numbers
were taken on the shared machine with the test databases on the home disk.

**Interpretations made in p4, for review (the owner has not decided them):**
- A visitor through a share link sees no labels, on any of the four routes. This is the proposal's reading of section
  6 ("link visitors reach none of this"), and the tests hold the refusal to the route table's own answer.
- The API accepts values on a shortcut's own row, because the rule does not single out a kind. The interface shows no
  labels for a shortcut, so the target's labels never travel through it.
- A value is refused, not trimmed. A text value holds no control character and no bidi embedding or isolate
  character, and it is at most 500 characters. Descriptions are trimmed of these characters; a label is not, so the
  text the reader sees is the text the editor typed.
- A user value stores the member's ID. A deleted member's values go with the member (the `value_user` cascade). The
  API answer carries the member's ID for every caller who reads the value, and the display name only for a caller who
  may learn names (`peopleNamesSeen`). "Never the ID" is a rule for what the interface shows: the interface shows the
  name, or that the member has none, and never the ID. A caller that is not a user or a key may still see the ID in
  the API. Any principal kind other than those two that reaches the label routes is an open question; links are
  refused.
- The audit entries of `node.label_set` and `node.label_clear` name the field and its type, never the value, because a
  restricted value is sensitive. A clear of a field with no value changes nothing and is not audited.
- Label names are checked by the file-name rules (`names.Clean`), so a `/` or a control character is refused.
- A copy keeps a restricted value only when its caller manages the source (see Copies and moves). The proposal says "a
  copy keeps its labels" and does not single out restricted ones. We read the rule as the one that keeps a copy from
  widening who can read a restricted value, and the owner should confirm it.
- A move follows the same rule: a non-manager may not move a subtree with a restricted value out of its folder or into
  a place where the caller would manage it. The refusal reveals that such a value exists; it reveals nothing else.
- The owner's and the key's count of a field's values (`values`, and the total in the delete message) counts every
  value of the account, including values on nodes in members' My files. The owner session is not meant to reach
  My files, so the count reveals only how many values exist there, never their field, node or content. Recorded, not
  changed.
- The ownership transfer (`POST /nodes/{nod}/transfer-ownership`) and the hand-over of a deleted member's files (`transfer_to`) do
  not check the restricted rule. A transfer to a My files folder makes the recipient a manager of the moved subtree,
  so the recipient reads restricted values set by a manager other than the recipient. Who may do each: a transfer by
  the account key, or by the item's owner as a user holding editor on it; a deleted member's files by whoever may
  delete a member (the account key or an owner, through `managedAccount`). A transfer by the item's owner in My files
  is a move by a manager of the item, so it is within the rule. The hand-over of a deleted member's files is not:
  the account owner, who is not a manager of that My files folder, moves them to a recipient. This is open for the
  owner to decide.
- A restriction turned off makes the values readable by every reader of their items. The dialog says how many values
  there are before the save, and the audit entry `label_field.update` carries `values` with that count.
- A member's deletion removes the values that name the member. The audit entry `user.delete` carries `labels` with the
  count, when it is above zero.
- Deleting a field: the dialog reads the count when it opens, after a fresh list, so a value set since the list was
  shown is counted.
- Renames in one choice PATCH are applied before new choices are added, so a new choice may take a name that a rename
  gives up in the same request. A rename onto the name of a choice the request does not name is still `name_taken`.
- A count of nodes holding a value (`values`) is shown to the account owner and the account key only. A member sees
  the field, not how many items use it.
- A copy keeps restricted values for a caller that manages the source, even when it lands in another member's My files, and that member then reads them. Decision 36 records this for review.
- The interface offers labels on files and folders in the details panel, and the Manage > Labels page for the owner.
  Search by label is slice q.

### Accepted limits

These were reviewed in the slice n1 security review and accepted for now. They have no code of their own.

- **Owners and deleted users' files (n2).** An owner session may move a deleted user's My files only to
  an active member: never to the caller, and never to another owner. The account key is not limited
  this way. The limit is only as strong as the owners' control of accounts: an owner who can create a
  member account can move the files of that member's deleted account to it. Every move is audited, and
  that entry is visible to owners; it names the recipient only: no file, no count of what moved, and
  neither the recipient's personal folder nor the new directory.

- **Groups (L4).** Account owners manage groups. Sharing with a group shares with whoever is in it at
  the time, the sharer included if the sharer is a member. Membership changes are audited.
- **Quota through My files (L6), closed by slice p1.** Per-user limits now apply to My files (section 8, Storage
  limits). The limits slice p1 leaves are these:
  - Team spaces still fill the account's quota until slice u adds limits per space.
  - The sum of the per-user limits may exceed the account's quota.
  - A contributor on a subfolder of another member's My files fills that member's limit.
  - Deleting a user with `transfer_to` may put the recipient over its limit.
- **Shared subtree root (L7).** The root of a shared subtree shows its `parent_id`, which is an ID only.
  It names no ancestor, but it does tell the user that a parent exists.
- **Login CSRF by sign-in link.** Any holder of an account key on a multi-tenant installation can make a
  sign-in link for a user of its own account and send it to someone who is signed in to another account.
  A browser that is already signed in as another user is asked first, and it switches only if its person
  confirms (section 8). A browser that is not signed in still lands as the link's user, and its shell shows
  that user's name. That remains accepted, bounded by single use and the 60-second life
  (`users.sign_in_link_ttl`). Only an account key can make a link, so the link comes from whoever holds such
  a key. A verification or reset link already carries the same risk.
- **A handed-in user skips TOTP (decided 2026-10-29).** A sign-in link starts a session without a TOTP
  code, because the client holding the account key vouches for the user. TOTP does not protect a user
  against that client. The TOTP slice (section 9) must keep this rule.
- **The account directory (k3).** Any active user of an account, whatever its role and whether or not
  it reaches any folder, can find the name and email of every active user of the account through
  `GET /directory`, 20 at a time and rate limited. That is the purpose of the directory: a share dialog
  must name the people it shares with. Delegated tokens cannot search it. An account setting to keep
  plain members out of it is not built.
- **Per-address open limit (o1, L3).** `share.open.ip` is charged before the session check, so members behind
  one NAT or proxy share its budget. The per-member limit `share.open.user` does not change that: it caps one
  member, not a crowd behind an address.
- **Share links, organisation (o1).** A member who has opened an organisation link keeps the access it gives
  until the link is deleted, paused, reset, expired or trashed. Nothing else ends it. Turning a team space's
  `allow_links` back on revives its paused links, since their visits are kept. The secret is shown once: a
  lost URL is replaced with "Get a new link", which also ends the old copies. A link's members are everyone
  who holds the account's sign-in, so the link is only as private as the account's membership.
- **Links that anyone can open (o2).** A leaked anyone link works until it expires or is deleted, and a visitor's
  credential outlives a revocation by up to 15 minutes. Turning the account's switch back on revives paused links.
  The secret is shown once: a lost URL is replaced with "Get a new link", which ends the old copies. Anyone who holds
  the secret can use up the password attempts of its link, and slow its real visitors for an hour. The limits are
  in memory. Nothing scans a visitor's files: virus scanning comes in slice u.
  The one-guess-per-two-seconds bound holds per link once its hourly budget of wrong passwords is spent (the budget
  and the lock are in memory and reset on a restart, which only shortens a lock). Switching an organisation link to
  anyone keeps its secret, as decided: a URL that members already hold opens it for anyone from then on. When an
  address map is full (100000 keys), a new address is not recorded, and an audit entry for a key that finds no room
  is not written; that is the cost of bounding the maps.
- **Drop links (o3).** Drop uploads are not scanned for viruses until slice u. Their received bytes fill the account's
  quota and disk until the uploads complete or expire: the per-address and per-link limits cap abuse, and do not stop
  it. A leaked drop link accepts uploads until it expires or is deleted. A drop upload can stop partway: when the
  account fills, its next piece is refused and the bytes it already has are kept, so it resumes only after space
  frees (owner's decision, 2026-10-29). A paused link (its switch off, a members-only space, or a trashed node) keeps
  its visitors' uploads until their normal expiry, and answers `not_found` meanwhile. Trashing a directory does not end
  its drop uploads; deleting the directory for good does. A visitor's name and email are typed, never verified, are
  visible in the audit log as long as that log is kept, and nothing is mailed to them. The owner's mail is at most one
  an hour per link, so a burst of files is reported in one mail with a count, not one by one.
  A zero-length drop upload completes at creation: it takes no quota and no disk, and is bounded only by the
  `drop.ip` and `drop.link` rate limits (its file is an empty file in the link's directory). A link that expires
  releases its open uploads when the next visitor request arrives or at each upload's own expiry, whichever is first.
  Pieces in flight count against the quota and the disk together, and a drop link's visitors are held to
  `upload.max_disk_share_percent` as other uploads are.

- **Share links, the owner's list (o4).** Decision 11 lets an owner see, for a link in another member's My files, its
  audience, role, end, paused and password state, its creator, its visitor count and the folder's owner, but not the
  item's name or path. The owner may delete it. The account key sees the names. An admin sees no item name or path. A
  link that ended less than 24 hours ago keeps its row until the sweep removes it; it is refused at use and not listed.

- **Stars and recent (p2).** A view is recorded at most once per user and file in 10 minutes, and a view waiting to be
  written is lost in a crash. The gate's keys are in memory and reset on a restart, which only lets a view through
  again. Stars and recent rows of items the user can no longer reach stay in the database and in backups, and their
  names are not listed. Migration 0018 adds two tables and rewrites no rows.
- **Stars and recent, review round (p2).** Creating a signed link counts as a view of the file, by design: web previews
  and downloads go through signed links, and a link is how the interface opens a file. Recent's 500-per-user cap applies
  when the sweep runs, so the table may hold more than 500 for a user between sweeps. The prune is one full-table window
  query (`ROW_NUMBER() OVER (PARTITION BY user_id ...)`) inside the sweep transaction; it reads every recent row once per
  sweep, at the sweep's interval (five minutes). A view whose node or user was removed before its batch is written is
  skipped, and the rest of the batch is written.
- **Shortcuts, colours and labels (p3, p4, p5).** A shortcut's name is visible to every reader of its directory, even one
  who cannot reach the target, and it defaults to the target's name (decision 24). A colour and a description are shared:
  one editor's change shows for every reader. The names of restricted label fields are visible to every member of the
  account, since every user reads the field list (section "Labels (p4)"). A copy into another member's My files keeps
  restricted values for a caller who manages the source (decision 36).

## 9. API outline

Everything below is under `/api/v1`. The full contract will be an OpenAPI 3 document at
`api/openapi.yaml`. IDs are opaque strings with a type prefix (`acc_`, `usr_`, `fld_`, `nod_`,
...). Times are RFC 3339 in UTC. Lists use cursor paging: `?limit=&cursor=` in, `next_cursor`
out.

```
System
  GET  /healthz /readyz /metrics           (outside /api/v1)
  GET  /info                               version, features, public_url, limits (max file size, max_chunk_size), sharing_max_expiry_days (the longest a share may run, in whole days rounded up; 0 for no cap, from `sharing.max_expiry`)

Accounts                                   admin key
  POST|GET              /accounts                           ?external_id= to look one up
  GET|PATCH|DELETE      /accounts/{acc}                     name, quota_bytes, status; DELETE answers 202 and a job
  POST|GET              /accounts/{acc}/keys                secret shown once
  DELETE                /accounts/{acc}/keys/{key}

Inside an account                          account key, admin key, or owner session
  GET                   /accounts/{acc}/usage
  GET|PATCH             /accounts/{acc}/storage-policy      {user_quota_bytes}: the default limit for each user; 0 is none (owner, key, admin)
  GET                   /accounts/{acc}/storage             ?users_limit=&users_cursor=: account and folder totals; the users, by use, for an owner or the key (no names from members' My files for an owner; admin: totals only)
  GET                   /accounts/{acc}/storage/largest     ?folder=&limit=&cursor=: largest files of team spaces (owner) or of any folder (key); admin refused
  GET                   /me/storage/largest                 the caller's largest My files, with in_trash and versions_bytes (session)
  POST|GET              /accounts/{acc}/users               verification: email | link | none; ?external_id=
  GET|PATCH|DELETE      /accounts/{acc}/users/{usr}         PATCH and GET: quota_bytes (null follows the account default, 0 is none); GET and PATCH answer effective_quota_bytes. DELETE: ?transfer_to=usr_ moves the user's files to another user's My files; ?delete_files=true deletes them; the two are refused together
  POST                  /accounts/{acc}/users/{usr}/verification     send again, or a new link
  POST                  /accounts/{acc}/users/{usr}/sign-in-links    account key only: a single-use sign-in link, {next}; answers {url, expires_at}
  POST|GET              /accounts/{acc}/folders             ?external_id=
  PATCH|DELETE          /accounts/{acc}/folders/{fld}
  GET                   /accounts/{acc}/folders/{fld}/grants
  PUT|DELETE            /accounts/{acc}/folders/{fld}/grants/{usr}   {level}   (alias: viewer or editor on the root, section 8)
  POST|GET              /accounts/{acc}/groups              ?external_id=
  GET|PATCH|DELETE      /accounts/{acc}/groups/{grp}
  GET                   /accounts/{acc}/groups/{grp}/members
  PUT|DELETE            /accounts/{acc}/groups/{grp}/members/{usr}
  POST                  /accounts/{acc}/tokens              mint a delegated token
  POST                  /accounts/{acc}/tokens/revoke       revoke all issued before now
  POST                  /accounts/{acc}/users/{usr}/tokens/revoke   revoke one user's tokens issued before now
  GET                   /accounts/{acc}/audit
  POST|GET              /accounts/{acc}/webhooks
  PATCH|DELETE          /accounts/{acc}/webhooks/{hook}
  POST                  /accounts/{acc}/webhooks/{hook}/test          one webhook.test event (slice j3)
  GET                   /accounts/{acc}/webhooks/{hook}/deliveries    delivery log, newest first (slice j3)
  POST                  /accounts/{acc}/webhooks/{hook}/deliveries/{dlv}/retry   queue a failed delivery again (slice j3)
  GET|PUT|DELETE       /accounts/{acc}/quota-warning                 the share of the quota at which quota.warning is sent, 1 to 99; DELETE is 80 (slice j4)

Files                                      any caller, limited to what it may reach
  GET    /folders                          team spaces the caller reaches at the root, plus its personal folder; ?hidden=true lists only the hidden ones
  PATCH  /folders/{fld}/settings           {members_only, sharing_managers_only, no_download_for_viewers, allow_links}: managers of the space root; refused on a personal folder
  PUT|DELETE /folders/{fld}/hidden         hide or show a team space in the caller's own list (users only; changes no access)
  GET    /shared[?order=recent]            nodes shared with the caller (users only), each with its role; `order=recent` lists the newest share first, with `shared_at` (p2)
  PUT|DELETE /nodes/{nod}/star             star or unstar a node the caller reaches, at any role (users only; 204). At most 5000 stars per user (p2)
  GET    /starred                          the caller's starred nodes, newest star first, with `starred_at` (users only) (p2)
  GET    /recent                           the caller's recent nodes, latest first, with `viewed_at`, `edited_at` and `last_at` (users only) (p2)
  DELETE /recent, DELETE /recent/{nod}     clear the recent list, or remove one entry (users only; 204) (p2)
  POST   /nodes/{nod}/shortcuts            make a shortcut in a directory: {target, name?}; contributor on the directory, reach of the target; 201 (p3)

Labels (p4)
  GET    /label-fields                     the account's label fields, restricted ones included (any user of the account, the key, a token)
  GET|POST /accounts/{acc}/label-fields    list, or define {name, type, restricted, choices[]} (owner, key); PATCH|DELETE /accounts/{acc}/label-fields/{lbf} (owner, key)
  GET    /nodes/{nod}/labels               the values the caller may see (a restricted field only to a manager of the node)
  PUT|DELETE /nodes/{nod}/labels/{lbf}     set {value} or clear a value (editor; manager for a restricted field)

Files, other (s1, l3)
  GET    /nodes/{nod}/thumbnail            a JPEG, PNG or GIF thumbnail of an image (s1)
  GET    /nodes/{nod}/preview              a document as PDF, or its first page as PNG (?format=png); 202 while made (s3, decision 37)
  POST   /nodes/{nod}/exports              an export of the document to a fixed format; 202 with its ID and Location (s3, decision 37)
  GET    /nodes/{nod}/exports/{drv}        the export, with Range; 202 while it is made (s3, decision 37)
  POST   /zips                             a zip of the nodes named, as a job; 202 with the job (s2, decision 35)
  GET    /zips/{zip}                       the zip, with Range; 202 while it is made; only its maker (s2)
  DELETE /zips/{zip}                       the maker deletes it now, rather than at its expiry (s2)
  GET    /nodes/{nod}/comments             top-level comments, newest first, with the first 50 replies of each; ?parent=cmt_ lists one thread's replies; viewer and up (r1)
  POST   /nodes/{nod}/comments             a comment, or a reply with parent_id; commenter and up; a user or a token with write (r1)
  PATCH  /comments/{cmt}                   the author edits the body (r1)
  DELETE /comments/{cmt}                   the author or a manager deletes it; a comment with replies becomes a tombstone (r1)
  POST   /comments/{cmt}/resolve           a commenter resolves or reopens a top-level comment (r1)
  GET    /nodes/{nod}/people?q=            up to five reaching users to name, as opaque references; commenter and up; a user, not a token (r2)
  GET|PATCH /accounts/{acc}/sharing-policy also carries comments {comments}: the account's comment switch, default on (r1, decision 39)
  POST   /nodes/{nod}/unlock               a manager breaks a WOPI lock (l3, decision 32)
  PATCH  /nodes/{nod}                      + color (a palette name on a directory, or null) and description (plain text, at most 1000 characters, or null or ""); editor (p3)
  GET    /nodes/{nod}, listings            a node carries color and description; a shortcut carries target {id, kind} when the caller reaches the target, and unavailable: true when it does not or the target is gone (p3)
  GET    /directory?q=&kind=user|group|all people and groups of the account, for the share dialog (k3). Any user by session, and the account key; admins and every delegated token get `forbidden`, since a token is narrowed to its folders. `q` is 2 to 100 characters after trimming; a user matches by its email local part (prefix) or its name (substring), a group by its name (substring); `%` and `_` are literal. Active users only; at most 20 entries; rate limited per caller. Each entry carries only ID, kind, name and, for a user, email
  GET    /folders/{fld}/children?path=/a/b list a directory by path
  GET    /nodes/{nod}                      stat: name, size, mime_type, etag, modified_at, sha256
  GET    /nodes/{nod}/children             list a directory by ID (sort, paging); each child carries the caller's role
  GET    /nodes/{nod}/permissions          the permissions on a node and above it (viewer on the node)
  PUT|DELETE /nodes/{nod}/permissions/{subject}   {role, expires_at, notify, message}: a usr_ or grp_ subject; manager on the node, or an editor where editors_can_share allows; expires_at is in the future and within sharing.max_expiry
  GET|POST /nodes/{nod}/share-links        POST {audience, role, expires_at}: the node's organisation link, shown with its URL once (201); GET lists the links on the node and above it
  PATCH  /share-links/{shl}                {role, expires_at}: change a link; null clears the expiry
  POST   /share-links/{shl}/reset          a new secret, with the new URL (201); the old copies stop
  DELETE /share-links/{shl}                ends the link and its visits at once (204)
  POST   /share/open                       {secret, password}: a signed-in member opens an organisation link ({node_id}); anyone opens an anyone link ({token, expires_at, link, node}); not_found for every dead link
  POST   /share/renew                      bearer scl_: a live visitor credential, renewed ({token, expires_at})
  GET    /share/info                       bearer scl_ of a drop link: {title, message, ask_name, expires_at, max_file_size}
  GET|PATCH /accounts/{acc}/sharing-policy {public_links} (answers {public_links, anyone_links}): the account's switch for links that anyone can open, and how many such links it has (owner or key)
  GET    /accounts/{acc}/share-links ?audience=organisation|anyone&kind=access|drop&limit=&cursor=  every live link of the account, with its item and folder (owner, key or admin; o4)
  POST   /accounts/{acc}/share-links/delete-public  deletes every anyone link of the account, access and drop; answers {deleted} (owner, key or admin; o4)
  POST   /nodes/{nod}/access-requests      {role, message}: ask for a role (users only); always 202, with no body
  GET    /nodes/{nod}/access-requests      the requests on a node (manager, or editor where editors_can_share allows)
  GET    /access-requests                  the pending requests the caller may decide (the oldest 5,000 in the account)
  POST   /access-requests/{arq}/approve    {role}: optional; grants the role and closes the request
  POST   /access-requests/{arq}/deny       closes the request without a grant
  POST   /nodes/{nod}/transfer-ownership   {to}: the item's owner or an account key, holding editor on the item
  POST   /folders/{fld}/directories        {path, parents: true}
  POST   /folders/{fld}/files?path=        small file as the raw body (If-None-Match: * refuses an existing one),
                                           or a blank document from a template
  PATCH  /nodes/{nod}                      {name, no_download, editors_can_share}: rename in place (editor); no_download (editor); editors_can_share (manager); optional If-Match with the etag (412 on a mismatch)
  POST   /nodes/{nod}/move                 {folder, parent | path, name, on_conflict: fail | rename | replace}; optional If-Match, as PATCH
  POST   /nodes/{nod}/copy                 same; read on the source, write on the target; 202 and a job
                                           above 10,000 nodes, too_large above copy.max_nodes (default 50,000)
  DELETE /nodes/{nod}                      to the trash (editor); ?permanent=true deletes for good (manager)
  POST   /nodes/{nod}/restore              restore a trashed node: its trasher or a manager, and editor on the parent
  GET    /trash                            the trashed items the caller may restore; ?folder=, paged, newest first
  POST   /trash/empty                      purge the trashed items the caller manages, at most 1000; ?folder=
  DELETE /trash/{nod}                      purge one trashed item for good (manager)
  GET    /nodes/{nod}/versions             the current content, then the earlier versions (viewer)
  GET    /nodes/{nod}/versions/{ver}/content   an earlier version's contents (viewer; HEAD too)
  PATCH  /nodes/{nod}/versions/{ver}      label, keep_forever (editor)
  POST   /nodes/{nod}/versions/{ver}/restore   make it current; If-Match (contributor)
  DELETE /nodes/{nod}/versions/{ver}      delete an earlier version (editor)
  GET    /nodes/{nod}/content              download: Range, If-Range, ETag, Last-Modified, ?disposition=inline
  PUT    /nodes/{nod}/content              replace a small file; If-Match required
  POST   /nodes/{nod}/links                signed download link {ttl, disposition}
  GET    /links/{token}                    follow a signed link (no other auth); HEAD too
  GET    /jobs/{job}

Uploads (tus 1.0)
  OPTIONS /uploads   POST /uploads   HEAD|PATCH|DELETE /uploads/{upl}
  POST /uploads/{upl}/commit               {parent | path, name, on_conflict}: save an upload that
                                           finished under on_conflict=fail into a taken name

Users                                      session
  POST /auth/login /auth/logout /auth/verify /auth/password/forgot /auth/password/reset
  POST /auth/sign-in-link                  redeem a sign-in link: the session, and next
  POST /auth/totp/{enrol,confirm,disable}
  GET|PATCH /me                            PATCH: display_name, locale

Office editing
  POST /nodes/{nod}/office                 editor URL and WOPI token for the client to open
  /wopi/files/{nod}...                     WOPI host (section 10)
```

**Grants alias.** `/accounts/{acc}/folders/{fld}/grants` and `/accounts/{acc}/users/{usr}/grants` (slice d) keep working after slice n1 with the same requests, responses and status codes. They map `read` to the viewer role and `write` to the editor role, on the folder's root (section 8). Listing reports viewer and commenter as `read`, and the other three roles as `write`. A client that predates roles needs no change.

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
minutes, capped by `links.max_ttl`). They exist for `<img>`, `<video>` and plain downloads, where
the browser cannot send an `Authorization` header. A link is `/api/v1/links/{token}` on
`content_url` (else `public_url`); the token is the node, account, disposition and expiry, with
an HMAC-SHA256 that also covers the file's content tag: a random value kept in `nodes.content_tag` (migration 0012),
rotated by every content write (a PUT of content, an upload that replaces, a version restore) and not by a rename or
move. So any change of contents kills the link, and the link stays dead when the same bytes come back. Signing on the
SHA-256 would revive a link when an old version returns, which is why the tag is random. Links made before migration
0012 stop working when it runs, and their holders ask for new ones.
A link is a capability: serving it needs no grant, only that the node still exists unchanged and
the account is not suspended. Expired gives `410 link_expired`, forged or stale `404`, suspended
`403`. On `content_url`, only link paths are served and no cookie is read. Creating links and
serving them are rate-limited. The key is `keys/signing.key` (32 random bytes, created on first
start); `keys/signing.prev.key` is also accepted, so rotating means renaming the current key to
`signing.prev.key` and starting again. Inline display is allowed on `content_url`; on the main
origin only for images other than SVG, PDF, audio, video and plain text, and otherwise the file
is sent as an attachment. Every content response and every JSON error carries
`X-Content-Type-Options: nosniff` and a sandbox Content-Security-Policy.

**Management filters and guards (k4).** `GET /accounts` (an installation admin) takes `q` (a name
substring, trimmed, at most 200 characters, with `%` and `_` literal) and `status` (`active` or
`suspended`; any other value is refused with 400). `GET /accounts/{acc}/usage` adds `users_active` and
`users_pending`. `GET /accounts/{acc}/audit` takes `action` and `actor` (each at most 100 characters). The
folder responses carry `root_node_id`, so a client can open the share dialog on a team space's root; the
folder detail adds `files` and `bytes` for the whole tree. An account always keeps an active owner, whoever
the caller (the account key included): removing the last active owner, or demoting or disabling it, answers
`conflict` (409) with `details.reason` `last_owner`; an application replaces an owner by adding the new one
first, and ends a tenant by suspending or deleting the account. A person who deletes themselves gets
`conflict` with `reason` `self`. Removing a pending owner is allowed. The last
admin cannot be removed; that answers `conflict` with `details.reason` `last_admin`.

**Small-file edit.** `PUT /nodes/{nod}/content` requires `If-Match` with the ETag the editor
read; a stale ETag gets `412 precondition_failed`, so two editors cannot overwrite each other,
and a missing one gets `428 precondition_required`. Every write checks the caller's access again
inside its transaction, so a grant revoked or a node moved while a body was arriving stops it.
`on_conflict: replace` on move and copy replaces a file with a file only; a folder on either side
is a `conflict`.

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
| `download_restricted` | 403 | The node is marked no download and the caller may only view it. A download with attachment disposition (`HEAD` too), a signed link with attachment disposition, or a copy of the node or of a subtree containing a flagged node was refused. Inline viewing is not affected. |
| `account_suspended`, `user_not_verified`, `user_disabled` | 403 | Account or user state blocks access. |
| `not_found` | 404 | No such resource, or the caller may not know it exists. |
| `conflict` | 409 | Name taken, duplicate `external_id`, or a concurrent upload. |
| `upload_offset_mismatch` | 409 | `Upload-Offset` differs from the server's. |
| `locked` | 409 | Locked by an office editor (WOPI), or another request is writing this upload. |
| `upload_expired` | 410 | The upload expired; start again. |
| `link_expired` | 410 | The signed link expired; ask for a new one. |
| `precondition_failed` | 412 | Stale ETag in `If-Match`. |
| `precondition_required` | 428 | `If-Match` is required here and was not sent. |
| `too_large` | 413 | File over the size limit. `limit_bytes`. |
| `chunk_too_large` | 413 | Piece over the limit. `max_chunk_size`. |
| `range_not_satisfiable` | 416 | Requested range outside the file. |
| `rate_limited` | 429 | Slow down; `Retry-After` says when. |
| `too_many_uploads` | 429 | Too many open uploads; finish or delete some. `scope`: principal or account. |
| `unsupported_media_type` | 415 | A tus request without `Content-Type: application/offset+octet-stream`. |
| `checksum_mismatch` | 460 | Piece failed its `Upload-Checksum` (status as tus defines). |
| `internal` | 500 | Server fault; the `request_id` finds it in the log. |
| `unavailable` | 503 | Shutting down or in maintenance; retry after `Retry-After`. |
| `quota_exceeded` | 507 | Account quota. `required_bytes`, `available_bytes`; `scope` is `quota`, or `account_reservations` for the unfinished-upload cap of an account with no quota. These details go only to the callers section 8 names; any other caller gets the code with no details. |
| `storage_full` | 507 | The server's disk is full. `scope` is `server`, or `account` when the account holds its share of the disk in unfinished uploads. |

Messages are English. Clients translate by code; `details` gives them the numbers to fill in.

## 10. Webhooks, office editing and the web interface

A stored zip (decision 35) emits no webhook event: a download made by its maker is not a change to a file. Its audit entry is `node.zip`, with counts and no names.

This section describes the built webhook (slice j), office (slices l1 to l3) and web-interface slices.

**Webhooks.** An account registers endpoints with a URL, a secret and the events it wants:
`user.created`, `user.verified`, `file.created`, `file.updated`, `file.deleted`, `file.moved`,
`upload.completed`, `file.restored`, `quota.warning` (at a configurable share of the quota), `quota.exceeded`, and `webhook.test`.
Events go to an outbox table in the same transaction as the change, and a worker delivers them
at least once, retrying with exponential backoff for up to 24 hours. Payloads name the account
and user by ID and by `external_id`. Each delivery carries an event ID for de-duplication and a
signature header,
`Selfcloud-Signature: t=<unix time>,v1=<hex HMAC-SHA256 of "<t>.<body>">`. Webhook URLs that
resolve to loopback, link-local or private addresses are refused unless the operator allows
them, to prevent server-side request forgery.

**Office editing (WOPI).** The supported editor is **Collabora Online**, which runs as its own
server (for example the `collabora/code` image) next to Selfcloud. Selfcloud is the WOPI host;
Collabora is the WOPI client and serves the editor. Slice l (l1 to l3) is built in the API. Its tests run against a fake Collabora (`internal/wopi/wopitest`),
not the real editor. Other WOPI clients may work but are not a goal. Selfcloud implements `CheckFileInfo`,
`GetFile`, `PutFile`, `Lock`, `Unlock`, `RefreshLock` and `GetLock`. `RenameFile` and the other
overrides are not built (slice l4): the request is checked, then answered 501. The editor's
discovery URL (`office.discovery_url`, Collabora's `/hosting/discovery`) comes from config; the
discovery document tells which file types open for viewing or editing. Requests are checked
against the editor's proof keys. Locks expire after 30 minutes, as WOPI specifies. `PutFile` is
copy-on-write and honours the lock. The access token is a delegated token with audience `wopi`,
bound to one file. A client calls `POST /nodes/{nod}/office` and embeds the editor URL it gets
back. Collabora must be allowed to call Selfcloud's WOPI address (its `aliasgroup` setting), and
the README has no Compose example of the two together (decision 32).

**Web interface.** Served at `/` when `ui.enabled` (on by default). Screens: sign-in, verification
and password set-up, password reset, the file browser (upload with progress and resume,
download, preview of images, PDF, video and text, new folder, rename, move, copy, delete), and owner screens for users, folders and grants. Text editing and opening a file in the office editor are not in the interface yet. Light, dark
and follow-the-system themes. It works at phone width. The stack is TypeScript, React 18 with Ant Design 5,
React Router 7 and Vite (`web/`), since the flip (decision 25). Node is needed only to build it. The first
interface was built in four Svelte parts: **k1** (foundation and the auth pages), **k2** (file browser),
**k3** (sharing) and **k4** (admin pages). Slice v rebuilt it in React, and the Svelte app was removed at the flip.

*React interface (slice v).* The React interface was built beside the Svelte one, in `webui/`, with React 18,
Ant Design 5 and React Router 7 (decision 25), and was selected by the `reactui` build tag until the flip. Until
the flip the two shared the server's routes, headers and branding; the React build enforced the nonce policy and
the Svelte build reported it. After the flip (slice v4b) the React tree is `web/`, the only interface; the
paragraphs of slices v1 to v4a name `webui/` as it was then. Part v1 holds
the theme, the branding (`ui.brand.palette`, `ui.brand.sidebar`, which `/api/v1/info` returns when they are set), the
nonce, the languages, the app gate, the shell and the auth pages; each other route shows a "not yet" page in the shell.

*React file browser (slice v2).* The browser is in `webui/`, with the routes and rules of the Svelte one (the k2 paragraph
above). `pages/FilesPage.tsx`, `pages/TrashPage.tsx` and `pages/NodeLinkPage.tsx` load with `React.lazy`. The listing
(`components/files/Listing.tsx`) is an antd Table in list view, with the row checkbox column, and a set of tiles in grid view.
Both views share one keyboard model: `lib/files/keys.ts` maps a key to an action, and the selection itself stays in
`lib/files/selection.ts`. The other parts are `Breadcrumbs`, `Details` (a Drawer), `Preview` (a full-window Modal),
`Versions` (a Drawer), `PickDestination` (a Modal with a lazy Tree), `Conflict`, `Icon` (lucide), `UploadTray` and
`UndoToast`. The tray is mounted in the shell, as the Svelte shell mounts it. Uploads run through the unchanged
`UploadManager` and tus client, and the manager is never started from an effect. Team spaces are listed under the main
navigation. Not in this part, and left to v3: the share dialog, the Share button, "Who has access", and the access
request on a not-found view. The Svelte preview has no text editing, so the React one has none either.

*React file browser, slice v2 fixes.* Where the React browser behaves differently from the Svelte one, the change is
deliberate and listed here.
- Dialogs and drawers (Conflict, pick destination, Preview, Details, Versions, the purge confirmation, and the new-folder
  and delete dialogs) close on Escape and on their own close control, not on a click outside them (`maskClosable={false}`,
  as Svelte's dialogs closed on Escape only). `scripts/check-source.mjs` fails a Modal or Drawer without it in the
  file-browser views.
- The listing draws every row, with no virtual window. Svelte had none, and a virtual window lost keyboard focus past the
  rendered rows. The cost is on the carry list, not fixed here.
- Row checkboxes are out of the tab order. Enter and Space on a checkbox belong to the checkbox, and the arrow keys still
  move the selection. The header box that selects all is kept.
- Sizes write bytes in full ("9 bytes", in each language's plural); the larger units stay short ("2 kB").
- Details and Versions keep only the drawer's X. The footer and header Close buttons are removed.
- People are named, not numbered. Details and the Trash line show the signed-in person's own name, "Account key" for an
  account key's ID, and leave out a row for another user, since the node answers give no name for that user. Svelte
  showed the raw IDs; the React build never shows them. Names for other users would need a change to the node JSON in
  the Go code, which is not made here.
- Trash looks up each place once, and only the places that arrive with a new page. Restore and purge refresh the
  listings, the places and the trash, and a visit reads the trash again. A rename of a directory refreshes its place, so
  the breadcrumbs and the title read the new name.
- A failed load-more keeps the listing and shows the failure as the notice. The full-page error with "Try again" appears
  only when no listing has loaded.
- A sign-out or a sign-in as someone else empties the query cache and every store that holds a person's data: the
  folders, the hidden folders, the storage figures, the pending requests and the share limits.
- Preview loads a link only when it is http, https or same-origin; any other scheme shows the error. Its PDF frame has no
  `sandbox` attribute, because Chrome's PDF viewer shows nothing inside a sandboxed frame (checked in Chrome 2026-10-30
  with a probe page that failed under `sandbox` and showed the file without it).
- A file dropped on a dialog or on Preview starts no upload. The upload tray is not shown when no upload or conflict is
  waiting, and it is a region named by an h2 heading.
- The page's grid keeps each row at its own height. A short folder used to stretch the breadcrumb and the toolbar apart;
  an empty folder had 111 px between them, and the rows now sit at 16 px (the grid gap). The selection bar still pushes
  the listing down, and the toolbar does not move.
- The document title is restored when the person leaves the page.
- A file read sends nothing without a session. A read still running when the person signs out (the place lookup, a
  page of children, a node, the folder list) is refused on this side and not sent, since the server would answer 401.
  The folder lists are read only inside a session, so a sign-out does not start a read either. The e2e check for
  console errors failed on this in some runs before the change, and passed in all four runs after it.

*File browser (k2).* The routes: `/files` opens My files (the caller's `personal_folder_id`); `/files/{fld}` a folder's
root; `/files/{fld}/{dir}` a directory, which must belong to that folder; `/n/{nod}` resolves a node: a file opens its
preview in its folder (`?preview=`), a directory opens its parent with the directory selected (`?select=`). Anything the
caller cannot reach is the not-found view. Breadcrumbs walk `parent_id` upward with `GET /nodes/{nod}` and stop at the
first `not_found`, so no name above the first reachable ancestor is shown; a directory whose folder root is not reachable
shows only its own name, and no folder can be created in it, since the API creates directories by path from the root.
The list is cursor-paged, with infinite scroll and a "load more" button. The server has no sort parameter on the
children route, so sorting applies to the items loaded so far, and the page says so. Selection is kept by item ID, so a
later page cannot move it. The view and the sort are kept in local storage, in a guarded read and write.

*Uploads.* The browser's own client (`web/src/lib/files/tus.ts`) speaks tus 1.0.0 to `/api/v1/uploads`: it creates an
upload, sends pieces of `max_chunk_size` (from `GET /info`) with `PATCH`, asks `HEAD` where it stopped, and deletes an
upload that is cancelled. Every unsafe request carries the session's `X-CSRF-Token` and, because the browser sends its
own origin, passes the same check as any other route. A library was considered and not used: its dependency tree
includes a package with no licence field, which the licence check refuses. A name taken under `fail` answers `conflict`
at creation; the browser asks once per batch (replace, keep both, or skip) and creates again with that `on_conflict`. A
completion that finds the name taken answers `awaiting_commit`; the browser asks for a new name and calls
`POST /uploads/{upl}/commit`. Three uploads run at once; a `too_many_uploads` or `rate_limited` answer waits and tries
again. A reload keeps each unfinished upload's name, size, time stamp, folder, target, upload URL and the SHA-256 of
the file's first MiB in local storage, with the signed-in user's ID (no token). Records are checked when read: a URL
must be an upload's path, IDs must be valid, and records of other users are ignored. The list follows the session, and
a sign-out removes that user's records from the browser; signing out while uploads run asks first. The tray asks for
the same file again; a file whose name, size and time match but whose first MiB differs starts a new upload, and the
stored one is dropped. Resuming reads `HEAD`: an upload whose bytes are all in (`Selfcloud-Upload-State:
awaiting_commit`) waits for a name and is never shown as done. An upload is done only on the answer that carries its
`Selfcloud-Node-ID`. The browser computes the first MiB's SHA-256 with crypto.subtle, or with its own pure-TypeScript SHA-256 where
crypto.subtle is missing (a plain-HTTP LAN address); a record without a fingerprint is never resumed. Each piece
carries `Upload-Checksum: sha256 <base64>` only where crypto.subtle exists, since pure JavaScript over 8 MiB pieces
would slow large uploads; elsewhere no checksum header is sent. A 460 reads the offset once and sends the piece
again. Use HTTPS in production for per-piece checksums. A `beforeunload` prompt is registered only while an upload runs.

*Downloads and previews.* Before a download the browser makes a `HEAD` on
`/api/v1/nodes/{nod}/content?disposition=attachment`: a refusal (403, `download_restricted`) is shown, since a plain link
could not show it, and an accepted one starts the download through a link. That response is an attachment, so it works
whether or not `content_url` is set. Images, PDFs, video and audio load from a signed link (`POST /nodes/{nod}/links`,
`disposition=inline`, ttl 300) on the content origin, and the CSP allows that origin for `img`, `iframe` and `media`
(`media-src` was added for this). Text is read on the main origin with a `Range` of the first 1 MiB and shown as text in
a `pre`, never as HTML. A signed URL is never stored or shown.

*Undo.* The toast lasts 8 seconds and is announced through a live region. A delete is undone with
`POST /nodes/{nod}/restore`. A rename is undone with the inverse call and `If-Match` set to the `etag` the rename
returned; a move likewise. A `412` reads "it changed since; undo not possible". A multi-item undo runs each item and
reports how many failed.

*Sharing (k3).* The sharing interface is built on the sharing model of section 8. Nothing here
adds a rule: the server checks every change, and the screens only show what the caller's role allows.

- **Share dialog** (`web/src/components/share/ShareDialog.tsx`). Opened from the selection bar
  ("Share") and the details panel ("Share", where the caller may share, and "Who has access"). It
  lists the permissions from `GET /nodes/{nod}/permissions`: the name (or "You"), the email or group,
  the role, "from <name>" when the ancestor it sits on is readable (else "from a parent folder"), the
  end date, and an "Invited" badge for a pending user. A person below manager sees only its own rows,
  and the dialog says "You have <role> access" in words. A manager changes a row's role from its
  dropdown (a change is sent with `notify: false`) and removes a row with a confirmation. An inherited
  row cannot be removed here; "Open the parent" opens its ancestor through `/n/{nod}`.
- **Adding people.** A combobox (ARIA combobox pattern: `role="combobox"`, a `listbox`, and
  `aria-activedescendant` on the arrow keys) searches `GET /directory` after 250 ms, from two characters.
  Only a choice from the list is sent. The role offered is capped by the caller's role: a manager
  grants up to manager; an editor grants up to editor only where the node's own `editors_can_share` is
  on and the space does not keep sharing for managers (`mayShare`). An optional end date is capped at
  `sharing_max_expiry_days`, and a message of at most 500 characters is sent with the share. Notify is
  on by default. The reply's `notified` and `reason` become one sentence ("No email was sent: the group
  has more than 50 members."); the share itself stands. Errors are shown by code: `forbidden`,
  `conflict` (members only), `invalid_request` with `max_expiry_seconds` (the day limit), `not_found`
  (the person or group no longer exists). Results are announced through a live region.
- **Advanced (managers).** "Editors can share" and "Viewers and commenters can't download" are the
  node's own flags (`PATCH /nodes/{nod}`). The download switch says that it discourages, and does not
  prevent, downloads (section 8). Transfer ownership appears to the item's owner, with a confirmation
  that states the consequence for a personal folder (the item moves to the recipient's My files, and
  the caller keeps editor) and for a team space (only the owner changes).
- **Shared with me** (`/shared`). Lists `GET /shared`, each item with its role and its last change.
  The item opens through `/n/{nod}`. The owner's name is shown only when it is the caller; the API
  gives no other name for an owner, so the list shows none.
- **Access requests.** On a not-found view for an item (`/n/{nod}`, or a directory in `/files/{fld}/{dir}`), a
  signed-in person sees "Request access": a role (viewer, commenter or editor) and an optional message. Whatever
  the server answers, the form shows one confirmation, "If this item exists, the people who manage it have been
  asked", because the server answers 202 for every item. Only a rate limit or a lost connection is said apart.
  A not-found view for a folder's root has no item ID and so offers no form. The Requests page (`/requests`)
  lists the pending requests the caller may decide, with the requester, the item's name when the caller can
  see it, the role asked for, and the message. Approve grants the role asked for, or a role the caller
  chooses that is not above its own; deny closes the request. A decided request answers `conflict`, which is
  shown as its own message. The entry in the navigation has a count. It is read when the shell starts and
  after a decision, never polled. An owner always sees the entry; anyone else sees it only while a request waits.
- **Team spaces.** Each space in the sidebar has a menu: Hide or Show (`PUT` and `DELETE /folders/{fld}/hidden`),
  and, for a manager of the space's root, Space settings. Hidden spaces are read into their own store, and only
  when "Show hidden spaces" is on, so the move and copy pickers still list only what the caller shows. Space
  settings change `members_only`, `sharing_managers_only` and `no_download_for_viewers` one switch at a time;
  "Allow links" is live from slice o1 (it was shown disabled until then).
- **No download.** Under a space whose `no_download_for_viewers` is on, the browser hides its own Download
  buttons from a viewer or commenter, as it does for a node's own flag. The server still refuses the download,
  so the button is a convenience, not the rule.
- **Uploads into a shared directory.** A directory shared with a person whose folder root that person cannot
  reach takes files by its ID, so a contributor there can upload. Folders are made, and folder uploads placed,
  by path from the root, so those stay off in that view.

*Share links (o1).* The share dialog's "General access" section, above the people, shows Restricted or "Anyone in
{account}". Choosing the account's link shows the role and the optional end date, and "Create link" makes it; the
URL is shown once, with "Copy link", and "Get a new link" replaces it. A paused link says "Paused by policy". The
links that reach the item from above are listed, with "Open the parent". The role list stops at editor. The
`/s` page (`PUBLIC_VIEWS`) shows nothing but the outcome: it opens the item for a signed-in member, sends a
signed-out visitor to sign in and back, and shows the one message a dead link gets, "This link doesn't work. It
may have expired or been turned off." It has no app shell.

*Storage (p1).* Every signed-in user has a storage meter in the navigation, from `GET /me`, and a `/storage` page: the meter,
a breakdown, its largest files with an "In trash" badge and a link to the Trash. Manage has a Storage page
(`/manage/storage`) for the owner: the account meter, the default limit (`limitBytes` and `splitLimit`), people sorted by
use with an editable limit, and the team spaces with their sizes and largest files. The person dialog carries the limit.
Admin tenant pages show the account's totals. Names are wrapped in `<bdi>`, and layout uses logical properties only.

*Links and the badge (o4).* Manage has a Links page (`/manage/links`) for the owner: the account's live links with
filters for audience and kind, a page at a time, each link's item, folder, creator, state and visitors, a per-row delete
with its confirmation, and "Delete all public links", which names the number of anyone links before it deletes them. An
item in another member's My files shows "Item in {member}'s My files" and no name. The Overview's switch and the admin
tenant Overview offer the same "Delete all public links" action. The file list shows a small link badge on an item with
`has_link`, with a label for screen readers; the badge comes from the listing and costs no request per row.

*Known gaps (k3).* An editor sees the add form only where the node's own `editors_can_share` is on: the
flag of an ancestor is not visible from the node, so an editor sharing through an ancestor's switch does not
see the form (the server would allow it). The share dialog cannot open on a team space's root: the listing
does not give the root's ID (closed in k4: folder responses carry `root_node_id`). Matching for user names is case-insensitive for ASCII only (SQLite `LIKE`);
groups fold case for every script. The Requests entry is shown to owners, and to others only while a
request waits, so a manager who is not an owner sees it after a request arrives, not before.

*Build.* `make web` runs `npm ci && npm run build` in `web/`, which writes `web/dist/`. `make build`
does that first and then the Go build, which embeds `web/dist` (`web/embed.go`, `internal/web`).
Only `web/dist/.gitignore` is committed; `web/dist/` is otherwise build output and is not tracked. `embed.go`
embeds `all:dist`, so the Go build compiles with no Node at all, but serves no interface until `make web` has run.
CI runs `npm ci`, `npm run check` (the TypeScript compiler with no errors, the source, licence and key checks,
and the unit tests) and `npm run build` (which checks the built files) before the Go steps, so the Go tests
run against the real build. The Dockerfile has a Node stage whose `dist/` is copied into the Go stage.
`make e2e` runs the browser smoke test (section 10, *Smoke test*).

*Serving.* `internal/web` answers every GET and HEAD outside `/api/`, `/wopi/`, `/brand/`, `/metrics`,
`/healthz` and `/readyz`. A path with no file gets `index.html`, so the client-side router can answer
it. An unmatched API path still gets the JSON 404: the web interface never answers under `/api/`.
A name that starts with a dot is never served. Hashed files under `assets/` get
`Cache-Control: public, max-age=31536000, immutable`; `index.html` and the fallback get
`Cache-Control: no-store`. Every UI response is enforced under a strict policy, and no option turns it off or
reports it (decision 25). The HTML page's `style-src` carries a fresh nonce per response, `'self' 'nonce-<n>'`,
which the build writes on its style and meta tags. Every other UI response carries:
`default-src 'self'; script-src 'self'; style-src 'self'; img-src 'self' data: blob:; font-src 'self';
connect-src 'self'; frame-src 'self'; frame-ancestors 'none'; base-uri 'none'; form-action 'self'`, with
the origin of `content_url` added to `img-src` and `frame-src` when it is set. Also `nosniff`,
`Referrer-Policy: same-origin` and `X-Frame-Options: DENY`. These headers are set only on UI responses;
content, link and API responses keep their own policies (section 9). The build emits no inline script
or style: CSS is in files, and the module preload polyfill, an inline script, is off. The interface ships
no font files: it uses the system font stack (decision 25), so no request leaves the origin.

*Branding.* Installation-wide only: `ui.brand.name` (default `Selfcloud`), `ui.brand.accent` (a colour as
`#rrggbb`, default `#053D8E`) and `ui.brand.logo_file` (a PNG or SVG, read at start, at most 256 KiB).
`GET /api/v1/info` returns them under `brand`, with the logo as a URL (`/brand/logo`, or null) so
the host's file path is never sent. `/brand/logo` is served whether or not the interface is on. An SVG logo
is sent with a sandbox policy. Per-tenant branding is not built: every tenant sees the installation's look.

*The client.* The interface is a client of the public API like any other. It has no private endpoints.
Every call goes through one client that sends `credentials: same-origin`, sends the CSRF token from the
sign-in or `GET /me` answer on every change (the token stays in memory, not in storage), turns a 401 into
a redirect to sign-in (except the start-up check and the auth pages), and reads the error envelope by its
`code`. The server's English message is never shown. `?next=` after sign-in is accepted only as a
same-origin path. Sign-in answers 409 with `details.accounts` when the email is in several accounts, and
the page then asks which one.

*Sign-in link page.* `/sign-in` reads the token from the fragment and removes the fragment from the address
bar before any request, and keeps the token in component state only. It makes one `POST /api/v1/auth/sign-in-link`,
then opens the `next` the server answered (checked again by `next.ts`; `/files` when it is unusable). When the
server answers `409 signed_in_as_other`, the page shows a dialog: "You're signed in as {current}. This link signs
you in as {target} instead.", with the names in `<bdi>`. Switch redeems the same token again with `confirm: true`;
Stay opens `/files` without redeeming, and Escape does the same. The link's 60 seconds still apply, so a late Switch
shows the expired message. The decision is in `web/src/lib/signin.ts`. A failure shows one message per code: a used, expired
or unknown link gets "This sign-in link has expired or was already used. Go back to the application that sent you
and try again.", with no form and no retry of the same token. A malformed token is refused without a request.

*Languages.* Seven catalogues (`web/src/i18n/{en,es,fr,pt,ru,ar,he}.json`), one flat object each, with
the same keys and no empty values. Plural keys carry all six categories in every language, so a count
always finds its form. The Go tests in `internal/web` check the keys, the values, the plural categories
and the placeholders. The order is `?lang=`, the signed-in user's `locale`, this browser's saved choice,
`navigator.languages`, then English. Hebrew and Arabic set `dir="rtl"`, and layout uses CSS logical
properties only: `npm run check` fails on a physical left or right. A language picked while signed in is
saved with `PATCH /me`; otherwise it is saved in the browser. The theme is saved in the browser only,
since no server field holds it.

*Smoke test.* `make e2e` builds, starts the server on a temporary data directory, makes an account, an
owner and a member through the CLI and the API, and drives headless Chrome through the interface: sign-in,
the shell and the name, Hebrew (checking `dir`, a translated string and that the choice is kept), the
dark theme, sign-out. It fails on a console error, a policy violation or a request to another origin. It
uses the Chrome DevTools Protocol directly rather than Playwright, which is Apache-2.0 and so outside the
licence rule for npm packages.

*Split.* k1 is the foundation and the auth pages. k2 replaces the file browser placeholder, k3 the
sharing placeholder, and k4 the administration placeholder. Each placeholder said "coming soon" through
the catalogue; none is left.

*Administration (k4).* Two areas, each shown only to the role that may use it. An account owner gets Manage
(`/manage`): Overview (storage meter, active and pending people, folder count, the last ten audit entries
as sentences, and next steps), People (list; invite by email; resend verification; send a password reset;
make owner or member; disable or enable; remove; a person dialog with status, team spaces, groups and
activity), Groups (create, rename, delete, members through the k3 people combobox), Team spaces (create,
rename, delete with the file count, size and a typed name; share the root through the k3 share dialog;
settings through the k3 SpaceSettings), and the Audit log (cursor paged, filter by action, the raw action
as a second line). An installation admin gets Admin (`/admin`): Tenants (search by name, status filter,
load more, new tenant with a storage limit or none, first owner by email or a link shown once, suspend,
resume, delete as a job polled with backoff: the wait grows from half a second to eight, a failed check is
tried again and only three failures in a row are shown, and after 120 checks the page says the deletion
goes on in the background rather than calling it failed), a tenant page with tabs Overview, Users (read-only, resend the
owner's invitation), API keys (shown once) and Audit log, Admins (list, add, remove; the last admin is
kept), and Installation (read-only facts from `/info`, the OpenAPI spec and `/readyz`). A member or an owner
who opens an admin page, or a member who opens `/manage`, sees the not-found view. The server checks every
call; the interface only hides what the role may not do. Secrets (a new key, a verification link) live in
component state, and are cleared when their dialog closes; they are never stored, logged or put in the URL.

*Known gaps (k4).* The person view is a dialog, not a side drawer. The tenant list has no users column, since
the API gives no per-tenant user count; the owner's Overview counts come from `usage`. Name search over
tenants and people is ASCII-only, as in k3. The audit log resolves names through the people list, and the
lookup stops after 20 pages, so an older entry may show an identifier instead of a name. The last-admin
check in the browser reads the admins page it has loaded; the server remains the authority. The person view
makes one call per group and per space it lists, so a large account makes many requests when one person is
opened. A team space's file count and size come from the folder detail, which totals the whole tree on each
request.

*Smoke test, k4 additions.* The run also has an owner invite a person (the invitation goes to a local SMTP
sink; the run takes the link from the mail and verifies the account with it), make a group with that person
in it and a team space; an installation admin, made with the CLI, create a tenant (its owner's link is shown
once) and an API key (shown once, and the key then works for the API); and a member who opens `/manage` see
the not-found view.

**Languages in the web interface and in email.** The seven languages of section 1.

- *Choosing one.* The web interface uses, in order: `?lang=` on the link that opened it (so a
  client can open Selfcloud in its own user's language), the signed-in user's `locale`, the
  browser's `Accept-Language`, then English. A choice made in the language picker is saved to
  the user's `locale` with `PATCH /me`. Email uses the recipient's `locale`, then English.
- *Setting it.* `locale` is a field on users, set when the user is created or with `PATCH` on
  the user (by a key or an owner) or on `/me` (by the user). A tag resolves by its exact
  match, then its base language, then English: `pt-BR` gets the European Portuguese text, the
  only Portuguese there is.
- *Catalogues.* Strings live in plain files, one JSON catalogue per language for the web
  interface and one template per purpose and language for email (`verify.he.txt`). A missing
  key falls back to English, and a test fails when any key or template is missing from any of
  the seven languages.
- *Layout.* Arabic and Hebrew set `dir="rtl"` and mirror the whole layout through CSS logical
  properties, never physical left and right. Dates, numbers and sizes are formatted for the
  locale. The language picker names each language in itself.
- *Wording.* Hebrew addresses the reader in the plural imperative (התחברו, not התחבר or
  התחברי), so no text assumes a gender. Portuguese follows European spelling and usage.

*React sharing interface (slice v3).* The sharing pages are in `webui/` with the rules of the Svelte ones they replace.
`pages/SharedPage.tsx` (Shared with me), `pages/RequestsPage.tsx` (access requests) and `pages/OpenLinkPage.tsx` (the
address `/s#secret`) load with `React.lazy`, as do the share dialog (`components/share/ShareDialog.tsx`, opened from the
selection bar and from Details) and the request-access form on the not-found view. Public browse and the drop page
(`components/share/PublicBrowse.tsx`, `components/share/DropPage.tsx`) are the visitor's view of a link.
- The link secret stays in the address fragment. It leaves the address by `history.replaceState` on each exit (dead,
  sign-in, organisation redirect). It is never in a request URL, a query string, storage (except the session-scoped
  sign-in hand-off, which is the Svelte rule), or an error text.
- A visitor's credential is held in component state only, renewed 2 minutes before it ends, and sent in the header to
  the server that issued it. A visitor's reads are not refused for lack of a session, and a visitor's 401 does not sign
  a signed-in person out (the visitor calls pass `noRedirect`, as in Svelte).
- A drop page never lists the folder. It lists only the files that this visit sent, from its own upload manager, which
  is created on mount and halted on unmount (a StrictMode remount gets a new one).
- Every error is shown by its code. A dead link gets one message whatever its cause.
- Dates are the five native date inputs; the cap from `GET /api/v1/info` (`sharing_max_expiry_days`) is an installation
  setting, set at start-up, and it survives a change of person.
- The share actions are hidden for a caller who may not share, and the dialog shows a viewer only its own row (the
  server checks every change again).

*React manage and admin interface (slice v4a).* The rest of the pages are in `webui/`, with the rules of the Svelte ones they
replace. The user pages are `pages/HomePage.tsx`, `pages/StarredPage.tsx`, `pages/RecentPage.tsx` and `pages/StoragePage.tsx`.
The owner pages are `pages/manage/*.tsx` (Overview, People, Groups, Team spaces, Storage, Links, Audit), and the
installation admin pages are `pages/admin/*.tsx` (Tenants, one tenant, Admins, Installation). All load with `React.lazy`
from `App.tsx`. Each owner page checks the owner role itself (`components/manage/OwnerPage.tsx`), and each admin page
checks the installation admin role (`isAdmin`); a page the person may not open is the not-found view, and no request is sent.
- A failed first load shows an error, never an empty or default state. A failed refetch keeps the list on screen.
- Secrets shown once (an invite's verification link, a new tenant's owner link, a new API key) live in the dialog's
  component state only. They are not in the query cache or a module store, they are gone when the dialog closes or the
  session changes, and they never reach a URL, a log or an error text (`components/admin/ShowOnce.tsx`).
- A destructive action (delete a tenant, remove a person, revoke a key, delete a group or a space) asks first, with the
  typed name where Svelte asks for it. A double click sends one request, and a failed request keeps the dialog open with
  its error by code.
- Every dialog holding a form, a secret or a question keeps `maskClosable={false}`. `scripts/check-source.mjs` enforces
  it for every Modal and Drawer in `src`, with one named exception: the navigation drawer in `components/Shell.tsx`.
- Errors are shown by their code. The server's text is never shown.
- The shell shows an error by code when the spaces cannot be read or a hide is refused (`components/Shell.tsx`).
- The top-level error boundary (`components/ErrorBoundary.tsx`) strips the fragment of the share-link address before it
  shows its message and reload button, so a render error cannot leave the link secret in the address bar.


## 11. Data model

| Table | Main fields |
|---|---|
| `accounts` | id, external_id, name, status, quota_bytes, used_bytes, used_files, reserved_bytes, tokens_not_before, public_links (0 or 1; off by default), user_quota_bytes (the default limit for each user; 0 is none), quota_warning_percent (80 by default) and quota_watched (decision 30, migration 0024) |
| `api_keys` | id, account_id (null for admin keys), name, prefix, hash, expires_at, revoked_at, last_used_at |
| `users` | id, account_id, external_id, email, display_name, locale, role, status, password_hash, totp_secret (encrypted), verified_at, tokens_not_before, quota_bytes (null follows the account default; 0 is none) |
| `verifications` | id, user_id, purpose (verify, reset), token_hash, expires_at, used_at |
| `sessions` | id_hash, user_id, created_at, last_seen_at, expires_at, ip, user_agent |
| `folders` | id, account_id, external_id, name, name_key, kind (`team` or `personal`), owner_user_id (personal only; at most one personal folder per user), members_only, sharing_managers_only, no_download_for_viewers, allow_links (0 or 1; team spaces only, personal folders keep them 0; read by the share-link rules), used_bytes, used_files, reserved_bytes (the folder's share of the account's counters; they sum to the account's, p1), staged (1 for a chunked copy's hidden staging folder, decision 34) |
| `groups` | id, account_id, external_id, name, name_key, created_at, updated_at |
| `group_members` | group_id, user_id, created_at |
| `permissions` | node_id, account_id, subject_kind (`user` or `group`), subject_id, role (viewer, commenter, contributor, editor, manager), created_at, created_by, expires_at (null: no expiry) |
| `access_requests` | id (`arq_`), account_id, node_id, user_id, role (viewer, commenter or editor), message (at most 500 characters), status (pending, approved, denied), created_at, decided_at, decided_by; at most one pending row per node and user |
| `share_links` | id (`shl_`), account_id, node_id (cascade), audience (`organisation` or `anyone`), role (viewer, commenter, contributor or editor; null for a drop link), upload_only (0 or 1), secret_hash (SHA-256, unique), password_hash (argon2id; anyone links only, null otherwise), expires_at (null: no end), credentials_at (the secret's issue time), created_by and created_kind, created_at, updated_by, updated_kind, updated_at; unique (node_id, upload_only). Drop fields (o3): title (100 characters), message (1000), ask_name, notify, unreported_files (files not yet mailed), notified_at (the last mail, null before the first) |
| `share_link_visits` | link_id (cascade), user_id (cascade), visited_at; primary key (link_id, user_id). A member's recorded open of a link; it counts only from `credentials_at` on |
| `hidden_folders` | user_id, folder_id, created_at; primary key (user_id, folder_id). A user's own list of hidden team spaces, which changes no access |
| `stars` | user_id (cascade), node_id (cascade), account_id (cascade), created_at; primary key (user_id, node_id). A user's private bookmarks, at most 5000 per user (p2) |
| `recent` | user_id (cascade), node_id (cascade), account_id (cascade), viewed_at and edited_at (either may be null, not both), last_at; primary key (user_id, node_id). A user's private last view and edit of a file, pruned by the sweep (p2) |
| `nodes` | id, account_id, folder_id, parent_id, name, name_key (for uniqueness), kind (dir, file or shortcut), size, mime_type, blob_id, etag, created/modified at and by, owner_id, no_download, editors_can_share (0 or 1, each inherited from ancestors), trashed_at and trashed_by (set on a trash root only), trashed_name_key (the name key before the trash), color (a palette name, directories only), description (at most 1000 characters), target_id (a shortcut's target; set NULL when the target is purged) (p3) |
| `label_fields`, `label_choices`, `node_labels` | the account's typed label fields (name, type, restricted), their choices (archived, never deleted), and one value per field on a node (p4, migration 0023) |
| `copy_jobs`, `copy_job_nodes` | a chunked copy's phase and failure, and the staged nodes it has written (decision 34, migration 0027) |
| `derived` | derived files such as thumbnails, with their state (s1, decision 33, migration 0028); document previews (`pdf`, `png_page1`) and exports (`export_<format>`) too (s3, decision 37, no migration) |
| `zips`, `zip_roots` | stored zip downloads: the maker and its credential (`created_cred`, the token's fingerprint or none), the state, sizes, the snapshot's reservation, the expiry, and the nodes asked for (s2, decision 35, migration 0029) |
| `comments`, `mentions` | comments on a node, one reply level (`parent_id`), a tombstone only while it has replies, the author cleared when its user is deleted, the resolution; and the people a comment names, one row each, kept only for those who reached the node when it was written. `accounts.comments_on` is the account's switch (r1 and r2, decision 39, migration 0030) |
| `versions` | id (`ver_`), account_id, node_id, blob_id, size, sha256, etag, mime_type, created_at and created_by (when that content was first written), replaced_at and replaced_by, label (at most 100 characters), keep_forever (0 or 1); indexed by (node_id, created_at) |
| `blobs` | id, account_id, size, sha256, state (`uploading`, `live`, `orphan`), delete_after, created_at |
| `uploads` | id, account_id, target (folder_id, parent_id or dir_path, parents, name, name_key, on_conflict, mime_type), length, durable_offset, hash_state, blob_id, created_by, created_kind, expires_at; uploader_name (100), uploader_email (254) and visitor_id, for uploads through a drop link (o3) |
| `wopi_locks` | node_id, account_id, lock_id, holder, created_at, expires_at, version_id (decisions 31 and 32; enforced from slice l3) |
| `wopi_sessions` | id, account_id, node_id, user_id (null for an account key), role, issued_at, expires_at, revoked_at, browser_session, served_tag (decisions 31 and 32) |
| `webhook_endpoints`, `webhook_endpoint_events` | an endpoint (URL, encrypted secret, disabled state and its reason) and the events it takes (slice j) |
| `webhook_events`, `webhook_deliveries`, `webhook_quota_marks` | the outbox of events, their deliveries, and the quota warning last sent (slice j, migrations 0020 to 0024) |
| `sign_in_links` | one-use sign-in links (slice c2) |
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
  depth, blob garbage-collection backlog, and installation totals `selfcloud_storage_used_bytes`
  and `selfcloud_storage_reserved_bytes`. Per-account figures come from the usage API
  (`GET /accounts/{acc}/usage`: `quota_bytes` and `available_bytes` are `null` when the quota
  is unlimited), not from metric labels.
- **Usage counters**: the sweep compares the reserved counters with the uploads, and each account's counters with the
  sum of its folders' (p1); it logs a warning when they disagree, and never repairs them. `selfcloud admin recount-usage
  [-account ID]` rebuilds the account's and its folders' used and reserved bytes from the rows, one account per
  transaction, and records the before and after figures in the audit log.
- **Access requests**: the sweep removes decided access requests older than 30 days. A pending request is
  kept however old it is, since someone still has to decide it.
- **Expired permissions**: the sweep removes each permission whose `expires_at` passed more than 24
  hours ago, audits each removal as `permission.expired`, and leaves the 24 hours as a grace period so
  the audit trail shows when access ended. Reads already ignore an expired permission, so the sweep
  only tidies rows and never changes what a user may reach.
- **Expired share links (o4)**: the sweep removes each share link whose `expires_at` passed more than 24 hours ago, up to 200
  links a pass. Each removal is one transaction: its visits go with the link, any open uploads of a drop link are deleted
  with their reservations, the link is audited as `link.expired` in its folder (under the system actor, private as every
  personal entry is), and the uploads' files are removed after the commit. A link that ended less than a day ago keeps its
  row and answers `not_found` from the moment it ends, as before; a second pass finds nothing more to do.
- **Trash**: each sweep purges up to 200 explicit trash roots trashed longer than `trash.retention` (default 30
  days), one per transaction, adjusting usage and writing a `node.purge` entry under the system actor. The rest
  waits for a later pass.
- **Versions**: each sweep prunes up to 200 nodes' versions: non-kept versions beyond `versions.max_count`
  (default 100; `0` means no limit), and those replaced more than `versions.max_age` ago (default 30 days; `0`
  means no limit). Each pruned version comes off usage. The replacements prune the count limit as they go.
- **Recount**: `selfcloud admin recount-usage` counts versions in used bytes, and trashed items as files, since
  they still count toward usage until they are purged.
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

| Slice | Contents | Status |
|---|---|---|
| a | Server skeleton, config, logging, `/healthz`, `/readyz`, `/metrics`, graceful shutdown, Dockerfile, CI | Built |
| b | Accounts, admin and account keys, `admin` bootstrap command, JSON errors, audit log | Built |
| c | Users, verification (`email`, `link`, `none`), SMTP and templates in the seven languages, passwords, sessions, rate limits | Built |
| d | Folders and grants | Built |
| e | File operations: list, stat, mkdir, rename, move, copy, delete, small-file write with `If-Match` | Built |
| f | Resumable uploads (tus), with crash and resume tests and the upload benchmark | Built |
| g | Ranged downloads and signed links, with the download benchmark | Built |
| h | Quotas and usage, enforced at upload creation and while writing | Built |
| i | Delegated tokens and CORS | Built |
| n1 | Sharing model: roles on any node with inheritance, groups, personal "My files", team spaces and their owners, the grants routes on top of permissions, `GET /shared`, delegated-token node scope | Built |
| n2 | Sharing v2: access expiry, no-download, the "editors can share" switch, ownership transfer, team-space restrictions and hiding, access requests, share emails | Built |
| m | Trash, version history, keep forever, restore, undo | Built |
| k1 | Web interface foundation: the Svelte app embedded in the binary (removed by v4b), its serving and headers, branding, themes, the seven languages with right-to-left layout for Arabic and Hebrew, and the sign-in, verification and password pages | Built |
| k2 | File browser: lists by role, new folder, rename, move, copy, delete, download, resumable uploads, previews, Trash, versions, undo | Built |
| k3 | Sharing interface: share dialog, Shared with me, access requests, hidden spaces, space settings, the account directory | Built |
| k4 | Owner pages (overview, people, groups, team spaces, audit log) and installation admin pages (tenants, admins, installation facts) | Built |
| c2 | Sign-in links for trusted clients: a client holding the account key hands a user into the web interface with a one-use link, and a browser signed in as someone else is asked before it switches | Built |
| i2 | Delegated tokens: revoking one user's tokens, and a scope that reaches whatever the user reaches | Built |
| o1 | Share links for people in the account (organisation links, recorded visits), the account's link policy | Built |
| o2 | Links that anyone can open: the visitor credential, password and its limits, public pages | Built |
| o3 | Drop folders (Request files): anonymous uploads into one directory | Built |
| o4 | The sweep of ended links, the account-wide link list, Delete all public links, the link badge | Built |
| p1 | Per-user quota (charged by folder) and the storage pages (decision 24) | Built |
| p2 | Starred, Recent and Home | Built |
| v1 | React web interface beside the Svelte one (`webui/`, section 10): Vite, React 18 with Ant Design 5, React Router 7; the theme and the brand palette and sidebar; the CSP nonce, reported by the Svelte build and enforced by the React build; the seven languages; the app gate, shell and auth pages | Built |
| v2 | React file browser: Files, NodeLink and Trash, the `files/*` components, uploads, undo | Built |
| v3 | React sharing interface: Shared, Requests, OpenLink, the public browse and drop pages, the share dialog and its parts, hidden spaces and space settings | Built |
| v4a | React rest: Home, Starred, Recent, Storage, the owner pages (Manage) and the installation admin pages (Admin) | Built |
| v4b | The flip: removes the Svelte app, moves `webui/` to `web/` (the only interface), drops the `reactui` tag and the report-only mode so the nonce policy is enforced for everyone, and drops the self-hosted fonts for the system stack | Built |
| p3 | Shortcuts, folder colours and descriptions (decision 24). Built, with migration 0019 (the `nodes` rebuild), and the React pages for shortcuts and colours | Built |
| p4 | Labels: typed fields the owner defines, their values, copies keeping them, Manage > Labels and the details panel (decision 29). Migration 0023 | Built |
| p5 | The OpenAPI file, the design record, the feature-parity rows, the browser smoke test of slice p, and the security review of slice p (decision 36) | Built |
| pg | PostgreSQL as an option for the metadata store (decision 7). It lands before q, whose full-text search gains from it | Planned |
| q | Search: filters, label search, full text and OCR within size limits | Planned |
| r1, r2 | Comments and replies, edit, delete, resolve, the account's comment switch; mentions resolved at write time and the people list (decision 39, migration 0030). Nothing is sent: the notifications are r3 | Built |
| r | Comments, mentions, activity pane, notifications, who-viewed. r3 to r7 are planned (r1 and r2 are built) | Planned |
| s1 | Thumbnails: the derived store and files (migration 0028), the worker pool, the child process with its limits, the JPEG, PNG and GIF thumbnails, the `thumb` route and the content-origin link, `has_thumbnail`, the budget in the sweep, and the cleanup on blob removal and account deletion (decision 33) | Built |
| s2 | Zip download: the stored zip job, its limits, `Range`, the routes, the `no_download` refusal (proposal s, questions 1, 7, 10; migration 0029, decision 35) | Built |
| s3 | Document previews and conversions through Collabora's conversion interface: the conversion client (`internal/office`), the preview and export routes, the converted kinds, the fake Collabora's conversion endpoint (proposal s, questions 2, 8; decision 10; decision 37). Not usable in production until a live run against Collabora | Built |
| s4 | The web interface's thumbnails, previews and export actions, and the security review of slice s | Planned |
| l | WOPI host for Collabora: the editor route, CheckFileInfo, GetFile and PutFile, the WOPI token, the proof keys (decision 31), and the locks with the save that honours them, the store's lock rule on every write, and a manager's break of a lock (decision 32). Rename (l4), the interface's button and its read-only view, the README example and the break's confirmation dialog are not built. The API's view mode (`office.go`) is built | Built (l1 to l3) |
| j1 | Webhook endpoints: the migration, the encrypted secret (AES-256-GCM, key derived with HKDF from the links signing key, bound to its row), the endpoint routes, the show-once secret and its rotation, the isolation tests | Built |
| j2 | Webhook events: each change writes its event in its own transaction (the audit hook and four explicit emits), the payloads and the event list including `file.restored`, the big-folder summary (1,000 files, 5,000 deliveries, 10,000 nodes: decision 27), the personal-folder rule, the outbox atomicity tests. The worker that sends them is j3 (decision 28) | Built |
| j3 | Webhook worker: claim, lease, per-endpoint order, signing, delivery, backoff and the give-up, the disable paths, the 100,000 pending cap, the sweep, the metrics, the delivery log, retry and test routes; the SSRF dialer and `webhooks.allow_networks`, moved here from j4 (decision 28) | Built |
| j4 | Quota warnings (`quota.warning`, `quota.exceeded`, once per crossing), the level setting and its route, the rewrap of secrets (`selfcloud admin rewrap-secrets`), the sweep of stale quota marks, the openapi for them (decision 30) | Built |
| j | Webhooks: the slice review of j1 to j4 (`review-j3.md`, `review-j4.md`). Its j4 fixes are in decision 30; its j3 fixes are the ones the code cites as "review finding", not re-verified in this round | Built |
| t | Changes feed, file picker, WebDAV, installable web app | Planned |
| u | Admin: audit viewer and export, sharing policies, retention and hold, ClamAV, limits | Planned |
| t2 | Approvals | Planned |

Slices are listed in build order; the letters keep their first meaning, and a slice named with a 2 (c2, i2, t2) follows up the slice of that letter. A slice built in parts (k, o, p, v) has a row for each part, each reviewed and pushed on its own. Status is Built, Building or Planned. Slices m to u come from
the Google Drive parity plan in `feature-parity.md` (approved 2026-10-28). An API client can switch
over once slices a to i work. Slice n1 keeps the `/grants` routes working, mapping `read` to viewer
and `write` to editor, so a client that switched after i keeps working. Slice n was split in two
(section 15, decision 19): n1 is the model, and n2 adds the features that sit on it. The OpenAPI file
grows with each slice, and a test checks that every route is in it. The Go client in `client/`
is generated from it.

## 15. Decisions

Decided on approval:

1. **Permission levels**: `read` and `write`. *Replaced 2026-10-28 by decision 11.*
2. **Grants** attach to whole top-level folders only. *Replaced 2026-10-28 by decision 11.*
3. **Verification**: an email link, or a link the client delivers. No numeric code.
4. **Public share links, file versions, trash bin**: not in the first release. *Replaced
   2026-10-28 by decision 11: all three are built (slices m and o).*
5. **On-disk layout**: blobs by ID (section 4).
6. **Delegated tokens**: Selfcloud mints them through the API.
7. **PostgreSQL**: added after slice i. *Scheduled 2026-10-29: slice pg, before slice q (section 14).*
8. **Web interface stack**: Svelte. *Replaced 2026-10-30 by decision 25.*
9. **Hierarchy** (2026-10-28): admin, then tenants, then sub-accounts (section 8). Admins can
   also be users who sign in, not only API keys.
10. **Office editor** (2026-10-28): Collabora Online, through WOPI (section 10). No headless
    LibreOffice inside Selfcloud; previews and conversions, if wanted, use Collabora's
    conversion API.
11. **Google Drive parity** (2026-10-28): every item marked *New* in `feature-parity.md` is
    built, in slices m to u. Sharing becomes viewer, commenter, editor and manager roles on any
    file or folder, inherited down the tree, with groups and a personal space per user.
    Slices n and m come before the web interface (k), so it is built once.
12. **Public links** (2026-10-28): "anyone with the link" sharing exists but is off by default.
    An installation admin or a tenant's owner turns it on, per account. Optional password and
    expiry.
13. **Search** (2026-10-28): full text and OCR are both on, within limits.
    - Full text: files up to 50 MiB (`search.fulltext_max_bytes`), with at most 1 MiB of
      extracted text indexed per file.
    - OCR: images up to 10 MiB and PDFs up to 20 MiB, first 50 pages (`search.ocr_max_bytes`,
      `search.ocr_max_pages`).
    - Indexing runs as low-priority background jobs.
14. **Licence** (2026-10-28): AGPL-3.0-or-later for the whole repository. Every Go file carries
    an SPDX header. Anyone who runs a modified Selfcloud as a service must publish their changes.
15. **Public repository** (2026-10-28): `github.com/shukiv/selfcloud`, public from the start. It
    is also the Go module path.
16. **Upload disk checks** (2026-10-28, slice h): upload files are not preallocated with
    `fallocate`. The disk checks at creation (section 5) give the early `storage_full` instead.
    One account's unfinished uploads may hold at most 50% of the usable disk by default
    (`upload.max_disk_share_percent`). Many accounts together can still fill the pool. The
    install-wide check then refuses new uploads with `storage_full`, and the operator adds disk.
17. **Sharing model** (2026-10-28, slice n1). One ordered role ladder: viewer, commenter,
    contributor, editor, manager (section 8). Permissions sit on any file or folder, and a
    subject's role on a node is the highest over the node and its ancestors, so inheritance only
    adds: a deeper permission never lowers the role above it. Groups hold permissions as members do.
    A token carries at most editor, and reaches a directory it is confined to, when it names one.
    Moves keep the permissions of the moved node; copies take none. Expiry is in the store and
    filtered at every read, and the API sets it in slice n2.
18. **My files is private from account owners; the account key reaches all** (2026-10-28, slice n1).
    Each user has a personal folder, made with the user. An account owner manages team spaces but
    does not reach another user's personal folder unless it is shared with them. The account key is
    the tenant application's own credential, so it reaches every folder, personal ones included.
    Admins never reach contents, personal folders included. Deleting a user with files in its
    personal folder needs `delete_files=true`.
19. **Slice n split into n1 and n2** (2026-10-28). n1 is the model: roles, permissions with
    inheritance, groups, personal folders, team-space owners, the grants routes on permissions, the
    shared list and token node scope. n2 builds on it: access expiry through the API, no-download,
    the "editors can share" switch, ownership transfer, team-space restrictions and hiding, access
    requests and share emails. Nothing of n2 is in n1, except the `expires_at` and `owner_id` columns
    that n2 needs and the store reads already filter on.
20. **Sharing controls defaults** (2026-10-28, slice n2). Editors cannot share by default:
    editors_can_share is off, so only managers share. No-download allows inline view: it refuses
    downloads, signed links and copies, and leaves inline viewing where the type allows it. The
    access-request POST always answers 202, whatever the node. The team-space link setting,
    allow_links, is off by default, and does nothing until slice o.
21. **Trash and versions** (2026-10-28, slice m): soft delete with `trash.retention` 30 days; trashed items count
    toward quota; versions kept on every content replace, pruned at 100 or 30 days unless kept forever, counting
    toward quota; no server undo API. A restore needs the trasher or a manager, and editor on the original parent.
    Admins never reach contents, so no admin can restore: an account key does it.
22. **Web interface** (2026-10-29): Svelte 5 SPA in `web/`, embedded with `go:embed` (through `web/embed.go`);
    built in four parts, k1 to k4; strict CSP with no inline code; errors shown by code, never the server's
    text; installation branding only (`ui.brand`), per-tenant branding later. *Amended 2026-10-30 by decision 25:
    the Svelte build stayed until the flip at the end of slice v, and its policy was reported, not enforced, for the nonce.
    Replaced at the flip (slice v4b) by decision 25: the Svelte app is removed.*
23. **Share links, slice o** (2026-10-29, owner's decisions, which override the proposal where they differ):
    - Anyone links grant viewer only (commenter comes with slice r).
    - Anyone links must expire: 30 days by default, 365 at most. Organisation links may have no end date,
      capped by `sharing.max_expiry` when that is set.
    - Drop folders have no per-link limits beyond the account quota, `upload.max_file_size` and the rate limits
      (30 uploads an hour per address, 300 an hour per link, at most 20 open uploads per link).
    - Anyone and drop links need manager. Editors cannot make them, even where `editors_can_share` is on.
      Organisation links follow `mayShare`.
    - An organisation link is a recorded visit (`share_link_visits`), never a permission row. Pausing or
      deleting the link ends the access at once.
    - Links are allowed in My files: organisation links always; anyone and drop links when
      `accounts.public_links` is on.
    - The secret is stored hashed and shown once, at creation or reset. "Get a new link" replaces it.
    - Slice o is built in four parts, o1 to o4, each reviewed and pushed on its own.
    - Owner's decision 11 (2026-10-29, for o4): the owner's account-wide link list hides the item's name and path for links
      on items in other members' My files. The owner may still delete those links one by one, and "Delete all public links"
      includes them. Links in the owner's own My files show normally.

    Interpretations made in o1, for review (the owner has not decided them):
    - A visit made before `credentials_at` does not count, so a reset ends the access of the old copies until
      each member opens the new URL. A reset keeps the recorded visits.
    - While policy pauses a link, its changes and resets are refused and only deletion is allowed, so a
      setting that is off cannot be used to change a link.
    - `GET /nodes/{nod}/share-links` leaves out expired links. The sweep that removes them is built in o4 (below).
    - `POST /share/open` is limited to 30 a minute per address (`share.open.ip`). The proposal sets that limit,
      in its section 3, under o2.
    - The conflict answers carry a `reason` (`exists`, `members_only`) for the dialog to name the cause.
    - Organisation links take no password in o1; the field is refused as unknown.

    Interpretations made in o2, for review (the owner has not decided them):
    - The visitor's credential is refused with `not_found` once its link is dead, and `unauthenticated` when it is
      forged, malformed or past its own 15 minutes, so a holder learns nothing it did not already hold.
    - The route flag names the routes a visitor may call (the table in section 8). `POST /share/renew` is one of
      them; the proposal's table did not list it, since it is the credential's own route.
    - A visitor sees no `created_by` or `owner_id` on any node, not only on the link's root. The proposal named the
      root's `parent_id`, `created_by` and `owner_id`.
    - The per-link budget of wrong passwords slows attempts rather than refusing them (the proposal left the form
      open). A wrong password that trips the per-address lock is audited once per link and reason in 15 minutes.
    - An anyone link's end is capped at the lower of 365 days and `sharing.max_expiry`, and its default is 30 days,
      or the cap when that is lower. The proposal set the cap; the setting also applies, as for every share.
    - Changing an organisation link to anyone gives it the viewer role, sheds its members' visits, and keeps the
      secret, as the owner decided. Changing back ends the anyone visitors, and clears the password.
    - A signed-out visitor who opens an organisation link is still answered `not_found` (o1), so the open page
      sends it to sign-in with the secret waiting, as the proposal's section 4 describes. The anyone path is tried
      first, anonymously, so the page can tell the two kinds apart without the server saying which is which.
    - Visits and credentials are compared in whole milliseconds, since the store keeps milliseconds. An o1 visit
      made in the same millisecond as its link's creation was not counted; o2 fixes that comparison.
    - The policy answers `anyone_links`, the number of anyone links that have not expired, live or paused, and the
      switch shows it. The owner's "Delete all public links" control and the account-wide list were built in o4.
    - The per-address limits and lockouts of `/share/open` count an IPv6 client by its /64. The other limits still
      count one address each.

    Security fixes to o2 (review, 2026-10-29), each with a test that failed first:
    - A renewal racing a reset could mint a credential after the reset. The renewal now reads and writes in one
      transaction, carries the presented credential's issue time, and refuses it when the link's `credentials_at` is
      later. It never clamps the issue time forward.
    - The per-link password turn was an unbounded queue. A locked address no longer takes or waits for it; one
      request per address per link may hold or wait for it; a waiter gives up after 10 seconds (`rate_limited`,
      `Retry-After`). Under a distributed attack, real visitors of a password link may be asked to retry; resetting
      the link ends the attack. The bounds are the 10 seconds, the one-per-address rule and the lockout, not a count
      of the whole queue.


    Slice o3 (drop folders), 2026-10-29. The owner's decisions 9 and 10 (below) and decision 3 apply.
    - Decision 9: a drop link must end, 30 days by default and 365 at most, lower when `sharing.max_expiry` is lower.
      Omitting `expires_at` takes the default; `null` is refused.
    - Decision 10: a drop link is anonymous only (audience `anyone`), and needs `public_links`.
    - Decision 3: no per-link byte or file caps; the account quota, `upload.max_file_size`, the rate limits and the 20
      open uploads per link are all there is.

    Interpretations made in o3, for review (the owner has not decided them):
    - A drop link's open answer carries `upload_only: true` and no `node` or `link`, so the visitor learns no directory
      ID. The proposal's answer shape is kept for access links.
    - A drop link's `role` is `""` in the API, not `null`, as the access links' field is a string.
    - A drop link's `title` and `message` reach a visitor only through `GET /share/info`. Listings show a drop link to
      its sharers alone, and a member of the account never sees it.
    - A drop upload's `folder`, `parent`, `on_conflict` and `mime_type` are ignored, not refused, so a visitor cannot
      learn from an error whether a directory exists.
    - `GET /share/info` answers the link's own end as `expires_at`, not the credential's.
    - The creator's notice counts the files that arrived, and is sent by a loop that runs every minute, rather than by
      the sweep. The loop claims the count before mailing and gives it back if the mail does not go, so a failed send
      is not lost. A failed send or a refused budget also sets the link's last mail to the failed try, so the link is
      tried again an hour later, not on the next run.
    - A visitor's upload ends, its row and reservation released, when its link is truly gone for the visitor's
      credential: deleted, expired, or reset after the credential was issued. Deleting or resetting the link does it in
      the same transaction. A paused link (switch off, members-only, node trashed) and any `not_found` that is not from
      the link's state answers `not_found` and keeps the upload until its normal expiry, so resuming works once the
      link is back. A credential refused only for its own 15-minute expiry ends nothing.
    - A directory moved to another folder does not destroy a finished transfer: the file lands where the directory now
      is.

    Owner's decision, 2026-10-29, "Reserve as received" (the security review of o3, item M1): a drop upload holds only
    the bytes it has received. Each piece checks and charges the quota and the disk for the bytes it brings, and is
    refused with the generic answer of section 8 when they do not fit, keeping the bytes already received.
    `upload.max_file_size` still caps the declared length at creation. Other uploads keep their up-front reservation.
    The accepted limit this leaves (a drop upload can stop partway when the account fills) is in section 8.
    - Trashing a directory keeps its drop uploads (they are paused, not dead); the permanent delete of the directory
      still removes them. This follows the paused-link rule above.
    - The per-link `upload.create` limit (60 a minute) does not apply to drop uploads; the drop limits do.
    - The uploader's name and email are stored with a line break replaced by a space. The email must be a bare
      address (`net/mail`), and is not verified.
    - The `mail.drop.*` budgets are 60 an hour per account and 10 an hour per address, as share mail's are.
    - A drop link is hidden from listings for every caller who is not a sharer, the same as a paused link.

    Slice o4 (the sweep, the account-wide list and bulk delete), 2026-10-29. Decision 11 of the owner (below).
    - Decision 11: the owner's account-wide list shows links on items in other members' My files without the item's name or
      path (`hidden`, with `item_name` and `item_path` null). The owner may delete them one by one, and "Delete all public
      links" includes them. Links in the owner's own My files, and in team spaces, show normally.

    Interpretations made in o4, for review (the owner has not decided them):
    - An account key sees every item's name and path, as it reads the audit log and every file. Only the owner session
      is held to decision 11, and only an admin sees no item name or path at all.
    - An admin uses the bulk delete only. The per-link delete refuses an admin, since an admin reads no item and the
      owner's list is what it would delete from. The bulk route accepts it, as the owner's session and the key do.
    - The owner's session and the account key delete any link of the account, whatever node it sits on (a trashed item
      too), without reaching the node. A member keeps the old rule, and gets not_found for a link it does not reach.
    - `GET /accounts/{acc}/share-links` and `POST /accounts/{acc}/share-links/delete-public` answer a member and a token
      `not_found`, as the sharing-policy and audit routes do, not `forbidden`.
    - A listing shows a link on a trashed item, its own or an ancestor's, with `paused` true.
    - The bulk delete removes every anyone link, ended or not. Its confirmation count is `anyone_links` from the policy,
      which leaves out the ended links: a confirmed count may therefore be lower than the number deleted.
    - The sweep takes links that ended more than 24 hours ago, one transaction each. It deletes a drop link's open
      uploads with their reservations, and audits each upload as `upload.delete` with the reason `link_expired`.
    - The account-wide list names the creator by its display name, or by its email when it has no display name.
    - `has_link` is shown to a sharer for any live link (drop links included), and to anyone else only for the links its
      own listing would show. It is never shown to a visitor.
    - The o1 gap (a) is repaired: a team space is listed under Team spaces when a member has opened an organisation link on
      its root, at the link's role, as a permission on the root lists it. The o1 gap (b) was not reproduced at the API: a
      node reached by a permission and a link is listed once, since the query groups by node.

24. **Slice p: personal organisation and per-user quota** (2026-10-30, owner's decisions on the slice p proposal):
    - Quota is charged by folder: bytes in a user's My files count against that user's limit; bytes in a team space
      count against the account only, until slice u adds limits per space. A drop upload counts where it lands.
    - The account owner, the account key and an installation admin set each user's limit and the account's default
      limit. There is no default limit out of the box, so an upgrade changes nothing.
    - Recent records views (by signed-in users) and edits, kept 30 days and at most 500 items per user. It is private
      to its user, who can clear it.
    - A shortcut is a node of its own kind (the `nodes` table is rebuilt once, by a migration that runs with foreign
      keys off). It grants no access. A shortcut whose target the viewer cannot reach shows as unavailable, with
      "Request access", and never shows the target's ID.
    - Folder colours (from a fixed palette) and item descriptions belong to the item: editors set them, and every
      reader sees them.
    - Labels: every reader of an item sees its values and editors set them. A field may be restricted, and is then
      seen and set only by managers of the item, the owner on team spaces, and the account key.
    - The storage page: owners see the account's total and each member's total, but no file names from members' My
      files; they see team-space files by name. Members see their own largest files. Admins see totals only.
    - Home is the landing page at `/`; My files stays at `/files`.
    - Slice p is built in five parts, p1 to p5, each reviewed and pushed on its own, p1 first.
    - A reservation counts against the folder of its upload's target directory, and a move of a directory moves the
      uploads aimed into it. Migration 0017 brings any earlier upload into line with its directory. This departs from
      the slice p proposal's "released from the folder the upload row names": the rows now follow their directories.
      The consequence: an upload aimed at a directory that moved across folders now completes into the directory's new
      folder, under the upload creator's access check there. Before, a non-drop upload failed with `not_found` at
      completion. (Owner's decision, 2026-10-30.)
    - The account's own refusal (`quota_exceeded`, scope `quota` or `account_reservations`) carries its numbers only to
      an installation admin, the account key, and an account owner in a session. A member, a link or drop visitor, and
      every delegated token (including one acting for an owner) get the same refusal with no details and no number.
      (Owner's decision, 2026-10-30; the token and scope details were accepted with it.)
    - A stat and a child listing carry `created_by_name` and `owner_name` for a caller of the account who reaches the
      node: the account key, a member or owner in a session, or a token acting for one. An installation admin, a link or
      drop visitor, a caller of another account, a deleted user, and a user with no display name get none. A listing
      names its page with one query. The interface shows no row for a missing name, never the ID. (Owner's decision,
      2026-10-30; who gets no name was accepted with it.)
    - The sign-in headline stays "Your files. / Your server. / Anywhere." (Owner's decision, 2026-10-30.)
    Interpretations made in p1, for review (the owner has not decided them):
    - The caller's own meter (`GET /me` `storage`) counts its My files and their reservations against its own limit. It
      does not count team spaces, and it shows no account figure.
    - The admin's storage view lists the team spaces' totals, with no names, and no personal folders or members.
    - Largest files sort by the file's own size, then by ID. Versions are shown beside each file, not added to the sort.
    - A file counts as in the trash when it sits below a trashed item.
    - A drop piece checks its user's limit with the in-flight drop bytes of the whole account (conservative: a piece
      can be refused when only another folder's pieces would fill the limit).
    - The folder detail reads the folder's own counters, not a walk of its tree (this closes the k4 gap).
    - A user's GET and PATCH answer `effective_quota_bytes`; list responses carry only the raw `quota_bytes`.
    - A team-space ownership transfer moves no bytes, as before, so it is not limit-checked.
    Interpretations made in p2, for review (the owner has not decided them):
    - Views are recorded by `GET /nodes/{nod}/content` and `POST /nodes/{nod}/links` only, and only for a file. A ranged
      or conditional GET counts as a view; the ten-minute gate keeps one view per file in that window.
    - Edits are recorded in the transaction of the writes that change a file's contents: a file created (`POST .../files`),
      an upload completed (both the commit and the last piece, which share one completion), `PUT .../content`, a version
      restored, and a copy (the new copy root, in the request or in its job). The copy rule is the owner's decision of
      2026-10-30 (decision 24).
    - `GET /starred` and `GET /recent` list only items the caller still reaches and that are not in the trash, as stars
      have always been. The proposal did not say so for Recent; the entry is kept in the database either way.
    - Starring needs reach at any role and write from a token. Unstarring by a signed-in user needs no reach (owner
      decision, review 2026-10-30): it answers 204 for any well-formed ID, so no node is revealed, and a star on a trashed
      or unreachable node is removable. A token needs write and reach to unstar. `DELETE /recent` and `DELETE
      /recent/{nod}` are refused to every token, and `DELETE /recent/{nod}` needs no reach for the signed-in user.
    - The star limit counts every star the user holds, listed or not.
    - `starred` is a `*bool` with `omitempty`: it is absent for a key, an admin or a visitor, and `false` for a signed-in
      user who has not starred the node.
    - A page of stars, recent items or shared items comes back shorter than its limit when the reach check drops some
      of its rows; `next_cursor` still follows the last row read, as `/shared` already did.
    - `/shared?order=recent` sorts by `shared_at`, the newest of the user's live permissions' creation times and its
      organisation-link visits on the node. The cursor is that time and the node ID.
    - The sweep's Recent limits (30 days, 500 per user) are constants, not settings.
    - Both new tables cascade from accounts, users and nodes, as the other tables do. Purging a node or deleting a user
      therefore removes its stars and recent entries with no extra code.
    Interpretations made in p3, for review (the owner has not decided them):
    - The palette is twelve names: red, orange, yellow, green, teal, blue, indigo, purple, pink, brown, slate, grey. A colour
      belongs to a directory only; a colour on a file or a shortcut is `invalid_request`. A folder's root may carry a
      description, and the palette is checked in the API and by the migration's CHECK.
    - Link visitors do not see colour or description, on a stat or in a listing. Decision 6's "every reader" means the
      signed-in readers of the item, and section 6 of the proposal strips both for visitors. The lead corrected this
      reading on review (2026-10-30): a description may hold private notes that the sharer did not mean to publish.
    - A link visitor's listing leaves shortcuts out. A visitor's stat of a shortcut is `not_found`, the same answer as
      a stat of an item outside the link: a visitor learns nothing of a shortcut, not even its name or kind.
      (Corrected on the automated review of e493443, 2026-10-30.)
    - `target` is omitted, not sent as null, when the caller does not reach the target. `unavailable: true` marks the
      shortcut in both cases (unreachable, and purged). A target's `kind` is given only to a caller who reaches it.
    - A shortcut cannot point at a shortcut or at a folder's root. The proposal did not say so; both are refused.
    - A shortcut's name defaults to its target's. When a name is taken, the "name (n).ext" rename treats a shortcut like a
      directory, so it gets no extension split.
    - A shortcut cannot be shared by link (`invalid_request`). Permissions on a shortcut are allowed and show it only.
    - Creation is audited as `node.shortcut`, with the target's ID in the details. A colour change is audited as
      `node.appearance` with the new colour (null when cleared); a description change records only "changed" or
      "cleared", never the text.
    - A description change bumps the ETag of a file and the modified time, as a rename does.
    - `PATCH` takes a tri-state colour and description: absent leaves them, null or "" clears them. A description is
      refused when it is not valid UTF-8 or is over 1000 characters (counted after the control characters are removed).
    - A copy of a shortcut keeps its target and does not check the copier's reach to it: the copy shows unavailable where
      the copier cannot reach the target, so the copy reveals nothing.
    - Recent records no edit for a shortcut, since it has no contents.
    - An ownership transfer of a shortcut takes the same path as any node's. Not tested; a shortcut holds no bytes, so none move.
    - Migration 0019 rebuilds the `nodes` table. Its backup note is in section 8 (p3). Its SQL is in
      `internal/store/migrations/0019_shortcuts.sql`.
    - A quota refusal (`quota_exceeded`, 507) is logged below ERROR and counted under the metrics status label
      `quota_refused`, not as a server failure. `storage_full` (also 507) stays a server failure, as the disk is the
      server's fault. The label is the reading the coordinator review of p3 gave (finding 10).
    - A request the server itself cancels at the shutdown deadline answers 500 and is logged as a server failure, not as
      a client disconnect. The server gives its cancel the cause `errServerShutdown`; a client that goes away keeps the
      499 path (finding 1).
    - Accepted, not fixed: a link visitor's listing drops its shortcuts after the page is read (`api/nodes.go`, the loop
      that skips `store.KindShortcut`), so a visitor's page can come back shorter than its limit. `next_cursor` still
      follows the last row read. Filtering in the store query would change the query; the coordinator review of p3 accepted
      the note without code.
    - Kind comparisons (node kind, Go and SQL, 48 in 16 files, counted by the guard test). The proposal said 31; I could not
      reproduce 31, and the guard counts every match of the pattern in `internal/api` and `internal/store`, excluding folder,
      job and principal kinds. The decision for each is:
      - *Usage and counters (SQL `kind = 'file'`, shortcut counted as nothing, so no quota):* `store/nodes.go` subtree files
        (2), `store/usage.go` recount (2), `store/sharing.go` totals (2), `store/storage.go` largest files (1),
        `store/folders.go` folder counters (1). Kept as they are.
      - *Content and ETag, file only:* `api/nodes.go` `nodeETag` (77), the rename ETag (912), PUT content (1024),
        `api/transfer.go` copy ETag and bytes (305) and move ETag (396), `api/versions.go` (81, 103, 125), `api/download.go`
        (400), `api/links.go` link create and link content (326, 381), `api/views.go` view recording (153), and
        `store/nodes.go` InsertNode content tag (305). A shortcut is refused by each refusal and takes no ETag or bytes of
        its own. Kept.
      - *Directory-only checks (refuse a shortcut):* `api/access.go` anchorDir (341), `api/nodes.go` walkDirs and
        walkDirsTx (276, 293), handleListNodeChildren (545), mkdir's existing-directory check (643: a shortcut is a name
        clash), `api/uploads.go` upload parent (340, 500), `api/tokens.go` token node scope (436), `api/transfer.go`
        move and copy destination (98), `api/share_links.go` drop link (374). Each refuses a shortcut, `invalid_request` or
        a path error. Kept.
      - *New, for the shortcut kind:* `api/nodes.go` toNodeJSON default `unavailable` (223), the shortcut-only children
        refusal (542), the visitor listing omission (468), the colour-on-directory rule (879); `api/access.go`
        `nodeAccessIn` refusing a shortcut to a link visitor (added with the review of e493443, so every route answers as
        for an item outside the link); `api/shortcuts.go` target
        lookups (39, 79), the creation directory check (121, 144) and the target-is-not-a-shortcut check (194); and
        `api/share_links.go` link refusal (368). Each is covered by a test in `shortcuts_test.go`.
      - *Replace and conflicts:* `api/transfer.go` replace (193, two comparisons): a shortcut neither replaces nor is
        replaced, because replace is only file over file. Kept.
      - *Name rule:* `api/transfer.go` freeName extension split (207) splits only for a file. Shortcut is treated as a
        directory; see the name interpretation above.
      - *Copy edit (review 2026-10-30):* `api/transfer.go` (two new: the sync copy and the queued copy's job) skip the
        Recent entry when the copied root is a shortcut. A shortcut has no contents, so copying it is not an edit. Kept.
      - *Request through a shortcut (review 2026-10-30):* `api/access_requests.go` (one new comparison) resolves a request
        that names a shortcut to its target. Every other request is unchanged. Kept.
      - *Cycle check:* `api/transfer.go` (149) checks only a directory moved into itself. A shortcut has no children, so it
        cannot make a cycle. Kept.
      - *Node-kind in toNodeJSON:* `api/nodes.go` (233) gives MIME and SHA-256 to a file only. A shortcut has neither.
        Kept.
    Accepted on the security review of p3 (2026-10-30), recorded without a code change:
    - A shortcut's default name is its target's name. The creator could see the target and chose to place the shortcut
      beside it, so the name adds nothing the creator did not have.
    - Copying a shortcut clones its target without re-checking the copier's reach. An unreachable target shows as
      unavailable to the copier and grants nothing, so the copy reveals no target.
    - `foreign_key_check` covers the whole database, so an upgrade is refused while the database holds dangling
      references from earlier releases. The operator repairs them first; the error names the tables.
    Request access on a shortcut (decision 5, decided by the lead on review, 2026-10-30; migration 0019):
      - A request that names a shortcut is filed on its target, resolved on the server. The access request row
        records the shortcut in `via_node_id` (nullable, `ON DELETE SET NULL`) and keeps `via_shortcut` set. The
        target's managers receive the request, and approving it grants the target.
      - The caller must reach the shortcut itself. A shortcut it cannot see, a shortcut with no live target, a missing
        node, a trashed or foreign target, and a target the caller already reaches all answer the same `202` with no
        body, and file nothing and mail nothing. A duplicate of a pending request answers the same way. A test pins
        status and body.
      - The decision mail to the requester: an approval names the shortcut and links to it only while the requester
        still reaches it now (checked as the requester, not trashed). If the shortcut is trashed, moved out of reach or
        deleted (`via_node_id` NULL, `via_shortcut` still 1) after filing, the mail is generic and carries no link. A
        denial is always generic, with no name and no link, because nothing was granted and a contributor chose the
        shortcut's name. The mail never falls back to the target's name or link. The generic wording has no empty line
        where a link would be, in all seven languages (`access_decision` templates, guarded on `.Link`).
      - Accepted on the second review, no code: a decision mail confirms that the target is live, which is the one place
        target state shows to a requester; zero-width and line-separator characters stay in descriptions.
      - The manager's view (`GET /nodes/{nod}/access-requests`) shows the target, as for any request, and no field of
        the shortcut. `via_node_id` and `via_shortcut` appear in no response.
      - The request's audit entry and the decision's audit entry name no node when the request came through a shortcut;
        they record `via_shortcut`. Both are filed under the target's folder, as before.
    Corrections made on review, with tests: a link visitor gets no colour or description; the `node.shortcut` audit
      entry carries no target ID; the runner checks `PRAGMA foreign_keys` inside the transaction and reads the mode
      directive from the first line only; a `foreign_key_check` failure names the tables; bidirectional controls
      (U+202A-202E, U+2066-2069) are stripped from descriptions, and ZWJ, ZWNJ, LRM and RLM are kept; `""` clears a
      colour as null does; a copy of a shortcut is not recorded as an edit in Recent, sync or queued.
    Interpretations made in p3's web pages, for review (the owner has not decided them):
    - "Add shortcut" is in the selection toolbar, for one selected item that is not a shortcut. The destination is chosen
      with the move tree (`PickDestination`, mode `shortcut`) and must be a directory, since a space's root cannot hold a
      shortcut. The name dialog starts with the target's name and shows it, editable, before the shortcut is made.
    - Opening a reachable shortcut goes to `/n/{target}`, the target's own address. Opening an unavailable one shows a
      dialog with Request access on the shortcut (decision 5). No page shows the target's ID for an unavailable shortcut.
    - Listings sort folders, then shortcuts, then files, under every key.
    - Each colour name has a light and a dark shade in `web/src/index.css`, and each keeps 3:1 against its page
      (`palette.test.ts`). A folder's icon takes its colour, and the colour's name is also said in text for screen readers.
    - An editor sets the colour and description in the details drawer, and sends both together. A cleared colour or an
      empty description is sent as null. A reader sees the colour's name and the description as text: `dir="auto"`,
      line breaks kept, never HTML.
    - A shortcut's icon is its own, whatever its target is, so an unavailable target shows nothing of what it is. A
      shortcut has no size to show.
    - A shortcut has no colour or description control in the interface, and a reader sees none either. The owner has
      not decided whether a shortcut may carry a description; the server's description rule does not check the kind.
    - Not checked in the browser: Home, Starred and Recent show shortcuts only as their own rows do; no shortcut mark is
      added there. Arabic was not looked at in a browser; only Hebrew was (the layout shots).
25. **React web interface, slice v** (2026-10-30, owner's decisions). It replaces decision 8, and amends decision 22.
    - Stack: React 18.3 and Ant Design 5.29, pinned exactly, with TanStack Query, React Router 7 (the router is
      built on `react-router-dom`), i18next and lucide icons. Node is needed only
      to build.
    - The look is the default one: a blue sidebar with a yellow active pill, radius 4, and the palette in `webui/src/theme/tokens.ts`. The
      default accent is `#053D8E`. Branding overrides named tokens (`ui.brand.palette` and `ui.brand.sidebar`), and
      the accent alone derives the rest of the family.
    - Fonts: the system stack. The self-hosted Inter and Noto faces stayed in the Svelte
      build until the flip, when they went with it.
    - Strict CSP with a per-request nonce. The page carries a nonce in its meta tag and on its script and link tags;
      the policy's `style-src` allows that nonce. The Svelte build reports this policy as
      `Content-Security-Policy-Report-Only` and enforces today's policy. The React build enforces it from its first
      build, and its tests fail on any violation. At the flip, the nonce policy is enforced for everyone and the
      option that selects the mode is removed. The report-only reports stay in the browser console: there is no
      report endpoint, and none is added until the flip shows a need for one.
    - The React build runs beside the Svelte one, selected by the `reactui` build tag. Until the flip there is no
      runtime switch and no new endpoint.
    - The flip, at the end of slice v: the Svelte app is removed and `webui/` takes the `web/` paths, so the Makefile,
      Dockerfile, CI and the catalogue tests keep their paths.
    - *Slice v2, for review.* These are the interpretations the file browser needed. They are not in the owner's decisions,
      so each is marked for review.
      - The React bundle splits the library core (React, the router, the query client, i18next) into one `vendor`
        chunk with `manualChunks`. Without it the chunk that holds the shared antd pieces is 184 KB gzip, over the
        120 KB budget. This reverses the "no manualChunks" measure of section 8 for the library core only, not for antd.
      - The first-load budget is 330 KB gzip, and it is the measure. It is the total of the JavaScript files that
        `index.html` names at once (its module scripts and its modulepreload links), and `webui/scripts/check-dist.mjs`
        computes and enforces it on every build. The first build with the vendor split comes to 313.0 KB across 6 files.
        The entry budget (290 KB) and the per-chunk budget (120 KB) stay. The earlier measure, "entry plus static imports
        at 250 KB", is retired. The 330 KB figure is set here: section 8 has no first-load budget.
      - The share actions are hidden, not disabled, so a viewer or editor never sees a control that v3 will own.
      - Alt+ArrowUp moves focus up a row, as the Svelte listing does (the arrow case matches first). Backspace goes to the
        parent folder. Kept as it is, not changed.
    - *Slice v3, for review.* These are the interpretations and deliberate differences from the Svelte sharing interface.
      Each is a choice the owner has not made; each is listed so that it can be changed.
      - The selection bar's Share button is shown for any single picked item, as Svelte shows it. The dialog then shows a
        viewer only its own row and says what the caller's access is. (A narrower gate was tried and reverted.)
      - Team spaces are a list of links beside the sidebar's menu, each with its own options menu. Svelte's row nests a
        button in a link, which antd's menu items do not allow. The look is close, not identical.
      - The recipient picker is a custom ARIA combobox on an antd input, not antd Select: Svelte's keyboard rules (Escape
        closes the list only, arrows, Enter) and the input id are the contract the e2e drives. The role pickers are antd
        Select.
      - A request row shows "A person" when the requester has no name; Svelte showed the raw user ID. The v2 rule holds:
        no raw IDs.
      - A failed refetch of Shared with me or Requests keeps the list and shows the failure above it. Only a failure with
        no data is a full error. Svelte replaced the list with the error.
      - A change of session while `/s` is open ends the visit: the credential, the renewal timer, the secret and any typed
        password are dropped, the address is stripped, and the dead-link message is shown. Svelte did not watch the session.
      - A signed-out visitor to a dead link is sent to sign in, as in Svelte, and keeps the secret for the sign-in. The
        dead-link message is shown to a signed-in person.
      - An ancestor with an empty name (a folder root) is named "from a parent folder", not "from " followed by nothing.
      - The Requests entry shows to anyone who has a request waiting, with its count, as Svelte does. It is shown to an
        owner always.
      - `/s` sits in the split-screen layout (AuthLayout) for signed-in and signed-out people alike. Svelte's PublicLayout
        is the same frame for both.
      - Two faults of earlier slices are fixed here. The sign-in page read `next` after the session was set, so a sign-in
        that started from a link landed on home (v1). A folder read that finished after the person had moved to another
        page moved the address back (v2, NodeLink).
      - The Manage > Overview switch and the Manage > Links check (Delete all public links) are not in this build: both
        pages are part v4. The switch is set through the API for now, and the Links check is kept to move with its page.
      - The `maxExpiryDays` cap is a plain writable set at start-up, not a per-person store, so the reset at sign-in
        keeps it.
      - The sharing cap is applied to the dates a person types. React clamps an organisation link's end, a person's
        end and a drop link's end to `maxExpiryDays` (`expiryFromInputCapped`); Svelte does not clamp, and a date past
        the cap is refused by the server. The React choice shows the cap in the picker, so the person sees the limit
        before the server refuses. The Svelte behaviour goes at the flip. (Review v3, M3.)
      - When the first read of a link list fails, React shows the failure and no choices or form (the switch, the
        radios, the date fields): an action there would act on a link that was not read. Svelte's fieldset stays visible
        with the error above it. A failed refetch of a list already shown keeps the list in both. (Review v3, H1.)

    - *Slice v4a, for review.* These are the interpretations and deliberate differences from the Svelte pages.
      - The Svelte Manage pages swallow a failed links count or a failed names read in the audit log. React shows the
        failure by code. (Not a review item; a deliberate difference.)
      - Escape and the close control do not close DeletePublicLinks while its delete is in flight, and Svelte's Confirm
        does. (Review v3, L4, applied in v4a.)
      - The switch-account dialog on the sign-in page does not answer to a click outside it: a question about the session
        is answered only by its buttons.
      - The shell's team-space error is shown in the sidebar. On a phone width the sidebar is the drawer, so the error is
        shown inside it. (Review v3, L8, applied in v4a.)
      - The owner pages' row menus are antd Dropdowns, not the Svelte menu, and the admin tabs are buttons with
        `role="tab"` rather than antd Tabs. Both are for the e2e selectors that the Svelte suite uses.
      - A sign-in goes straight to the signed-in person's own home (`afterSignIn(next, user)`, `homeFor`), not to "/"
        first. The old two-step path lost the admin's second navigation, and the page stayed blank after an admin's
        sign-in (v4a, found by the e2e suite). Svelte has the same two steps but no role-specific home.
      - An installation admin does not read the requests count, which the server refuses for an admin (403). Svelte
        skips the read for admins too.
    - *Slice v4b, the flip (done; the flip steps above are now the state of the tree).*
      - The Svelte app is removed. `webui/` is `web/`, and its Go package is `web` (`web/embed.go`). The `reactui`
        build tag and `internal/api/ui_assets.go` and `ui_assets_reactui.go` are gone; `internal/api/server.go`
        embeds `web/dist` directly.
      - One content policy: enforced for every installation, with a fresh nonce on the HTML page. `web.Options` has no
        style option, `StyleMode` and the `Content-Security-Policy-Report-Only` header are gone, and `web.New` refuses an
        index without the nonce placeholder (a stale build). No config key chose the mode.
      - Fonts: the system stack only. The Inter and Noto faces and their OFL licence texts went with the Svelte tree. OFL
        stays in the licence rule's allowed list for code that may ship later; no font ships now.
      - Makefile: `make web`, `make build` and `make e2e` drive the one interface; `web-react`, `build-react` and
        `e2e-react` are gone. CI runs one web job (check and build) and the Go steps.
      - The e2e suite is the React one, now in `web/e2e`. Its 136 checks are unchanged; only comments moved. Every
        Svelte check description is in it when parenthetical values are ignored. The accent check writes its value as
        `rgb(...)` in the React suite, where the Svelte one wrote the hex colour.
      - For review: `web/scripts/convert-catalogues.mjs`, the one-shot conversion of the Svelte catalogues, is deleted.
        Comments in components and tests that say "as Svelte does" are kept as the record of the behaviour they match.
        The paragraphs of slices k1 to v4a still describe the Svelte app and the `webui/` paths of their time.
26. **Webhook endpoints, slice j1** (2026-10-30, the owner approved slice j's proposal with every recommendation (a)).
    Decisions and interpretations:
    - **Rotation and the old secret: decided.** The old secret stays valid for 24 hours, as the approved proposal
      says. After a rotation it is no longer the endpoint's current secret, and it still signs during the grace
      period. A second rotation ends the first old secret at once, even if the caller lost the response. The brief's
      wording "rotation invalidates the old secret" was the lead's error and is not the design.
    - **Disabled endpoints.** A disabled endpoint leaves its state only by a PATCH that changes its URL in the same
      request (the state then becomes the one asked for, or paused). A state change alone, to paused or to active,
      is refused as a conflict, whatever the reason (`failing`, `overflow`, `secret_unreadable`). A PATCH with an
      unchanged URL keeps the state and the reason. A new URL resets `consecutive_failures`. A secret rotation
      pauses a disabled endpoint, clears the reason and resets the count; the owner then sets it active. The
      proposal said "URL or secret" in one step; this build splits them.
    - **Endpoint limit: 100 per account.** The lead's choice; the proposal named a limit but no value. Going over it
      is `conflict` with `reason: limit`. The count is read inside the same write as the insert, so concurrent
      creates cannot both pass. The limit is per account.
    - **URL shown to others: origin only.** An owner's reads and every audit entry carry only the scheme, host and
      port of an endpoint URL (`urlOrigin`), and never its path, query, fragment or user name. The account key's
      reads keep the full URL. This is tighter than the proposal, which only removed the query from audit entries:
      a token may sit in the path (`/services/T/B/SECRET`). Slice j3's worker logs and its `last_error` must use the
      same helper.
    - **Secret encoding.** The shown secret is `whsec_` plus 43 unpadded base64url characters of 32 random bytes.
      The proposal did not fix the text form.
    - **Binding.** The additional data names the account, the endpoint and the column (`secret` or
      `previous_secret`). A rotation re-seals the old secret under the previous column's binding.
    - **Expired grace copy.** `previous_secret_enc` stays in the row after its grace period until the next rotation.
      `previous_secret_until` is returned as null once it has passed, and nothing signs with the copy. The worker
      part (j3) clears it.
    - **Responses that show a secret** carry `Cache-Control: no-store`, as token answers do.
    - **Previous links key.** Decryption also tries the previous links key, so a rotation of the links key does not
      lose secrets. The rewrap command is slice j4, as the proposal says.
    - **Not in j1 (later parts).** The address deny list and the resolution of the host are slice j3 (decision 28). The test
      route, the delivery log and the retry route are j3 as well. The quota-warning route is j4. Note for
      j4: its guard must check the addresses actually resolved at delivery, and must not rely on `net.ParseIP` of the
      host string, because `parseHostLiteral` accepts forms such as `127.1`, `2130706433`, `0x7f.1` and `localhost`,
      which name loopback without being an IP literal. A private IP literal is accepted now and nothing is sent
      before j3. The test route, the delivery log and retry routes, and the quota-warning route are later parts.
    - **Choices not in the proposal.** A URL with a fragment is refused. An installation admin key reads no webhooks.
      One delivery per endpoint per event (a UNIQUE constraint). The config key is `webhooks.allow_http` only.
      The schema's CHECK keeps `disabled_reason` set exactly when the state is disabled.
    - **Slice j2** (event emission) is recorded in decision 27, below. Its interpretations are for review there.
27. **Event emission, slice j2** (2026-10-30, the owner approved the proposal with every recommendation (a)). The
    interpretations below are for review. The code is `internal/store/webhook_events.go`, and the guard tests are in
    `internal/store/webhook_guard_test.go`.
    - **The hook.** `InsertAudit` reports the change that its audit entry records, in the change's transaction. The
      action-to-event map is `webhookAuditEvents`: `node.create` and `node.shortcut` give `file.created`;
      `node.write`, `node.rename` and `node.version.restore` give `file.updated`; `node.move` gives `file.moved`;
      `node.trash` gives `file.deleted`; `node.restore` gives `file.restored`; `user.create` gives `user.created`, and also `user.verified` when the user is
      verified at once; `user.verify` gives `user.verified`. Each payload is built from the row the change left, never
      from the audit details, so a drop upload's visitor name and email never reach a payload.
    - **Explicit emits.** Four paths cannot be described by one audit entry, so they emit themselves: a copy reports
      each file it makes (`copyTree`, since its audit names only the root), or one folder summary above 1,000 files; an upload reports `upload.completed` after
      its file event (no audit action exists for it); a deleted user's files report `file.moved` each (the audit
      names the recipient only); and an ownership transfer into a user's My files reports `file.moved` for each file
      below (`ownership.go`).
    - **Folders.** A folder reports nothing. Trashing, moving or restoring a folder reports each file below it, because
      each file's row changes, and a folder with no file reports nothing. Renaming a folder reports nothing, since no
      file row changes. Directory events stay out of v1 (question 7).
    - **Big folders (owner decision, 2026-10-30), with two further limits (round 3).** A change too large to report
      file by file reports the folder once instead: one event of the same type (`file.deleted`, `file.moved`,
      `file.restored` or `file.created` for a copy) whose data is a `WebhookFolderData` with `kind` `dir` and `subtree`
      `true`, and no file named in it. A change is too large when any of these holds, decided by `ReportFor`, which
      every subtree emit and every copy calls:
      1. more than `WebhookFolderThreshold` (1,000) files (the owner's rule);
      2. more than `WebhookDeliveryLimit` (5,000) deliveries, that is the files times the active endpoints that
         subscribe to the type. At five endpoints or fewer the first limit is the binding one, so the owner's rule
         stands unchanged there; above five endpoints it is tightened (1,000 files with six endpoints is a summary);
      3. more than `WebhookNodeCap` (10,000) nodes in the tree, the root included, whatever its file count. The walk
         that counts a tree (`countSubtree`) stops at 10,001 rows, so a huge tree costs the cap and no more, and the
         tree is never loaded. A tree over the cap reports the folder even when it holds no file, because a walk that
         stops at the cap cannot tell whether a file lies beyond it.
      A tree with no file within the cap reports nothing. The receiver lists the folder through the API. The flag and
      the three limits are documented in `api/openapi.yaml`. Tests hold the boundaries on each path: 1,000 by 5 per
      file, 1,001 by 1 summary, 1,000 by 6 summary, 100 by 50 per file, 101 by 50 summary, a tree of 10,000 nodes per
      file and of 10,002 a summary, and the copy at 1,000 by 5 and by 6 (`TestPerFileBoundaries`,
      `TestNodeCapBoundary`, `TestWebhookCopyFanoutLimit`).
      **Interpretation.** The owner's rule holds at five endpoints or fewer. Above five, the change reports the folder
      sooner than the rule's 1,000 files, which is the reason for the second limit. A replacing move or copy emits
      twice: the displaced node's event and the moved or copied node's event. Each is bounded by the limits, so the
      total is bounded by twice the limit, not once. Splitting a large folder into smaller moves is accepted: it is a
      way around the limit that costs the caller, not the receiver.
    - **Shortcuts.** A shortcut reports `file.created` with `kind` `shortcut` and no target. The proposal's payload
      carries `kind`, so shortcuts are in its event set. The brief warned that a shortcut event must not carry a target,
      and the test holds it to that.
    - **`file.updated` covers rename.** The proposal does not say whether a rename counts. A rename changes the name in
      the payload, so it counts.
    - **Restore (owner decision, 2026-10-30).** A restore reports `file.restored` per file, with the same big-folder
      rule. It is on the fixed event list and in the store's CHECK, through migration 0021, which rebuilds the two
      outbox tables with the new list (migration 0020 is not edited). This reverses the first draft, which reported
      no restore.
    - **Not reported.** `node.purge`, with the sweep's purge, as question 7 says. `node.version.update` and `node.version.delete`, `node.sharing`, `node.appearance`, `node.link`,
      and the link, permission, grant, group, folder, sign-in, token, key, account and administration actions. Each one
      has a reason in `webhookAuditNoEvent`. A change of owner inside a team space (`node.transfer` without a move)
      reports nothing, because it moves nothing.
    - **No row without a subscriber.** An event is written only when at least one active endpoint of the account
      subscribes to its type. The brief said "if it commits, the event exists". I read that as: the event exists when a
      delivery waits for it. The sweep's rule (delete events that no delivery refers to) assumes the same.
    - **Paused and disabled endpoints get no delivery row.** A change made while an endpoint is paused is not queued
      for it. This is the proposal's section 2 rule applied as written. It is a consequence, not an extra decision.
    - **Payloads.** The envelope is the proposal's: `id`, `type`, `time`, `account` (`id`, `external_id`, which is `null`
      when unset) and `data`. File events carry `id`, `folder_id`, `kind`, `size`, `name` and `parent_id`. User events
      carry `id` and `external_id`. `upload.completed` adds `upload_id`, since the proposal left its fields open. The
      stored bytes are the bytes the worker will sign. The proposal's question 2 said the worker would join the account's
      external ID at send time. Reading it into the stored payload instead is one query in the transaction, and it makes
      j3 simpler. This is a change from that wording.
    - **Personal folders (decision 18).** Only the account key manages endpoints, and an owner reads them without the
      secret (decision 26). So no endpoint belongs to a user, and every endpoint receives My files events. The rule holds
      by construction, and `TestWebhookPersonalFileReachesAccountKeyEndpoint` pins it. If an owner-managed endpoint is
      ever added, the fan-out must filter by the endpoint's creator before that change lands.
    - **Isolation.** The fan-out query filters on the account. A composite foreign key also refuses a delivery to another
      account's endpoint, which a test shows by removing the filter: the change then fails with a constraint error.
    - **No in-memory set (owner decision, 2026-10-30).** The first draft kept a set of accounts that may have an
      endpoint, loaded at `Open`. It was removed: a second process writing endpoints to the same database would not
      have been seen until a restart. Now the fan-out asks the database inside each transaction, at the first
      emission for the account. A mapped change to an account with no endpoint pays that query; the owner's estimate is about 60 µs per change (not re-measured here).
      `TestEndpointMadeByAnotherInstanceReceivesEvents` opens two `Store` instances on one file: an endpoint made through
      one reaches an event written through the other.
    - **Not in j2 (built in j3, decision 28).** The 100,000 pending cap (proposal question 10) is not checked here. It needs a count of pending
      deliveries per endpoint, and the worker's disable path (j3) owns it. j2 on main has no worker, so the outbox
      grows with its changes until the worker of j3 is on main. Since j3 (decision 28), the worker sends them, and the cap
      and the sweep are in place. `webhook.test` is sent by its route (j3). The quota events, `quota.warning` and
      `quota.exceeded`, are sent since j4 (decision 30). The slice review of j is done (`review-j3.md`, `review-j4.md`); what
      still blocks the release is in decision 30.
    - **Cost, measured.** The proposal has no performance section, so the brief's rule was applied as measured. Store
      benchmarks (`internal/store/webhook_events_bench_test.go`, pure-Go SQLite, one change per transaction, machine
      shared with other sessions at load 8 to 11): a file and its audit entry cost about 270 to 290 µs without the
      outbox and about the same in an account with no endpoint; with one active endpoint about 680 to 820 µs; with five,
      about 965 to 1,333 µs. A single `EmitEvent` costs about 0.2 to 0.3 µs with no endpoint and about 85 µs with one.
      A copy of N files with one endpoint therefore adds about 85 µs times N inside one transaction. The copy job's
      limit is 200,000 nodes, which would add about 17 seconds of writer time at one endpoint. This is the cost the
      owner should weigh. The big-folder rule bounds it for a folder: at exactly 1,000 files the writer hold of a trash
      is about 3 to 13 ms with no endpoint and about 290 to 370 ms with five (`BenchmarkTrashFolder1000NoEndpoints`,
      `BenchmarkTrashFolder1000FiveEndpoints`, the store's `webhook_folder_bench_test.go`; 1,000 events and 5,000
      deliveries). Load average on the machine was about 10 on 8 threads during that run, so these are rough.
      **Round 3 (the reviewer's measurements, same machine, load 4 to 7).** A trash of 1,000 files with 100 endpoints
      reports the folder once and holds the writer about 14 ms; before the delivery limit it wrote 100,000 delivery rows
      and held the writer 12.06 s, past the 10 s `busy_timeout`. A trash of 1,000 files with 5 endpoints still reports
      each file, at about 309 ms, the same as before within noise. One `EmitEvent` with 50 subscribers fell from 3.34 ms
      to 2.44 ms when the deliveries became one `INSERT ... SELECT` from `json_each` (about 67 down to 49 µs per
      delivery; `BenchmarkEmitFiftyEndpoints`). The decision on a 300,000-node tree (a count of at most 10,001 rows,
      then `ReportFor`) takes about 337 ms per call (`BenchmarkDecision300kNodes`, 20 runs). The reviewer measured the
      old unbounded count at 2.3 s on the same shape of tree; I did not re-measure the old count.
      **Event IDs.** Receivers see the event's `id` (an `evt_` ID, written into the payload), not the row's counter. The
      counter (`webhook_events.id`) is internal, read only by the deliveries. Migration 0021 now carries it over
      (`sqlite_sequence`), so a deleted top row's ID is not issued again; `TestMigration0021KeepsEventCounter` pins it.
      The migration is edited in place because 0021 is on this unpushed branch and has not shipped.
    - **Guard tests.** `webhook_guard_test.go` and `kind_guard_test.go` are tripwires: they fail when a path is added or
      moved without a reviewer deciding it, and they do not prove the decision is right. Accepted (owner, 2026-10-30).
    - **Atomicity, both ways.** A forced failure after the audit entry rolls back the change and its event. A SQL
      trigger that refuses the event insert rolls back the change, through the API too. A change that commits writes its
      event, and the guard tests classify every audit action and every node-writing call, so a new path is not missed
      silently. A mapped audit entry whose node or user is missing fails its change: every mapped action names a row its
      change has just written, so a missing one is a bug, and the change must not commit without its event. This interpretation
      stands after review (2026-10-30).
28. **Webhook worker, dialer and routes, slice j3** (2026-10-31, built on the owner-approved proposal, with the dialer and the
    `allow_networks` setting moved from j4 into j3 by the lead, so that no connection is ever made without its guard). The
    interpretations below are for review. The code is `internal/store/webhook_deliveries.go` (the queries), `internal/webhook`
    (the deny table, the one client, signing, the schedule, `Origin`) and `internal/api/webhook_worker.go` (the loop, which
    stays in `api` because `store` imports `webhook`; a loop in `webhook` would be an import cycle).
    - **Order (proposal question 5).** A claim takes only the oldest pending delivery of each active endpoint, the head. The
      head is leased while it is in flight and not due while it waits on its backoff, so no later delivery of that endpoint is
      sent meanwhile. Tested in `TestWorkerKeepsEachEndpointInOrder` and `TestClaimTakesOnlyTheHeadOfAnEndpoint`. Up to 8
      requests are in flight (`TestWorkerSendsToEightEndpointsAtOnce`, which sees exactly 8 of 12).
    - **One request per endpoint (review finding 4).** A claim refuses an endpoint that holds any unexpired lease, so a retry of
      an old failed delivery can never run beside the endpoint's head in flight. A retry resets `attempts` and
      `first_attempt_at`, so it has a fresh 24-hour window and its first wait is 30 seconds, not an hour
      (`TestRetryDoesNotRunBesideAnInFlightHead`, `TestRetryGivesAFreshWindow`).
    - **Outcome tied to its claim (review finding 5).** A claim's lease end, in milliseconds, is the claim's token. An outcome
      applies only while the delivery still holds that lease. An outcome whose lease has lapsed, because a later claim took the
      delivery, changes no counter and clears no lease (`TestOutcomeOfALapsedLeaseChangesNothing`).
    - **Fairness between accounts (review finding 1).** A lane keeps its token for at most 2 seconds (`webhookLaneBudget`,
      measured on the real clock): it sends its endpoint's next deliveries until that budget passes, then gives the token
      back. The claim ranks the due heads by account first (a window over the account, then the due time), so the next claim
      serves each account's oldest due head before any account's second one. A single-account install still uses every slot,
      because the ranking only orders work that is already due. The disabled-queue cleanup runs on its own ticker, not behind
      the token wait. `TestWorkerOneAccountDoesNotStarveAnother`: an account with 50 endpoints owed six deliveries each, and a
      second account with one. Over three runs on the test machine the second account's delivery started 4 ms, 5 ms and 305 ms
      after the worker started, against a bound of 5 seconds. Before the fix the same test failed with 10.96 seconds.
    - **Lease and tokens.** A claim takes up to 50 deliveries in one write, with a 2-minute lease. A lane waits for one of the
      8 request slots inside its lease. At 50 lanes and 8 slots, with a 10-second attempt, the longest wait is about 63 seconds,
      so a live send is not claimed twice. A crashed worker's delivery is claimed again after the lease, with the same event ID
      (`TestWorkerLeaseRecoversACrash`).
    - **Schedule (proposal question 6).** The first retry is 30 seconds after the failed attempt, doubling to a base of 1 hour.
      Jitter adds up to 20 percent of the base, so a wait is in [base, 1.2 base] and the longest is 72 minutes. The 24-hour window
      starts at the delivery's first attempt, which the claim records in the new column `first_attempt_at` (migration 0022),
      not at the event's creation: a delivery can wait behind a failing one for hours. A retry is never scheduled past that
      point, so the last attempt is at exactly 24 hours, and the failure there gives the delivery up as `failed`.
    - **Disable with reason failing.** A failure disables the endpoint when it is at least 24 hours after the later of the
      endpoint's last success and this delivery's first attempt. Deliveries of an endpoint go in order, so nothing else was
      sent in that window, and the give-up and the disable usually fall at the same attempt. Interpretation: if an earlier
      delivery was given up without a success in between, the window starts later than "the last success" would put it, so
      the disable is later, not earlier. A 410 gives the delivery up and disables the endpoint at once. Both write the audit entry
      `webhook.disabled` with the reason and the origin of the URL, in the same write as the change they follow.
    - **Queues of disabled endpoints.** A disable does not fail the endpoint's pending deliveries in its own write, because a
      queue can hold 100,000 rows. Each worker round fails up to 2,000 of them, one write per endpoint, with `last_error` set to
      the reason. Measured: one such write (2,000 rows) took 36 ms on the test machine (`BenchmarkFailDisabledBatch`, a loaded
      machine, one run of 100).
    - **Generations: a re-enabled endpoint never sends its old queue (review findings 2 and 3).** A disable raises the
      endpoint's `generation` and sets `stale_queue`. Every delivery takes its endpoint's generation when it is queued: fan-out,
      test event and retry. A claim takes only deliveries of the endpoint's current generation, and the head is the oldest of
      those. The cleanup (`FailStaleDeliveries`) fails the pending deliveries of an older generation whatever the endpoint's
      state is by then, so an owner who changes the URL or rotates the secret of a disabled endpoint, or activates it in one
      request, sends none of the old queue. Each cleanup write moves `pending_count` by the rows it changed, and clears
      `stale_queue` when it finds no more. A stale delivery keeps the endpoint's reason as its `last_error` while the endpoint
      stays disabled; after a re-enable the reason is gone and the token `disabled` is used. Tested by
      `TestReEnabledEndpointSendsNoOldQueue` (no claim takes the old queue, the cleanup fails exactly the three of it, and
      `pending_count` equals the pending rows after each step), `TestFailStaleDeliveriesTakesOnlyTheOldGeneration`, and
      `TestWorkerGivesUpAfter24HoursAndDisables`. No 409 is needed, so the owner does not wait.
    - **Decision 28 consequences the owner should know.** (a) A poison head blocks its endpoint: one event that the receiver
      answers with 4xx or 5xx holds that endpoint's queue, with its backoff, until the 24-hour give-up. Then the endpoint is
      disabled and its queue fails, and each delivery of it must be retried by hand. The retry route cannot skip a head that is
      still pending. A "skip this delivery" action is not built. (b) With `webhooks.allow_networks` set, `last_error` (`connect`,
      `timeout`, `tls`, `status <code>`) and `last_code` tell an owner whether a host inside those allowed networks is reachable.
      That is a port-scan oracle for the allowed networks only. With the default empty list, a denied address gives
      `address_refused` only for a name the owner already controls, because registration refuses such a name with one message.
      (c) Any port is allowed on a public host; no port is restricted. (d) `GiveUpAfter` is 24 hours from the first attempt, and
      no retry is scheduled past it. But a delivery whose lease lapses after that point (a stall of the worker or a clock step)
      gets one more attempt before it is given up. This is not fixed: it needs a worker stall longer than the 2-minute lease
      and a delivery already 24 hours old, and the attempt is recorded the same way. (e) The URL and the secret are read at the
      claim, so one request already claimed may go to the old URL, with the old secret, after the owner changes either (review
      finding 6). The fix would be a re-read at the send, and it is not made: no wrong state results, and the window is one
      request per lane.
    - **Secret that cannot be read.** Disables the endpoint with `secret_unreadable`, and nothing is sent to it. Other endpoints
      carry on (`TestWorkerUnreadableSecretDisablesOnlyItsEndpoint`).
    - **Overflow (proposal question 10), decision in the store.** `webhook_endpoints.pending_count` is maintained in the writer:
      an event adds one to each queued endpoint, each outcome takes one away, and a disable does not change it. The check in
      `EmitEvent` reads that count, so its cost does not depend on the queue. The alternative, a bounded probe of the pending
      rows, walks up to 100,001 index entries per event. Measured on the test machine, with 100,000 pending deliveries:
      `BenchmarkPendingProbeAtCap` 30.6 ms per probe; `BenchmarkEmitAtCap`, one event with the check, the counter and the
      delivery, 1.24 ms, against 1.17 ms for an event with no endpoint (`BenchmarkEmitNoEndpoints`). The benchmarks ran 100
      iterations on a machine shared with other work, so these are rough, but the difference is two orders of magnitude.
      At the cap the change still commits. Its delivery is recorded `failed` with `last_error` `overflow`, and the endpoint
      is disabled with reason `overflow` and audited, in the same transaction. The transaction's fanout forgets the endpoint, so
      the next event of that transaction reaches no endpoint from it (`TestOverflowAtTheLimit`). The queued deliveries stay
      pending, and the cleanup fails them. The cap is 100,000: a placeholder until real volumes are known (question 10).
    - **Retry and test routes.** The test route queues one `webhook.test` event for one endpoint, whatever it subscribes to.
      It refuses a paused or disabled endpoint (conflict, `reason: state`) and a full queue (`reason: limit`). The retry route
      queues a failed delivery again with a fresh 24-hour window and its attempts reset to 0, in the endpoint's current
      generation, and refuses a delivery that is not failed, a disabled
      endpoint and a full queue. Both are the account key's, as the other endpoint writes are; an owner gets 403. Both are
      audited (`webhook.test`, `webhook.retry`), and both are classified in the guard with a reason.
    - **Delivery log and detail.** The log is newest first by event row, which keeps the order of events made in one millisecond, and its cursor is that row. The detail holds the newest
      five deliveries. A delivery shows its event's ID and type, never its payload, and `last_error` is one of the fixed
      tokens below, so an owner can read it.
    - **Errors, and what the worker stores and logs (proposal section 6).** `last_error` is one of `address_refused`,
      `resolve`, `timeout`, `connect`, `tls`, `redirect`, `status <code>`, `overflow`, or a disable reason. The error text is
      never stored, since it can hold the URL. The log lines name the origin, the endpoint and the event ID, never a query,
      a path, a body, a secret or a signature. `TestWorkerNeverStoresOrLogsTheURL` makes a transport error whose text carries
      the URL and checks both the row and the log.
    - **The client (proposal section 5).** One `webhook.Client` makes every connection. Its transport dials a checked address
      directly, and a name is looked up once per connection. A host is refused when any address it resolves to is denied, so a
      mixed answer cannot slip a private address past. The deny table is the proposal's list plus `192.0.0.0/24`,
      `198.18.0.0/15`, the documentation ranges and 6to4, which is checked as the IPv4 address in bytes 2 to 5. The IPv4-mapped
      form is checked as the IPv4 address it holds. Review finding 7 changed the IPv6 part (`TestDenyTableRestOfIPv6AndSIIT`).
      IPv6 is allowed only inside `2000::/3`, and everything outside it is denied: the IPv4-compatible `::/96`, the translated
      `::ffff:0:0:0/96` (SIIT), NAT64 `64:ff9b::/96` and `64:ff9b:1::/48`, site-local `fec0::/10`, the discard prefix `100::/64`,
      and the rest. Inside `2000::/3`, Teredo `2001::/32`, `2001:2::/48`, `2001:10::/28`, `2001:20::/28`, `2001:db8::/32` and
      `3fff::/20` are denied. The IPv4 part adds `192.88.99.0/24` (6to4 relay anycast). So a public IPv4 address in a NAT64 or
      compatible form is refused, not checked as public. An operator can allow such a form only by listing that form in `webhooks.allow_networks`,
      since the allow list compares parsed values and does not unwrap these forms.
      Addresses are compared as parsed values (`netip`), never as text (`guard_test.go`). The allow list, `webhooks.allow_networks`, lets
      through only the networks listed, by parsed value, and nothing else. No proxy, no redirect, a 10-second limit covering
      the lookup, the connect, the handshake and the answer, 4 KiB of answer read and discarded, 8 connections per host, TLS
      verified for the URL's name while the connection goes to the checked address. Tested against a stub resolver that answers
      a public address and then a private one (`TestMixedAnswerIsRefused`, `TestRebindingRefusedAtDelivery`), with
      `HTTP_PROXY` set (`TestProxyEnvironmentIsIgnored`), with a stalled answer, a streaming answer and a redirect.
    - **Registration.** The host is checked by the addresses it resolves to, within 5 seconds, before any write. A denied
      address, a name that does not resolve, a mixed answer, a lookup that times out, and a numeric form such as `127.1` or
      `2130706433` all get one message: "url cannot be used: its host is not allowed", an `invalid_request`. The message never
      says whether a name resolves or what it resolved to (`TestRegistrationRefusesWithoutSayingWhy`). A change of URL is
      checked; a change that keeps the URL is not looked up. The numeric-host rule was found while reading j1's parser for this
      slice; `TestNumericHostsAreNotNames` failed on j1's parser before the rule was added.
    - **Sweep (proposal section 2).** Delivered rows go after 7 days and failed rows after 30, in batches of 500 per write;
      events that no delivery names go after; and an expired grace secret is cleared. Review finding 8: the event prune walks
      the events from a cursor kept on the sweeper, by row ID, in batches of 500, at most 200 batches per sweep. Each write reads
      and deletes only its own batch, so no write scans the table. Measured on a store of 1,000,000 events and 1,000,000
      deliveries (`TestWebhookScaleMeasure`, run with `SELFCLOUD_SCALE=1`): one prune batch at the far end of the table, all of
      its events named, 0.64 ms median; the full walk of 1,000,000 events deleting all of them took 6.6 seconds in 2,001 batches,
      about 3.3 ms each. The earlier query, which scanned from the first row for each batch, was not measured at this size.
    - **Metrics.** `selfcloud_webhook_queue_depth` (the sum of the maintained counts), `selfcloud_webhook_deliveries{state}` and
      `selfcloud_webhook_endpoints_disabled{reason}`. No label holds a URL or an endpoint ID. The queue is read at most once
      every 30 seconds (`webhookStatsTTL`), so a scrape reports the queue as it was up to 30 seconds earlier. Measured at
      1,000,000 deliveries (`TestWebhookScaleMeasure`): the counts by state took 113 ms median on a warm cache, and 293 ms on the
      first run after seeding, which is over the 100 ms limit, so the cache applies. `TestWebhookMetricsReadTheQueueEvery30Seconds`
      checks the window. (An earlier measurement at 200,000 deliveries was 47 ms, `BenchmarkQueueStats`.)
    - **Measured writer holds.** One claim plus 50 outcomes, as one tick of the worker with 50 deliveries, took 166 ms on the
      loaded test machine (`BenchmarkClaimAndRecordFifty`), about 3 ms per write, which is mostly the commit. The writes are short
      and none is held across a request.
    - **Not built, and a dependency on pg.** The claim is SQLite SQL. PostgreSQL (slice pg) needs `FOR UPDATE SKIP LOCKED` in the
      claim and a rewrite of the head query, which the proposal already says. I don't know yet whether pg will want a second
      writer for the claim; that decision belongs to pg.
    - **Migration 0022.** `pending_count` (backfilled from the pending rows), `first_attempt_at`, `generation` and `stale_queue`
      on the endpoint, `generation` on the delivery, and four indexes: `(endpoint_id, state, generation, event_row_id)` for the
      head, `(state, finished_at)` for the retention, `(event_row_id)` for the sweep's "no delivery refers to" check, and a
      partial index on `(endpoint_id, lease_until)` for the one-request rule. Migration 0022 is unshipped and was edited in place.
    - **Still open after j3.** The quota warnings and the rewrap are built in j4 (decision 30). The slice review of j is done;
      its j4 fixes are in decision 30, and the release statement is there too.

30. **Quota warnings, the rewrap of secrets and the quota-warning route, slice j4** (2026-10-31, built on the owner-approved
    proposal, with every recommendation (a); questions 3 and 9 are the two the brief names). The
    interpretations below are for review. The code is `internal/store/webhook_quota.go` (levels, marks, the watch flag),
    `internal/store/webhook_seal.go` and `internal/api/webhook_rewrap.go` (the rewrap), `internal/api/quota_warning.go` (the
    route), and migration `0024_quota_warning.sql`. Number 30 is taken here; the p4 lane takes 29.
    - **Levels.** A level is measured on used bytes, the bytes the tree holds. A reservation alone crosses no level, so an
      upload in progress sends nothing (`TestReservationsDoNotCountTowardTheLevel`). The refusal check still counts used plus
      reserved bytes, as before. The account's level is taken against its quota (`accounts.quota_bytes`); a user's against the
      limit of its personal folder (`users.quota_bytes`, or the account default `user_quota_bytes` when the user's follows it).
      `quota.warning` is sent at the account's warning percent, and `quota.exceeded` at 100. A level is reached at or above its
      share, so exactly 100 percent sends `quota.exceeded`, and the docs say "reached", not "passed". A limit of 0 is no limit,
      and no level is reached. The percent is a whole number from 1 to 99: the proposal gives 80 as the default and puts
      `quota.exceeded` at 100, so 100 has no setting of its own, and the route refuses 0, 100 and anything that is not a whole
      number. The setting is the column `accounts.quota_warning_percent` (`NOT NULL DEFAULT 80`), so DELETE sets it back to 80.
    - **Once per crossing, by marks.** A mark in `webhook_quota_marks` says a scope is past a level. The check runs once per
      write transaction, before commit (`settleQuotas`), on the state the transaction leaves. A scope at or above a level with
      no mark sends the event and writes the mark, in the same transaction. A scope below a level has that level's mark
      deleted, so a later crossing sends again. A transaction that moves a scope up and back down inside itself sends nothing,
      and a dip within a transaction does not fire a second time. One transaction that passes both levels sends
      `quota.warning`, then `quota.exceeded`. A scope above a level with no mark is announced by the next transaction that
      touches it.
    - **Which changes check.** Every usage change goes through `AdjustFolders` or `settleAccount`: uploads and their reservations
      (`uploads.go`), version writes and prunes, copies, folder moves (`MoveUsage`), trash and purge, and a deleted user's files.
      The account is checked on each of them. A user is checked when a change touched that user's personal folder; a team space
      has no user limit. Trashed items still count (decision 21), so a trash sends nothing and a purge does. A folder deleted
      with its counters (`DeleteFolder`) checks the account. Two paths move counters without a change, and send nothing:
      `RecountAccount` (`selfcloud admin recount-usage`), and an edit of a limit or of the quota, which send nothing (the edits clear
      marks, see below). The sweep removes the marks
      that no longer hold (`PruneQuotaMarks`): a level the scope is back under, a limit that is gone, a user that is deleted,
      and every mark of an account that nothing watches. So a recount can delay a crossing by one sweep, and cannot make one
      repeat. I don't know of a path that moves usage and is not covered.
    - **The hot path.** Whether an account is watched is the column `accounts.quota_watched`, read in the statement that already
      reads the account's counters (`adjustAccount`), so an account nobody watches runs the same statements as in j3. The
      endpoint writes keep the column (`syncQuotaWatch`): it runs in the transaction of each insert, update and delete of an
      endpoint and of each disable, sets the flag from the active endpoints that subscribe to a quota event, and removes the
      account's marks when the flag goes to 0. The first version asked the endpoint tables in each usage change, as a correlated
      subquery; that cost about 30 to 55 µs more per account read on the test machine (a micro-benchmark, plain read 18 to 24 µs),
      so the column replaced it. The brief's rule, no read per file while nothing subscribes, is met: no endpoint query is made
      on the hot path at all.
    - **A subscriber added later.** An account nobody watches has no marks. A subscriber added later starts from none, so the next
      change that leaves a scope above a level announces it. A crossing that happened while nobody watched is not announced, and a
      subscription sends nothing for the past. A paused endpoint does not watch: pausing the last watcher removes the marks, and
      resuming starts from none. An overflow that disables the last watcher leaves no mark, because the mark is written only while
      the account is still watched (`TestAnOverflowThatDisablesTheLastWatcherLeavesNoMark`).
    - **Payload.** `webhook.Quota`: `scope` (`account` or `user`), `id` (the account's or the user's ID), `percent`, `used_bytes`
      and `limit_bytes`. No file or folder name, and no path. The envelope is the one of j2.
    - **The percent.** Changing the level sends nothing. It clears the account's warning marks in the same transaction, and the
      new level applies from the next transaction that touches a scope: a level lowered below a scope's use is announced then,
      and a level raised above it sends again at the next crossing.
    - **The rewrap (`selfcloud admin rewrap-secrets`).** It reads the current links key and the previous one, and never makes a
      key. A missing current key is an error, since a new one would make every secret unreadable. A previous key that the
      configuration names must exist and be readable, as the server requires; a previous key the configuration does not name,
      and that is not there, counts as gone. It works in batches of 100 endpoints, one write each. A secret the current key
      opens is left alone. A secret only the previous key opens is sealed again under the current key, and the write names the
      blob it replaces, so a rotation made meanwhile is not overwritten: that endpoint is counted `skipped` and the next run
      reaches it. A secret that neither key opens is counted `unreadable`. The default run disables nothing and exits 1, so the
      operator decides. With `-disable-unreadable` it disables each active or paused endpoint with reason `secret_unreadable`,
      and its queue is cleared by the generation rule of decision 28. A disabled endpoint keeps its reason. A grace copy that
      cannot be read counts as unreadable while its grace period runs; after that it is counted `expired` and left, since
      nothing signs with it. An endpoint disabled as `secret_unreadable` whose secret opens again goes to paused with its reason
      cleared, and is counted `restored`; it does not send until its owner sets it active. Each change writes a `webhook.rewrap`
      audit entry that names the endpoint and never a secret: `previous` (false or true) for a sealing, `restored` for a restore.
      Its output is counts only: `endpoints=… rewrapped=… unreadable=… disabled=… restored=… expired=… skipped=…`. It exits 1 on
      an error, or when `unreadable` is above 0 without `-disable-unreadable`. Running it twice is safe: the second run seals
      nothing. Interrupting it is safe: each batch is committed. The distroless image's entrypoint is the binary, so
      `docker exec <container> selfcloud admin rewrap-secrets` runs it with no shell.
    - **The metric the proposal asked for.** No new metric was added. `selfcloud_webhook_endpoints_disabled{reason="secret_unreadable"}`
      (j3) is the count the proposal named. It counts the endpoints disabled for an unreadable secret. After a rewrap run with
      `-disable-unreadable` it covers every unreadable active or paused endpoint; a run without the flag disables none, so it does
      not move. An endpoint disabled for another reason that also holds an unreadable secret is not counted there until its
      reason changes, and no metric counts secrets directly.
    - **The route.** `GET|PUT|DELETE /accounts/{acc}/quota-warning`. GET is read by the account key and an owner
      (`webhookReader`). PUT and DELETE are the account key only: an owner and a token are refused, an installation admin is
      refused, another account's key gets 404, and no credential gets 401. The member 404 on every method is the deliberate match
      of `managedAccount` described above. The openapi lists GET as `getQuotaWarning`.
    - **Migration 0024.** `0024_quota_warning.sql`: `quota_warning_percent` and `quota_watched`, and a backfill of the flag. It
      follows p4's `0023_labels.sql`, so the numbering has no gap and no placeholder.
    - **A fresh links key refuses the start, on every start.** When the default signing key file is absent and the database holds
      any webhook endpoint, the start is refused before any key is made, so a refused start leaves no key file and changes no
      endpoint. The refusal names both ways forward: put the earlier key file back into the keys directory, or, if the key is
      lost for good, run `selfcloud admin new-signing-key -i-lost-the-old-key`. Every start refuses until the operator acts. The
      check is `refuseFreshLinkKey`, called from `api.New` before `loadLinkKeys`. Tested through `api.New`
      (`TestFreshKeyRefusalHoldsThroughNew`): two refused starts in a row, no key file afterwards, the endpoint unchanged; a start
      with the earlier key restored proceeds and leaves that key untouched; a new install with no endpoint starts and makes its key.
      The earlier version made the key first and refused after it, so the second start went on with the new key. That is fixed.
    - **The escape for a lost key.** `selfcloud admin new-signing-key -i-lost-the-old-key` makes the default signing key (or the
      configured one) when none is there. It refuses without the flag and refuses when a key exists; it never prints the key, and
      it names the path. It is run with the server stopped. A `signing.prev.key` that is there is left alone and still verifies
      what it signed. What it invalidates, each checked in a test:
      - webhook secrets: a secret sealed under the lost key does not open under the new one (`TestNewSigningKeyInvalidatesWhatItSays`).
        The endpoint is not disabled at the start. It goes `secret_unreadable` at its next delivery attempt
        (`webhook_worker.go`), and its owner rotates the secret;
      - delegated tokens: a token minted before the change is refused with 401 by a server started after it
        (`TestNewSigningKeyInvalidatesWhatItSays`);
      - signed download links and visitor credentials: their keys are derived from the signing key, and the test checks that the
        MAC and the derived credential key change. It does not run a full link or credential round trip, so that is checked by
        derivation, not by an end-to-end test.
    - **The member 404 on GET.** `GET /accounts/{acc}/quota-warning` gives a member 404, not 403. That is a deliberate match of
      `managedAccount`, which answers 404 to a principal that does not manage the account, as every webhook route does.
      `TestQuotaWarningReadByKeyAndOwner` asserts it.
    - **Delivery log (decision 18).** The owner's delivery-log metadata is unchanged in this slice. This is a reading of
      decision 18, not a new rule.
    - **Audit classification.** `account.quota_warning` and `webhook.rewrap` are in `webhookAuditNoEvent` (`webhook_guard_test.go`),
      each with its reason.
    - **Measured.** Store benchmarks, `internal/store/webhook_quota_bench_test.go`: one usage change in its own transaction
      (`BenchmarkUsageWrite…`) and b.N changes in one transaction (`BenchmarkUsageCalls…`), three runs each at 3,000 iterations, on
      an 8-thread machine at load 7 to 9, so the numbers are rough. The j3 base and the j4 tree were run one after the other.
      No endpoint: j3 about 222 to 234 µs per write and 123 to 140 µs per call; j4 about 163 to 180 µs and 118 to 130 µs. One file
      endpoint: j4 about 173 to 204 µs per write. A quota endpoint, with the change crossing no level: j4 about 201 to 235 µs per
      write and 138 to 160 µs per call, against 196 to 232 µs and 117 to 145 µs for j3, which does not check quota. The difference
      of an unwatched account is inside the noise on this machine: no statement is added on that path, and a run earlier in the
      session measured the two within a few tens of microseconds in either direction. The j4b changes (the end-of-transaction
      check, the edits, the rewrap) were not measured again. The unwatched path runs the same statements; the end-of-transaction
      step does nothing when no watched account was touched.
    - **Sweep.** The sweep's new step is `PruneQuotaMarks`, one write, after the expired grace secrets are cleared.
    - **Release blocker.** Nothing in the j4 review blocks the webhook release. Each j4 finding is fixed in code and tested (findings
      1, 2, 3 and 8 at the store and api level; finding 4 through `api.New`; finding 5 is a decision, not a change, and finding 6 is
      not reviewed here). Not verified in this round, and so not claimed: the j3 review's fixes beyond the code citations (its
      findings 1 to 8 are cited as "review finding" in the code, and I did not re-test each), the cost at millions of rows
      (not measured), and whether any target deployment has NAT64 or SIIT routes (j3 finding 7). Those three are what remain
      open before release.
    - **Mutation proof.** Each guard of this slice was removed in a scratch copy, one at a time, and its named test was run.
      Every guard is caught by a test that fails on its assertion, except the per-transaction check: the per-call form of it no
      longer exists, so no faithful mutant is built for it. The one-fire-per-transaction property rests on `settleQuotas` being the
      only check site.

29. **Labels, slice p4** (2026-10-31, built to the design the owner approved; the slice p proposal's section 5 and
    section 7 define it). The interpretations are in section 8 (Labels (p4)) and are listed for review there. The code is
    `internal/store/labels.go`, `internal/api/labels.go` and migration 0023 (`0023_labels.sql`).
    - Label changes are not webhook events in v1 (design-j-proposal section 10). Their five audit actions are in
      `webhookAuditNoEvent`, with the reason "decision 29".
    - A label change writes no node row. The node-write guard therefore counts no new site, and the kind guard no new
      comparison: a label check does not test a node's kind, since the rule does not depend on it.
    - A visitor through a share link sees no labels. The refusal is the route table's own (`link_route`).
    - A copy and a move never raise who can read a restricted value unless a manager of the item makes them: a copy
      carries restricted values only from a manager of the source, and a non-manager's move of a subtree with a
      restricted value out of its folder, or into a place it would manage, is refused (`restricted_label`). The
      copy's measured cost is in section 8. The ownership hand-overs are not under this rule yet, and the owner decides
      them (section 8).
31. **Office editing through WOPI, slices l1 and l2** (2026-10-31, built on the owner-approved proposal with every
    recommendation (a); the licence question stays open, so there is no Compose example and no statement about Collabora's
    licence). Locks (l3) and rename, the read-only view and the editor button (l4) are not in this part. The interpretations
    below are for review. The code is `internal/wopi` (discovery and proof keys), `internal/api/office.go` (the editor route and
    the WOPI host), the WOPI token in `internal/api/tokens.go`, `internal/store/wopi.go`, and migration `0025_wopi.sql`.
    - **Off by default.** With `office.enabled` false, the editor route and every `/wopi` route answer `not_found`, and nothing
      is fetched. `Validate` checks the office block even while it is off.
    - **What is unsafe until l3.** Locks are not enforced, so: (1) PutFile does not check a lock or an `If-Match`, so a save
      overwrites an API write made after the editor read the file, and two editors overwrite each other; (2) Selfcloud's own
      writes (PUT, rename, move, trash, restore, version restore) are not refused while an editor has the file; (3)
      `CheckFileInfo` says `SupportsLocks` false and `POST /wopi/files/{nod}` answers `501`, so Collabora should not take a
      lock. I have not verified how Collabora behaves with that answer. (4) A token's folder list is fixed when it is issued:
      a file moved to another folder is refused until the editor is opened again; (5) every autosave keeps a version, so the
      version history fills up (see Versions). Turning `office.enabled` on before l3 is unsafe for those reasons; that is why
      it defaults to off.
    - **Versions.** Each PutFile keeps one version, as a PUT does. Proposal question 1(a) asks for one version per editing
      session, so that an autosave does not add a version each time. That rule depends on the lock, so it is slice l3's, and it
      is not built here. Until l3, every autosave keeps a version. That is unsafe for the version history, so `/wopi` stays off
      by default, and it is in the unsafe list above.
    - **One replace path.** The transaction body of `handlePutContent` moved into `replaceContent` in `nodes.go`, and PutFile
      calls the same function. It charges the new size in full, keeps the old content as a version, sets the new ETag, notes
      the edit for Recent, and audits `node.write`, which reports `file.updated` through the existing hook. The `UpdateNode`
      count in `nodes.go` is unchanged. The helper is in the store's node-write guard (`replaceContent`), with a pin for each
      caller, so a new caller in any file is counted. The second replace path, in `uploads.go`, is not touched.
    - **Sizes and quota.** `office.max_file_bytes` (50 MiB) limits opening and PutFile. The body is streamed to a blob by
      `writeBlob`, and a refusal removes the blob. A quota refusal answers `413`: the WOPI page lists no quota status, so `413` is
      my choice, not a protocol fact. GetFile streams from the blob in pieces and is not limited by the editing size; the
      length is checked against the record first. Measured in `TestWOPIPutAndGetStreamALargeFile` on the test machine: a 64 MiB
      PutFile grew the allocated total by 117 KiB, and a 64 MiB GetFile by 74 KiB, both against a 16 MiB budget. The body was
      generated as it was read, so the test itself held none of it. A PutFile whose Content-Length is known is refused over
      the account quota before any byte is read (F9; `TestWOPIPutRefusesOverQuotaBeforeTheBodyIsRead`). That check is the
      account quota only: the folder and personal limits are checked inside the write, after the body. The write also checks
      the token again, so a revocation made while the body is arriving refuses the save
      (`TestWOPIRevocationDuringTheUploadRefusesTheSave`).
    - **GetFile headers (F7).** GetFile sends `X-Content-Type-Options: nosniff`. When the caller sends
      `X-WOPI-MaxExpectedSize`, a file larger than that value is refused with 412 before any byte is sent; the GetFile page
      lists 412 among its codes and describes this case. A value that is not a whole number of bytes answers 400, which is my
      choice, as the page gives no code for it. Test: `TestWOPIGetFileHeadersAndMaxExpectedSize`.
    - **Names (F6).** CheckFileInfo's `UserFriendlyName` is the person's display name. Without one, it is the word `User`, in
      English, because Selfcloud has no translation of that word yet. It is never an ID or an email. `UserId` and `OwnerId` stay
      IDs: Collabora keys its editors by `UserId`, and the page does not describe it as shown to people. Test:
      `TestWOPICheckFileInfoNamesThePersonNotTheirID`.
    - **Token alphabet (the choice).** The token is `sct_` followed by unpadded base64url, a `.`, and the base64url MAC, so it is
      URL-safe and the signing is unchanged. `TestWOPITokenAlphabetIsURLSafe` pins that: tokens over payloads holding `+ / = ?
      & %` and non-ASCII text survive a query escape unchanged and hold only `A-Za-z0-9_-.`. It passed on its first run, because
      the code was already right. It is a pin, not a fix.
    - **Statuses.** A forged or unverifiable request answers `500`, as the proof page says. A read-only token, or a no-download
      file, answers `404` for PutFile, because the page's list gives `404` for an unauthorized user. An operation other than
      PutFile's `X-WOPI-Override: PUT` answers `501`, and so does every lock or rename request on `/wopi/files/{nod}`, which
      this slice does not have. Discovery not available: `500` on `/wopi` and `503` with `Retry-After` (60 s) when a person
      opens an editor. Every refusal the client causes (the proof failures, the discovery gap, the 501s, the 404s) is logged
      as a warning, not an error: the access log's error level is for internal failures only. Test:
      `TestWOPINoLocksAreImplementedYet` fails if a 501 is logged as an error (it did, before the fix).
    - **Read-only rule and no-download (the brief asked for it to be stated).** The role a token carries is the role the person
      holds at issue, capped to viewer when the token is read only, and every WOPI call recomputes it from the database. The
      edit mode needs the editor role. The no-download rule is the one the API already applies to downloads (`restrict.go`,
      `downloadRestricted`): it binds a viewer or a commenter, and not an editor or above, and it follows inherited flags as
      `NoDownloadApplies` does. So a restricted caller opens to view only, and gets `UserCanWrite` false, `ReadOnly` true and
      `DisablePrint`, `DisableExport`, `DisableCopy` true; an editor is unaffected, opens to edit, and may save. The proposal
      said "a member with no_download gets view only" and I read it as the API reads it, which is the reading the code already
      carries. The owner should confirm it. Decision 20 leaves inline viewing alone, so GetFile still sends the bytes to a
      restricted viewer's editor. That the editor honours the three Disable properties I have not verified (item 5 below).
      Test: `TestOfficeNoDownloadBindsViewersOnly`. A view on a type whose discovery lists no view action (only `edit`) opens
      through the edit action. The token and CheckFileInfo stay read-only, so the editor shows the file without a save, and a
      PutFile with that token answers 404 (`TestOfficeViewFallsBackToTheEditActionWhenThereIsNoView`).
    - **The token.** Format as a delegated token (`sct_`, payload and HMAC), with its own key from HKDF under the label
      `selfcloud wopi token v1`, and audience `wopi`. The payload names one file (`nod`), one folder (`fld`), one editing
      session (`ses`, a `wps_` row), and a user; `key` is set only when a delegated token opened the editor. `wopiValid` and
      `valid` each accept only their own audience, so a token is refused by the other kind of route twice over, once by the MAC
      and once by the audience. Tests re-sign each kind with the other's key to show both checks. The expiry is the earlier of
      `office.token_minutes` (600 by default) and the delegated token's expiry. Only a person opens an editor: an account key
      and an admin are refused.
    - **Refusal on every call.** Each WOPI call runs `authenticateWOPI`: the MAC under the WOPI key, the audience, the session row
      (not revoked, not past its end, same file and user), `Delegation` and `callerLive` (the account's, the user's and the key's
      cut-offs), then `nodeAccessWith`, which re-reads the permissions, the trash and the folder on every call. Between the
      session row and `Delegation` comes the browser session that opened the editor, when there is one (below). A revoked
      permission, a trashed file and a file the path does not name all answer `404`. Revoking a user's tokens or the account's
      marks the user's rows, or all the account's rows, revoked, in the same write.
    - **Ended sessions.** The sweep deletes a `wopi_sessions` row once its end has passed (`DeleteEndedWOPISessions`). Refusal
      past the end comes from the token's own expiry, so no refusal is lost.
    - **Single sign-out, and the browser session that opened the editor (F2).** An editor opened from a browser session records
      that session's hash in `wopi_sessions.browser_session`; an editor opened through a delegated token records none. Every
      WOPI call, and the write of a PutFile, checks that the browser session still exists, has not passed its end, and belongs
      to the same user. The reading: signing out that browser ends its editor (`POST /api/v1/auth/logout`); signing out another
      browser of the same person does not (`TestWOPIEditorIsNotEndedByAnotherBrowsersSignOut`); a password change, a reset
      (`/api/v1/auth/password/reset` and the admin's reset link) ends the user's sessions, so it ends the editor; a disabled
      user is refused by the user cut-off in `callerLive`. The API has no sign-out-everywhere route, so none is tested. Test:
      `TestWOPIEditorEndsWithTheBrowserSessionThatOpenedIt` has one subtest per way, and each refuses CheckFileInfo, GetFile and
      PutFile with 401 and leaves the file unchanged. When I disabled the browser check for one run, the sign-out, password
      change and reset subtests failed; the disabled-user subtest still passed, because the user cut-off refuses it first.
    - **Discovery fetch (SSRF).** The URL is operator configuration, and it is the only address this server fetches for
      office editing. The config requires https, the host, and the path `/hosting/discovery`, with no query, fragment or user
      information. Collabora is usually on a private network, so the fetch does not use the webhook deny table. The client has a
      10-second timeout, reads at most 1 MiB and then refuses a larger body, and refuses every redirect, whatever client is
      injected. The document is cached in memory: refreshed on use after one hour, not by a timer, so there is no goroutine; a
      failed refresh keeps the old copy; with no copy, a failed fetch is not repeated for a minute; a failed proof asks for one
      refetch, again at most once a minute. A stale copy keeps answering while the last fetch was within a minute, so an outage
      costs one fetch a minute, not one a read (F4; `TestStaleDiscoveryDuringAnOutageFetchesOncePerRetryGap`). The copy has no
      hard expiry. While Collabora stays unreachable, the last copy answers, so a key Collabora rotated in that time fails
      proofs, closed, until a fetch succeeds. Every proof is still limited to the 20-minute window. The fetch runs with the lock released, so readers of the cache are not held up by
      a slow fetch; while one fetch runs, a second caller gets the cached copy and starts none of its own. An editor action whose URL is on another host is dropped, so a file never opens
      through a foreign address. Discovery is parsed for the first `external` zone, or the first zone when there is none.
    - **Proof keys.** The expected proof is built as the page gives it: a 4-byte length and the bytes of the token, of the URL in
      uppercase, and of the timestamp. The URL is `office.wopi_url` (or the origin of `public_url`) plus the request path and
      the raw query, so it matches what Collabora signs. The three combinations are all checked, and the old key is used only
      for `X-WOPI-Proof`. The window is 20 minutes old (the page's rule) and 5 minutes ahead (my limit; the page does not
      give one).
    - **What the proof does not cover (F5).** The proof covers the access token, the URL with its query, and the timestamp. It
      does not cover the method or the body. A captured request can therefore be replayed with another method or body, to the
      same URL, within 20 minutes. This is a property of the protocol as the page describes it, and the window is its only
      bound. Selfcloud adds no nonce, and the page gives none. No code change.
    - **Deployment shapes (F8; check at l4).** Three limits are enforced now and are to be checked against a live Collabora at
      l4: (a) an action URL must be on the host of the discovery URL, so a Collabora served from another host cannot open a
      file; (b) discovery must be https; (c) a request without proof headers is refused, so a Collabora that does not sign
      its requests cannot be used. No code change.
    - **The access log.** It logs the path, not the query, so the token is never in it. The `/wopi` routes are `secretPath`
      too, so their log line names the route pattern. Test: the log of a successful call holds the route pattern and not the token.
    - **CSP.** The discovery URL's origin, scheme and host only, is added to `frame-src` and `form-action`, and to nothing else.
      Test: it appears exactly twice when on, and not at all when off.
    - **Audit label (untested).** Audit entries made through a WOPI token say `via: office_editor`, not `delegated_token`. No
      test checks this yet.
    - **Config drift from the proposal.** The proposal's test list (section 7, Config) requires "https, a host, no path" for both
      URLs, but its section 2 gives `discovery_url` the path `/hosting/discovery`. I follow section 2 for the discovery URL, and
      require no path on `wopi_url`.
    - **Checked against Microsoft's proof-key vectors.** The eight cases of `ProofKeyTests.cs` in Microsoft's
      Office-Online-Test-Tools repository, as its Python sample carries them, are copied as data into
      `internal/wopi/proof_vectors_test.go`, with values unchanged and no code from that repository. The repository's licence
      is the MIT licence, `Copyright (c) 2015 Microsoft`, and the file carries the notice. `TestMicrosoftProofVectors` runs the
      verifier on all eight: six verify (through `X-WOPI-Proof` with the current key, `X-WOPI-ProofOld` with the current key, or
      `X-WOPI-Proof` with the old key), and two are refused. Because they verify, the vectors confirm the expected-proof layout
      (4-byte big-endian lengths, the uppercased URL, an 8-byte timestamp), the RSA PKCS#1 v1.5 SHA-256 signature, and the .NET
      ticks reading of the timestamp, whose first case is about 2015. The vectors are Microsoft's test data, not Collabora's:
      they show what the verifier accepts from Microsoft's sample, not how a live Collabora signs.
    - **Not verified against a live Collabora, and marked as such.** How a live Collabora signs is not known to me. The
      Microsoft vectors above fix the unit, epoch and byte order that the verifier uses, and they agree with it, but they are
      not Collabora's output. (1) The unit and epoch of `X-WOPI-TimeStamp`: the page says only "convert to a long"; Selfcloud
      reads .NET ticks, as the Microsoft vectors do. (2) The byte order of the length prefixes and of the timestamp: the page
      calls an `EncodeNumber` helper it does not define; big-endian is what the Microsoft vectors use. (3) That the editor takes `access_token` in a form post, as the proposal
      says; I did not check Collabora's own pages. (4) The editor URL template: the `<...>` placeholders are removed and
      `WOPISrc` appended; I did not check that against a real discovery document. (5) The property names of `CheckFileInfo`
      and what Collabora honours. (6) That a `501` for locks makes Collabora refuse to edit. The tests use a fake Collabora
      (`internal/wopi/wopitest`), which signs with the same `ExpectedProof` the verifier uses, so they show that the two agree.
      They do not show that either agrees with Collabora, and no client has been run.
    - **Not in this part.** Lock, GetLock, RefreshLock and Unlock; the store's lock check on every write; the sweep's unlock step
      and its `node.unlock` entry; the reset of the one-version rule at unlock; RenameFile; the read-only view in the React
      interface; the editor button; the README example (held until the licence question is answered); and the security review
      of slice l.
    - **Closed by slice l3 (decision 32).** Items (1), (2), (3) and (5) of the list above are closed: PutFile honours the lock, every
      Selfcloud write that changes the file is refused while a lock holds it, `SupportsLocks` is true and the lock operations are
      answered, and one version is kept per editing session. Item (4) is closed for a locked file, since the lock rule refuses a
      move of it; for an unlocked file a move still refuses the token until the editor is opened again, which fails closed. Whether
      `/wopi` may be turned on is decided in decision 32.
32. **Office locks and the lock rule, slice l3, with its review round (l3b)** (2026-10-31, built on the owner-approved proposal; the proposal's question 1 (a) and its locks sections are followed, and where the proposal is silent the interpretation is marked). The licence question stays open, and no Compose example is written.
    - **Sources.** The WOPI lock, PutFile and UnlockAndRelock rules are taken from Microsoft's Learn pages for Lock, PutFile, RefreshLock, Unlock, GetLock and the concepts page on locks. The pages were read through a public mirror of Microsoft Learn's source (`DanielBradley1/MicrosoftLearnPublicRepos`, whose files carry the Learn source URL and a sitemap date of 2023-03-04); the official repository could not be read through the tools available here. The copies are kept as data outside the repository and run nothing. Protocol facts about how a live Collabora behaves are not checked; they are listed at the end.
    - **The lock.** One row in `wopi_locks` (migration 0025 made it; 0026 adds `version_id`, and it has not shipped, so it is edited in place). It is live until `expires_at`. A row past that is absent to every reader and writer, and the sweep removes it and audits it as `node.unlock` with `reason` `expired`. A lock lasts `office.lock_minutes`, which is 30 by default and at most 30: the WOPI concepts page says a lock expires after 30 minutes unless refreshed, so the config refuses a longer value. A file holds at most one live lock. The lock ID is opaque: 1 to 1024 printable ASCII characters. A lock is held by its ID, not by a person, as the concepts page says: any editor whose token names the file may refresh or end it with the ID. The holder recorded in the row is the user, or the account key that acted, and it is for the audit only.
    - **The cap.** An account holds at most `store.MaxLiveLocks`, 1,000 live locks at once (`PutLock`, in the write transaction). A lock past the cap is refused with `409`, `X-WOPI-Lock` empty, and `X-WOPI-LockFailureReason` "the account has too many open editors". Expired rows do not count. The cap is a constant rather than configuration, because it bounds the cost of the writer's check (below). Expiry is unchanged.
    - **Who may break a lock (decided by the owner, 2026-10-31).** A manager of the file, an owner of the account and the account key may end a live lock with `POST /api/v1/nodes/{nod}/unlock`. It answers `204`; a file that no editor holds answers `409` with the code `conflict`; a folder answers `404`. The break ends the row (`BreakLock`) and is audited as `node.unlock` with `reason` `broken`, the lock's holder and the caller. A delegated token and a share-link visitor are refused with `403` before the role is read, and the WOPI routes do not take this route, so an editor's own token cannot end its lock. The holder's next refresh or save then answers with no lock on the file (`409`, `X-WOPI-Lock` empty), and nothing it sends overwrites a write made after the break (`TestBreakLockHolderIsRefusedAfterwards`). **Recorded for slice l4:** the confirmation before a break is part of the web interface, which l4 builds; this route has no confirmation and no page is built in l3. The owner asked for the break with no token, visitor or holder-only path, and the tests cover each caller kind (`TestBreakLockAuthorisationByCallerKind`, `TestBreakLockRefusesAShareLinkVisitor`, `TestBreakLockRefusesAWOPIToken`).
    - **The explicit refusals are defence in depth.** A delegated token is capped at the editor role and a share link at viewer or editor (`tokenRoleCap` in access.go, and the link role rule), so the role alone already refuses a token or a visitor. Mutants that remove the token or the link refusal (M10, M10b, below) leave every test passing, because the role check refuses them; the refusals stay in the code and in the spec, and the tests assert the outcome.
    - **The lock rule.** While a live lock holds a file, a store write that changes what the file is, where it is, or who owns it is refused inside the write transaction, by three guards in `internal/store/wopi_locks.go`: `guardNode` (the node; for a folder, a file below it), `guardFolder` (a folder's contents, for a move of contents or a folder's delete) and the subtree check that `MoveSubtree`, trash and purge use. The refused changes are: for a file, its name, its place, its content (PUT, PutFile, an upload that replaces it, a version restore), its owner, its trash and purge, a version's label and a version's delete; for a folder, its place (a move of the folder or of its contents), its owner, its trash and purge, and its delete. Trash, restore and purge of a folder refuse while a file below it is open.
    - **Metadata passes (l3b, finding 1; owner's decision).** A write of a file's or folder's description, colour, `no_download` or `editors_can_share`, and a rename of a folder above a lock, change no content, no place and no path an editor holds, since the editor holds the file by its ID. They pass on the locked file and on every folder above it. A rename of the locked file itself is refused, and so is a move of the file or of a folder above it. The owner's list named three flags; `editors_can_share` is the same kind of field, so it passes by the same rule. Implemented in `UpdateNodeHeld`: `changesLockedPlace` reads the node's current name, place, content and owner, and the guard runs only when one of them changes (for a folder, only its place or owner). Tests: `TestMetadataOfALockedFileAndItsFolderPasses` (store), `TestMetadataPassesWhileAnEditorHoldsTheFile` (API).
    - **Account and user deletion (l3b, finding 2; owner's decision).** Nothing waits for a lock to end. The account job clears the account's `wopi_locks` and `wopi_sessions` rows in the same write that deletes the account (`DeleteAccount`), so the first run succeeds; nobody can hold a lock on a deleting account, since its tokens are refused. A user's deletion runs `ReleaseUserLocks` before the personal folder is moved or deleted: it clears the user's own locks and every lock on a file in its personal folder, and the user's WOPI sessions go by cascade. The deletion is neither refused nor retried. Locks held by other users on files elsewhere are left alone. Tests: `TestDeleteAccountClearsItsLocksAndSessions`, `TestAccountDeletionWithAnOpenFileFinishesAtOnce`, `TestUserDeletionReleasesTheLocksItLeaves`, `TestUserDeletionReleasesItsOpenFile`. The earlier claim that the job fails and is not requeued is withdrawn: the job no longer meets a lock.
    - **One version per editing session (question 1 (a)).** An editing session is one lock: from its `LOCK` to its `UNLOCK` or its expiry. A new `LOCK` begins a new session. The session's first save keeps a version of the content before it (`wopi_locks.version_id`). Each later save of the same lock replaces the content without a version, and it changes the quota by the size difference, since the content it replaces is not kept. The session's version stays until pruning removes it; then the next save makes one. A manager can restore across a session: the restore takes a version from before the session, after the lock has ended, and it keeps the current content as a version, as any restore does. Tests: `TestWOPIOneVersionPerLockSession`, `TestWOPISaveNeedsTheLockOnTheFile`.
    - **UnlockAndRelock (l3b, finding 3; the spec).** `LOCK` with `X-WOPI-OldLock`: when the file holds a live lock with that ID, the row takes the new ID and holder and a new expiry, keeps its `version_id` (the session goes on), and is audited as `node.unlock` with `reason` `relock` and `node.lock` with `relock` true. Otherwise the answer is `409` with the current lock, or `X-WOPI-Lock` empty when no editor holds the file. A malformed `X-WOPI-OldLock` is `400`. The earlier 501 test named `UNLOCK_AND_RELOCK`, which is not an override in the spec; it is replaced by `TestWOPIUnlockAndRelock`, and the 501 test now covers `RENAME_FILE` and `PUT_RELATIVE`. Whether Collabora sends this call is not known here (listed below).
    - **The save.** `PutFile` with the lock ID the editor holds is saved. With another ID, or with no lock to a file that is not empty, it is refused with `409` before the body is read, and again inside the write. An empty unlocked file takes a save without a lock, which is the specification's document creation, but only when the editor's session holds the file's current content (l3b, finding 8): an API write that emptied the file after the editor opened it is refused with `409` and `X-WOPI-Lock` empty (`TestWOPIEmptyFileSaveChecksTheSession`). The save goes through `replaceContent`, which PUT also uses, and it never overwrites a write that the store refused.
    - **A stale editor cannot take the lock.** Each editing session records the content tag it was given (`wopi_sessions.served_tag`, set at the open), and each save it makes moves that tag on. `LOCK` on an unlocked file is refused with `409` and `""` when the tag is not the file's current one (`TestWOPIStaleEditorCannotLock`). The residual case fails closed: an API write between the open and the editor's first read is also refused at `LOCK`, so the editor must be opened again. I do not know whether Collabora reads the file before or after it calls `LOCK`.
    - **Expiry during a save.** The lock is checked inside the write, on the store's clock. A lock that expires while the body is arriving refuses the save with `409` and `""`. Test: `TestWOPILockExpiringDuringASave`.
    - **A crashed editor.** Its lock ends 30 minutes after its last refresh, and the file is free after that. The sweep removes the row and audits it. Tests: `TestWOPILockExpiresWithoutARefresh`, `TestSweepRemovesOnlyExpiredLocks`.
    - **Disabled users and sign-out.** A disabled user's editor is refused at its next call (`401`), as decision 31 says. Its lock is not released early: it lapses within at most 30 minutes, and no other writer gets in before then (`TestWOPIDisabledUsersLockLapses`). A sign-out or password change ends the editor's session the same way. A closed browser does not release the lock. This is a choice: releasing on sign-out would need the browser to say so, and it cannot always.
    - **Other accounts.** A lock is checked against the account's own locks, and a token of one account cannot lock another's file (`404`). Tests: `TestWOPILockOnAnotherAccountIsNotFound`, `TestLockOnAnotherAccountDoesNotRefuse`.
    - **Races.** The writes are serialized by the single writer, and each lock operation is one write transaction. Tests: `TestWOPIConcurrentLocksOneWins` (two editors ask at once; exactly one is granted, 20 rounds), `TestWOPIRefreshAgainstUnlockRace` (a refresh and an unlock of one lock; 20 rounds; never a 500), `TestWOPITwoEditorsNeverOverwrite`, and `TestWOPILockExpiresWithoutARefresh` on a fake clock.
    - **The guard's cost, measured (l3b, finding 5).** The file check is one read by key (`guardNode` reads the file's own lock row); the account's list is read only for a folder. Benchmarks `BenchmarkUpdateNodeGuard` and `BenchmarkSubtreeGuard` (`internal/store/wopi_locks_bench_test.go`), run with `-benchtime 300x -count 3` on a shared i7-6700HQ, noisy; medians of the three runs are given, with the range where it matters:
        - A file write (`UpdateNode` in its own transaction), with 0, 8, 64 and 1,000 live locks in the account: medians 0.29, 0.33, 0.30 and 0.31 ms; one run at 8 locks reached 0.73 ms and one at 1,000 locks reached 1.2 ms. The write no longer grows with the number of locks.
        - A folder check (`guardNode` on a folder with no lock in its subtree, the chain 10 deep), with 0, 8, 64 and 1,000 live locks: medians 0.12, 0.69, 4.4 ms (range 4.0 to 6.1) and 81.7 ms (range 77.5 to 81.7). The l3 round measured 2.3 ms at 64 locks; this run is slower on the same host, and the difference is noise I cannot separate.
        - Consequence: a folder operation at the cap holds the single writer for about 80 ms. The cap bounds it; a faster folder check (one walk of the folder's subtree, not one walk per lock) is a follow-up and is not built here.
    - **The store's rule is pinned by tests, and the pins are checked by a scan (l3b, finding 6).** `webhook_guard_test.go` scans every non-test Go file under `internal/` for calls of the node-writing methods and for raw statements. The scan is case-insensitive, collapses whitespace and line breaks, and counts the insert-or-replace forms (`INSERT OR REPLACE INTO`, `REPLACE INTO`), on the four tables a write can change (`nodes`, `versions`, `folders`, `accounts`). A raw statement is pinned per function, since one file can hold writes that follow different rules (`versions.go` has a guarded delete and an unguarded prune). Each pin states its rule: `guarded` (a store guard runs inside the transaction, or the write is refused by the lock as above), `holder` (the lock's own save), `adds` (creates a node, a folder, an account or a history row, which no lock holds), `free` (counters, settings, retention pruning: no file's content, name or place), or `clears` (the account's delete, which clears its locks in the same write). `TestNodeWriteScanCountsEveryForm` plants one statement of each form and checks each count. Mutants: making the match case-sensitive (M8) and dropping `versions` from the tables (M9) each fail that test. Not built: an AST check that every function holding a write calls a guard. The per-function pin and its count do that job by hand: a new raw write changes a count and fails until a reviewer states its rule. The scan does not see SQL assembled by concatenation or from a variable, nor writes to `wopi_locks` and `wopi_sessions`, which are lock state and not node state.
    - **GET_LOCK to a view-only token (l3b, finding 7): no change.** It answers the lock's ID to a viewer. The ID is not a credential: each WOPI call also needs a token for the file and Collabora's signed proof, and a viewer's token cannot lock, refresh, unlock or save (`404`). An editor learns the same ID from its own `409`. Nothing a viewer can do with the ID is possible.
    - **`file.updated` per autosave (l3b, finding 9): no change.** Every save audits `node.write` through `replaceContent`, which reports `file.updated` (decision 27). A webhook receiver therefore sees one `file.updated` per save, and none when the lock opens or closes (`node.lock` and `node.unlock` are not on the event list). The version count is one per session; the event count is one per save. Receivers that want the session must correlate the saves themselves.
    - **Mutation checks (l3b, each restored and compared byte for byte).** M1, the metadata guard always on: store `TestMetadataOfALockedFileAndItsFolderPasses` fails (`wopi_locks_test.go:248`), API `TestMetadataPassesWhileAnEditorHoldsTheFile` fails with `409 locked` (`office_lock_rules_test.go:90`). M2, a folder's rename refused: the same store test fails (`:259`). M3, account deletion refused while a lock is live: the store and the API deletion tests fail (`:296`; the job ends `failed`). M4, user deletion does not release locks: `TestUserDeletionReleasesItsOpenFile` fails with `409 locked`. M4b, the holder clause of `ReleaseUserLocks` off: `TestUserDeletionReleasesTheLocksItLeaves` fails. M5, relock ignores the old ID: `TestWOPIUnlockAndRelock` fails. M6, the cap one too high: `TestLiveLocksAreCappedPerAccount` fails (`:414`). M7, the empty-save session check off: `TestWOPIEmptyFileSaveChecksTheSession` fails (`:68`). M8 and M9, the scan: `TestNodeWriteScanCountsEveryForm` fails. M11, the break's role lowered to viewer: `TestBreakLockAuthorisationByCallerKind` fails (the editor gets `204`). M12, the break leaves the row: `TestBreakLockEndsTheLockAndNothingElse` fails. M10 and M10b, the token and link refusals removed: no test fails, for the reason given above.
    - **Not checked against a live Collabora (I don't know).** (a) Whether Collabora calls `LOCK` before or after `GetFile`, and whether a stale `LOCK` refusal leaves the editor read-only or shows an error. (b) How Collabora reacts to a `409` on `LOCK`, to a `GET_LOCK` answer, and to a `SupportsGetLock` of true; the page says Microsoft's own client does not always call `GET_LOCK`. (c) Whether it refreshes its lock with `REFRESH_LOCK` at the 30-minute pace, and what it does when the refresh is refused. (d) Whether `PutFile` on an empty file without a lock is what Collabora sends for a new file. (e) The page says `X-WOPI-LockFailureReason` is for logging only, so no client should act on it; I have not checked that Collabora ignores it. (f) Whether Collabora ever sends `LOCK` with `X-WOPI-OldLock`.
    - **Is `/wopi` safe to enable?** The lock rule, the save and the break cover what decision 31 listed as unsafe, so the store and the WOPI host are safe as far as this code and its fake Collabora can show. `/wopi` stays off by default, and I would not turn it on for users until the items below are done. What still stands between this and enabling it:
        1. A live Collabora run against the WOPI host: the manual integration test of the proposal (section 7). It needs the owner's go-ahead for an image, and it has not been run.
        2. The licence question for Collabora. No Compose example is written until it is answered.
        3. Part l4: rename (`RenameFile`), the read-only view, the editor button, the README example, and the web interface's confirmation for a lock break (the break route exists; its page does not).
        4. The folder-check cost at the cap (about 80 ms per folder operation, above). Acceptable at the cap; not acceptable if the cap rises.
        5. The unverified Collabora behaviours (a) to (f) above.
        6. Sign-out and disable do not release a lock; it lapses within 30 minutes. A restore across a session is tested in the store, not through the API.
    - **Slice l3b is one commit with l3.** Migration 0026 is edited in place (it has not shipped), and no schema change was made in l3b.

34. **Chunked folder copy** (built on the owner's decisions of 2026-10-31: the staging folder is the mechanism; the default
    moves with this change, to 50,000, the largest of 20,000, 50,000 and 100,000 whose worst longest write, over three runs
    of the real copy, is at most 5 s). A copy above the sync limit
    (10,000 nodes) is a job in phases. It stays hidden until one write publishes it, and a failure leaves nothing visible,
    charges nothing and holds no reservation. Migration 0027.

    **Mechanism.** A staging folder is a folder row with `folders.staged = 1`, added by `ALTER TABLE ... ADD COLUMN`, so
    the foreign keys and the `kind` CHECK are unchanged. Every folder read filters `staged = 0`, and
    `store/staged_guard_test.go` fails a new folder read that does not, unless it is classified there with a reason. The
    nodes are built in the staging folder, one batch at a time, under its root. The publish moves them into the destination
    with one `UPDATE` of `folder_id`, sets the new root's parent and name, deletes the staging root and folder, and settles
    the reservation, in one write. Rejected: a column on `nodes` filtered in every node statement (about 100 statements),
    and a side table for the folder list (the same filter in more places).

    **Enumeration.** Every folder read in `internal/store`, and what can see a staged folder or node. A staged node has no
    ID a caller was given, and no grant, link or permission can name its folder, so a read that starts from an ID a caller
    gave finds nothing staged. The checked list:

    | Read | Rule |
    |---|---|
    | `Folder`, `FolderByNameKey`, `FolderByExternalID`, `PersonalFolder` (both) | filtered `staged = 0` |
    | `Folders` (the list), `pageFolders`, `pageFoldersAliased`, `FolderTotals` | filtered `staged = 0` |
    | the folder count of usage | filtered `staged = 0` |
    | `LargestFiles` (the page query and the outer join) | filtered `staged = 0` in both. Found in review: the page read a staged file, and the outer join only hid it on output |
    | node access (`nodeRoleIn`, `nodeAccessWith`, `GET /nodes/{root}`, children, the job's target) | reads the folder through `Folder`, so a staged node is `not_found` for every caller, owners included |
    | account counters (`usage.go`) | the copy's reservation is counted, as an upload's is; staged bytes and files are not (`RecountAccount` writes 0 for a staging folder) |
    | permissions, grants, groups, links, shortcuts, sharing, hidden spaces | name a node or folder by an ID the caller holds; a staged one has none. Classified in the guard with the reason |
    | `RootReachedFolders` and the root-permission join | a staging root has no permission, so the join finds none |
    | personal-folder owner lookups | a personal folder is never staged: staged folders are team folders |
    | webhook outbox, fan-out and `EmitTreeEvents` | events are written at publish only; a staged node reports nothing |
    | `ExpiredTrash`, purge | a staged node is never trashed |
    | label values count (`CountFieldValues`) | counts staged values too, because deleting the field removes them too. A field deleted during a copy also drops the staged values, so the published copy has fewer labels. The race is stated |
    | account delete | the account row cascades to its folders, nodes and copy rows |
    | backup | a copy of the database holds staged rows and their `copy_jobs` row; a restored job is requeued and resumes |
    | metrics | no folder or node query |
    | changes or sync feed | none exists; the webhook outbox is the only feed |

    **Guards** (`internal/store/staged_guard_test.go`). A folder read counts as filtered only when `staged` is in the SQL
    literal it sits in, so a filter in another literal of the same function does not count. The folder map records the
    unfiltered folder reads per file: `storage.go` went from 4 to 2 (`LargestFiles` filters in its own statement), and the
    reasons in `folders.go` changed, not its count.
    A node read is a `FROM nodes`, a `JOIN nodes`, or a use of the `nodeSelect` statement. The guard reads the condition
    that follows each one, across the literals a Go expression adds to it, and a read is scoped only when that condition
    names an ID, a folder or parent ID, or a node ID with `=` or `IN`. A read with no such condition must be classified.
    The first version of this guard was vacuous: a condition that only said `folder` matched the select list, and a
    `nodeSelect` use had no `FROM` in its own literal, so it was never seen. The review's two planted reads
    (`nodeSelect + " WHERE n.account_id = ? AND n.name_key LIKE ?"` and `SELECT n.id, n.parent_id FROM nodes n WHERE
    n.account_id = ?`) now fail the test, which reports `zz_plant.go: 2 unscoped node reads not classified` (the plant file
    was removed afterwards). The classified reads are four: two blob-reference checks in `nodes.go` and two trash reads in
    `trash.go`, for which no staged node can match. The `RootNodeIDs` read, scoped by `folder_id IN`, is not counted.

    **Phases** (`copy_jobs.phase`). `snapshot`: the source's untrashed subtree is read in one statement, so it is one
    consistent view, with each node's parent and depth, in depth order; a tree over `copy.max_nodes` fails the copy with
    nothing copied. The rows are stored in batches of 5,000, with their parents. An earlier version read the tree a page
    at a time, with a keyset over a walk that changed between pages; it could list a node twice or miss one, so it is
    gone. `copy`: the batches. `publish`: one write. `done`: the snapshot rows are dropped in steps. `cleanup`: after a
    failure, the staged nodes are removed deepest first in steps of 2,000, then the reservation is released and the folder
    and row are removed. A failed job reaches `cleanup`; a restart or the sweep resumes it. A chunked job whose copy row is
    gone fails with `copy_state_lost`, and never runs as a one-write copy; a job queued before chunked copies had rows
    (no `chunked` flag in its payload) still runs as a one-write copy.

    **Rules, as built.**
    - *Hidden until done:* the staged tree is unreachable (above). The 202 names the new root, which is `not_found` until
      the publish.
    - *Batches:* each batch is one write: the caller's guard, the next rows and the cursor, so a failed batch is rolled back
      whole. The size follows the measured hold: halved above 400 ms, grown below 200 ms, between 100 and 5,000 rows.
      `batchSizer` is tested with fixed inputs, not with timing.
    - *Restart:* the phase and cursor are in the database. Batch IDs are derived from the job and the source ID
      (`id.Derive`), and a batch is atomic, so a rerun names the same nodes and makes none twice. An interrupted snapshot
      is reset first.
    - *Reservation:* the bytes of the files the copy will make are reserved at the request: untrashed files, no versions
      (`CopyableTotals`; a trashed file was counted before, which refused some copies for quota they did not need). The
      destination user's limit is checked at the request too, not only at the publish, because the reservation sits in
      the team staging folder, where no user limit applies. `checkUsage` refuses an over-quota copy before anything is
      written. The reservation is `reserved_bytes` on the staging folder, which is how an unfinished upload
      shows. It is released exactly once: the publish and the cleanup each set `copy_jobs.reserved_bytes` to zero in the write
      that releases it. The publish charges the copy's real bytes and files, from the staged rows.
    - *Access:* the caller is checked at the request, at every batch (delegation live, source readable, `no_download` and the
      restricted-label rule: `copyGuard`), and at the publish (destination role, name, and the sharing rule the copies were
      made with). A change at any of them fails the copy, and nothing becomes visible.
    - *Restricted labels:* the first batch records `keep_restricted` (a manager of the source keeps restricted values; any
      other role drops them). Every later batch and the publish must give the same answer, or the copy fails with a conflict.
      The queue must write the undecided value `-1`: it wrote `0`, which every manager copy then read as a decision to drop
      them, so every manager copy failed at its first batch. The failure tests assert the error text, so such a failure
      cannot pass them, and `TestManagerCopyKeepsRestrictedAndSucceeds` checks that a manager's copy succeeds.
    - *Name:* checked at the request for a fast refusal and again at the publish with the request's `on_conflict`: `fail`
      fails the job, `rename` takes `name (1)`, `replace` is file over file only. The name is not held between the two.
    - *Source change during the copy (the rule):* a copy never succeeds with a tree whose structure differs from its
      snapshot, and never hides the difference. Each snapshot row records its node and the node's parent. A snapshot node
      that is trashed, deleted or under another parent when its batch copies it fails the copy with `source_changed`, and
      so does a node whose parent is not staged; the publish checks every snapshot row again, inside its write
      (`CopySnapshotChanged`), so a directory moved after its batch fails too. The failure is an ordinary failed job:
      nothing is visible, the reservation is released, the caller retries. A snapshot listing a node twice fails the same
      way (`InsertCopySnapshot` refuses it). A rename, or new content, is not structural: the node is copied as read at
      its batch, and the copy takes the new name or content. A node created after the snapshot is not in it, so it is
      not copied, and a file moved into the tree after the snapshot is not copied either. Tested: trashed, deleted and
      moved deeper or out, at the batch and at the publish; a directory moved after its batch; a rename; a file moved in
      (`TestSourceChangesDuringACopy`), and the duplicate (`TestInsertCopySnapshotRefusesANodeTwice`). Mutation proofs:
      removing the publish check fails the three "after its batch" cases. Removing the batch parent check fails no test,
      because the publish check catches the same moves at the end; the batch check only stops the copy earlier.
    - *Fairness:* an account may have two chunked copies queued or running (`too_many_copies`, a `conflict`, refused at the
      request before anything is staged, `TestTooManyUnfinishedCopiesRefused`). A running copy yields its turn after
      `copyYieldAfter` (30 s, a server constant): after a batch that has rows left, the handler returns `jobs.ErrYield`, the runner puts the job at
      the back of the queue (its `created_at` is now) and runs the next job, and the copy resumes from its cursor
      (`TestChunkedCopyYieldsToQueuedJobs`: a job queued behind the copy runs before it ends).
    - *on_conflict:* validated at the request for both paths. A small copy used to accept a bad value when no name clashed
      (the value was read only at a clash), and a chunked copy stored it and failed with a server error. Both now answer
      `invalid_request` (`TestCopyOnConflictIsValidatedOnBothPaths`; the mutant that accepts `bogus` makes the small copy
      answer 201).
    - *Events:* the publish reports one `file.created` per non-folder node, shortcuts included, as the one-write copy does.
      The usage charge counts files only, as it did. The test copies the same tree (five files and a shortcut) both ways:
      the one-write copy makes 6 events, and the chunked copy made 5 before this fix (`TestChunkedCopyReportsWhatTheOneWriteCopyReports`).
    - *Sweep and counts while a copy runs:* the sweep's reservation check compares the accounts' reserved bytes with the
      uploads' bytes plus the copies' reserved bytes; before, every sweep during a copy logged a false warning
      (`TestReservationTotalsCountsACopy`). The field list's value counts leave out staged values
      (`TestLabelListCountLeavesOutAStagedCopy`). Counts that decide a deletion (`CountFieldValues`,
      `CountUserLabelValues`) still count staged values, because the deletion removes them.
    - *Source change, earlier design:* a node created after the snapshot is not copied; the rest of the rule is above.
      The content of a file is read at its batch, as before.
    - *Events and audit:* nothing is written before the publish. The publish reports with `ReportFor`, as `copyTree` does:
      one `file.created` per file below the threshold, or one folder summary above it. `node.copy` is written once, at publish.
    - *Blobs:* a copy shares its source's blobs by reference, as copies always have. A failed copy's staged rows are deleted,
      which drops those references, and the blobs are collected by reference as before. The copy keeps no refcount of its own.
    - *Sweep:* `internal/sweep` finishes the cleanup of every copy whose job has failed or succeeded, in bounded steps. It
      never touches a copy whose job is queued or running.
    - *Cancel:* there is no cancel route for a copy, and none is added. A copy ends by publishing or by failing.

    **Limit.** The default `copy.max_nodes` is 50,000. The rule is the largest of 20,000, 50,000 and 100,000 whose worst
    longest write, over three runs of the real copy, is at most 5 s. At 100,000 the worst run's publish held the writer for
    7.87 s, so 100,000 is out; 50,000 has a worst of 1.56 s, so it is the default (section 8 has the table). An operator may
    raise it, and the cost is the publish's hold, which grows with the tree.

    **Measured, a full copy with a label on every node, per phase** (`TestMeasureChunkedCopyAt100k`, run with
    `SELFCLOUD_MEASURE_COPY=1` and `SELFCLOUD_MEASURE_NODES`; database under `/var/tmp`, ext4 on NVMe; a shared machine,
    load average 4.5 to 16 across the runs; one small write every 100 ms during the copy). The publish is the only write
    per copy, and it is the longest write at every size:
    - 20,000 nodes, publish 0.46, 0.90, 0.88 s; longest batch 0.33, 0.71, 0.37 s; total 6.7 to 11.9 s.
    - 50,000 nodes, publish 1.22, 1.44, 1.56 s; longest batch 0.45, 0.50, 1.03 s; total 20.8 to 26.9 s.
    - 100,000 nodes, publish 7.06, 7.87, 4.16 s; longest batch 0.99, 1.31, 0.48 s; total 55 to 146 s.
    The publish, not a batch, is the write over 5 s. Inside the publish, the move of the staged rows (one `UPDATE` of
    `folder_id`) is most of the time: in an instrumented run at 100,000 it was 3.35 s of a 4.1 s publish, and the other
    steps (the guard, the destination, the totals, the audit) took about 0.7 s, the deletes of the staging folder and the
    job update 2 ms. The check added in the review fixes (`CopySnapshotChanged`) is inside the publish too; it was not timed
    on its own, so its share of the 100,000 publish is not known.
    The batches stay between 0.3 s and 1.3 s, above the 400 ms the batch sizer aims at: a batch holds the writer for its
    whole write, and the sizer reacts after it. Under the 5 s rule, so not a blocker, but not what the sizer targets.
    Earlier runs, before the review fixes, held the publish at 2.39, 2.84 and 3.01 s at 50,000 and 3.22, 4.96 and 6.54 s at
    100,000, on a different load; the 100,000 worst case was over 5 s in both.
    The "longest write" of a run is the largest single hold, including the wait for the writer; the copy's own writes do
    not wait for each other, so this is mostly the write's time. One run is three values; the spread is wide at 100,000.
33. **Thumbnails, slice s1** (2026-10-31, built to the owner-approved proposal for slice s with every recommendation (a) in its section 9). Decision 10 holds: no LibreOffice, and no converter runs in the server. Revised the same day after the review of s1 (`review-s1.md`, findings M1 to M4 and L1 to L5); the outcome of each is under "Review outcomes" below.
    - **Isolation.** The decoder runs in a child process: the server's own binary, as `selfcloud thumb`. Before it reads its input the child applies its limits and then takes its privileges away (`harden`). `RLIMIT_AS` is relative: the address space the child holds at start, plus `derived.memory`. An absolute limit cannot be used, because the Go runtime maps about 1.2 GB before `main` runs. `RLIMIT_CPU` is enforced by the kernel: SIGKILL at the hard limit, one second after the soft one (measured: a 1 s budget was killed at 2.01 s). Lowering one's own limits needs no capability. Docker with no extra capabilities was not run here.
    - **What is enforced (Linux).** Address space (relative), CPU time, wall-clock time (the parent's context, with `WaitDelay`), the input size (the parent refuses above `derived.max_source_bytes`, and the child reads at most that), the pixel count (read from the header before any pixel is allocated), the output size (the parent refuses output above 16 MiB), the child's environment (empty) and working directory (`/`), its open descriptors (16 at most; the input, the output and the errors are open before the limit applies), its file size (0: it writes no file), its core dump (0), and its privileges: `no_new_privs` is set, and a Landlock ruleset with no rule denies every file-system access, and from ABI 4 every TCP bind and connect. The child is killed when the server thread that started it exits (`Pdeathsig`; Linux counts the thread, not the process), and runs in its own process group (`Setpgid`). Measured on this kernel (6.12, Landlock ABI 6): the probe child cannot open the data file, cannot open `/etc/passwd`, and cannot connect to a listening socket, and the same probe without the sandbox does all three (`TestSandboxDeniesFilesAndNetwork`).
    - **Where the sandbox is applied, and what it does not cover.** The privileges are taken from one OS thread: `harden` locks the goroutine to it. The decoder spawns no goroutine, so every line of decoder code runs on that thread. The Go runtime's other threads run no decoder code, and any thread it starts later inherits the restriction. `syscall.AllThreadsSyscall` was not used: it returns `ENOTSUP` in a binary that links cgo, which the default `go build` here does. Landlock is best effort. Where the kernel has none, the child runs with `no_new_privs` and its limits, and the server logs once at start which of the two it has (`thumbnail decoder sandbox`, with `landlock_abi`). No seccomp filter is applied: a filter would need the Go runtime's whole syscall list, which is not a cheap job. Docker's default seccomp profile may refuse `landlock_create_ruleset`, `landlock_restrict_self` or `prctl(PR_SET_NO_NEW_PRIVS)` on some versions, in which case the child would refuse to start (`ExitLimits`) or run without Landlock; this was not tested in Docker. A decoder compromised in the child is still a process of the server's user with the open descriptors it has, and with the sandbox it can reach neither the file system nor the network. The standard decoders are memory-safe and nothing outside `image/jpeg`, `image/png` and `image/gif` is linked into the decoder.
    - **Platforms.** Other than Linux, the child refuses to decode (`ExitLimits`), because the other systems accept an address-space limit without enforcing it, and there is no Landlock there. Thumbnail requests answer `unavailable`.
    - **Fallback.** A child that cannot set its limits or take `no_new_privs` exits before it reads its input. The parent records nothing about the source, removes the pending row, and logs an error; a later request tries again. The server never decodes without limits.
    - **Failures.** Every outcome that is not a success is recorded in one of two classes. *The image's own refusal* (corrupt, not matching its header, over the pixel cap, unsupported, over the source cap, or output over its limit) is sticky for its key: a later request answers `404` and queues nothing. *A condition of the host or the run* (a signal, the time limit, the runtime's out-of-memory exit `2`, any other exit, a full disk) is retried: the row is failed with a retry time (1 minute after the first failure, 10 minutes after the second), a request after the retry time queues it again with its attempt count kept, and after the third failure it is sticky under the same key, with the reason suffixed `gave up after 3 attempts`. A request before the retry time answers `404`. A shutdown records nothing: the job is dropped and the row removed, and the next start tries again. A child that could not start is a server fault: the row is removed, nothing is recorded. The reason of a failure is one of a fixed set of short server messages; the child's stderr is kept to 4 KiB and never shown.
    - **Formats.** The format is found from the first bytes, never from the stored type. The stored type decides only whether a file is eligible: `image/jpeg`, `image/png` and `image/gif`. A GIF is decoded at its first frame, placed on its logical screen: a first frame smaller than the screen is composed on a transparent screen, which is shown on white, and a frame that sticks out of the screen is corrupt. The output is always a JPEG at quality 85, on white where a source is transparent, and never enlarged. EXIF orientation is not applied (the standard decoders do not read it), so a photograph taken on its side is thumbnailed on its side. SVG is never decoded (`safeInlineType`).
    - **Memory (measured).** The decoder's memory is set by its input and its output, and `derived.memory` must cover it. Progressive JPEG with 4:4:4 sampling is the most: it keeps its coefficients (three components of 4 bytes each) and its YCbCr planes, 15 bytes a pixel. A 16-bit PNG is 8 bytes a pixel, and the output is 4 bytes a pixel of the thumbnail. The output buffer of the box filter was 40 bytes a pixel of the thumbnail before this revision (sums and counts for the whole output); it is now one row of sums, so the thumbnail's own image is the only output buffer. Measured with the production binary at the largest image of the default pixel cap (7071 by 7071, 49,999,041 pixels), smallest budget that decodes, and every fixture passes at the default 1 GiB at sizes 256, 1024 and 4096 (`TestMatrixDecodesEveryFormatAtTheCap`, guarded by `SELFCLOUD_MATRIX_DIR`):

      | fixture (largest of its format at the cap) | smallest budget that decodes, before this revision, size 256 | smallest budget that decodes, this revision, size 4096 |
      |---|---|---|
      | 8-bit PNG, GIF, baseline JPEG 4:4:4, baseline JPEG 4:2:0 | 256 MiB or less | 384 MiB or less |
      | 16-bit PNG, 16-bit interlaced PNG | 512 MiB | 512 MiB |
      | progressive JPEG 4:2:0 | 768 MiB | 512 MiB |
      | progressive JPEG 4:4:4 | more than 768 MiB, 1024 MiB decodes | more than 768 MiB, 896 MiB decodes |

      Each figure is the smallest of the budgets run (256, 512, 768, 1024 MiB before; 384, 512, 640, 768, 896, 1024 MiB after), so it is an upper bound. The old 512 MiB default failed on both progressive JPEGs at size 256. At size 256 this revision was not measured separately; the matrix test passes at 1 GiB at 256, 1024 and 4096. The floor is now `thumb.MemoryFloor(max_pixels, max_source_bytes, size)`: 16 bytes a pixel, twice the input cap, 4 bytes a pixel of the thumbnail, and 64 MiB. With the defaults it is about 0.99 GiB at size 4096, so the default `derived.memory` is 1 GiB (it was 512 MiB, which the progressive JPEGs at the cap did not fit in). The other choice, lowering `max_pixels` to 25 Mpx, would refuse legitimate 25 to 50 Mpx images that the default now decodes; that was not taken. The config validator refuses a `derived.memory` below the floor for every configured `thumb_sizes` entry, and names the entry.
    - **Budget.** `derived.max_bytes` (10 GiB by default) limits the ready files of all accounts. The budget is checked and the row completed in one write, so two finishing workers cannot both pass it; when it is full, the least recently used ready files are evicted, file first, then row. A file that cannot be removed stops that eviction, and the sweep logs it and runs its other steps. Derived files do not count toward any quota, and no usage counter changes (proposal section 5).
    - **Workers and queue.** `derived.workers` decodes run at once (2 by default), each in its own child. `derived.queue` requests may wait over all accounts (256 by default), and at most 32 of them belong to one account. The queue is round-robin across accounts: each account with work waits in turn, so one account's backlog does not hold the workers while another waits. A request is admitted by reserving a place in the queue before it writes anything, so a request that finds its account's share or the total full is refused with `rate_limited` and `Retry-After` and costs no writer lock; nothing is written that would have to be undone.
    - **Access.** The route calls `nodeAccess` on every request, as the content route does, so a revoked permission, a trashed item, a move, a token outside its node scope and another account are answered by the same rules. A folder, a shortcut and a file of another stored type are `not_found`. The route has no `link` flag (proposal section 3): a share-link visitor gets `link_route`. Viewing is not downloading, so `no_download` does not refuse a thumbnail (decision 20; proposal question 9). A thumbnail is not a view, and does not record one in Recent. A derived row is found only by its account and its blob, and the insert checks in SQL that the blob is the account's. A `HEAD` request reads and never starts work: it answers what a `GET` would find (`200`, or `303` on a content origin, or `202` if a `GET` already queued it), and `404` for a thumbnail not made.
    - **Content origin.** With `content_url` set, a ready thumbnail answers `303` to a signed link on the content origin (proposal question 6). The link's disposition is `thumb_<size>`; only the server signs it (`handleCreateLink` still accepts only attachment and inline), and it is checked as every link is: its MAC against the file's content tag, its expiry, the trash state, and the account's status. As with content links, a link is a capability that revocation does not reach before its expiry (`links.default_ttl`).
    - **Caching.** A thumbnail is keyed by blob, kind and converter. The converter is `thumb-go-2+` and a hash of the limits that decide a refusal (pixel cap, source cap, memory, CPU, output cap). Raising one of them makes a new key, so a refusal made under the old limit is not kept for the new one. The time limit is not in the key: a time-out is a condition of the host and is retried. The encoder version changed from `thumb-go-1` in this revision (the GIF composition and the box filter change the output), so no row of the first revision is read as current; s1 is unreleased, so no row of that revision is expected to exist. A new version of a file is a new blob, so it never reads an old thumbnail. Blobs are not deduplicated in this store (blob IDs are random), so one blob serves every node that holds it, as a copy does. Uses are recorded in memory and written in batches every ten seconds, not per request.
    - **Cleanup.** A blob's derived rows and files are removed in the sweep before the blob's own file, in `removeDue`, which is where purge and version eviction end (both orphan blobs). The rows go in one write, then the files. Account deletion removes the account's directory after its rows (they cascade). The sweep also removes: temporary files older than an hour; derived files that no row names, when they are older than an hour, at most 1000 files per pass (a file being made is in `tmp/`, and a finished one has its row pending, so a young file is left for the next pass); and failed rows that failed 30 days ago or more, so the next request queues them again. At start-up, pending rows are removed: the server assumes one process per data directory.
    - **Events and audit.** Thumbnails emit no webhook event and write no audit entry. The node-kind comparisons added in `thumbnails.go` are reviewed (`reviewedNodeKindSites`).
    - **has_thumbnail.** Set on an image file (stored type eligible) to true when its smallest configured thumbnail is ready, and false otherwise. Absent for other kinds and for a link visitor (decision 24: a visitor sees no more than the item's own content). Computed in one query per listing page.
    - **Migrations.** `0028_derived.sql`, unreleased, so it was edited in place: `failed_at`, `attempts` and `retry_at` were added, with an index on failed rows.
    - **Review outcomes.** Each finding was checked against the code before it was changed, and each fix has a test. The mutation proofs are the guard, test and failing line for each fix (run on this branch, one at a time, with the file restored after each).
      - **M1, verified.** A failure caused by the server or the host was sticky for its key, and the key had no limits, so raising `max_pixels` did not retry it. Reproduced on the unchanged code with the review's test: after the cap was raised, the row stayed `failed` and nothing was queued. Fixed by the two classes and the converter key above. Guards: `TestImageRefusalIsStickyUntilALimitChanges` (guard: the converter key; reverted, the row stays failed after the cap is raised), `TestTimeoutIsRetriedWithBackoffThenSticky` (guard: `exitTimeout` recorded as a transient failure; reverted to `fail`, the first failure has no retry time), `TestShutdownDuringADecodeRecordsNothing` (guards: the cancelled-context branch in `process` and the cancelled-context check in `record`. Mutation result: removing either guard, or both, is NOT caught by this test. Once the context is cancelled, the store's reads in `record` fail, so no failure is written either way. The guards are explicit, but the test checks the outcome (no failed row), and cannot tell them apart from that read failure), `TestClearFailedRemovesOldFailures` and `TestFailedDerivedRowsAreClearedAfterThirtyDays` (guard: the sweep's clear step).
      - **M2, verified, and the floor was wrong.** The review said a decode holds 4 bytes a pixel. Progressive JPEG and 16-bit PNG need more, as the table above shows. Fixed by the floor function, the 1 GiB default, the per-size validator, the output change and the guarded matrix. Guards: `TestDerivedRefusesASizeTheMemoryCannotServe` (reverted to the old floor, the 4096 entry is accepted), `TestMatrixDecodesEveryFormatAtTheCap` (guard: `SELFCLOUD_MATRIX_DIR`; it fails when a fixture needs more than the default).
      - **M3, verified.** Reproduced on the unchanged code with the review's test: 14 of 400 requests cancelled while the queue was full left a pending row with no job, and answered an error. Fixed without a cleanup write: the place is reserved before the row is inserted, so there is nothing to undo. The review's remedy (`context.WithoutCancel` with a timeout for the cleanup) is therefore not needed and not used. Guard: `TestCancelledRequestsLeaveNoPendingRowWithoutAJob` (reverted to inserting without a reservation, it fails).
      - **M4, verified.** One FIFO of 256 with no per-account cap. Fixed by the round-robin queue and the 32-job share. Guards: `TestRoundRobinBetweenAccounts` (reverted to a FIFO for the accounts, the second job served is A's), `TestRequestOverTheAccountShareTakesNoWriterLock` (guard: the share; without it the request waits for the writer, and the test fails at five seconds), `TestTotalQueueIsStillBounded`.
      - **L1, verified.** The child had no hardening. Fixed as above: `Pdeathsig`, `Setpgid`, `cmd.Dir = "/"`, `no_new_privs`, the descriptor, file-size and core limits, Landlock. Guards: `TestSandboxDeniesFilesAndNetwork` (reverted: Landlock not applied, zero of three attempts refused; the descriptor limit not set, the limits line fails).
      - **L2, verified.** A first GIF frame smaller than its screen was refused as corrupt, which was sticky. Fixed by composing it on the screen. Guards: `TestGIFFirstFrameSmallerThanScreenIsComposed` (failed on the unchanged code with `the image cannot be decoded`; reverted to refusing, it fails), `TestGIFFrameOutsideItsScreenIsCorrupt`.
      - **L3, verified.** The sweep returned at the first eviction error. Fixed: the error is logged and counted, and the later steps run. Guard: `TestUnremovableDerivedFileDoesNotStopLaterSteps` (reverted to returning the error, the pass stops with `directory not empty`).
      - **L4, verified.** A file no row named was never removed. Fixed by the orphan step. Guard: `TestOrphanDerivedFilesAreRemovedWhenOldAndUnnamed` (reverted, the old orphan survives).
      - **L5, verified.** A `HEAD` queued work, and a refusal was logged at warning. Fixed: a `HEAD` answers from the rows only, and a refusal is logged at information. Guards: `TestHeadStartsNoWork` (reverted to queueing, the `HEAD` answers `202` and a row is queued), `TestRefusalIsLoggedBelowWarning`. The review's point that a full queue returns `404` on a race (`ErrNoBlob` after an insert) is not changed: it is a race between a request and a concurrent removal, and the next request answers correctly. The review's point on `os.Executable` is not changed: the server reads its own binary once at start, as before.
    - **Measured, not verified here.** Docker with its default seccomp profile (not run). The Landlock behaviour on kernels other than this one (6.12, ABI 6). The disk-full path, which is read from `os/exec` and the code, not run against a full disk.
    - **Not in s1.** Metrics for the derived pool are not added. EXIF orientation is not applied. Video, HEIC, WebP and TIFF are not thumbnailed. Zip, previews, conversions and the web interface are slices s2 to s4.

35. **Stored zip downloads, slice s2** (2026-10-31, built to the owner-approved proposal for slice s with every recommendation (a) in its section 9, questions 1, 7 and 10 among them; migration 0029). The proposal's sections 2 to 7 are followed, with the interpretations below, each stated where the proposal is silent or contradicts itself. Decision 10 holds: no converter runs in the server. Zip is standard library only (`archive/zip`, deflate), so no new dependency.

    **Interpretations, and why.**
    - *The worker is the jobs runner, not the derived pool.* The proposal says both (section 2: a `jobs` row; section 4: the derived pool). The job is `node.zip`, its target the zip's ID, and it yields its turn with `jobs.ErrYield` as the copy does (decision 34). The derived pool keys work by blob and is the wrong shape for a zip.
    - *Deflate, not stored.* The brief said stored unless the proposal says otherwise; question 7 (a), approved, says deflate at the default level. Deflate streams: nothing is read whole.
    - *The disk budget is its own, not the thumbnails'.* Proposal section 2 would share `derived.max_bytes` with LRU eviction, so one large zip would evict every thumbnail and could evict another caller's zip. Stored zips have their own budget, `zip.max_disk` (30 GiB, a placeholder), counted over the ready zips and the bytes the unfinished zips reserve, and each account has a share of it, `zip.max_disk_per_account` (10 GiB, a placeholder): one account's zips may not pass its share, and another account's zips are not held back by it. The two are checked in the same write, at the snapshot and again at publish, and a refusal names its own reason (`zip_disk` for the installation, `zip_account_disk` for the share), both `storage_full`. A zip's reservation is its snapshot's byte total, taken inside the write that records the snapshot, so two zips cannot both pass the budget. The check is repeated with the real size inside the publish write. A zip that would pass it is refused before its first byte; expired zips are swept first; no unexpired zip is evicted for another. Stored zips do not count toward any account's quota (question 5 (a)).
    - *The state between turns is in memory.* `archive/zip` keeps its central directory until `Close`, and it cannot be reopened on a partial file. So a yielded zip keeps its open file, its writer, its hash and its plan in a map on the server, keyed by the zip's ID, and the next turn continues from its cursor. This differs from the copy, whose state is in the database. A restart loses the map: the job is requeued, finds no state, truncates the partial file and makes the zip again from a new snapshot. The partial file is `zips/<account>/<zip>.part`, named by the zip's ID, so the restart finds it. At most `zip.max_unfinished` (8) zips are in progress, which bounds the memory.
    - *Owner-only, and by credential.* A zip is for the caller who made it: its user or key (`created_by` and its kind) and the credential it came in on. A session or key's zip is read by no token; a token's zip is read only by that token, and not by another token of the same user, which has the same user and folders. The credential is stored as `created_cred`, the fingerprint of the token's signed fields (its mint time, expiry, user, scope, and its key), or empty for a caller with no token. A different credential, another member of the account and an admin get `not_found`. Share-link visitors are refused by the route table (`link_route`), as on every route without a link flag. The download does not re-check the no-download rule (question 10 (a)): a rule changed after the zip was made does not block it, and a different, weaker credential is refused by the rule above, not by a second copy of the rule.
    - *A zip is a snapshot of the tree at job time*, and it reads each file from the blob the snapshot named. Blobs never change (`blob.go`), so a file replaced or renamed after the snapshot does not change the zip. A node trashed or deleted after the snapshot is still in the zip, because the zip is what the snapshot says; a node trashed before the job's snapshot is left out. The structural rule of decision 34 (`source_changed`) is not used here, because a zip reads blobs, not positions in a tree. Each turn re-checks, as the caller, that every root is readable and not restricted (`zipGuard`, as `copyGuard` does), so a caller who loses access mid-job gets no zip.

    **Access, per node.** The snapshot checks each node the walk reaches with `nodeAccessWith` as the caller, so the zip holds only what that caller could download at job time.
    - A descendant the caller cannot read is left out and counted in `skipped`. Roles inherit downward (`effectiveRank` takes the chain of permissions from the node up), so a descendant of a readable root is readable unless a rule denies it; the snapshot checks each one, rather than assuming.
    - A shortcut is left out and counted. A zip never follows one, so no shortcut reaches a file the caller cannot read, and no cycle can form.
    - A trashed node is not reached: `ZipTree` stops at trashed nodes, as `SubtreeRows` does for a copy.
    - A node the no-download rule restricts for the caller (`downloadRestricted`: a viewer or commenter, the flag on the node or an ancestor) refuses the whole zip with `download_restricted`, as a copy does (`copyRestricted`). Refused at the request (`POST`), again at the job's start, and in the snapshot for each node.
    - A zip request names at most 1,000 nodes, each a valid node ID, none twice, none a shortcut.
    - Download (`GET /zips/{zip}`) re-checks, on every call including a `Range` resume: the caller is the maker; each stored root passes `nodeAccessWith` (revocation, a trashed root, a token outside its node scope, another account all refuse). The no-download rule is not re-checked on download: decision 35 follows question 10 (a), a zip stays until its expiry once made. A zip past `expires_at` that the sweep has not yet removed answers `not_found`.
    - `DELETE /zips/{zip}` is the maker's, and refused while the zip is pending (`conflict`).

    **Names.** A zip entry's path is built from node names, each passed through `zipComponent`. Node names are checked when made (`names.Clean`: no `/`, `\`, control or format character, no `.` or `..`, at most 255 bytes), so the component rule changes nothing for a name the server accepted; it is the second line of defence. Within a zip: a path separator, a colon, a control, a format character (bidi overrides included), or a line separator becomes `_`; `.`, `..` and the empty name become `_`; the result is NFC and at most 255 bytes, cut at a character boundary. Siblings whose case-folded keys collide become `name (1).ext`, `name (2).ext`, with the extension kept (`nameSet`). Two roots with one name are kept distinct the same way. A path longer than 4,096 bytes fails the zip (`too_large`); none is cut silently. Folders are entries of their own (`name/`), so an empty folder survives. Every entry has the UTF-8 flag; the test reads it back. The entry's modification time is the node's; no comment is written; no label value is written, and no label of any kind (a zip does not widen the restricted-label rule). The only other fields are the names, the sizes and the times, plus the audit entry (below).

    **Zip64.** `archive/zip` switches to Zip64 by itself. Proved: a single entry of 4 GiB plus one byte, streamed from a generator of zeros through the job's writer settings (deflate), reads back with its uncompressed size (`TestZip64EntriesAndCountPastTheLimits`, run with `SELFCLOUD_ZIP64_BYTES=4294967297`, 16 s); and a central directory of 65,540 entries reads back (the same test, always run). The 4 GiB entry is a generator's zeros, not a stored file, so no 4 GiB is written to disk; the deflated output is a few megabytes. This tests the library's writer with the job's settings; the job's own path to a 10 GiB file is measured below.

    **Limits, fairness and disk.**
    - Entries: `zip.max_entries` (100,000); bytes of files: `zip.max_bytes` (10 GiB); both checked as the snapshot grows, so a refused zip writes nothing (`TestZipLimitsRefuseBeforeWriting`).
    - Per caller: `zip.max_per_principal` (2) unfinished zips, refused with `conflict` (`too_many_zips`). Per account: `zip.max_per_account` (4), refused with `too_many_zips_account`. Per installation: `zip.max_unfinished` (8), refused with `rate_limited` and `Retry-After`. The first two are counted inside the request's write, so a race cannot pass them. `zip.create` is rate-limited per caller (60 a minute, the limiter's unit).
    - The turn yields after 30 s (`zipYieldAfter`), and only after at least one entry in the turn, so a zero budget still makes progress (`TestZipYieldsItsTurnAndResumes`: a zip of six entries finishes; with the progress rule removed the test fails, see below).
    - Expiry: `zip.ttl` (24 h), counted from the zip's publish. The sweep removes an expired zip's row, then its file (`derived.ZipFiles.Sweep`, bounded to 500 a pass), and fails a pending zip whose job has ended without making it, removing its partial file (`TestZipSweepRemovesExpiredAndAbandoned`).

    **Consistency and integrity.** The blob is opened before anything else, so a purge after the open cannot change what is read. Each file's size must equal the node's size, and its SHA-256, computed as the bytes are written, must equal the blob's stored SHA-256 (`blobs.sha256`, which `nodeSelect` joins). A mismatch fails the job with "do not match its checksum" (`TestZipDetectsAContentsMismatch`: the blob's bytes changed in place, keeping their size). The zip's own CRC-32s are written by `archive/zip`, and read back with it, which the tests do, and each entry is read to its end in the measurement. The zip's file is hashed as it is written, and its SHA-256 is the ETag.

    **Failure and restart.** A failure in any stage (`entry` is the fault point; the snapshot, the publish and the rename fail the same way) removes the partial and the finished file, sets the row to `failed` with a short reason (at most 200 bytes, the server's own text, never a path), and fails the job (`TestZipFaultFailsTheJobAndRemovesItsFiles`). A shutdown returns the context's error and records nothing: the row stays pending, the job is requeued at the next start, and the zip is made again from a new snapshot (`TestZipCrashStartsOverAndLeavesNothing`: cancelled after the second entry, then run again from a cleared map, the zip is complete and holds six entries). The publish renames the partial file to its final name before the row is marked ready; a failed write removes the final file, and a crash between the two leaves a pending row, which the restart makes again. Account deletion removes `zips/<account>` with the account's other files (`runAccountDelete`, `TestZipAccountDeletionRemovesItsFiles`), and the rows cascade with the account.

    **Writer hold.** No writer is held across file I/O. The job has three writes: the snapshot's (`SetZipSnapshot`, a single `UPDATE` after the walk), the publish's (`CompleteZip` and the audit row), and the failure's. Each is a small statement, not a function of the tree's size; the walk and the copying are reads and file writes, outside any write. The measurements below report the job's wall time; the writer hold of each of the three writes is not timed separately.

    **Events and audit.** A zip emits no webhook event: a download made by its caller is not a change to a file. The audit entry is `node.zip`, written in the publish's write, with the target (the zip's ID), the actor, the number of roots, entries, skipped nodes and bytes, and no names. It is classified in `webhook_guard_test.go` with that reason.

    **Routes and guards.** `POST /api/v1/zips` (202 with the job, `Location` set), `GET /api/v1/zips/{zip}` (200 `application/zip` with `Range`, `If-Range`, `ETag` the SHA-256, `Content-Disposition` attachment, `X-Selfcloud-Skipped`; 202 while pending; 404 otherwise), `DELETE /api/v1/zips/{zip}` (204). All `auth: true`, with no link flag. `TestRoutesMatchSpec` and `api/openapi.yaml` cover them. The node-kind comparisons of `zips.go` (five) are reviewed in `reviewedNodeKindSites`: each is a shortcut refusal or skip, or a folder test that decides the entry kind. `TestZipCallerMatrix` covers the visitor of an anyone-link (refused, `link_route`, on the three routes) and the admin key (no zip, no download). The matrix has no delegated-token row: the token scope is checked by the same `nodeAccessWith` the copy uses, and no test here mints a token scoped to a folder. The store guard (`staged_guard_test.go`) passes: `ZipTree` reads nodes by a recursive walk from one ID, and `zips` rows are never staged.

    **Download refactor.** The `Range` block of `serveFile` moved to `applyRange`, which the zip download uses too. Its behaviour is the same, and the range and conditional tests of decision 33's content route pass unchanged.

    **Tests and what each proves** (`internal/api/zips_test.go`, `zips_matrix_test.go`): the round trip with names and empty folders read back; `Range` resume and `416`; the maker rule, another member, another account, revocation and the trash; delete while pending; restricted roots at request and at job start; the shortcut counted and not followed; the limits refused before writing; the disk budget counting ready and reserved bytes; the per-caller count; request refusals; the yield and its progress; the crash and restart; a fault; the checksum mismatch; the sweep; account deletion; zip64; the component rules; sibling names; roots with one name; the caller matrix.

    **Mutation proofs** (run on this branch, one change at a time, restored after each; the file restored and the tree checked for residue): (1) the SHA-256 comparison disabled: `TestZipDetectsAContentsMismatch` fails ("job succeeded, want failed on the checksum"). (2) the maker check removed on download: `TestZipIsForItsMakerAndCheckedEveryCall` fails at line 167 (another member gets 200). (3) the format-character stripping removed from `zipComponent`: `TestZipComponentsAreSafe` fails on three names (U+200B, U+202E, and a `..` with U+202E). (4) the yield made before the first entry of each turn: `TestZipYieldsItsTurnAndResumes` fails ("did not finish within 100 turns"). Not run: the snapshot's own restriction check, removed alone. The job-start guard (`zipGuard`) covers the same case first, so `TestZipRestrictedAfterRequestFailsTheJob` would not show that check's removal. The check is kept as a second line.

    **A defect found by measuring, and fixed.** The first 20,000-entry run took 4 min 19 s (5,000 took 10.8 s; 10,000 took 53 s): the cost per entry grew with the count. A CPU profile of the 10,000-entry run put 90% of the time in `store.trashState`, the ancestor walk that every per-node access check runs (`nodeRoleIn`). Its final join filtered `nodes` by `account_id`, and SQLite answered it with a scan of the whole nodes table on every call (`SCAN n` in `EXPLAIN QUERY PLAN`), so a walk of n nodes cost O(n²). The join now reaches each chain node by its primary key (`trashChainSQL`); the account stays filtered in the recursion, so the chain is still scoped to the account. It is shared by every access check, so the fix speeds up every request on a large tree, not only the zip. Guard: `TestTrashChainSQLDoesNotScanNodes` reads the plan and fails on any scan of `nodes`; mutation (the account filter restored on the final join) fails it with "the ancestor walk scans the nodes table: SCAN n", and the revert passes. The growth is measured below, before and after.

    **Review fixes (review-s2.md, seven defects, each with a test that failed first or a mutation proof).**
    - *A name collision with a long extension panicked (defect 1).* The suffix is kept and the extension is cut first, then the stem, so every name is at most 255 bytes and distinct (`fitName`). Guard: `TestZipLongExtensionCollisionDoesNotPanic` (mutation: the cut removed, panic `slice bounds out of range [:-3]`).
    - *One account could take the whole disk (defect 2).* The per-account share (above). Guard: `TestZipAccountDiskShare` (mutation: both checks removed, the second zip succeeds).
    - *A different credential of the same user could read a zip (defect 3).* The credential rule above, with the second credential cases in `TestZipIsForItsMakerAndCheckedEveryCall` (mutation: the check removed, a token reads the session's zip, 200).
    - *Failed rows were never removed (defect 4).* The sweep removes a failed row, with its roots, once `zip.ttl` has passed since its creation. Guard: `TestZipFailedRowsAreSwept` (mutation: the cut-off set to zero, no row removed).
    - *A file with no row was never removed (defect 5).* The sweep's orphan step removes a `<zip>.zip` or `<zip>.part` that no row names and that is more than an hour old by its modification time; a file whose row exists is never removed, so a file being published is safe. The comment on `Sweep` is now true. A delete removes the file first and the row second: a failed removal is returned and the row stays for a retry, so no file is left without a row by a delete. Guard: `TestZipOrphanFilesAreSwept` (mutation: the step disabled, the old orphan stays).
    - *Any error while walking skipped a node (defect 6).* A node is left out only when the caller is refused it (`not_found` or `forbidden`); any other error fails the zip. Guard: `TestZipWalkErrorFailsTheZip` (mutation: every error skipped, the job succeeds).
    - *A left-out folder counted once (review gap).* A folder the caller cannot read is left out with everything under it, and the header counts all of it. Guard: `TestZipLeftOutSubtreeCountsEveryNode` (mutation: counted as one, `skipped "1", want 2`).
    - *A panic leaked the work (defect 7).* A panic in a turn releases the registry entry and the open file, fails the row through the same path as any failure, removes the partial file, and then re-panics so the runner records the job. Guard: `TestZipPanicReleasesTheWork` (mutation: the release removed, the work stays registered).
    - *The Zip64 test did not run the job.* `TestZip64ThroughTheJob` (gated by `SELFCLOUD_ZIP64_BYTES`, with `SELFCLOUD_MEASURE_DIR` under `/var/tmp`) makes a zip of one file of 4 GiB plus one byte, a sparse blob of zeros, through the snapshot, `writeZipEntry` with its checksum, the publish and the download. It passed: the job took 54.9 s, the zip is 4.17 MB, and the entry read back and checked to its end in 3 s.

    - *The job did not check its caller (review finding 1).* A zip is made for a caller who may still act, as a copy is. `zipGuard` checks the caller at the start and the end of each turn and before publish, with `callerActive`, the rule copy jobs and the transfer use (one rule, not a copy of it). The publish check is inside the write that makes the zip ready, so a revoke before it is seen there. A refused zip says no zip was made, fails its row, and holds no disk. Guard: `TestZipJobRefusesARevokedToken`, `TestZipJobRefusesARevokeBeforePublish`, `TestZipJobRefusesARevokeBetweenTurns`, which also asserts that no entry is written after the revoke (mutations: the turn check removed, the job writes entries after the revoke and fails the entry count; the publish check removed, the zip is made ready for a revoked caller).
    - *The subtree was read whole before any limit (review finding 2).* `ZipTree` takes a limit and the query stops there, so a subtree of any size costs `zip.max_entries + 1` rows in memory at most. `planZip` keeps a running total of nodes read across the roots, skipped-for-access ones included, and refuses with the too-large error before building anything else. Guard: `TestZipTreeReadsAtMostTheLimit` (the query returns the limit, not the 3,006 rows of the subtree: the mutation is no `LIMIT`), `TestZipLimitIsARunningTotalAcrossRoots`, and `TestZipLimitCountsNodesSkippedForAccess` (mutation: skipped nodes not counted, and the zip succeeds).

    **Measured.** Set-up is one write (`InsertNode` for each file, all naming one small blob), so set-up time is not in the job's time. Each job time is the zip job alone (snapshot, entries, publish), from `createZip` to `runZip`, not the HTTP read-back. Peak RSS is the test process's, so it includes the set-up.

    | entries | before the fix | after the fix | ms per entry after |
    |---|---|---|---|
    | 5,000 | 10.8 s | 3.7 s | 0.73 |
    | 10,000 | 53.0 s (profiled) | not run | n/a |
    | 20,000 | 4 min 19 s (258.9 s) | 13.3 s | 0.67 |
    | 50,000 | not finished before a run was stopped | 36.1 s | 0.72 |

    After the fix the cost per entry is flat within 10%, from 5,000 to 50,000: linear. The 50,000-entry zip is 7.7 MB, peak RSS 244 MiB, and its read-back (every entry, CRC checked) takes 0.47 s. Set-up of 50,000 nodes in one write takes 12.8 s.

    10 GiB, one file. The blob is a sparse file of 10 GiB of zeros (the seed blob truncated to zero, then extended, so no byte is stored), with its size and SHA-256 set to match (the checksum of 10 GiB of zeros took 29 s). The job wall time is 1 min 7 s, the zip is 10.4 MB (deflate of zeros, so this overstates real throughput: it is about 160 MB/s of input on this machine, which is not the speed of real data), the read-back of every entry with CRC checked takes 3.4 s, and peak RSS is 66 MiB. The writer is never held across this I/O. The 10 GiB case is one entry; it shows the job does not read the file whole, not the speed of a real file.

    Not run: a 50,000-entry measurement with a real (non-zero) 10 GiB file, and the cost of a folder of many large files at once.

    **Not in s2.** Previews and exports (s3). The web interface (s4). A signed link to a zip: the proposal's section 3 mentions one (question 1, option c), which is not an approved recommendation, so no link route is built. Quota charging for stored zips (not charged, question 5 (a)). Metrics for the zip queue. A zip of a tree larger than `zip.max_entries` or `zip.max_bytes` is refused, not split.

36. **Slice p, closing review (p5)** (2026-10-31). The review read slice p as a whole, against sections "Storage limits (p1)" to "Labels (p4)" above, and proved each defect with a test. Its interpretations, for the owner to confirm:
    - **Job failures show no quota numbers (fixed).** A failed copy job stores its quota refusal with the code and without the numbers, because `GET /jobs` shows the stored text to every owner session and to the key (`jobFailure`, `internal/api/copy_job.go`). A refusal at the request still carries its numbers, as section 8 says. Guard: `TestCopyJobQuotaFailureHasNoNumbers`.
    - **Links to a shortcut (fixed).** A link to a shortcut is refused as `invalid_request` with the message that a shortcut cannot be linked; it said that a folder cannot be linked. Guard: `TestShortcutLinkRefusalNamesAShortcut`.
    - **A copy keeps restricted values into another person's files (not changed).** A copy keeps restricted values when its caller manages the source. When the copy lands in another member's My files, that member becomes a manager of the copy and reads the values. This is the same as a manager's move into that member's files, which section 8 accepts, and a manager of the source can already share the item at manager. The owner may instead want a copy to drop restricted values wherever its landing place has managers the source does not.
    - **Not decided: user numbers in webhooks.** `quota.warning` and `quota.exceeded` for a member's My files carry that member's used bytes and limit to every account endpoint. The owner already sees those totals on the storage page, but the refusal rule (section 8, "Who learns the numbers") gives a member's numbers to the user and the key only. Open for the owner.
    - **Not decided: a token acting for a user.** The user-scope quota numbers are given to a delegated token that acts for the charged user, where the account refusal gives a token none. Open for the owner.
    - **Not decided: views before the download.** A view is recorded before the content is served, so a download that then fails still counts in Recent. Open.
37. **Document previews, exports and the conversion client, slice s3** (2026-10-31, built to the owner-approved proposal for slice s with every recommendation (a) in its section 9, question 2(a) (a conversion is a download, never a node) and question 8(a) (only the types the converter's discovery lists for conversion) among them). Decision 10 holds: no converter runs in the server, and the document goes to Collabora. **This part is not usable in production until a live run against a real Collabora.** The owner's go-ahead is needed for that run, and none was given. No migration: the `derived` table holds the new kinds.
    - **Code.** `internal/office` is the one conversion client: the fixed format list, the bounds, the capabilities copy and `ConverterKey`. `internal/derived/convert.go` is the conversion pool, and `install.go` holds the install step and the row helpers, which the thumbnail pool now also uses (the same budget rule, one copy). `internal/api/exports.go` holds the routes. `internal/wopi/discovery.go` gains `ConvertibleFrom`. `internal/wopi/wopitest/convert.go` is the fake's conversion endpoint, added to slice l's fake. Config: `derived.convert_workers` (2), `derived.convert_queue` (32), `derived.convert_account_queue` (8) and `derived.max_convert_bytes` (500 MiB), validated while office is off too. Kinds `pdf`, `png_page1` and `export_<format>` are at most 11 bytes (`export_pptx`); converter keys `office-convert-1+` and 12 hex digits are 29 bytes, under the table's limits of 32 and 64 (migration 0028), so no migration.
    - **Off.** With `office.enabled` false, the three routes answer `not_found`, and nothing is fetched (decision 31's pattern; the proposal names no code for this). The signed preview link on the content origin checks the switch too, and answers `not_found` (`TestConvertedLinkIsNotFoundWhileOfficeIsOff`).
    - **Access.** Each call runs `nodeAccess`, as the content route does: the token's folder scope, revocation, the trash, another account, and the route's refusal of share-link visitors. The kind must be a file. An export's row must belong to this node's blob and account, so the ID of another file's export is `not_found`.
    - **No-download (a reading the owner should confirm; the owner has not ruled on it).** Exports are refused with `download_restricted` for a viewer or commenter, at the request and at the download, as a copy is (decision 20). **Previews are refused too.** The proposal's section 5 allows a thumbnail for a restricted viewer, because a thumbnail is a view of one image. A preview is a converted copy of the whole document, and the brief's rule is that a viewer gets no converted copy of what they may not download. That makes this part stricter than the content route's inline rule, which allows inline viewing where the type permits.
    - **Which types.** A file is converted only when the converter's discovery lists a `convert-to` action for its extension on the discovery host. The parser already keeps only actions on that host, so a foreign action is dropped. Otherwise the answer is `unsupported_media_type`. Whether discovery lists conversion per source type is unverified (see the list below).
    - **The client.** It sends requests only to the scheme and host of `office.discovery_url`, to `/cool/convert-to/<format>`. No URL from the discovery document or a caller is followed, and the discovery action's URL is not used. The format is from the fixed list (`pdf`, `png`, `docx`, `xlsx`, `pptx`, `odt`, `ods`, `odp`), and the extension must match `^[a-z0-9]{1,8}$`. The multipart file name is `source.<ext>`, never the node's name, so no name reaches Collabora. The request carries a `Content-Length`, the source's known size, and is not chunked (`TestConvertSendsAFixedLengthBody`; a source that shrinks while read is `TestConvertFailsAShortSource`). Redirects are refused, and the target is not reached (`TestConvertDoesNotFollowRedirects`). The whole request has `derived.timeout`. At most `derived.convert_workers` conversions run at once, and a waiting caller gives up on its own context. The output is read to `derived.max_convert_bytes` plus one byte, and an answer over the limit is refused. The answer must be 200, with the format's content type, and its first bytes must be the format's signature (`%PDF-`, the PNG signature, or the ZIP container for the OOXML and ODF formats). A 4xx is a refusal, and its body is read to 4 KiB and dropped. A 502, 503 or 504, a connection failure or reset, a short body, or capabilities that say conversion is unavailable is an outage. A time-out is its own class (below). The classification is the client's (`internal/office/convert.go`).
    - **Failures and the converter's text.** Three classes (`internal/derived/convert.go`, `failConvert`). A refusal, a wrong type, an answer over the limit and an unlisted format are sticky for their key for 24 hours (`stickyFor`), then queued again (`TestRefusalIsRetriedAfterADay`). A time-out is counted: retried after one minute, then ten, and the third time it is a day's wait (`TestTimeoutCountsThreeAttemptsThenWaitsADay`). An outage is transient and is not counted: it is retried after one minute, with the attempts unchanged, for as long as requests come (`TestOutageIsRetriedAfterAMinuteAndNotCounted`). **Judgement, not seen against real Collabora:** that a 502, 503 or 504 is an outage and a 4xx is a refusal, and that the split above is the right one, is the author's reading of the failure modes. Nothing here was checked against a live Collabora. The row's error is one of the server's fixed messages (`msgRefused`, `msgBadOutput`, `msgUnavailable`, `msgConvertTimeout`, `msgConvertOutput`, `msgConvertDisk`, `msgNotConvertible`), so neither Collabora's text nor a name or a path reaches a row or a response. `TestConverterRefusalIsStickyAndItsTextIsKept` checks the row and the answer. The client's own error text is the sentinel and its kind, never the body (`TestConvertClassifiesEveryAnswer`).
    - **Source limit.** `derived.max_source_bytes` (50 MiB) is checked before any request: `too_large` with `limit_bytes`, and nothing is sent.
    - **Queue and limits.** A request that starts work reserves a place before it writes, as a thumbnail does: `derived.convert_queue` (32) in all, and `derived.convert_account_queue` (8) for one account, so one account cannot fill it (`TestConvertAccountShareLeavesRoomForAnotherAccount`; the default is `TestConvertDefaultAccountShareIsEightOfTheQueue`). `rate_limited` with `Retry-After` when full. The `convert.enqueue` limiter, per caller, is charged when a request starts work; a read of a row already made is not charged.
    - **The caller, at each step.** The pool asks the caller again before the conversion starts and again before the file is kept. Each check is the zip job's rule: the caller is live (`callerActive`), still reaches the node (`nodeAccessWith`), and is not under the no-download rule. A refusal drops the pending row and keeps nothing. A caller revoked before the start is never sent to the converter (`TestCallerRevokedBeforeTheConversionKeepsNothing`). A caller revoked while the converter works gets no file (`TestCallerRevokedDuringTheConversionKeepsNothing`). **Stated, not closed:** the second check runs in its own read, before the install write, so a revoke made in the gap between that read and the write is not seen. The zip job's publish check runs inside its write. Moving this check into the write needs the store's transaction to carry the delegation read, which it does not yet do; the owner should decide whether a window of one write is acceptable.
    - **Budget.** The install step is the thumbnails' (`Cache.Install`): one write checks `derived.max_bytes` and completes the row, eviction runs once, and a full budget drops the row. An output larger than `derived.max_bytes` fails for its key with the output-limit message before anything is evicted, so it cannot empty the other files (`TestOversizeOutputFailsStickilyAndEvictsNothing`). Eviction removes only what the budget needs (`TestEvictionRemovesOnlyWhatTheBudgetNeeds`). Config refuses `derived.max_bytes` below `derived.max_convert_bytes` (`TestDerivedRefusals`). Converted files are not counted toward any quota. **Not tested here:** a race of two conversions against the budget. The thumbnail pool's race test covers the shared step, and no conversion-specific race test was written.
    - **Repeat POST.** A repeated `POST /exports` for the same document and format, while the first is pending or after it is ready, answers the real export ID and its `Location`, not an empty ID (`TestExportRepeatPostAnswersTheSameID`). The ID is set on the ready, pending and failed answers alike (`answerFor`).
    - **Jobs.** There is no row in the jobs table. The derived row is the job: its pending state is the lock, and the worker is the conversion pool, not the jobs runner. A conversion can wait on the network for up to `derived.timeout`. The conversion pool was kept apart so that such a wait does not occupy a jobs-runner turn; the runner's loop is not changed, and its behaviour under that load was not measured. A restart clears pending rows (the derived pool's `Recover`), so an unfinished conversion is queued again by the next request, not resumed.
    - **Served files.** `nosniff`; `Content-Security-Policy: sandbox; default-src 'none'` (the content policy); `Cross-Origin-Resource-Policy: same-origin`, except on a signed preview link on the content origin, which is `cross-origin` as a signed content link is (`TestPreviewLinkIsCrossOriginWhenFollowed`); `Content-Type` from the format the server asked for, never the source's; `Content-Disposition` through `contentDisposition` (inline for a preview, attachment for an export, named with the document's name and the new extension); ETag from the SHA-256; `Range` through `applyRange` (at most 16 ranges, the content route's rule, or a 416 in the API's format) and then `http.ServeContent` (`TestExportRangeIsCappedAsTheContentRouteCapsIt`). A preview is inline on the main origin only when `content_url` is not set, as `inlinePolicy` allows an inline PDF there. When `content_url` is set, a ready preview answers `303` to a signed link with the disposition `preview_pdf` or `preview_png`, which only the server signs (`validLinkDisposition` accepts those two), and the link is served on the content origin. What the CSP does is all that is claimed for an inline PDF: it stops the document from running script in the main origin's context, if the viewer honours the policy. Whether Chrome's PDF viewer honours a sandbox policy was not checked here.
    - **The converter key and the build.** The key is `office-convert-1+` and a hash of the client's version, the build from `/hosting/capabilities` (`productVersionHash`, `productVersion`) and the limits (`derived.max_source_bytes`, `derived.max_convert_bytes`). A new build or new limits make new rows, and a row of another key is not served: the export route checks it, and the preview lookup uses the key. The row key is `<key>+<ext>`, the source's extension, so copies with other extensions keep their own rows (`TestConverterKeyCarriesTheExtension`). The capabilities answer is kept for an hour, so a build change reaches the key up to an hour after it happens (a restart sees it at once). A failed fetch is not repeated for 30 seconds (`capsRetryGap`), whatever the outcome. When the copy is due and the fetch fails, the stale copy answers (`TestStaleCapabilitiesServedWhenRefetchFails`). The fetch runs without the lock held, so other callers are not held behind the network call (`TestRefetchDoesNotHoldTheLock`). Ready rows stay downloadable while Collabora is down, and a cold start with no copy serves the newest ready row for the blob, kind and extension (`TestReadyFilesAreServedWhileCapabilitiesAreDown`). An answer without `convert-to` available is `unavailable`. `TestNewBuildMakesNewRowsAndOldOnesAreNotServed` makes the new key with a fresh client, as a restart would.
    - **Preview kinds.** `format=pdf` (the default) is kind `pdf`, and `format=png` is the first page as an image, kind `png_page1`. Exports are `export_<format>` for `docx`, `xlsx`, `pptx`, `odt`, `ods`, `odp` and `pdf`. A PNG is not an export.
    - **HEAD.** A HEAD of a preview answers from the rows and queues nothing (`TestConvertHeadStartsNoWork`). It may fetch the capabilities copy when that copy is due, which is a read and not work.
    - **Recent.** A preview is not a view and is not recorded in Recent, as a thumbnail is not.
    - **Audit and webhooks.** Neither a preview nor an export writes an audit entry or emits a webhook event. An export is a download in another format, and a thumbnail writes neither (decision 33). The zip job does audit its creation (decision 35), so an export's creation is a gap if the owner wants it audited, and the decision is left to the owner.
    - **Review of 12213cd (round 2).** The review's nine defects were checked one by one. All nine were real, and each is fixed and proved below. (1) One account could hold the whole queue, because the default share was the whole queue: now 8 of 32, a setting. (2) The converter's extension was the first node's, and the row was per blob: the key now carries the extension. (3) A converter outage counted toward sticky failure: outages are now transient and not counted; a time-out counts; sticky refusals expire after 24 hours. The split in (3) is a judgement, not seen against real Collabora. (4) A preview served through the content origin was `same-origin`: a signed preview link is now `cross-origin`. (5) The capabilities copy was not kept when a refetch failed, and the one-minute gate applied only before the first good fetch: now a failed refetch keeps the stale copy, the gate is 30 seconds, and the fetch runs without the lock. (6) Export and preview downloads bypassed the range cap: now the 16-range cap. (7) A signed preview link with office off reached the nil client: the link checks the switch and answers `not_found`. (8) A budget smaller than an output made each poll queue the same conversion, and one oversize output evicted every other file: an oversize output fails sticky without eviction, eviction removes only what is needed, and config refuses `max_bytes` below `max_convert_bytes`. (9) A repeated POST answered an empty ID and a `Location` with no ID: fixed, as above.
    - **Mutation proofs.** Each rule below was broken on purpose, one at a time, on this branch, and the file was restored byte for byte after each (`mutate` run: 22 of 22 caught, after a fix to five mutations that did not compile the first time, which are rerun and caught). `A1` the export's no-download refusal removed: `TestNoDownloadRefusesPreviewAndExport` fails. `A2` the preview's: the same test. `A3` the export download's: the same test. `A4` the blob check on an export row removed: `TestExportIDOfAnotherFileIsNotFound`. `A5` the source limit removed: `TestConversionRefusesSourcesOverTheLimit`. `A6` the discovery host filter removed: `TestConvertibleTypesAreTheDiscoveryOnes` (an xlsx action on another host is convertible). `A7` the caller check at the start removed: `TestCallerRevokedBeforeTheConversionKeepsNothing`. `A8` the check before the keep removed: `TestCallerRevokedDuringTheConversionKeepsNothing`. `A9` a refusal made transient: `TestConverterRefusalIsStickyAndItsTextIsKept`. `A10` `nosniff` removed: `TestPreviewIsMadeOnceAndServedWithItsHeaders`. `A11` a preview served as an attachment: the same test. `A12` the content-origin redirect removed: `TestPreviewOnTheContentOriginIsASignedLink`. `A13` the routes live while office is off: `TestConversionRoutesAreOffWhileOfficeIsOff`. `A14` HEAD queues work: `TestConvertHeadStartsNoWork`. `A15` a row of another converter key served: `TestNewBuildMakesNewRowsAndOldOnesAreNotServed`. `A16` redirects followed: `TestConvertDoesNotFollowRedirects`. `A17` no time limit: `TestConvertHonoursItsTimeout`. `A18` content type not checked: `TestConvertClassifiesEveryAnswer`. `A19` file signature not checked: the same test. `A20` output limit not enforced: the same test. `A21` the concurrency limit ignored: `TestConvertLimitsConcurrency`. `A22` Collabora's body put in the error: `TestConvertClassifiesEveryAnswer`.
    - **Collabora behaviour assumed and not verified.** Nothing below was checked against a live Collabora. For each, the source is given where one was found; "no source" means none was found, and the behaviour is an assumption. (1) Discovery lists `convert-to` actions per source extension. No source found. If discovery lists target formats instead, nothing converts, and every request answers `unsupported_media_type`; that fails closed. (2) The conversion endpoint is `POST /cool/convert-to/<format>` with a multipart part named `data`, and it answers the converted bytes inline. Sources: https://www.collaboraonline.com/document-conversion/ (a curl example with `-F "data=@..."`); https://github.com/LibreOffice/online/blob/master/wsd/reference.md (the older `/lool/convert-to/` path); a third-party PHP client's source file, RemoteService.php at commit 2c007c8f (`/cool/convert-to/` with the file as a multipart part). (3) The answer is 200 with the exact content type of the format (for example `application/pdf`). No source found. Another type, such as `application/octet-stream`, would make every conversion fail as a bad output. (4) A PNG conversion gives the first page only. No source found. The kind `png_page1` assumes it. (5) The converter detects the input type from the extension of the multipart file name, which is `source.<ext>` here. No source found; a third-party client sends the real file name, so the extension may not be what decides. (6) `GET /hosting/capabilities` answers JSON with `convert-to.available`, `productVersion` and `productVersionHash`. Sources: https://forum.collaboraonline.com/t/page-or-api-call-to-retrieve-version/2987 and https://forum.collaboraonline.com/t/convert-to-not-available/1132 (sample answers); https://github.com/CollaboraOnline/online/pull/11991 (the available flag is computed per requester). (7) Conversion is allowed only from the hosts and addresses that the server's configuration lists (`post_allow` in `coolwsd.xml`, and the allowed hosts). Sources: https://forum.collaboraonline.com/t/convert-to-is-not-available/1121 (a maintainer's answer, 2022); https://www.collaboraonline.com/collabora-online-3-release-notes/ (2018: "Convert-to is now allowed only for the hosts explicitly allowed in the configuration file"). A deployment needs the Selfcloud server's address listed; that is not checked. (8) Collabora's own load limit (`limit_load_secs`, default 100 seconds, per the 3.0 release notes) applies to conversions. `derived.timeout` (60 seconds) is shorter, so the converter may still be working when this server gives up. (9) Collabora answers 4xx for a refused document and 5xx for an overload. No source found. (10) Collabora does not redirect the conversion request. No source found; a redirect is refused either way. (11) Collabora sandboxes the conversion (macros, external links, embedded objects). No source found. Proposal question 8 raised this, and the owner's acceptance of recommendation (a) rests on it. (12) `productVersionHash` identifies one build, and changes with each build. The samples show one value each; no source says it changes with every build. If a build changes its conversion output and not its hash, the key does not change. (13) The fake's discovery (`internal/wopi/wopitest/wopitest.go`) lists `convert-to` for `docx` and `xlsx`, and no real discovery was seen; the fake's conversion endpoint is invented to match the client. A real discovery with no `convert-to` action makes conversion report `unavailable`, not `unsupported_media_type` (`TestConversionIsUnavailableWhenDiscoveryListsNoConversion`). (14) The 30-second gate and the one-minute retry after an outage are chosen values, not measured against Collabora's behaviour.
    - **Mutation proofs, round 2.** Each fix was broken on purpose, one at a time, and the file was restored byte for byte and checked: D1 the default share set to the whole queue (`TestConvertDefaultAccountShareIsEightOfTheQueue`); D2 the extension left out of the key (`TestConverterKeyCarriesTheExtension`, `TestExportHasNoRowOfAnotherExtension`); D3 an outage counted as an attempt (`TestOutageIsRetriedAfterAMinuteAndNotCounted`); D4 the link served `same-origin` (`TestPreviewLinkIsCrossOriginWhenFollowed`); D5a the 30-second gate removed (`TestStaleCapabilitiesServedWhenRefetchFails`, `TestCapabilitiesAreCachedAndFailuresAreNotRepeated`); D5b the stale copy not served (`TestStaleCapabilitiesServedWhenRefetchFails`); D5c the lock held across the fetch (`TestRefetchDoesNotHoldTheLock`); D6 the range cap removed (`TestExportRangeIsCappedAsTheContentRouteCapsIt`); D7 the link's office switch removed (`TestConvertedLinkIsNotFoundWhileOfficeIsOff`, which was strengthened for this: a ready preview is made first, so the mutation serves it); D8a the oversize check removed (`TestOversizeOutputFailsStickilyAndEvictsNothing`); D8b the eviction loop not stopped at the need (`TestEvictionRemovesOnlyWhatTheBudgetNeeds`); D8c the config check removed (`TestDerivedRefusals`); D9 the ID left out of the ready and pending answers (`TestExportRepeatPostAnswersTheSameID`); X1 the Content-Length removed (`TestConvertSendsAFixedLengthBody`). All fifteen were caught. The first D7 run passed, because the nil client also answers `not_found` on an empty row, so the test was made to serve a ready row first.
    - **Not in this part.** A live run. A web interface (s4). Text extraction (slice q, which will reuse `internal/office`). A conversion-specific race test against the budget. Audit or webhook entries for exports (see above). A signed link for an export; the export is served on the route only. Docker and other kernels: not run.
    - **Tests.** `internal/derived/convert_test.go` (the share, outage, refusal, time-out, oversize and eviction rules at the pool level, with a fake converter); `internal/office/refetch_test.go` (Content-Length, the short source, the stale copy, the lock); `internal/api/exports_review_test.go` (repeat POST, the link's CORP, the office-off link, the extension key, discovery with no conversion, the cold start, the range cap, the row's extension); `internal/config/derived_test.go` (the account share and the budget refusals). `internal/office/convert_test.go`: the fixed name and format, refusals before sending, the answer classes, redirects, the timeout, the concurrency limit, the wait for a slot, the capabilities cache, the URL scheme and the converter key. `internal/api/exports_test.go`: the routes off while office is off; a preview made once and served with its headers; HEAD; an export queued, resumed by `Range`, and not served under another node's ID; the format list; the discovery types; the source limit; the no-download rule; another account and a token outside its folder; sticky refusal; retried outage; the output limit; the caller revoked before and during the conversion; the new build; the PNG kind; the content origin; the key widths.
    - **Run.** Narrow runs of `internal/office`, `internal/derived`, `internal/wopi/...`, `internal/config`, the api tests for thumbnails, zips, links, routes, offices and the new conversions, `gofmt` and `go vet`. The whole `internal/api` package is run by the gate, and that run is recorded with the commit.

39. **Comments and mentions, slices r1 and r2** (2026-10-31, built to the owner-approved proposal for slice r (`design-r-proposal.md`) with every recommendation; migration 0030). Built here: the comments and mentions tables, the comment routes (list, write, edit, delete, resolve), the account's comment switch, mention resolution at write time, and the people list. Not built: notifications and the outbox (r3), the activity pane (r4), who-viewed (r5), the comment webhook event (r6) and the web interface (r7). Nothing is sent to anyone for a comment yet.

    **Interpretations and deviations, each stated where the proposal is silent, contradicts itself, or differs.**
    - *The body CHECK.* The proposal's `CHECK (length(body) BETWEEN 1 AND 2000)` contradicts its own tombstone rule (the body is cleared). The table holds `length(body) <= 2000`, `deleted_at IS NOT NULL OR length(body) >= 1`, and `deleted_at IS NULL OR body = ''`.
    - *A tombstone exists only while it has replies.* A delete removes a comment with no replies, and the tombstone goes with its last reply (`RemoveComment`). The proposal kept every deleted row. This keeps the table bounded by the live comments: a tombstone always has a live reply, and a reply has no children.
    - *Mentions are named by a reference, not by a user ID.* Proposal section 5 sent `usr_` IDs. The people list is read by the person who comments, and "no raw IDs of people in anything a person reads" applies, so the list answers with a reference (`mnt_...`). A reference is the user's ID sealed with AES-256-GCM, under a key derived with HKDF from the links signing key, with the account and the node as additional data (`mentions.go`). So a reference opens only for its own node and account, a client cannot make one, and a reference copied from another node is refused as `invalid_request`. The derived key follows the links signing key, so a rotation of that key makes an outstanding reference unreadable: the composer then asks the list again.
    - *Two references to one person are one mention.* The write opens every reference and dedupes by the user it names, not by the reference string, so the cap of 10 counts people. Before this fix a comment that named one person twice failed with a 500 (on the unique key of `mentions`). `TestTwoReferencesToOnePersonMakeOneMention` runs it before and after a revoke: 201 both times, one mention row while the person reaches the node, none after.
    - *One walk decides reach.* A mention counts when `nodeRoleIn` (the walk every read uses, with organisation links) gives the user a role of viewer or above on the node, inside the write transaction. No second rule is written. A missing user, a disabled one and a user with no role are the same case: nothing is resolved and nothing is said.
    - *Enumeration gives one answer.* A reference naming a missing user, a disabled user, a user with no access, or the author is dropped without an error, and the comment is saved. The count of distinct people (at most 10) and the shape of each reference are checked before any lookup. A reference that does not open for this node and account is refused as `invalid_request`, with the message "a person named in the comment is not valid for this item". The write's answer lists the names of the resolved mentions only. So the author learns that a named person reaches the node, which the people list already shows: this is the residual leak the proposal accepted (question 6). `TestMentionEnumerationGivesOneAnswer` compares the four cases' answers.
    - *The role check runs inside the write.* Create, edit, and resolve use `requireCommenter`, which is `requireNodeRole` with the refusal given `reason: role`. Delete checks the author or a manager of the node inside its write. The shared helper was not changed.
    - *The comment switch is on the sharing-policy route.* `PATCH /accounts/{acc}/sharing-policy` takes `comments` as well as `public_links` (either may be given alone), and `GET` answers it. Off refuses every comment route, reads and people included, and keeps the rows. It is checked before the node is looked at, and again inside each write. It is audited as `account.comments_policy`.
    - *The body rule.* A body is NFC, not blank, at most 2,000 characters, and at most 50 lines. Carriage returns (CRLF and CR) become LF, and the body is never trimmed. The shared rule `checkTextChars` (extracted from `cleanLabelText`, decision 29) refuses any control character other than LF in a multi-line body, and any bidi control, and it now also refuses U+061C (Arabic letter mark), which the bidi test had missed. Labels call it with `multiline = false`, so a label is still one line and gains the U+061C refusal. `blankText` refuses a body made only of whitespace, zero-width or invisible filler characters (including braille blank and the Hangul filler). Like labels, the rule does not refuse U+200E and U+200F (left-to-right and right-to-left marks). The body is plain text: the API stores and returns it as typed, and the OpenAPI says it is never rendered as HTML. The first version refused every line break; the review found that too strict for a comment, and the test `TestBodyRules` and the fuzz target `FuzzCleanCommentBody` were written against the rule now kept.
    - *Who acts.* A write needs a signed-in user, or a token with write that acts as its user (`changeCaller`). An account key and a read-only token are refused. Edit, delete, resolve and the people list take a signed-in user only: a token is refused (the proposal's table says "user only"), and so is an account key (the same). An edit needs the commenter role on the node as well as authorship, so an author who lost the role cannot change the text. A viewer reads the thread, and a token or key with read reads it too.
    - *Authors and names.* `author_name` is a display name, given only to a caller who may learn names (`peopleNamesSeen`). A deleted author is `author_gone: true` with no name; the interface shows the translated words "former member". The API carries no English text for it. A comment carries no user ID, and a resolver is shown the same way (`resolved_by_name`, `resolved_by_gone`). `mine` is set on the caller's own comment, never on a comment with no author.
    - *Shortcuts.* A shortcut has no comments, and every comment route answers `not_found` for it, as a link visitor sees it (decision 24). This is a kind comparison in `comments.go` (two sites, pinned in `reviewedNodeKindSites`, each a refusal after the walk has run).
    - *The people list.* At most 5 suggestions, 2 to 100 characters, matched by display name (not e-mail), active users only. The SQL reads at most 50 matching users, and the walk checks each for reach, so a name shared by more than 50 users may show fewer than five. The limiter is `people.search` (60 a minute per user, as the directory search is). The ordering is unchanged: the 10,000-user measurement below was far inside the budget, so no index change was made.
    - *Limits.* A body over 2,000 characters is `invalid_request`. 5,000 live comments per node: a write past it is `conflict` with `reason: limit`, counted with `LIMIT`, so the count reads no more than the cap. 10 people per comment: `invalid_request` with `reason: mentions`. 30 writes a minute per user, counted together: create, edit, delete and resolve share one budget, and the limiter runs before the write, so a refused call costs no write. A token is counted as its user. A thread shows its first 50 replies (`replies_more` is set); `?parent=cmt_...` pages the rest, oldest first. The 2,000-character, 50-line, 10-person and 30-a-minute numbers are proposed, not measured. The 5,000 cap was measured (below).
    - *Lifecycle (proposal section 11).* Trash: every route answers `not_found`; restore brings the comments back unchanged (`TestTrashedNodeRefusesEveryCommentRoute`). Purge: the rows go by cascade (`TestCommentLifecycleCases`). Copy: a copy has new node IDs (`transfer.go:302` and `:592`), so it carries no comments (tested). Move: the node ID does not change, so comments stay (tested). Version restore: comments stay (tested). Account deletion: the rows go by cascade on `account_id`; the audit entries stay, as the proposal says, for slice u (tested by a direct `DeleteAccount`, because the account job runs later). User deletion: the author and the resolver are cleared, the text stays (`TestDeletedAuthorShowsFormerMember`). A disabled user's comments stay and cannot be read or written by that user (tested).
    - *Audit.* Target: the node. Folder: the node's folder, so a comment on a personal folder is private to its owner, as labels are. `comment.create` carries `comment` (the ID), `reply`, and `mentions` (the count sent, not the count resolved, so the audit does not depend on reach). `comment.edit` carries `comment`. `comment.delete` carries `comment` and `tombstone`. `comment.resolve` carries `comment` and `resolved`. `account.comments_policy` carries `comments`. No body, no name. All five are in `webhookAuditNoEvent` with the reason that r6 decides the comment event.
    - *The r3 place.* `handleCreateComment` marks its write with a comment after `InsertMentions`, in the same transaction: the notifications for the resolved mentions and for a reply's parent go there (proposal 6.1 steps 8 and 9). No code for r3 is written.
    - *Parity.* Rows 126 and 127 of `feature-parity.md` (comments, and mentions) are built, with the deferred parts stated; row 182 and the r row of the build table are updated.
    - *Migration number.* Main (27efbbb) has migrations up to 0029, and no comment migration; 0030 is the next free number and is kept.

    **Plans and measurements.** `TestCommentsSQLDoesNotScan` reads the plan of each comment statement and of the people query, and fails on any scan of `comments` or `mentions` by table (as `TestTrashChainSQLDoesNotScanNodes` does). It does not fail on the `users` query: that plan is an index search on `users` by account, then a temporary sort of the account's matching users. The test logs that plan, and the timing below is the evidence for it. The top-level list, the threads, the reply window, the live count and the existence test use `comments_node`.

    The scale measurement is `TestCommentsAtScale` (skipped unless `SELFCLOUD_SCALE` is set). It was run once, by hand, on this container with TMPDIR on `/var/tmp` at `nice -n 19`, not in CI. The table held 1,000,000 comments, 5,000 of them on one node and the rest spread over 1,000 nodes, and the account held 10,000 users. Foreign keys were off for that fixture only. Each figure is the median of the runs and the worst run in brackets:

    | Operation | Median (worst) |
    |---|---|
    | Insert the 1,000,000 comments | 19.5 s (one statement) |
    | Insert the 10,000 users | 107 ms (one statement) |
    | List: top-level page of 50, on the hot node (30 runs) | 272 µs (534 µs) |
    | List: page of 50 after 2,500 rows (30 runs) | 1.84 ms (2.06 ms) |
    | Count of live comments, capped at 5,001 (30 runs) | 2.79 ms (4.87 ms) |
    | Write at the cap: count reads 5,000, refused, no insert (30 writes) | mean 2.65 ms (worst 4.36 ms) |
    | Write to a node with room: count, then insert (30 writes) | mean 4.94 ms (worst 5.93 ms) |
    | People: "Person" (90% of users match), 50 kept (20 runs) | 8.14 ms (15.2 ms) |
    | People: "Pat" (10% match), 50 kept (20 runs) | 6.44 ms (10.8 ms) |
    | People: "zzz" (no match, every user read) (20 runs) | 5.92 ms (11.1 ms) |

    The worst write measured is 5.93 ms (a write to a node with room). The people query was far inside the 50 ms the proposal set, so the ordering was left as it is. These are one run on one machine with a warm cache, and they show the statements do not grow with the table; they are not a load test. The fuzz target `FuzzCleanCommentBody` checks that an accepted body is NFC, not blank, at most 2,000 characters, at most 50 lines, and holds no control or bidi control character except LF.

    **Mutation proofs** (one change at a time, on this tree, each restored and the tree checked for residue). Each row names the mutation and the tests that failed on it:
    - in-transaction role check replaced by a read with no reach check: `TestRoleIsCheckedInsideTheWrite`, `TestViewerReadsCommenterWrites`.
    - switch not checked inside the create write: `TestCommentsSwitchOff`.
    - live cap doubled: `TestCommentLimitPerNode`.
    - a mention resolved for any existing user, reach ignored: `TestMentionEnumerationGivesOneAnswer`, `TestMentionOfAReachingPersonIsNamed`.
    - the people list returns every matching user: `TestPeopleListAnswersOnlyReachingUsers`.
    - the people list open to tokens and keys (the first run was not caught, because the read token failed on role and the write token was not tried; the test now tries a write token too): `TestPeopleListGivesNothingToNonReachers`.
    - a comment looked up without its account (the first run was not caught, because the walk after it is account-scoped too; the test now asks the store directly): `TestCommentIDOfAnotherAccountIsNotFound`.
    - comment write rate limit removed: `TestCommentWriteRateLimit`.
    - edit, delete and resolve not limited (the review found them outside the budget): `TestEditDeleteAndResolveShareTheWriteLimit`.
    - mentions per comment at eleven: `TestMentionsPerCommentAreCapped`.
    - people list at six: `TestPeopleListCapsAtFive`.
    - people list rate limit removed: `TestPeopleListIsRateLimited`.
    - edit by any commenter: `TestOnlyTheAuthorEdits`.
    - shortcut refusal removed: `TestShortcutHasNoComments`.
    - delete never leaves a tombstone: `TestDeleteKeepsRepliesAsATombstone`.
    - the author's own mention kept: `TestMentionEnumerationGivesOneAnswer`.
    - control characters allowed in the shared rule (the first run was a build error, an unused import; the mutant now keeps the import in use): `TestBodyRules`, `FuzzCleanCommentBody`.
    - tombstone kept after its last reply: `TestDeleteKeepsRepliesAsATombstone`.
    - comments index without `node_id`: `TestCommentsSQLDoesNotScan` (store), eight plans scan.
    - the list ignores the switch: `TestCommentsSwitchOff`.
    - a reference not bound to its node: `TestMentionReferencesAreNodeBound`, `TestForgedOrForeignMentionIsRefused`.
    - a reply to a reply allowed: `TestReplyRules`.
    - mention resolution given no candidates: `TestMentionOfAReachingPersonIsNamed`.
    - two references to one person counted twice (the cap by reference string): `TestTwoReferencesToOnePersonMakeOneMention`.

    **Accepted items (the review's findings, and what the owner must know).**
    - *Duplicate display names.* Two people with the same display name look the same in the people list. The reference differs, so the choice is exact, but the list cannot tell them apart. r3 and r7 must revisit this (show a disambiguator, or the e-mail to the owner only).
    - *A replayed reference.* A reference is stable for one person and node, so a reference kept from an earlier people list gives the same answer as the list does now: named while the person reaches the node, dropped after.
    - *The write's answer names resolved mentions.* The write answer lists the names of the people it resolved, which the author may already see in the people list. Question 7 of the proposal said the answer carries no names; that wording is relaxed for this case, because the author has already been shown those names.

    **Not built, and open for the owner.** Notifications and the outbox (r3), the activity pane (r4), who-viewed (r5), the comment webhook event (r6), the web interface (r7). Assignment (question 14). Whether an account key may delete a comment (it may manage the node, but the table says user only). Whether a token may edit or resolve its user's comment.
