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
`web/` (UI source in Svelte 5 with Vite; `web/embed.go` embeds the built files, since Go's `embed` cannot
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
   must fit, or the reply is `quota_exceeded`. It then reserves `length` bytes against the
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
   usage, and queue the `upload.completed` webhook. The conflict policy is `rename` (save as
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
- `audience`: `api`. `wopi` is refused until the office-editing slice.

Only an account key of the same account may mint. An installation admin, a signed-in session and a token itself get `forbidden`, so a token never widens itself. Tokens manage nothing: the management routes (users, folders, grants, groups, permissions, keys, usage, audit, webhooks, minting and revocation) refuse a token as they refuse any caller outside the account. The reply carries the token and its `expires_at`, so the back end can cache a token for most of its lifetime instead of minting one per request. Minting is rate-limited per account key.

*Format.* `sct_` followed by the unpadded base64url of a compact JSON payload, a `.`, and the unpadded base64url of HMAC-SHA256 over the payload bytes. The payload holds the format version, the signing key ID (`kid`), the account, the user (empty when none), the minting key, the folders, the permission, the audience, and the issue and expiry times in Unix milliseconds. An all-reachable token carries `"all": true` with no folders; the server refuses a payload that has `all` without a user, or with folders. A token without the field is a folder-list token, as every token minted before the field existed. A token is at most 4096 bytes; a longer one is refused before anything is decoded.

*Keys.* The MAC key is derived from the signing key of section 9 (`keys/signing.key`, with `signing.prev.key` also accepted) by HKDF-SHA256 with the label `selfcloud token v1`, so a token and a signed link never share a MAC key. `kid` is the first 8 bytes of the SHA-256 of the derived key, in base64url. A token is checked against both keys, so it survives one rotation; once the previous key file is removed, the tokens it signed fail.

*Verification.* In order: the shape and length; the MAC; the payload, parsed strictly (an unknown field is refused), whose `kid` must be the key that verified the MAC; the expiry, which is the one distinct answer, `token_expired`; then one read of the database. The account must be `active`; the minting key must exist, not be revoked and not have expired; a user must exist and be `active`; and the token must have been issued after the account's cut-off. A token that acts as a user must also have been issued after that user's own cut-off. The user's cut-off is read in the same query, so it costs no extra round trip. Every other failure is `unauthenticated` with no detail. So checking a token's signature needs no database read, but revocation needs one. A client refreshes an expired token silently, and asks the person to sign in only when a token is refused. A long upload that outlives its token keeps going: tus-js-client fetches a fresh token from the client's back end when the current one nears expiry (section 5).

*Revocation.* `POST /api/v1/accounts/{account}/tokens/revoke` moves the account's cut-off to now, so every token issued before it stops working; a token issued in the same millisecond is void too. The cut-off only moves forward, so two revocations racing each other cannot reopen tokens. `POST /api/v1/accounts/{account}/users/{user}/tokens/revoke` does the same for one user: it moves that user's own cut-off (`users.tokens_not_before`) to now, so every token acting as that user stops, while the account's other tokens, the user's sessions and the tokens of other users keep working. Revoking the minting key ends its tokens, disabling the user ends the user's tokens, and suspending the account ends all of them until it is reactivated. The account key or a person who manages the account may revoke, and the call is audited (`tokens.revoke`, or `user.tokens_revoke` naming the user). A delegated token is refused both routes, as every management route refuses it. A revocation does not end a signed link that a token made earlier: such a link is a capability that no revocation reaches, and it lives to its own expiry, which is at most the token's remaining life when the link was made, as *Signed links* above already says.

*Signed links.* A signed link is a capability that no revocation reaches, so a link made with a token may not outlive the token: its default and longest lifetime are cut to what the token has left, and a longer `ttl` is refused with that limit in `details.max_ttl_seconds`.

*Visitor credentials (share links, slice o2).* A visitor who opens a link that anyone can open holds a credential, `scl_` followed by base64url(payload JSON), a `.`, and base64url(HMAC-SHA256 of the payload bytes). The payload holds the version, the signing key ID, the account, the link, the node, and the issue and expiry times in Unix milliseconds. Its MAC key is derived from the signing key by HKDF-SHA256 under the label `selfcloud share credential v1`, so it is never a token's or a link's. It lives 15 minutes; `POST /share/renew` swaps a live one for a fresh one. A credential is not a user, a key or a token: it reaches only the routes that allow it (the table in section 8), and every other route refuses it with `forbidden`, `reason: link_route`. Its role and its link's state are read from the database on every request, in one read: a credential issued before the link's `credentials_at` (a reset or a password change), a deleted, paused, trashed or expired link, a team space that pauses it, or a suspended account is refused at once with `not_found`. A forged, malformed or expired credential is `unauthenticated`.

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
  rows. A copy counts in full even though it shares the blob. Suspending
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
`node.restore`.

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
links, and slice o2 builds links that anyone can open. Drop folders (o3) come later; the table and the
routes already carry their columns.

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
  page renews two minutes before the credential ends, and reopens with the secret (and the password) when renewal
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
- **Quota through My files (L6).** Any member can fill the account's quota through its personal folder
  until the quotas slice (p) adds per-user limits.
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
  POST|GET              /accounts/{acc}/users               verification: email | link | none; ?external_id=
  GET|PATCH|DELETE      /accounts/{acc}/users/{usr}         DELETE: ?transfer_to=usr_ moves the user's files to another user's My files; ?delete_files=true deletes them; the two are refused together
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

Files                                      any caller, limited to what it may reach
  GET    /folders                          team spaces the caller reaches at the root, plus its personal folder; ?hidden=true lists only the hidden ones
  PATCH  /folders/{fld}/settings           {members_only, sharing_managers_only, no_download_for_viewers, allow_links}: managers of the space root; refused on a personal folder
  PUT|DELETE /folders/{fld}/hidden         hide or show a team space in the caller's own list (users only; changes no access)
  GET    /shared                           nodes shared with the caller (users only), each with its role
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
  GET|PATCH /accounts/{acc}/sharing-policy {public_links} (answers {public_links, anyone_links}): the account's switch for links that anyone can open, and how many such links it has (owner or key)
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
                                           above 10,000 nodes, too_large above copy.max_nodes
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
| `quota_exceeded` | 507 | Account quota. `required_bytes`, `available_bytes`; `scope` is `quota`, or `account_reservations` for the unfinished-upload cap of an account with no quota. |
| `storage_full` | 507 | The server's disk is full. `scope` is `server`, or `account` when the account holds its share of the disk in unfinished uploads. |

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

**Office editing (WOPI).** The supported editor is **Collabora Online**, which runs as its own
server (for example the `collabora/code` image) next to Selfcloud. Selfcloud is the WOPI host;
Collabora is the WOPI client and serves the editor. Slice l is built and tested against
Collabora. Other WOPI clients may work but are not a goal. Selfcloud implements `CheckFileInfo`,
`GetFile`, `PutFile`, `Lock`, `Unlock`, `RefreshLock`, `GetLock` and `RenameFile`. The editor's
discovery URL (`office.discovery_url`, Collabora's `/hosting/discovery`) comes from config; the
discovery document tells which file types open for viewing or editing. Requests are checked
against the editor's proof keys. Locks expire after 30 minutes, as WOPI specifies. `PutFile` is
copy-on-write and honours the lock. The access token is a delegated token with audience `wopi`,
bound to one file. A client calls `POST /nodes/{nod}/office` and embeds the editor URL it gets
back. Collabora must be allowed to call Selfcloud's WOPI address (its `aliasgroup` setting), and
the README gives a Docker Compose example of the two together.

**Web interface.** Served at `/` when `ui.enabled` (on by default). Screens: sign-in, verification
and password set-up, password reset, TOTP set-up, the file browser (upload with progress and resume,
download, preview of images, PDF, video and text, new folder, rename, move, copy, delete, text
editing, opening in the office editor), and owner screens for users, folders and grants. Light, dark
and follow-the-system themes. It works at phone width. The stack is TypeScript, Svelte 5 and Vite
(`web/`). Node is needed only to build it. The web interface is built in four parts: **k1**
(foundation and the auth pages), **k2** (file browser), **k3** (sharing) and **k4** (admin pages).

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

- **Share dialog** (`web/src/components/share/ShareDialog.svelte`). Opened from the selection bar
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
CI runs `npm ci`, `npm run check` (svelte-check with no errors or warnings, a source check and the
unit tests) and `npm run build` (which checks the built files) before the Go steps, so the Go tests
run against the real build. The Dockerfile has a Node stage whose `dist/` is copied into the Go stage.
`make e2e` runs the browser smoke test (section 10, *Smoke test*).

*Serving.* `internal/web` answers every GET and HEAD outside `/api/`, `/wopi/`, `/brand/`, `/metrics`,
`/healthz` and `/readyz`. A path with no file gets `index.html`, so the client-side router can answer
it. An unmatched API path still gets the JSON 404: the web interface never answers under `/api/`.
A name that starts with a dot is never served. Hashed files under `assets/` get
`Cache-Control: public, max-age=31536000, immutable`; `index.html` and the fallback get
`Cache-Control: no-store`. Every UI response carries a strict policy:
`default-src 'self'; script-src 'self'; style-src 'self'; img-src 'self' data: blob:; font-src 'self';
connect-src 'self'; frame-src 'self'; frame-ancestors 'none'; base-uri 'none'; form-action 'self'`, with
the origin of `content_url` added to `img-src` and `frame-src` when it is set. Also `nosniff`,
`Referrer-Policy: same-origin` and `X-Frame-Options: DENY`. These headers are set only on UI responses;
content, link and API responses keep their own policies (section 9). The build emits no inline script
or style: CSS is in files, and the module preload polyfill, an inline script, is off. Fonts are
self-hosted (`@fontsource-variable`, OFL licence texts in `web/public/licenses`), so no request leaves
the origin.

*Branding.* Installation-wide only: `ui.brand.name` (default `Selfcloud`), `ui.brand.accent` (a colour as
`#rrggbb`, default `#2563EB`) and `ui.brand.logo_file` (a PNG or SVG, read at start, at most 256 KiB).
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

## 11. Data model

| Table | Main fields |
|---|---|
| `accounts` | id, external_id, name, status, quota_bytes, used_bytes, used_files, reserved_bytes, tokens_not_before, public_links (0 or 1; off by default) |
| `api_keys` | id, account_id (null for admin keys), name, prefix, hash, expires_at, revoked_at, last_used_at |
| `users` | id, account_id, external_id, email, display_name, locale, role, status, password_hash, totp_secret (encrypted), verified_at, tokens_not_before |
| `verifications` | id, user_id, purpose (verify, reset), token_hash, expires_at, used_at |
| `sessions` | id_hash, user_id, created_at, last_seen_at, expires_at, ip, user_agent |
| `folders` | id, account_id, external_id, name, name_key, kind (`team` or `personal`), owner_user_id (personal only; at most one personal folder per user), members_only, sharing_managers_only, no_download_for_viewers, allow_links (0 or 1; team spaces only, personal folders keep them 0; read by the share-link rules) |
| `groups` | id, account_id, external_id, name, name_key, created_at, updated_at |
| `group_members` | group_id, user_id, created_at |
| `permissions` | node_id, account_id, subject_kind (`user` or `group`), subject_id, role (viewer, commenter, contributor, editor, manager), created_at, created_by, expires_at (null: no expiry) |
| `access_requests` | id (`arq_`), account_id, node_id, user_id, role (viewer, commenter or editor), message (at most 500 characters), status (pending, approved, denied), created_at, decided_at, decided_by; at most one pending row per node and user |
| `share_links` | id (`shl_`), account_id, node_id (cascade), audience (`organisation` or `anyone`), role (viewer, commenter, contributor or editor; null for a drop link), upload_only (0 or 1), secret_hash (SHA-256, unique), password_hash (argon2id; anyone links only, null otherwise), expires_at (null: no end), credentials_at (the secret's issue time), created_by and created_kind, created_at, updated_by, updated_kind, updated_at; unique (node_id, upload_only) |
| `share_link_visits` | link_id (cascade), user_id (cascade), visited_at; primary key (link_id, user_id). A member's recorded open of a link; it counts only from `credentials_at` on |
| `hidden_folders` | user_id, folder_id, created_at; primary key (user_id, folder_id). A user's own list of hidden team spaces, which changes no access |
| `nodes` | id, account_id, folder_id, parent_id, name, name_key (for uniqueness), kind, size, mime_type, blob_id, etag, created/modified at and by, owner_id, no_download, editors_can_share (0 or 1, each inherited from ancestors), trashed_at and trashed_by (set on a trash root only), trashed_name_key (the name key before the trash) |
| `versions` | id (`ver_`), account_id, node_id, blob_id, size, sha256, etag, mime_type, created_at and created_by (when that content was first written), replaced_at and replaced_by, label (at most 100 characters), keep_forever (0 or 1); indexed by (node_id, created_at) |
| `blobs` | id, account_id, size, sha256, state (`uploading`, `live`, `orphan`), delete_after, created_at |
| `uploads` | id, account_id, target (folder_id, parent_id or dir_path, parents, name, name_key, on_conflict, mime_type), length, durable_offset, hash_state, blob_id, created_by, created_kind, expires_at |
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
  depth, blob garbage-collection backlog, and installation totals `selfcloud_storage_used_bytes`
  and `selfcloud_storage_reserved_bytes`. Per-account figures come from the usage API
  (`GET /accounts/{acc}/usage`: `quota_bytes` and `available_bytes` are `null` when the quota
  is unlimited), not from metric labels.
- **Usage counters**: the sweep compares the reserved counters with the uploads and logs a
  warning when they disagree; it never repairs them. `selfcloud admin recount-usage
  [-account ID]` rebuilds used and reserved bytes from the rows, one account per transaction,
  and records the before and after figures in the audit log.
- **Access requests**: the sweep removes decided access requests older than 30 days. A pending request is
  kept however old it is, since someone still has to decide it.
- **Expired permissions**: the sweep removes each permission whose `expires_at` passed more than 24
  hours ago, audits each removal as `permission.expired`, and leaves the 24 hours as a grace period so
  the audit trail shows when access ended. Reads already ignore an expired permission, so the sweep
  only tidies rows and never changes what a user may reach.
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

| Slice | Contents |
|---|---|
| a | Server skeleton, config, logging, `/healthz`, `/readyz`, `/metrics`, graceful shutdown, Dockerfile, CI |
| b | Accounts, admin and account keys, `admin` bootstrap command, JSON errors, audit log |
| c | Users, verification (`email`, `link`, `none`), SMTP and templates in the seven languages, passwords, sessions, rate limits |
| d | Folders and grants |
| e | File operations: list, stat, mkdir, rename, move, copy, delete, small-file write with `If-Match` |
| f | Resumable uploads (tus), with crash and resume tests and the upload benchmark |
| g | Ranged downloads and signed links, with the download benchmark |
| h | Quotas and usage, enforced at upload creation and while writing |
| i | Delegated tokens and CORS |
| n1 | Sharing model: roles on any node with inheritance, groups, personal "My files", team spaces and their owners, the grants routes on top of permissions, `GET /shared`, delegated-token node scope |
| n2 | Sharing v2: access expiry, no-download, the "editors can share" switch, ownership transfer, team-space restrictions and hiding, access requests, share emails |
| m | Trash, version history, keep forever, restore, undo |
| k | Web interface, in the seven languages, with right-to-left layout for Arabic and Hebrew |
| o | Organisation and public links (off by default), link policy, drop folders. In four parts: o1 organisation links and the policy (built), o2 links that anyone can open, o3 drop folders, o4 the sweep and bulk delete |
| p | Starred, recent, Home, shortcuts, colours, descriptions, labels, per-user quota, storage page |
| q | Search: filters, label search, full text and OCR within size limits |
| r | Comments, mentions, activity pane, notifications, who-viewed |
| s | Thumbnails, document previews, conversions and export, zip download |
| l | WOPI host for Collabora |
| j | Webhooks |
| t | Changes feed, file picker, WebDAV, installable web app |
| u | Admin: audit viewer and export, sharing policies, retention and hold, ClamAV, limits |
| t2 | Approvals |

Slices are listed in build order; the letters keep their first meaning. Slices m to u come from
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
7. **PostgreSQL**: added after slice i.
8. **Web interface stack**: Svelte.
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
    text; installation branding only (`ui.brand`), per-tenant branding later.
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

    Interpretations made in o1, for review (the owner has not decided them):
    - A visit made before `credentials_at` does not count, so a reset ends the access of the old copies until
      each member opens the new URL. A reset keeps the recorded visits.
    - While policy pauses a link, its changes and resets are refused and only deletion is allowed, so a
      setting that is off cannot be used to change a link.
    - `GET /nodes/{nod}/share-links` leaves out expired links. The sweep that removes them is in o4.
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
      switch shows it. Not built in o2: the owner's "Delete all public links" control on the policy page; the
      account-wide list and bulk delete come in o4.
    - The per-address limits and lockouts of `/share/open` count an IPv6 client by its /64. The other limits still
      count one address each.

