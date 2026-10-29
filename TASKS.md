# TASKS.md: Milestones for the Asana Extractor

Everything runs locally: no Docker, CI, or deployment tasks.
Assumptions: one workspace, two separate output files (`users.json` and `projects.json`) overwritten each run, authentication via Personal Access Token (PAT) only. `OUTPUT_PATH` and `--output` select the output directory (default `data/`).

**Rules for every milestone:**
- Read the relevant `.agents/skills/*/SKILL.md` files before starting, and state which skills you used when you finish.
- Finish with `gofmt`, `go vet` and `go test ./... -race` all passing.
- Tick the checkboxes in this file as tasks are completed.
- Stop after each milestone for review before starting the next.

---

## Milestone 0: Bootstrap

**Tasks**
- [x] Review `.agents/skills/` and list which skills apply to which milestones.
- [x] Complete the existing module setup with `github.com/snowtoslow/asana-extractor` and Go 1.27.3, and add `resty.dev/v3` and `github.com/joho/godotenv`.
- [x] Create the folder layout from AGENTS.md with placeholder `doc.go` files.
- [x] Add `.gitignore` (`.env`, `data/`, binaries) and `.env.example` with placeholders only.
- [x] Add a `Makefile` with `build`, `test`, `lint` (`gofmt` + `go vet`) and `run` targets.

**Done when:** `go build ./...` and `make test` succeed on the empty skeleton.

---

## Milestone 1: Configuration and CLI flags

**Tasks**
- [x] `internal/config`: load `.env` via godotenv (a missing file is fine, real env vars win).
- [x] Read `ASANA_PAT`, `ASANA_WORKSPACE_GID`, `ASANA_BASE_URL` (with default) and `OUTPUT_PATH` (output directory, default `data/`).
- [x] Validate that the PAT and workspace GID are non-empty, and return clear errors that never include the PAT value.
- [x] `cmd/extractor`: add `--interval` (only `5m` or `30s` accepted; anything else gives a usage error and a non-zero exit), `--once` and `--output`.
- [x] Add a `Config` struct with a `String()` or `LogValue()` that masks the PAT.

**Tests**
- Defaults, env override, `.env` loading, missing PAT, invalid interval, and PAT masking.

**Done when:** running the binary with bad flags or missing env vars exits cleanly with a helpful message.

---

## Milestone 2: Asana client, happy path

**Tasks**
- [x] `internal/asana`: build a resty client with base URL, Bearer PAT, timeout and `Accept: application/json`.
- [x] Define `User` and `Project` structs with only the stored fields.
- [x] Implement `ListUsers(ctx)` and `ListProjects(ctx)` using `workspace`, `opt_fields` and `limit=100`.
- [x] Implement pagination by following `next_page.offset` until it is empty.
- [x] Define the small interface the extractor will consume (declared in the extractor package).

**Tests** (`httptest.Server`)
- Auth header and query params are correct.
- Single page and multi-page pagination.
- The Asana `{"data": [...]}` envelope is decoded.
- Context cancellation aborts the request.

**Done when:** the client returns complete lists from a fake multi-page server.

---

## Milestone 3: Retries and error handling

**Tasks**
- [x] Configure resty retries with backoff for network errors, 429 (honouring `Retry-After`) and 5xx.
- [x] Never retry other 4xx; treat 401 as an invalid or revoked PAT with a clear, token-free error.
- [x] Make the retry count and wait times configurable so tests run fast.
- [x] Wrap errors with `%w` and add typed or sentinel errors where useful (e.g. `ErrUnauthorized`).
- [x] Ensure the PAT never appears in errors or logs, including resty debug output.

**Tests**
- Retry then success on 500, and on 429 with `Retry-After`.
- Gives up after max retries.
- No retry on 400, 401, 403 or 404.
- Token absent from error strings and logs.

**Done when:** all failure modes behave as specified without real sleeps or slow tests.

---

## Milestone 4: JSON storage

