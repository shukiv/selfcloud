<p align="center">
  <img src="docs/brand/selfcloud-logo.svg" alt="SelfCloud" width="420">
</p>

# SelfCloud

A self-hosted drive, written in Go. It has its own accounts, users, sign-in and storage, and it
depends on no other system. Any application can manage it and move files through its HTTP API.
It runs as a headless API server, or with its own web interface, on a host or in Docker. Files
can be up to 100 GB, uploaded in resumable pieces.

**Status: early development.** The design is in [docs/design.md](docs/design.md) and is being
built in slices. The API document at [api/openapi.yaml](api/openapi.yaml) lists only what works
today.

## Build and run

Go 1.26 or newer, and Node 22 for the web interface. The web interface is TypeScript, React 18.3 with
Ant Design 5.29, built with Vite and React Router 7 (`web/`):

```sh
make build            # builds the web interface (web/dist), then the binary that embeds it
./bin/selfcloud serve
```

Open `http://localhost:8080/` for the web interface, or `curl localhost:8080/api/v1/info` for the API.
A checkout with no Node can still build the Go binary: the web interface then shows a page saying it is
not built, because the committed placeholder is what gets embedded.

Developing the web interface:

```sh
make web              # build web/dist only
cd web && npm run dev # Vite on :5173, forwarding /api and /brand to a server on :8080
```

Start the server (`./bin/selfcloud serve`) first. Set `SELFCLOUD_API` to point the dev server
somewhere else. `npm run check` runs the TypeScript compiler, the source checks, the licence and key
checks and the unit tests; `make e2e` builds the binary and runs the browser smoke test. It needs
Chrome or Chromium, found on `PATH` or named by `SELFCLOUD_E2E_BROWSER`.

Or with Docker:

```sh
docker build -t selfcloud .
docker run -p 8080:8080 -v selfcloud-data:/data selfcloud
```

Then `curl localhost:8080/api/v1/info`.

## First steps

An installation admin key manages accounts. Make one on the server (it is printed once):

```sh
./bin/selfcloud admin create-key -name ops
# in Docker: docker exec <container> selfcloud admin create-key -name ops
```

For automated deployments, write a key from `selfcloud admin generate-key` into a secret file
and point `admin_key_file` at it; the server adds it on start.

With the admin key, create an account and an account key for the application that will use it:

```sh
curl -H "Authorization: Bearer $ADMIN_KEY" -H 'Content-Type: application/json' \
  -d '{"name":"Example","external_id":"customer-42","quota_bytes":53687091200}' \
  localhost:8080/api/v1/accounts
curl -H "Authorization: Bearer $ADMIN_KEY" -H 'Content-Type: application/json' \
  -d '{"name":"backend"}' localhost:8080/api/v1/accounts/$ACCOUNT_ID/keys
```

`selfcloud admin create-account -name Example` does both in one step.

### Recovering an admin who forgot the password

The `admin` commands work on the data folder, so they need no key, no mail and no running browser
session. With the server running or stopped, an admin who forgot the password can get a new one:

```sh
selfcloud admin users -admins                        # find the admin's ID
selfcloud admin reset-password -user usr_... -link-file reset-link.txt
```

Open the link in a browser and choose a new password. The link works once and expires after
`users.reset_ttl`. The file is created new with mode 0600, and the command refuses to overwrite it.
On a server console with no browser, the temporary method prints a generated password once, and the
admin must change it at the next sign-in:

```sh
selfcloud admin reset-password -user admin@example.com -method temporary -yes
```

`-ttl` sets how long a link works instead of `users.reset_ttl`, from 5m to 168h (link method only).
`-enable` makes a disabled user active again, and only for a user who verified once; a user who
never verified needs `admin verify -enable` first, which sets it back to pending.

The other recovery commands are `selfcloud admin verify -user ID|EMAIL` (a fresh verification link
for a pending user, with the same `-ttl`), `enable`, `disable` and `end-sessions`. `disable` refuses
the last active installation admin and the last active owner of an account, with no override flag.
`disable` and `end-sessions` ask for `-yes` when stdin is not a terminal.

