# Asana extractor

A local Go CLI being built to export Asana users and projects to JSON.
Milestone 2 adds a paginated Asana client alongside configuration and CLI
validation. The CLI currently exits successfully after configuration validation;
client wiring, file writing, and scheduling will be implemented in later milestones.

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
| `OUTPUT_PATH` | No | `data/asana.json` |

The CLI reads `.env` from its current directory if present. Existing environment
variables take precedence, including explicitly empty values. Empty or
whitespace-only PAT and workspace GID values are rejected. Malformed `.env`
files fail with a message that omits file contents. Configuration diagnostics
mask the PAT.

## Flags and validation

```sh
go run ./cmd/extractor --help
go run ./cmd/extractor --interval=5m
go run ./cmd/extractor --interval=30s --once --output=data/custom.json
```

- `--interval`: accepts exactly `5m` (default) or `30s`.
- `--once`: records the request for a single run; execution comes later.
- `--output`: a non-empty value overrides `OUTPUT_PATH` for this invocation.

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

Construct the client with a `config.Config` and a request timeout. Configuration
is checked by `Config.ValidateAsana()` in `internal/config`.
A zero timeout selects 30 seconds; negative timeouts are rejected.
Caller cancellation propagates to each request. Redirects are not followed.
Call `Close()` when finished. Timeout configuration is currently a Go constructor parameter,
not a new environment variable or CLI flag.

Retries and specialized API-error handling are reserved for Milestone 3.
