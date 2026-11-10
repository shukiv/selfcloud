# The selfcloud command-line client

`selfcloud` has two kinds of command. The local ones (`selfcloud admin …`) read the data folder directly and need no key;
they are described in the README. The remote ones call a server's HTTP API with a key, and they are described here.

This document covers what exists now: the profiles, the connection rules, output and exit codes, and the commands
`info`, `api spec` and `profile add|use|ls|rm`. Later groups (sign-in at a terminal, files, large files, sharing and
the rest) are added to this document as they land.

## Profiles

A profile is a name for a server address and a key. The profiles file is one JSON document:

```json
{
  "version": 1,
  "current": "home",
  "profiles": {
    "home": { "url": "https://selfcloud.example", "kind": "account", "token": "…" }
  }
}
```

- **Where it is.** `$SELFCLOUD_PROFILES_DIR/profiles.json` when `SELFCLOUD_PROFILES_DIR` is set, otherwise
  `selfcloud/profiles.json` under the user's configuration directory.
- **Permissions.** The directory is created with mode `0700` and the file with mode `0600`. The file is written to a
  temporary file in the same directory (created exclusively, with a random name) and renamed over the old one, so an
  interrupted write leaves the old file whole. A file, or a directory, that any other user can read or enter is refused,
  and the message gives the `chmod` that fixes it. A symbolic link is refused.
- **Which profile is used.** `--profile NAME`, then `SELFCLOUD_PROFILE`, then the current profile (`profile use`). The
  first profile added becomes current. A name given with `--profile` must name a profile. A name from `SELFCLOUD_PROFILE`
  or the current profile that names none is ignored when the command has its address and any key it needs; a one-line
  note on standard error says where it came from, and never repeats the name.
- **Names in messages.** A name that starts with `sc_`, `scd_`, `sct_` or `scl_` is shown as "that name" in error
  messages, even when it is a valid name (for example `sc_ops`), so a key pasted where a name goes is not repeated. Such a
  name still shows in `profile ls` and in the success lines. A value that is not a valid name is shown as "that name" too.
- **A bad address.** A profile whose address is not valid (a user name in it, a scheme other than http or https, a query,
  or no host) makes every `profile` command and every remote command refuse the file, and the message names the profile.
  `profile rm NAME` still works, so the bad profile can be removed.

Commands:

```sh
selfcloud profile add home --url https://selfcloud.example --token-file ./key      # or: pipe the key in
printf '%s\n' "$KEY" | selfcloud profile add ops --url https://selfcloud.example
selfcloud profile use ops
selfcloud profile ls                  # NAME, URL, KIND and CURRENT; never the key
selfcloud profile rm home             # removes the local key only; the server's key is not revoked
```

`profile add` takes the key from `--token-file` or from standard input. It has no flag for the key, so a key never
appears in the command line, in a shell history or in `ps`. It refuses to read a key from a terminal, where what is typed
would be shown. `--ca-file FILE` saves the path of a PEM file of roots with the profile. A plain-http address that is not
loopback needs `--insecure-http` when it is added, and the address is saved; the flag is not saved, so every later use of
that profile needs `--insecure-http` too. `--insecure-skip-verify` is not accepted by `profile add`; see TLS below.

`profile rm` of the current profile leaves no profile current, and says so on stderr.

## The key

| Kind | Prefix | Made by |
|---|---|---|
| `admin` | `sc_admin_` | `selfcloud admin create-key` (local) |
| `account` | `sc_acct_` | `selfcloud admin create-account` (local) |
| `device` | `scd_` | device sign-in, a later slice; the prefix is recognised now |

Any other value is refused. No command in this slice prints a key: `profile add` and `profile ls` show the kind, and a
refusal says what was wrong without repeating the value.

## Where the address and the key come from

Flags win, then the environment, then the profile, then the defaults.

| Setting | Flag | Environment | Profile |
|---|---|---|---|
| Address | `--url` | `SELFCLOUD_URL` | `url` |
| Key | `--token-file FILE` | `SELFCLOUD_TOKEN` | `token` |
| Profile | `--profile NAME` | `SELFCLOUD_PROFILE` | the current profile |
| CA file | `--ca-file FILE` | | `ca_file` |

A profile's key is sent only to that profile's own address. If the address is given another way (`--url` or
`SELFCLOUD_URL` pointing elsewhere), a command that needs a key refuses, and says to pass `--token-file` or set
`SELFCLOUD_TOKEN` for that address. A command that needs no key (such as `info`) sends none when the address is not the
profile's own; when it is, the profile's key is sent with the request, as it is for every command.

A token file must be private (no group or other bits), a regular file, and hold one line.

## Connections, TLS and proxies

- **https only.** Plain http is accepted only for a loopback address (`localhost`, `127.0.0.0/8`, `::1`), or with
  `--insecure-http`, which prints a warning on every use.
- **Certificates are checked.** A certificate that does not verify is refused, and the message says to use `--ca-file`
  for a private root. `--ca-file` adds the roots of a PEM file to the system's.
