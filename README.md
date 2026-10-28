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
| `log.level`, `log.format` | `info`, `json` | |

## Development

```sh
make check   # gofmt, go vet, tests with the race detector
```

## Licence

Not chosen yet.