Exit codes: 0 when done; 1 for a refused or failed action, such as a pending or disabled user without
its flag, or a last admin or owner; 2 for bad usage, a missing `-yes`, an email that several users
have (the command lists their IDs), or a password that fails the rules. `selfcloud admin help` lists
the flags. In Docker, prefix each command with `docker exec <container>`.

## Checking the data directory

`selfcloud fsck` compares the files under the data directory with the database. It changes no blob, row or derived file, except that `--repair` renames orphan blobs into quarantine; it can run
beside a running server. It renews its read transaction every 500 judgments and reads the row lists a page at a time,
so no single read holds back the server's checkpoints for the whole tree (decision 100). The longest hold measured on
100,000 empty files on a warm cache was 0.26 s. A cold disk or large directories can make a batch longer, and the
integrity check is one read that grows with the database (decision 100). It reports blob files that no row names, rows whose file is missing or the wrong size,
and SQLite's integrity check. Exit status 0 means clean, 1 means findings, 2 means it could not run.

```sh
./bin/selfcloud fsck               # text summary
./bin/selfcloud fsck --json        # one JSON document
./bin/selfcloud fsck --repair      # moves orphan blobs to quarantine/<UTC date>; deletes nothing
# in Docker: docker exec <container> selfcloud fsck
```

`--verify-content` also hashes every file, which reads all the contents and is slow. `--min-age 1h` (the
default) leaves alone files written less than that long ago. How to put a quarantined blob back is in
decision 100 of `docs/design.md`. Files in quarantine are outside the server's deletion guarantees: they stay until an
operator empties the folder. On a network file system, run the check where the clocks agree with the file server, since
a clock difference larger than `--min-age` can make a fresh blob look old (decision 100).

## Configuration

Defaults, then an optional YAML file (`-config FILE` or `$SELFCLOUD_CONFIG`), then environment
variables. Each setting's variable is `SELFCLOUD_` plus its YAML path in upper case:
`upload.max_chunk_size` is `SELFCLOUD_UPLOAD_MAX_CHUNK_SIZE`. See
[internal/config/config.go](internal/config/config.go) for every setting.

