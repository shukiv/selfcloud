# Selfcloud

A self-hosted drive, written in Go. It has its own accounts, users, sign-in and storage, and it
depends on no other system. Any application can manage it and move files through its HTTP API.
It runs as a headless API server, or with its own web interface, on a host or in Docker. Files
can be up to 100 GB, uploaded in resumable pieces.

**Status: early development.** The design is in [docs/design.md](docs/design.md) and is being
built in slices. The API document at [api/openapi.yaml](api/openapi.yaml) lists only what works
today.

## Build and run

Go 1.26 or newer:

```sh
make build
./bin/selfcloud serve
```

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
| `client_ip_header` | `X-Forwarded-For` | Or a single-address header such as `CF-Connecting-IP`. |
| `upload.max_file_size` | `100GiB` | Largest file. |
| `upload.max_chunk_size` | `64MiB` | Largest upload request. Keep it at or under `90MiB` behind a proxy that caps requests at 100 MB. |
| `metrics_listen` | none | Serve `/metrics` on a separate address. |
| `admin_key_file` | none | File holding an admin key to add on start. |
| `log.level`, `log.format` | `info`, `json` | |

## Development

```sh
make check   # gofmt, go vet, tests with the race detector
```

The upload benchmark pushes 1 GiB through the resumable-upload routes in 64 MiB pieces and reports
MB/s. It writes about 1 GiB to a temporary directory, so point `TMPDIR` at a disk with room:

```sh
TMPDIR=/path/with/space go test ./internal/api -run '^$' -bench BenchmarkUpload -benchtime 1x
```

## Licence

Not chosen yet.