- **`--insecure-skip-verify`** turns the check off for one command. It prints a warning on every use and is never saved.
- **Proxies.** `HTTPS_PROXY`, `HTTP_PROXY` and `NO_PROXY` are honoured. A proxy never changes certificate checking.
- **Redirects are not followed.** The key would go to the new address; a redirect is reported as the error it is, and
  the address should be the final one.
- **Credentials** are sent only as `Authorization: Bearer …`, never in an address, a message or a log line.

## Timeouts and retries

| Flag | Default | Meaning |
|---|---|---|
| `--connect-timeout` | 10s | connecting and the TLS handshake |
| `--header-timeout` | 30s | the wait for the response headers |
| `--timeout` | 2m | the whole request, its body included |

A `429` or `503` answer to a `GET` or `HEAD` is repeated, at most twice. The wait is the server's `Retry-After` (seconds,
or an HTTP date); without a usable one, the client waits one second. A wait longer than 30 seconds is not made: the
command fails at once, and the error says how long the server asked to wait. Other methods (`POST`, `PUT`, `PATCH`,
`DELETE`) are never repeated, so a command cannot send the same change twice.

## Output

- **Default.** A single object is printed as `key=value` lines. A list is a table, with a header row.
- **`--json`.** One JSON document, with the API's own field names. `profile ls --json` is a list of
  `{"name","url","kind","current"}`; it never has a key in it.
- **Text from the server** is shown with control and format characters (including bidi overrides) replaced by spaces, so a
  message cannot make a terminal act or misreport. In `--json` those characters are written as `\uXXXX` escapes (a rune above
  U+FFFF as a surrogate pair), which decode to the same text; a byte that is not valid UTF-8 is written as U+FFFD.
- **Errors** go to standard error. Without `--json`, one line: `selfcloud: <code>: <message>`. With `--json`, the API's
  envelope: `{"error":{"code":"…","message":"…"}}`. The server's error code is cleaned the same way as its message, in
  both modes; a code with a control or format character shows as spaces.

The codes are the API's where the server answered. Otherwise the CLI uses one of: `usage` (a flag or argument is wrong),
`not_found` (no such profile), `unreachable` (no connection), `tls` (a certificate did not verify), `timeout`,
`failed` (anything else), `partial`, or `http_<status>` when the server gave a status without a code.

## Exit codes

| Code | Meaning |
|---|---|
| 0 | done |
| 1 | failed: a connection, a server error (5xx), an I/O or an unexpected error |
| 2 | usage or validation: refused before anything changed (a bad flag, a bad address, a profile file that is not private) |
| 3 | refused by the server: 401, 403, 409, 429, and the other 4xx that are not listed elsewhere |
| 4 | not found: a 404, or a profile name that does not exist |
| 5 | a check failed after a transfer (a checksum did not match); used by the file commands |
| 6 | an upload expired on the server; the file must be sent again from the start; used by the file commands |
| 7 | partial success: some of several targets were done and some failed; used by the commands that take several targets |

Codes 5, 6 and 7 have no command that reaches them yet. Their mapping is fixed now.

## Commands

### `info`

```sh
selfcloud info --url https://selfcloud.example
selfcloud info --json            # with a profile: selfcloud info --profile home
```

Prints the server's name, version, public address, whether the web interface is on, the minimum password length, the
size limits, the sharing limit in days, the optional features and the brand name. The route is public, so the call needs
no key; the profile's key is sent only as described under "Where the address and the key come from". `--json` prints the
whole document.

### `api spec`

```sh
selfcloud api spec > openapi.yaml
selfcloud api spec --out openapi.yaml            # refuses to replace a file
selfcloud api spec --out openapi.yaml --overwrite
```

Writes the server's OpenAPI document to standard output, or to a new file. A file is created exclusively, so an existing
file is never replaced without `--overwrite`, and a symbolic link at the name is refused, with or without it. The answer
must be YAML; an HTML page is refused.

### `profile add|use|ls|rm`

See Profiles above.

## Docker

The image holds the same binary. A remote command in a container needs the profiles file mounted; the directory must be
private on the host.

```sh
mkdir -m 700 profiles
printf '%s\n' "$KEY" | docker run --rm -i --user "$(id -u):$(id -g)" -e SELFCLOUD_PROFILES_DIR=/profiles -v "$PWD/profiles:/profiles" <image> selfcloud profile add home --url https://selfcloud.example
docker run --rm --user "$(id -u):$(id -g)" -e SELFCLOUD_PROFILES_DIR=/profiles -v "$PWD/profiles:/profiles" <image> selfcloud info --profile home
```

The container runs as the host user, so the mounted directory and its file keep the private modes the CLI requires. This
example was written but not run against an image.

A local command runs in the running server's container, against its data folder, with no key:

```sh
docker exec <container> selfcloud admin reset-password -user EMAIL -method link -link-file /tmp/link
```

## Build and platforms

The client is part of the same binary as the server, uses only the Go standard library, and builds wherever the server
does. It is tested on Linux.
