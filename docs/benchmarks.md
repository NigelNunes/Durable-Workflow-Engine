# Benchmarks

Measured numbers only. Reproduce with Postgres up and:

```bash
make up && make migrate
DATABASE_URL=postgres://durable:durable@localhost:5433/durable?sslmode=disable \
  go run ./bench -mode=all -lease-ttl=1s
```

Or `make bench`. The load generator lives in `bench/` (HTTP client + subprocess engine/workers). It does not import `internal/`. Order runs use `pace_seconds: 0` and demo-services `LATENCY=0` so Sleep/HTTP lag do not dominate. Dispatch latency is `now - tasks.created_at` sampled when a worker leases a task.

## Machine

- Go: go1.27.2
- OS/Arch: darwin/arm64
- CPUs: 10
- uname: Darwin NIGELs-MacBook-Air.local 27.0.0 Darwin Kernel Version 27.0.0: Tue Aug 11 21:02:57 PDT 2026; root:xnu-13432.1.9~1/RELEASE_ARM64_T8132 arm64
- RAM: 16.0 GiB
- Postgres: local Compose on host port 5433 (defaults)
- Engine `LEASE_TTL`: **1s** for all experiments below (documented; production default is 10s)
- Run date: 2026-10-16

## Throughput

Completed workflows per second. Median of 3 runs.

| Scenario | Workers | Workflows | Median wf/s | Spread (min–max) |
|----------|---------|-----------|-------------|------------------|
| bench_noop | 1 | 80 | 111.52 | 104.98–115.30 |
| bench_noop | 3 | 80 | 165.77 | 163.50–166.62 |
| bench_noop | 6 | 80 | 192.59 | 192.54–208.22 |
| order_fulfilment | 1 | 40 | 75.12 | 72.54–78.30 |
| order_fulfilment | 3 | 40 | 107.01 | 96.79–109.84 |
| order_fulfilment | 6 | 40 | 134.96 | 129.38–135.46 |

## Latency and dispatch

End-to-end: client start timestamp → terminal history event. Dispatch: pending → leased. Three runs; values are medians across runs.

### bench_noop, 3 workers (50 workflows/run)

| Metric | Median across runs | Spread |
|--------|--------------------|--------|
| e2e p50 (ms) | 195 | 186–198 |
| e2e p95 (ms) | 208 | 195–209 |
| e2e p99 (ms) | 209 | 197–209 |
| dispatch p50 (ms) | 41.8 | 37.2–44.0 |
| dispatch p99 (ms) | 52.0 | 51.7–55.5 |

### order_fulfilment, 3 workers (40 workflows/run)

| Metric | Median across runs | Spread |
|--------|--------------------|--------|
| e2e p50 (ms) | 268 | 258–280 |
| e2e p95 (ms) | 282 | 263–288 |
| e2e p99 (ms) | 283 | 265–289 |
| dispatch p50 (ms) | 41.3 | 38.0–43.8 |
| dispatch p99 (ms) | 49.9 | 45.9–52.6 |

## Recovery time

120 `bench_noop` workflows with `BENCH_SLOW_ACTIVITY_MS=1500` so kills hit in-flight activities. 100 forced worker SIGKILL + replace. Measure: lease-expiry event → next `ActivityCompleted` or `WorkflowCompleted`.

| Stat | ms |
|------|-----|
| Samples | 184 |
| Min | 1503 |
| Median | 3507 |
| p95 | 7613 |
| Max | 10624 |

Expectation: on the order of `LEASE_TTL` (1s) plus poll/reaper delay; medians above that include activity remaining work after the replacement worker leases.

## Correctness under chaos

200 `order_fulfilment` workflows, 3 workers, kill+replace one every 10s, `LATENCY=0`, `LEASE_TTL=1s`.

| Check | Result |
|-------|--------|
| Completed | 200 / 200 |
| Failed | 0 |
| Lost | 0 |
| Duplicate charges | 0 |

Chaos suite F1–F12 (Phase 7): 20/20 each — see [guarantees.md](guarantees.md).

## Database footprint

One completed workflow; `pg_column_size` sum of workflow row + events + tasks.

| Scenario | Rows | Approx bytes |
|----------|------|--------------|
| bench_noop | 12 | 2256 |
| order_fulfilment | 16 | 4160 |
