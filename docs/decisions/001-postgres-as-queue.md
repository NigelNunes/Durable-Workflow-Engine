# 001. Postgres as state store and task queue

## Status

Accepted

## Context

The engine needs durable workflow state and a work queue workers can dequeue from safely under concurrency.

## Decision

Use a single Postgres database for workflows, event history, timers, worker registration, and the task queue. Workers dequeue with `UPDATE … FOR UPDATE SKIP LOCKED`.

## Consequences

- One system to operate and back up.
- Row locks and `SKIP LOCKED` prevent two workers from leasing the same task.
- Throughput is limited by Postgres; sharding or a separate broker would be future work.
- Engine logic stays the single writer; workers never talk to Postgres directly.
