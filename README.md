# ingest-service — practice scaffold

A small, production-shaped **data-ingestion REST API** in Go. Built as a
rehearsal target for the DigitalOcean live coding exercise (build a REST
service, test it, deploy it to DO in 3 hours). Treat this as the *reference
shape* to reproduce from memory — not something to copy in the room.

> The real exercise will hand you different requirements. The value here is the
> **skeleton and the muscle memory**: package layout, validation, async
> processing, tests, container, CI, deploy. Reproduce this in ~20 minutes and
> you can spend the interview on their actual problem.

> **Note:** this directory lives *inside* the job-hunt repo for reference. To
> actually run it (and have GitHub Actions pick up `.github/workflows/ci.yml`),
> **copy `practice-scaffold/` out to its own repo root** — `cp -r` it elsewhere
> and `git init`. A nested workflow won't run in the parent repo.

## What it does

Ingests JSON events, validates them, processes them asynchronously through a
worker pool, and exposes the results.

| Method | Path | Purpose |
|--------|------|---------|
| `POST` | `/v1/events` | Validate + enqueue an event → `202` with `pending` event |
| `GET`  | `/v1/events/{id}` | Fetch one event and its processing result |
| `GET`  | `/v1/events?status=&limit=&offset=` | List events (filter + paginate) |
| `GET`  | `/v1/stats` | Aggregate counts by status and type |
| `GET`  | `/healthz` | Liveness probe |
| `GET`  | `/readyz` | Readiness probe (gates traffic on startup/shutdown) |
| `GET`  | `/metrics` | Prometheus metrics |

## Architecture (and why each piece scores points)

```
cmd/server        wiring + graceful shutdown
internal/config   env-based config, fail-fast validation     → configurability
internal/api      handlers, middleware, router, metrics      → engineering quality + observability
internal/ingest   validation + async worker pool (the core)  → async processing, testability
internal/store    Store interface + in-memory impl           → swappable persistence
```

Design decisions that map to the DO rubric:

- **`Store` is an interface.** In-memory impl ships now; MySQL/Postgres is one
  new file with zero handler changes. This is the headline "with more time"
  upgrade.
- **Async pipeline with backpressure.** `POST` returns `202` immediately and
  enqueues; if the queue is full it returns `503 + Retry-After` instead of
  blocking. Workers drain on shutdown.
- **Graceful shutdown.** SIGTERM → stop accepting → drain HTTP → drain workers,
  bounded by `SHUTDOWN_TIMEOUT`. Matches the K8s `terminationGracePeriod`.
- **Validation returns `422` with field-level `details`** — never a `500` on
  bad input.
- **Observability built in:** structured JSON logs with request ids,
  Prometheus `http_requests_total` + latency histogram, split liveness/readiness.

## Run it

```bash
make run          # or: go run ./cmd/server
make test         # go test -race ./...
make cover        # coverage report
make docker-run   # build image + run in a container
make smoke        # fire a sample request at a running server
```

Config (all optional, env vars): `PORT`, `LOG_LEVEL`, `WORKER_COUNT`,
`QUEUE_SIZE`, `READ_TIMEOUT`, `WRITE_TIMEOUT`, `SHUTDOWN_TIMEOUT`.

## Deploy to DigitalOcean

Two paths in `deploy/` — see `../14-Coding-Interview-Live-Exercise.md` for the
full playbook and time budget.

- **`app-platform.yaml`** — primary, low-risk: `doctl apps create --spec deploy/app-platform.yaml`.
- **`k8s/`** — the Kubernetes signal for this role: Deployment + Service + HPA
  for DOKS. `kubectl apply -f deploy/k8s/`.

## With more time (say this out loud in the interview)

- Swap in-memory store for **MySQL** (the JD names it) behind the same interface.
- Add **OpenTelemetry tracing** + propagate the request id as a trace id.
- **Rate limiting** + auth middleware on the write path.
- **Idempotency keys** so retried POSTs don't double-ingest.
- Replace the in-process queue with a real broker (NATS/Redis/Kafka) for
  durability across restarts.
- Contract tests + load test (`k6`/`vegeta`) to validate the HPA thresholds.