| Setting | Default | |
|---|---|---|
| `listen` | `:8080` | Address to listen on. |
| `public_url` | `http://localhost:8080` | Address clients reach the server at. |
| `data_dir` | `data` | Database and file contents. Use a local disk, not a network share. |
| `trusted_proxies` | none | Proxies whose `client_ip_header` is believed. |
| `webhooks.allow_networks` | none | Networks a webhook may reach although the deny table refuses them (for example a Docker network a same-host application runs on). Webhooks refuse loopback, private, link-local and other reserved addresses otherwise. |
| `webhooks.allow_http` | `false` | Admit `http://` webhook URLs. For development only. |
| `client_ip_header` | `X-Forwarded-For` | Or a single-address header such as `CF-Connecting-IP`. |
| `upload.max_file_size` | `100GiB` | Largest file. |
| `upload.max_chunk_size` | `64MiB` | Largest upload request. Keep it at or under `90MiB` behind a proxy that caps requests at 100 MB. |
| `upload.max_lifetime` | `168h` | Longest life of an upload from its creation, however often it is written to. |
| `upload.max_reserved_per_account` | `1TiB` | Bytes an account with no quota may hold in unfinished uploads. |
| `copy.max_nodes` | `50000` | Most nodes one folder copy may make; a larger copy is refused with `too_large`. A copy above 10,000 nodes runs as a background job, in phases: each batch is one write, and the publish is one write that grows with the copy (design section 8). |
| `derived.max_bytes` | `10GiB` | Budget for the thumbnails of all accounts together; the least recently used go first. |
| `zip.max_entries` | `100000` | Most nodes one zip may read and hold, across all its roots; nodes left out for access count too. A larger walk is refused with `too_large`. |
| `zip.max_bytes` | `10GiB` | Most bytes of files one zip may hold, counted on the tree as it was read. |
| `zip.max_disk` | `30GiB` | Disk for all stored zips together, ready and unfinished; a zip that would pass it is refused before it writes. |
| `zip.max_disk_per_account` | `10GiB` | One account's share of `zip.max_disk`; refused with `storage_full` when its zips would pass it. |
| `zip.ttl` | `24h` | How long a finished zip is kept after it is made. |
| `imports.enabled` | `true` | Turns background imports on. Off, no import can be started and the source list is empty. |
| `imports.max_concurrent_jobs` | `2` | Import jobs that run at once across the installation, each in a worker of its own (1 to 32). |
| `imports.job_retention_days` | `30` | How long a finished import's record (its state and item rows) is kept. The files it imported stay. |
| `imports.max_unfinished_per_user` | `3` | Most imports one user may have queued, running or paused at once (1 to 100). A paused import holds its archive. |
| `imports.paused_expiry_days` | `14` | How long an import may stay paused. Then it is cancelled, and what it staged and its archive are removed. |
| `imports.max_archive_uploads_per_user` | `3` | Most unclaimed import archives one user may hold at once (1 to 100). Each one holds its declared size against the quota. |
| `imports.unclaimed_expiry_hours` | `24` | How long an uploaded archive may wait unclaimed before it expires and its reservation is released. At least 1. |
| `imports.archive.enabled` | `true` | Turns the archive source (an uploaded zip or tar) on. An account's owner may turn it off for one account. |
| `imports.archive.max_entries` | `50000` | Most entries (files and folders) one archive may hold. Refused from the headers before anything is written. |
| `imports.archive.max_depth` | `32` | Deepest folder nesting an archive entry may have. |
| `imports.archive.max_ratio` | `100` | Most a file may expand: uncompressed over compressed size, per zip entry, and over the whole stream for a tar.gz. |
| `imports.archive.max_total_bytes` | `100GiB` | Most uncompressed bytes all the files of one archive may hold. Each file is also held to `upload.max_file_size`. |
| `imports.archive.max_path_bytes` | `64MiB` | Most bytes the full paths of one archive's entries may hold, summed over the entries. It bounds the memory a scan keeps for the names that clash. |
| `derived.convert_workers` | `2` | Document conversions (previews and exports) that run through Collabora at once. Needs `office.enabled`. |
| `derived.convert_queue` | `32` | Document conversions that may wait, over all accounts. A request past it is refused with a retry-later. |
| `derived.convert_account_queue` | `8` | Places of the conversion queue one account may hold, so one account cannot fill it. At most `derived.convert_queue`. |
| `derived.max_convert_bytes` | `500MiB` | The largest file one conversion may return. A larger answer fails the conversion. |
| `zip.max_per_principal` | `2` | Unfinished zips one caller may have at once. |
| `zip.max_per_account` | `4` | Unfinished zips an account may have at once. |
| `zip.max_unfinished` | `8` | Unfinished zips the installation may have at once; each holds its file and its plan in memory. |
| `search.enabled` | `true` | Turns the search route on. Off, `/api/v1/search` answers `not_found`; the index is still kept, so turning it on again needs no backfill. |
| `search.max_query_bytes` | `200` | Longest `q`, in bytes (1 to 1000). |
| `search.max_terms` | `8` | Most terms one `q` may have (1 to 16). A term is at least 2 characters and at most 64 bytes. |
| `search.timeout` | `2s` | Longest one search may take; past it the answer is `unavailable`. A file the caller cannot read never changes the answer, but it does take time: a very large number of them can make a search answer `unavailable` where none would be (decision 46, timing residue). |
| `search.rate_per_minute` | `60` | Searches one caller may start a minute. |
| `write.one_request` | `300` (fixed) | One-request writes (a file sent in one request, replaced or new, and a WOPI save) one caller may start a minute, large or small. Not a setting: a caller who writes more than 300 files a minute, as a bulk script may, is refused with `429` until the minute passes (sec1, decision 84). |
| `write.one_request_large` | `60` (fixed) | The one-request writes among those above that are large (over 1 MiB) or have no declared length. A large write takes this limit as well as `write.one_request`; both are per caller. Fixed, as upload.create's limit is (sec1, decision 84). |
| `search.fulltext_max_bytes` | `50MiB` | Largest file whose text is read, plain or converted. Larger files get no content search. |
| `search.text_max_bytes` | `1MiB` | Text kept for each file; a longer text is cut at a character boundary and marked truncated. At least 1 KiB. |
| `search.office_text` | `false` | **Off by default.** Whether office documents and PDF files are sent to Collabora to get their text. Word-processing documents (`.docx`, `.odt`, `.doc`) give their text. Spreadsheets, presentations and PDF files are sent too, but Collabora refuses to turn them into text, so they have no content search, and each costs one refused conversion (decision 46). Off, no document is sent to the converter and document files have no content search (names still search). Turning `office.enabled` on for previews does not turn this on. |
| `search.max_text_per_account` | `64MiB` | Extracted text one account may hold, in bytes of text (about 3 times that on disk for high-entropy words, decision 46). A file over the cap is skipped with its reason; the owner sees the count on the usage page. `0` means no cap. Must be `0` or at least `search.text_max_bytes`. Not charged to quota. |
| `search.max_text_total` | `1GiB` | Extracted text the whole installation may hold. `0` means no cap. Must be `0` or at least `search.max_text_per_account`. |