**Tasks**
- [x] `internal/storage`: write separate indented JSON files: `users.json` with `{ "fetched_at", "users": [] }` and `projects.json` with `{ "fetched_at", "projects": [] }`, sharing one timestamp.
- [x] Stage, `fsync` and close both temporary files in the output directory before publishing either file. Atomically rename each, users first, projects second; the pair is not transactional.
- [x] Create the output directory if missing.
- [x] Always emit `[]` rather than `null` for empty lists.
- [x] Clean up all unpublished temporary files on failure.

**Tests**
- Content, shape and entity separation of both files.
- Overwrite behaviour.
- A staging failure leaves both previous files intact; a publication failure preserves each file's completeness and may leave different timestamps.
- Directory creation and empty-list handling.

**Done when:** neither output file is ever partially written, even if the process is interrupted mid-write. A failure or interruption between renames may leave different timestamps.

---

## Milestone 5: Extraction run

**Tasks**
- [x] `internal/extractor`: `Run(ctx)` fetches users and projects concurrently (`errgroup`) and then calls the writer once to publish the two separate files.
- [x] If either fetch fails, write nothing, keep both previous files and return a wrapped error.
- [x] Log run start, duration and counts using `log/slog`.
- [x] Inject the client, the writer and a clock via small interfaces.

**Tests** (with fakes)
- Success writes each list to its own file with the same timestamp.
- A users failure or a projects failure leaves both previous files untouched.
- Counts are logged and the PAT is never logged.

**Done when:** a single run is correct and side-effect-safe.

---

## Milestone 6: Scheduler and graceful shutdown

**Tasks**
- [ ] Implement a scheduler that runs once immediately, then on each tick of the configured interval.
- [ ] Inject a ticker/clock abstraction so tests never sleep.
- [ ] Prevent overlapping runs: if a run is still going when a tick fires, skip that tick and log it.
- [ ] A failed run is logged and the loop continues.
- [ ] Stop cleanly when the context is cancelled, waiting for any in-flight run to finish or abort.

**Tests**
- Immediate first run and the correct number of runs for N ticks.
- No overlap under a slow run.
- Continues after an error.
- Stops on context cancel with no goroutine leaks (run with `-race`).

**Done when:** both the 5-minute and 30-second modes work identically except for the interval.

---

## Milestone 7: Wire everything in `main`

**Tasks**
- [ ] Parse flags and load config, then build the client, writer, extractor and scheduler.
- [ ] Set up `signal.NotifyContext` for SIGINT and SIGTERM.
- [ ] `--once` runs a single extraction and exits with code 0 on success and non-zero on failure.
- [ ] Log the startup config with the PAT masked.
- [ ] Use sensible exit codes.

**Tests**
- Test the wiring and flag-to-interval mapping where practical, e.g. an `internal/app` or `run()` function that is testable.

**Done when:** `make run` works against a fake or real Asana workspace.

---

## Milestone 8: Test hardening

**Tasks**
- [ ] Add an end-to-end test using `httptest.Server` as fake Asana: the full flow from config to separate real `users.json` and `projects.json` files in `t.TempDir()`.
- [ ] Cover edge cases: empty workspace, large multi-page data, malformed JSON from the API, and a slow server hitting the timeout.
- [ ] Confirm that no test uses the real network or real clock sleeps.
- [ ] Review coverage and fill gaps (`go test ./... -race -cover`).
- [ ] Run the tests several times (`-count=5`) to catch flakiness.

**Done when:** the suite is deterministic, fast, and covers every behavior listed in AGENTS.md.

---

## Milestone 9: Docs and manual local verification

**Tasks**
- [ ] Write the README: purpose, requirements, how to generate a PAT in the Asana developer console, `.env` setup, how to find the workspace GID, and how to run both modes (`--interval=5m` and `--interval=30s`) plus `--once`.
- [ ] Document both output JSON formats with samples, their per-file atomicity, and how to run the tests.
- [ ] Verify `.env.example` is current and contains no real secrets.
- [ ] Manual smoke test (done by the project owner with a real PAT): `--once`, then 30s mode for a few cycles, then Ctrl+C to confirm graceful shutdown and valid separate `users.json` and `projects.json` files.
- [ ] Final cleanup: remove dead code, TODOs and unused dependencies (`go mod tidy`).

**Done when:** a new developer can clone the repo, follow the README, and get a working extraction in under 10 minutes.
