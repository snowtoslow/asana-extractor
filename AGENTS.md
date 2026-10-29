# AGENTS.md

## Project
A Go CLI that periodically pulls **users** and **projects** from the Asana API
and writes them to a local JSON file. It runs in one of two modes, chosen by a flag:
every **5 minutes** or every **30 seconds**.

## Stack (do not swap without asking)
- Go (use the version in `go.mod`)
- HTTP client + retries: `resty.dev/v3`
- Env loading: `github.com/joho/godotenv`
- Tests: standard `testing` + `net/http/httptest` (add `testify` only if I approve it)

## Skills — READ FIRST
Project skills live in `.agents/skills/`.
Before starting ANY task:
1. List `.agents/skills/` and read the `SKILL.md` of every skill whose description
   matches the task (Go conventions, testing, HTTP/retry, API clients, git, docs, etc.).
2. Follow those skills' instructions. If a skill conflicts with this file,
   this file wins; mention the conflict to me.
3. At the end of each task, state in one line which skills you used (or that none applied).
   Do not skip this step even for small changes.

## Layout
- `cmd/extractor/` — `main.go`, flag parsing, wiring, signal handling
- `internal/config/` — env/flag config and validation
- `internal/asana/` — API client (users, projects, pagination, retries)
- `internal/storage/` — atomic JSON writer
- `internal/extractor/` — one extraction run + scheduler loop

## Behavior requirements
- Flag `--interval` accepts only `5m` or `30s`; reject anything else with a usage error.
- Run once immediately on start, then on every tick. Runs must never overlap.
- Output is a single JSON file: `{ "fetched_at", "users": [], "projects": [] }`.
- Write atomically (temp file in same dir + rename). A failed run must not
  corrupt or truncate the previous output.
- A failed run is logged and the loop continues. Never crash the scheduler on API errors.
- Graceful shutdown on SIGINT/SIGTERM via `context`.

## Asana API rules
- Base URL from `ASANA_BASE_URL` (default `https://app.asana.com/api/1.0`) so tests can override it.
- Auth: `Authorization: Bearer $ASANA_PAT`.
- Always paginate (`limit=100`, follow `next_page.offset`) until no next page.
- Use `opt_fields` to request only the fields we store.
- Retry on network errors, 429 (respect `Retry-After`) and 5xx with backoff;
  never retry other 4xx.
- Always set a request timeout.

## Configuration
Env vars (loaded from `.env` if present; real env vars take precedence):
`ASANA_PAT`, `ASANA_WORKSPACE_GID`, `ASANA_BASE_URL`, `OUTPUT_PATH`.
Keep `.env.example` up to date. Never read & commit `.env`.

## Code standards
- Idiomatic Go: small packages, small interfaces defined where consumed,
  `context.Context` as first param, errors wrapped with `%w`, no globals, no `panic` in library code.
- Inject dependencies (HTTP client, clock/ticker, writer) so everything is testable.
- Use structured logging (`log/slog`). **Never log secrets** (PAT).
- Run `gofmt`, `go vet` and `go test ./... -race` before declaring a task done.

## Testing rules
- No real network calls. Use `httptest.Server` to fake Asana.
- Cover: auth header, pagination, retry on 429/5xx, no retry on 401/404,
  partial-failure safety, JSON output content, atomic write, scheduler timing
  (fake ticker, no real sleeps), no overlapping runs, flag validation.
- Tests must be deterministic and fast.

## Workflow
- Work in small, reviewable steps; one logical change at a time.
- Before big or ambiguous changes, propose a short plan and wait for my OK.
- If requirements are unclear, ask instead of guessing.
- Don't add dependencies, change the stack, or restructure the layout without asking.
- Update README/`.env.example` when behavior or config changes.

## Definition of done
- Code builds, `gofmt`/`go vet` clean, `go test ./... -race` passes.
- New behavior has tests.
- Docs updated.
- Final message lists: what changed, how it was verified, skills used.