Content search keeps the words of each file in the database. Deleting the text overwrites the freed pages of the database file (`PRAGMA secure_delete`, always on), and the full-text tables use FTS5's `secure-delete` option, also always on. The write-ahead log holds deleted words until a checkpoint: the sweep runs one every hour (`wal_checkpoint(TRUNCATE)`), so deleted words leave the database and its log within about an hour of the deletion. A checkpoint that a reader blocks is retried the next hour. The cost of FTS5's secure-delete is in decision 46. A `VACUUM INTO` copy carries only the text still held. Not closed, and outside SelfCloud: backups and file-system snapshots made before a deletion keep the words, as they keep the file; the file system may keep the bytes of an unlinked blob until its blocks are reused (a blob file is removed by the sweep, not overwritten); SSD wear levelling and the operating system's page cache.
| `office.enabled` | `false` | Turns on the office editor (the page in the interface and the WOPI host for Collabora). Needs `office.discovery_url`. Off, every `/wopi` route and the editor answer not found. |
| `office.discovery_url` | (none) | Collabora's WOPI discovery address, https, ending in `/hosting/discovery`. SelfCloud fetches it, and only it, for office editing. Its host is where the browser loads the editor from, so the browser must reach it. |
| `office.wopi_url` | (none) | The address Collabora uses to call SelfCloud, with no path, when it differs from `public_url` (a proxy in front, for example). SelfCloud checks Collabora's signatures on the URL built from it, so it must be the address Collabora really calls. |
| `office.lock_minutes` | `30` | How long an editor's lock lasts without a refresh, at most 30 (the WOPI limit). A crashed editor frees its file after this. |
| `office.token_minutes` | `600` | How long an editor's access token lives, at most a day. It never outlives the token that opened the editor. |
| `office.max_file_bytes` | `50MiB` | The largest office file that may be opened in the editor, and the largest one a save may write. |
| `upload.max_disk_share_percent` | `50` | Most of the usable disk space one account's unfinished uploads may hold. Set `100` for a single tenant. |
| `tokens.max_ttl` | `1h` | Longest lifetime of a delegated token. A longer `ttl` is refused. |
| `admin.tenant_access` | `false` | **Off by default.** When on, an installation admin may get into a tenant user's account three ways: a one-time reset link, a password the admin types (the user must change it at the next sign-in), and sign-in as the user for one hour. Turning it on means the people who run this installation can read and act as any user of any tenant. Each of these is recorded in the tenant's audit log under the admin's name. Off, the admin cannot use these three ways. Off does not stop an admin reaching a tenant's files, since an admin can create an account key, and that is audited too. The email reset stays available either way. |
| `sharing.max_expiry` | `8760h` | Longest a permission may run from the moment it is set (its `expires_at`). `0` means no cap. |
| `trash.retention` | `720h` | How long a deleted item waits in the trash before the sweep purges it. Must be positive. |
| `audit.retention_days` | `365` | How many days an audit entry is kept, installation-wide entries included. The sweep deletes older entries for good, in batches of 1,000 writes, at most 200,000 an hour. Pruning also runs once at each start. `0` keeps every entry for ever. `1` is accepted and deletes everything older than a day. Must not be negative. A change needs a restart. Pruning trusts the system clock: a clock set forward deletes the entries that are then past the period. Retention is a deletion: an operator with legal retention duties must set the period the law requires. The name of a deleted account stays in its installation-level record for the period (for ever with `0`), and it may be personal data, so an operator erasing a tenant for privacy should know that. A request in flight while its account is being deleted can answer 500 rather than leave an orphan entry. |
| `versions.max_count` | `100` | Most versions a file keeps that are not kept forever; the oldest go first. `0` means no count limit. |
| `versions.max_age` | `720h` | How long a version that is not kept forever is kept after its content was replaced. `0` means no age limit. |
| `sessions.device_cookie_lifetime` | `4320h` (180 days) | How long the device cookie lasts. A successful sign-in or password reset sets it, and a sign-in from a device that carries it is not refused by the email's cap on failed sign-ins (`login.failed`: 30 an hour, all addresses together). The cookie is HMAC-signed, `__Host-` and Secure over https, HttpOnly, SameSite=Lax. A device whose cookie has had 10 failed sign-ins in an hour stops being trusted for it. Must be positive. |
| `users.device_confirm_ttl` | `15m` | How long the link that confirms a new browser works. The link is mailed when the email's cap on failed sign-ins is spent, the browser holds no device cookie, and the password is right (sec2, decision 86). At most `30m`. Must be positive. |
| `users.device_confirm_url_template` | `{public_url}/confirm-device#token={token}` | The address of that link, with `{public_url}` and `{token}` filled in. The token must sit in the fragment, after `#`. |
| `rate_limit.capped_per_email_per_hour` | `10` | How many password checks one email gets an hour from one client address, while its cap on failed sign-ins is spent (sec2, decision 86, round 2 M1, per address since round 3). Past it no password is checked and no confirmation mail is sent; the answer is the same. Must be at least 1. |
| `rate_limit.capped_email_per_hour` | `100` | The ceiling on those password checks for one email from all addresses together, an hour (sec2, decision 86, round 4). An attacker with many addresses can spend it and block the real user's confirmation mail for the hour; the device cookie still works. Past it the same answer, with no check and no mail. Must be at least 1. |
| `download.timeout` | `30s` | The base time a download may take to be read. Before each 8 MiB segment of a download the write deadline is moved to now plus this plus the segment's bytes divided by `download.min_rate` (sec1, decision 84). There is no cap on a download's total time; a dropped download resumes with `Range`. Must be positive. |
| `download.min_rate` | `64KiB` | The slowest rate, in bytes a second, at which a reader is expected to take a download. A reader that stops is cut off within about `download.timeout` plus one segment's size over this rate. Must be positive. |
| `sessions.admin_idle_timeout` | `12h` | How long an installation admin's session may go unused before it ends. The shorter of this and the user's idle timeout applies. Must be positive (sec1, decision 84). |
| `sessions.admin_max_age` | `168h` (7 days) | The most an installation admin's session lasts from sign-in, however busy. The shorter of this and `sessions.max_age` applies. Must be positive (sec1, decision 84). |
| `versions.max_kept` | `200` | Most versions of one file that may be kept forever. Marking another is refused with a conflict. Must be positive. |
| `sessions.short_idle_timeout` | unset (the shorter of `12h` and `sessions.idle_timeout`) | How long a session the browser did not keep ("Keep me signed in" unticked on the sign-in page, or `remember: false` in the API) may go unused before the server ends it. Unset, it is the shorter of 12 hours and `sessions.idle_timeout`, so an installation with a short idle timeout starts without it. An explicit value must be more than `0` and not more than `sessions.idle_timeout`; a negative value is refused. The session's cookie has no Max-Age, so a browser that closes drops it, but a browser that restores its last session may keep it, and the server then ends the session after this long unused. An unticked session used at least every 12 hours still ends at `sessions.max_age` (30 days by default) at the latest. A kept session lasts `sessions.idle_timeout` unused and `sessions.max_age` in all. |
| `cors.allowed_origins` | none | Origins whose pages may call the API, as exact origins such as `https://app.example.com`. Empty turns CORS off. |
| `metrics_listen` | none | Serve `/metrics` on a separate address. |
| `metrics_on_main` | `false` | Serve `/metrics` on the main listener too. Off by default: `/metrics` shows storage totals, route counts and the webhook queue to anyone who can reach the listener (sec1, decision 84). Ignored when `metrics_listen` is set, which serves it on its own address. |
| `admin_key_file` | none | File holding an admin key to add on start. |
| `ui.enabled` | `true` | Serve the web interface at `/`. With `false`, the API alone runs; `/` is the JSON 404. |
| `ui.brand.name` | `SelfCloud` | The installation's name, in the web interface, the page titles, the installed app and `brand.name` in `/api/v1/info`. Up to 64 characters. An installation with only a logo still shows this name as text and in the titles, so set it to replace the default. |
| `ui.brand.accent` | `#053D8E` | Primary colour, as `#rrggbb`. The web interface derives its accent family from it. |
| `ui.brand.palette.light` / `.dark` | none | Colour tokens of each theme (`accent`, `accent_fill`, `accent_fill_hover`, `accent_fill_active`, `accent_text`, `accent_soft`, `accent_border`), each `#rrggbb`. Unset tokens keep the default. |
| `ui.brand.sidebar` | none | Sidebar colours (`bg`, `text`, `caption`, `active`, `on_active`), each `#rrggbb`. Unset ones keep the default. |
| `ui.brand.logo_file` | none | A PNG or SVG logo, at most 256 KiB, read at start and served at `/brand/logo`. Installation-wide only. |
| `log.level`, `log.format` | `info`, `json` | |

