# Asana extractor

A local Go CLI being built to export Asana users and projects to JSON.
Milestone 1 implements configuration and CLI validation. Valid configuration
currently exits successfully with a message; fetching, file writing, and
scheduling will be implemented in later milestones.

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
