# Asana extractor

A local Go CLI being built to export Asana users and projects to JSON.
Milestone 4 adds atomic JSON storage alongside the paginated Asana client.
The CLI currently exits successfully after configuration validation;
client/writer wiring, extraction runs, and scheduling will be implemented in later milestones.

Requires Go 1.27.3. Make is optional.

## Local configuration

Generate a Personal Access Token (PAT) in the Asana developer console under
**My apps → Personal access tokens**. Copy `.env.example` to `.env` and replace
the PAT and workspace GID placeholders locally:

```sh
cp .env.example .env
```

Keep the real PAT only in your local `.env` or environment. `.env` is ignored by
git. Never commit or share it.

| Variable | Required | Default when absent |
| --- | --- | --- |
| `ASANA_PAT` | Yes | None |
| `ASANA_WORKSPACE_GID` | Yes | None |
| `ASANA_BASE_URL` | No | `https://app.asana.com/api/1.0` |
| `OUTPUT_PATH` (directory) | No | `data/` |

The CLI reads `.env` from its current directory if present. Existing environment
variables take precedence, including explicitly empty values. Empty or
whitespace-only PAT and workspace GID values are rejected. Malformed `.env`
files fail with a message that omits file contents. Configuration diagnostics
mask the PAT.

## Flags and validation

```sh
go run ./cmd/extractor --help
go run ./cmd/extractor --interval=5m
go run ./cmd/extractor --interval=30s --once --output=data/custom
```

- `--interval`: accepts exactly `5m` (default) or `30s`.
- `--once`: records the request for a single run; execution comes later.
- `--output`: a non-empty value overrides the `OUTPUT_PATH` directory for this invocation.
  The directory contains two separate files, `users.json` and `projects.json`.

Unknown flags, invalid intervals, and positional arguments produce a usage
error. The binary exits with code 2 for usage errors, 1 for configuration errors,
and 0 for help or successful configuration validation.

## Development

```sh
make build  # bin/extractor
make lint   # gofmt and go vet
make test   # go test ./... -race
make run    # configuration validation only at this milestone
```

Tests use temporary files and synthetic credentials. They do not call Asana.
Retry timing tests use an in-memory `httptest.Server` and `testing/synctest`:
backoff and `Retry-After` delays advance synthetic time without real sleeps.

## Asana client

`internal/asana` provides `ListUsers(ctx)` and `ListProjects(ctx)` for one
workspace, using the configured base URL and a Bearer PAT on each request.
It stores only these fields:

| Resource | Fields |
| --- | --- |
| User | `gid`, `name`, `email` |
| Project | `gid`, `name`, `archived` |

The client requests `limit=100`, the workspace GID, and the stored fields via
`opt_fields`. It follows `next_page.offset` until absent or empty, keeping the
original endpoint and base URL. See Asana's
[users endpoint](https://developers.asana.com/go/reference/getusers) and
[projects endpoint](https://developers.asana.com/reference/getprojects).

Construct the client with a validated `config.Config` and a request timeout.
The caller is responsible for configuration validation before initialization.
A zero timeout selects 30 seconds; negative timeouts are rejected.
Caller cancellation propagates to each request. Redirects are not followed.
Call `Close()` when finished. Timeout configuration is currently a Go constructor parameter,
not a new environment variable or CLI flag.

### Retries and errors

Each page request allows three retries after the initial attempt. Network
failures, HTTP 429, and all HTTP 5xx statuses are retryable. Other HTTP 4xx
statuses are returned immediately. Cancellation stops requests and retry waits.

Backoff defaults to 1s, 2s, 4s, with a 30s cap if more retries are configured.
For 429 and 503, a valid `Retry-After` (seconds or HTTP date) takes precedence,
even when longer than the cap. Missing or invalid headers use normal backoff.

Optional constructor settings keep existing calls valid:

```go
client, err := asana.New(cfg, 0,
    asana.WithRetries(3, time.Second, 30*time.Second),
    asana.WithLogger(logger),
)
```

`WithRetries(0, 0, 0)` disables retries; positive counts with zero waits permit
immediate retries. These are Go constructor options, not new env vars or flags.

Unsuccessful responses return a wrapped `*asana.HTTPError`; use `errors.As` to
inspect `StatusCode`. A 401 error states that the PAT is invalid or revoked.
The client returns errors for its caller to log rather than logging them twice.
Scheduler continuation is implemented in a later milestone.

Response bodies and raw transport/decoder errors are omitted from diagnostics
because they can echo credentials. Known causes still match with `errors.Is`,
including cancellation and deadlines. Raw causes are not exposed by `Unwrap`.
Resty wire/debug dumps are suppressed, and its warnings/errors use fixed
structured messages. No PAT is included in those diagnostics.

## JSON storage

**Users and projects are stored in two different files:** `users.json` and
`projects.json`. `OUTPUT_PATH` and `--output` select their directory, defaulting
to `data/`. An existing single-file setting such as `data/asana.json` must be
changed to an output directory such as `data/`; there is no automatic migration
or deletion of old output files.

`storage.New(directory).Write(ctx, snapshot)` prepares both indented JSON
documents. The caller supplies `FetchedAt`, which is serialized in UTC using
RFC 3339 with fractional seconds when present. Both files share that timestamp.

`users.json`:

```json
{
  "fetched_at": "2026-10-29T14:30:00Z",
  "users": []
}
```

`projects.json`:

```json
{
  "fetched_at": "2026-10-29T14:30:00Z",
  "projects": []
}
```

Each document contains only its own entity list and timestamp. Nil and empty
lists produce `[]`. Missing directories are created with owner-only permissions
(`0700`); files use owner-only permissions (`0600`). Existing directory
permissions are left as configured.

Both unique temporary files are written, synced and closed in the output
directory before publication starts. The writer then atomically replaces
`users.json`, followed by `projects.json`, using `os.Rename` for each. Encoding
or staging failures preserve both previous files. All unpublished temporary
files are removed on returned failures; cleanup errors are reported too.
Cancellation is checked before preparation and before each publication.

**Atomicity applies to each file, not the pair.** A failure or forced process
termination between renames can leave complete files from different runs.
Compare `fetched_at` to detect a mixed pair. Forced termination can also leave
unpublished temporary files. Each destination remains a complete old or new file.

The storage layer accepts no authentication configuration. It is not yet wired
into the CLI; `make run` still performs configuration validation only.