## Office editing and conversion (Collabora Online)

`office.enabled` turns on the office editor and the document previews and exports. Both use a Collabora Online server, which
SelfCloud reaches at `office.discovery_url` (an https address ending in `/hosting/discovery`). Settings the operator must
make, found by running against Collabora Online 26.04.5.2:

- **Two directions.** The browser must reach Collabora (it loads the editor's page from the discovery address), and
  Collabora must reach SelfCloud (at `office.wopi_url`, or `public_url`). The checks of these two directions were run
  against a fake Collabora only, not a live one, so how a live Collabora reports either failure is not known here.
- **The page's security headers.** SelfCloud's content security policy allows the discovery address's origin, and nothing
  else, in `frame-src` and `form-action`, so the editor's frame and its form post are allowed. Nothing else of Collabora's
  is allowed; a Collabora on another host than the discovery address cannot open a file, since each editor address must be
  on the discovery host.
- **A WOPI proof key.** Collabora must have one at `/etc/coolwsd/proof_key`. Collabora starts without it and logs
  `Could not open proof RSA key`. Without it the discovery document has no `proof-key`, and SelfCloud refuses it, so
  every office route answers `unavailable`. Create it with Collabora's own tool (`coolconfig generate-proof-key`) or
  with `openssl genrsa`, and mount it.
- **`server_name` must match the discovery address.** Set Collabora's `server_name` to the host and port of
  `office.discovery_url` (for example `office.example.com:9980`). Collabora writes that host into every editor and
  discovery address. With a bare host, the discovery's addresses lack the port, SelfCloud keeps none of them, and every
  preview and export answers `unsupported_media_type`.
- **Trust.** Collabora's certificate must be trusted by SelfCloud, as `office.discovery_url` must be https. In a
  container, mount the CA and set `SSL_CERT_FILE` to it.
- **Allowed hosts.** Conversion takes requests only from the addresses in Collabora's `net.post_allow` list. The shipped
  list covers `10.0.0.0/8`, `172.16.0.0/12`, `192.168.0.0/16` and loopback, so a Docker or LAN address is allowed. Any other
  address needs an entry.
- **Time.** Collabora's `limit_convert_secs` (100 s by default) and SelfCloud's `derived.timeout` (60 s) both limit a
  conversion. On the test host (arm64), a 167-page document took about 5 s; a 834-page document (40,000 paragraphs) took
  about 56 s and passed the 60 s limit, so a preview or export of such a document is a counted time-out. Raise
  `derived.timeout` for large documents, or keep the documents small.

- **Logs.** When an upload fails, Collabora's log holds the full storage URI with the access token in it. That is Collabora's log, not SelfCloud's: keep it private. SelfCloud's own log holds no token. A refused editor call names its reason, node and session, and never the token.
- **Editor tokens.** An editor's token lives `office.token_minutes` (600 by default, and at most a day). It never outlives the token that opened the editor. An editor ends with the browser session that opened it. Its file's lock is freed by the next sweep (every five minutes) once no editor on that file is live (decision 106).

A conversion is refused when Collabora cannot save the document as the format asked (a spreadsheet asked for as a text
document, for example). A stopped Collabora is an outage: SelfCloud answers `unavailable` with `Retry-After` and tries
again. Collabora converts unknown content as text rather than refusing it, so a corrupt file yields a PDF of its bytes.

## Off by default

- **Links that anyone can open, and drop folders.** An account turns them on with its own public-link setting; it starts off.
- **The office editor.** `office.enabled` is `false`.
- **Per-user limits.** No account has a default limit for each user (`user_quota_bytes` is `0`), so a user has none
  until the owner or the account key sets one.
- **Webhooks.** No account has an endpoint until one is created for it.

Not built yet: search (by name, label or text), the web interface for
comments and notifications, the interface's thumbnails and office button. The [feature parity list](docs/feature-parity.md) says which slice each belongs to.

## Installing the web app

The web interface can be installed as an app on a phone or a computer. Installing and the service worker need a secure
address: HTTPS, or `localhost`. On a plain `http://` address the app works, but it cannot be installed, and the fast
start and the offline page are off.

With the web interface on, the server serves these paths, each named for the installation's own name and colour:

- `/manifest.webmanifest`, the app manifest;
- `/brand/icon-192.png`, `/brand/icon-512.png`, `/brand/icon-512-maskable.png` and `/brand/apple-touch-icon.png`, the icons;
- `/sw.js`, the service worker, which keeps the app's own files for a fast start and shows `/offline.html` when the
  network is gone. It never keeps API answers, file content, links or share pages.

Headless mode serves none of these. On an iPhone or an iPad, Safari has no install prompt: open the app in Safari, tap
Share, then Add to Home Screen.

## Development

```sh
make check   # gofmt, go vet, tests with the race detector
make web     # the web interface build (Node 22)
make e2e     # the browser smoke test (needs Chrome or Chromium)
cd web && npm run check   # TypeScript, source checks, licence and key checks, unit tests
```

CI runs `npm run check` and `npm run build` in `web/`, then the Go checks. A second job, `e2e`, runs the browser checks with Google Chrome through `scripts/ci-e2e.sh`, and keeps their output when it fails.

The upload benchmark pushes 1 GiB through the resumable-upload routes in 64 MiB pieces and reports
MB/s. It writes about 1 GiB to a temporary directory, so point `TMPDIR` at a disk with room:

```sh
TMPDIR=/path/with/space go test ./internal/api -run '^$' -bench BenchmarkUpload -benchtime 1x
```

## Dependency licences

Code that reaches the browser (the runtime packages of the web interface, which go into `web/dist/`)
must be MIT, ISC, BSD or OFL. Build-time packages may also be Apache-2.0, MPL-2.0, MIT-0, CC0-1.0 or
BlueOak-1.0.0 (test-runner packages only; none of them reaches the browser). `npm run check` in `web/`
enforces this by reading each package's licence from `node_modules`.

## Brand

The SelfCloud mark and wordmark in `docs/brand/` (and the copies the server serves) use the glyph outlines of the Nunito ExtraBold
typeface, under the SIL Open Font License 1.1 (copyright "Copyright 2014 The Nunito Project Authors
(https://github.com/googlefonts/nunito)", as the font's own notice gives it). The artwork is SelfCloud's own.

## Licence

SelfCloud is free software under the GNU Affero General Public License, version 3 or (at your
option) any later version. See [LICENSE](LICENSE). If you run a modified version as a network
service, you must offer its source to the people who use it.
