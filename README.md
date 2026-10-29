# Asana extractor

A local Go CLI that exports users and projects from one Asana workspace to
`users.json` and `projects.json`. It runs once immediately, then every five
minutes or every 30 seconds, or performs a single extraction with `--once`.
Authentication uses a Personal Access Token (PAT).

## Requirements

- Go 1.27.3, as specified in `go.mod`.
- An Asana account with access to the workspace you want to export and permission
  to create a PAT.
- Git to clone the repository; Make is optional.
- A C compiler for Go's race detector when running the tests.

## Setup

```sh
git clone https://github.com/snowtoslow/asana-extractor.git
cd asana-extractor
cp .env.example .env
```

### Generate a PAT

1. Sign in to the [Asana developer console](https://app.asana.com/0/my-apps)
   (**My apps → Personal access tokens**). You can also reach it through your
   profile → My Settings → Apps → Manage Developer Apps.
2. Select **Create new token**, give it a name such as `local extractor`, accept
   the terms if prompted, and create it.
3. Copy the token immediately; it is displayed only once. Put it in your local
   `.env` as the value of `ASANA_PAT`.

See Asana's [PAT documentation](https://developers.asana.com/docs/personal-access-token).
Keep the PAT private: never commit it, share it, or paste it into logs. `.env`
is ignored by Git. The CLI sends `Authorization: Bearer <PAT>` on API requests
and masks the PAT in configuration logs.

### Find the workspace GID

While signed in to Asana, open the
[workspaces endpoint](https://app.asana.com/api/1.0/workspaces) in your browser.
In the returned `data` array, find the workspace by `name` and copy its `gid`
into `ASANA_WORKSPACE_GID`. Use the workspace's GID, not a project or user GID.
This is the method described in Asana's
[quick start](https://developers.asana.com/docs/quick-start).

### Configure `.env`

Replace the two placeholders in the copied file locally:

```dotenv
ASANA_PAT=replace-with-your-personal-access-token
ASANA_WORKSPACE_GID=replace-with-your-workspace-gid
ASANA_BASE_URL=https://app.asana.com/api/1.0
OUTPUT_PATH=data
```

| Variable | Required | Default when absent |
| --- | --- | --- |
| `ASANA_PAT` | Yes | None |
| `ASANA_WORKSPACE_GID` | Yes | None |
| `ASANA_BASE_URL` | No | `https://app.asana.com/api/1.0` |
| `OUTPUT_PATH` | No | `data/` (output directory) |

The CLI loads `.env` from the current working directory. Existing environment
variables take precedence, including explicitly empty values. Missing `.env`
is fine; malformed syntax fails startup without printing its contents. Missing,
empty, or whitespace-only PAT and workspace GID values fail startup.
Defaults apply when optional variables are absent, not when explicitly empty.
Relative output paths are resolved from the current working directory.

## Run locally

```sh
go build -o bin/extractor ./cmd/extractor
./bin/extractor --once
./bin/extractor --interval=5m
./bin/extractor --interval=30s
./bin/extractor --once --output=data/custom
```

Run the periodic commands individually; each keeps running until interrupted.
You can also use `go run ./cmd/extractor` with the same flags.

| Flag | Behavior |
| --- | --- |
| `--interval` | Exactly `5m` (default) or `30s`; validated even with `--once`. |
| `--once` | One extraction, then exit. |
| `--output` | A non-empty value overrides the `OUTPUT_PATH` directory. |
| `--help` | Show usage without requiring configuration. |

Periodic mode starts immediately. Runs never overlap; ticks during an active
run are skipped. Failed runs are logged and later ticks continue. A 401 logs
that the PAT is invalid or revoked and is not retried within the run; update
the PAT and restart the process. Other 4xx responses except 429 are not retried.
Network errors, 429, and 5xx responses allow up to three retries per page with
backoff. Valid `Retry-After` headers on 429/503 are respected. The request timeout
is 30 seconds. Users and projects are fetched concurrently, with `limit=100`,
`opt_fields`, and pagination through every `next_page.offset`.

Logs go to stderr using `log/slog`. Ctrl+C (SIGINT) or SIGTERM cancels active
work and waits for cleanup. Normal periodic shutdown exits 0. The binary exits
2 for usage errors and 1 for configuration, initialization, or single-run
failures (including an interrupted `--once`); successful `--once` exits 0.
`go run` may report a failing binary's exit status through its own wrapper.

## Output

The selected output directory contains two separate, indented JSON files.
Example values below are fictional.

`users.json`:

```json
{
  "fetched_at": "2026-10-29T14:30:00Z",
  "users": [
    {
      "gid": "1234567890",
      "name": "Example User",
      "email": "user@example.com"
    }
  ]
}
```

`projects.json`:

```json
{
  "fetched_at": "2026-10-29T14:30:00Z",
  "projects": [
    {
      "gid": "9876543210",
      "name": "Example Project",
      "archived": false
    }
  ]
}
```

Empty lists are `[]`, never `null`. Both files from one extraction share a UTC
`fetched_at` timestamp recorded after fetching completes, in RFC 3339 format
with fractional seconds when present. Each run replaces previous output.
Missing output directories are created with `0700` permissions; new files use
`0600`. Existing directory permissions remain unchanged.

Both temporary files are prepared, synced, and closed in the output directory
before either is published. Each rename is atomic: `users.json` is published
first, then `projects.json`. API or staging failures leave both previous files
untouched. **The pair is not an atomic transaction:** a publication failure or
interruption between renames can leave complete files with different timestamps.
Compare `fetched_at` to detect this. Unpublished temporary files are cleaned up
on returned failures; forced termination may leave temporary files behind.

## Development and tests

```sh
gofmt -l .
go vet ./...
go build ./...
go test ./... -race
go test ./... -race -tags=integration
```

The last command includes end-to-end tests behind the `integration` build tag.
Tests use synthetic credentials, temporary directories, in-memory `httptest`
transports, manual tickers, and `testing/synctest`. They do not contact Asana,
read the repository's `.env`, or wait for real retry/scheduler delays.

Optional Make targets:

```sh
make build                         # bin/extractor
make lint                          # gofmt (writes files) and go vet
make test                          # unit + integration tests with race detection
make run ARGS="--interval=30s"      # run locally with your configured PAT
```

Code lives in `cmd/extractor` (CLI and wiring), `internal/config`,
`internal/asana`, `internal/storage`, and `internal/extractor` (runs and scheduler).

## Manual smoke test

To be performed by the project owner with a real PAT; not yet verified:

- [ ] Configure `.env`, build the binary, and run `./bin/extractor --once`.
  Confirm exit code 0 and valid separate `data/users.json` and
  `data/projects.json` files with matching `fetched_at` timestamps.
- [ ] Run `./bin/extractor --interval=30s` for a few cycles. Confirm an immediate
  extraction, successful later runs, and updated timestamps in both files.
- [ ] Press Ctrl+C. Confirm the process exits cleanly, logs `extractor stopped`,
  and both files remain valid JSON. For example, if Python 3 is installed:

  ```sh
  python3 -m json.tool data/users.json > /dev/null
  python3 -m json.tool data/projects.json > /dev/null
  ```
