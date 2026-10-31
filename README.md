# Selfcloud

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
| `zip.max_per_principal` | `2` | Unfinished zips one caller may have at once. |
| `zip.max_per_account` | `4` | Unfinished zips an account may have at once. |
| `zip.max_unfinished` | `8` | Unfinished zips the installation may have at once; each holds its file and its plan in memory. |
| `office.enabled` | `false` | Turns on the office editor route and the WOPI host for Collabora. Needs `office.discovery_url`. |
| `upload.max_disk_share_percent` | `50` | Most of the usable disk space one account's unfinished uploads may hold. Set `100` for a single tenant. |
| `tokens.max_ttl` | `1h` | Longest lifetime of a delegated token. A longer `ttl` is refused. |
| `sharing.max_expiry` | `8760h` | Longest a permission may run from the moment it is set (its `expires_at`). `0` means no cap. |
| `trash.retention` | `720h` | How long a deleted item waits in the trash before the sweep purges it. Must be positive. |
| `versions.max_count` | `100` | Most versions a file keeps that are not kept forever; the oldest go first. `0` means no count limit. |
| `versions.max_age` | `720h` | How long a version that is not kept forever is kept after its content was replaced. `0` means no age limit. |
| `versions.max_kept` | `200` | Most versions of one file that may be kept forever. Marking another is refused with a conflict. Must be positive. |
| `cors.allowed_origins` | none | Origins whose pages may call the API, as exact origins such as `https://app.example.com`. Empty turns CORS off. |
| `metrics_listen` | none | Serve `/metrics` on a separate address. |
| `admin_key_file` | none | File holding an admin key to add on start. |
| `ui.enabled` | `true` | Serve the web interface at `/`. With `false`, the API alone runs; `/` is the JSON 404. |
| `ui.brand.name` | `Selfcloud` | Product name in the web interface and `/api/v1/info`. Up to 64 characters. |
| `ui.brand.accent` | `#053D8E` | Primary colour, as `#rrggbb`. The web interface derives its accent family from it. |
| `ui.brand.palette.light` / `.dark` | none | Colour tokens of each theme (`accent`, `accent_fill`, `accent_fill_hover`, `accent_fill_active`, `accent_text`, `accent_soft`, `accent_border`), each `#rrggbb`. Unset tokens keep the default. |
| `ui.brand.sidebar` | none | Sidebar colours (`bg`, `text`, `caption`, `active`, `on_active`), each `#rrggbb`. Unset ones keep the default. |
| `ui.brand.logo_file` | none | A PNG or SVG logo, at most 256 KiB, read at start and served at `/brand/logo`. Installation-wide only. |
| `log.level`, `log.format` | `info`, `json` | |

## Off by default

- **Links that anyone can open, and drop folders.** An account turns them on with its own public-link setting; it starts off.
- **The office editor.** `office.enabled` is `false`.
- **Per-user limits.** No account has a default limit for each user (`user_quota_bytes` is `0`), so a user has none
  until the owner or the account key sets one.
- **Webhooks.** No account has an endpoint until one is created for it.

Not built yet: search (by name, label or text), comments, document previews and conversions, zip download, the
interface's thumbnails and office button. The [feature parity list](docs/feature-parity.md) says which slice each belongs to.

## Development

```sh
make check   # gofmt, go vet, tests with the race detector
make web     # the web interface build (Node 22)
make e2e     # the browser smoke test (needs Chrome or Chromium)
cd web && npm run check   # TypeScript, source checks, licence and key checks, unit tests
```

CI runs `npm run check` and `npm run build` in `web/`, then the Go checks.

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

## Licence

Selfcloud is free software under the GNU Affero General Public License, version 3 or (at your
option) any later version. See [LICENSE](LICENSE). If you run a modified version as a network
service, you must offer its source to the people who use it.
