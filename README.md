# Durable Flow

A small durable workflow engine in Go—the same category as Temporal. You write a multi-step process as ordinary Go. The engine stores an append-only event history in Postgres. Workers are disposable: if one dies, another replays the code from the top and skips steps that already finished.

![Kill a worker, workflows keep going](docs/assets/kill-recover.svg)

## Try it locally

Live deploy of the dashboard is Phase 13. Until then:

```bash
make demo          # Postgres, engine, demo-services, three workers
cd dashboard && npm run dev
```

Open the dashboard, start a batch of orders, click **Kill a worker**, and watch workflows resume. Duplicate charges stay at zero when activities honor idempotency keys.

## The problem and the core idea

Long-running business flows (charge → reserve → email) break when a process crashes mid-way: restart from scratch and you double-charge; do nothing and the order hangs. Teams usually invent status columns and crons per flow.

Durable Flow records every accepted step in an **event history**. On recovery, a worker **replays** deterministic workflow code against that history. Completed activities return their stored result; only unfinished work runs again.

## Architecture

```mermaid
flowchart LR
  client[Client or dashboard] --> engine[Engine HTTP API]
  engine --> pg[(Postgres)]
  worker[Stateless workers] -->|poll heartbeat complete| engine
  worker --> services[Demo services]
  services --> pg
```

Details: [docs/architecture.md](docs/architecture.md).

## Guarantees (honest)

The engine guarantees durable progress, at-most-once recording of each activity result, crash recovery bounded by lease TTL, fencing against stale workers, retries with backoff, durable timers, and non-determinism detection.

It does **not** guarantee exactly-once activity execution, high availability, or production-grade multi-tenancy. Side effects stay correct when activities pass `workflowID:commandSeq` as an idempotency key to the outside service.

Full list: [docs/guarantees.md](docs/guarantees.md).

## Quickstart

```bash
make up && make migrate
# terminal 1
DATABASE_URL=postgres://durable:durable@localhost:5433/durable?sslmode=disable \
  DEMO_KEY=dev go run ./cmd/engine
# terminal 2 — demo HTTP fakes
DATABASE_URL=postgres://durable:durable@localhost:5433/durable?sslmode=disable \
  go run ./cmd/demo-services
# terminal 3
ENGINE_URL=http://127.0.0.1:8080 DEMO_KEY=dev \
  SERVICES_URL=http://127.0.0.1:8081 go run ./cmd/demo-worker
# start an order
curl -s -X POST http://127.0.0.1:8080/v1/workflows \
  -H 'Content-Type: application/json' -H 'X-Demo-Key: dev' \
  -d '{"type":"order_fulfilment","input":{"order_id":"ord-1","amount_cents":2500,"sku":"widget","qty":1,"email":"a@b.c","pace_seconds":0}}'
```

Or use `make demo` and the Next.js UI under `dashboard/`.

## SDK example

```go
func Workflow(ctx workflow.Context, in orders.Input) (orders.Result, error) {
    retry := workflow.ActivityOptions{Retry: workflow.RetryPolicy{
        MaxAttempts: 5, InitialInterval: 500 * time.Millisecond,
        BackoffCoefficient: 2, MaxInterval: 10 * time.Second,
    }}
    charge, err := workflow.Activity[orders.ChargeIn, orders.ChargeOut](
        ctx, "ChargeCard", orders.ChargeIn{OrderID: in.OrderID, AmountCents: in.AmountCents}, retry)
    if err != nil {
        return orders.Result{}, err
    }
    if _, err := workflow.Activity[orders.ReserveIn, orders.ReserveOut](
        ctx, "ReserveStock", orders.ReserveIn{OrderID: in.OrderID, SKU: in.SKU, Qty: in.Qty}, retry); err != nil {
        return orders.Result{}, err
    }
    if _, err := workflow.Activity[orders.ReceiptIn, struct{}](
        ctx, "SendReceipt", orders.ReceiptIn{OrderID: in.OrderID, ChargeID: charge.ChargeID, Email: in.Email}, retry); err != nil {
        return orders.Result{}, err
    }
    return orders.Result{Status: "fulfilled"}, nil
}
```

## Failure scenarios

| Scenario | Pass rate (Phase 7) |
|----------|---------------------|
| F1–F12 chaos suite (20 runs each) | 20/20 each |

See [docs/guarantees.md](docs/guarantees.md) for the full table.

## Benchmarks

On a MacBook Air (10-core, 16 GiB, Compose Postgres, `LEASE_TTL=1s`): trivial two-step workflows peaked at about **193 wf/s** with 6 workers; order fulfilment about **135 wf/s**. Chaos load (200 orders, kill every 10s) finished with **0** duplicate charges and **0** lost workflows. Median recovery after forced kill was about **3.5 s**. Full tables and commands: [docs/benchmarks.md](docs/benchmarks.md).

```bash
make up && make migrate
make bench
```

## Design decisions

- [Postgres as queue](docs/decisions/001-postgres-as-queue.md)
- [Replay, not checkpoints](docs/decisions/002-replay-not-checkpoints.md)
- [HTTP polling workers](docs/decisions/003-http-polling-workers.md)
- [Fencing tokens](docs/decisions/004-fencing-tokens.md)
- [Recovery is not a retry](docs/decisions/005-recovery-not-a-retry.md)

## Limitations and future work

Single Postgres and a single engine process. Go SDK only. No signals, queries, child workflows, versioning, or parallel activity fan-out in v1. Deploy to AWS + Vercel (`nigelnunes.in`) is Phase 13.
