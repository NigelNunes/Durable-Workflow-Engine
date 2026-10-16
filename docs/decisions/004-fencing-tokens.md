# 004. Fencing tokens on task leases

## Status

Accepted

## Context

A worker can die after performing an activity side effect, or stall long enough for its lease to expire. Another worker may then lease the same task. The first worker might still wake up and report a result.

## Decision

Every lease carries a monotonic `lease_token`. Completions, heartbeats, and failures must present the current token while the task status is `leased`. Mismatches return HTTP 409 and have no effect; the engine records `StaleCompletionRejected`.

## Consequences

- A stale worker cannot overwrite a newer worker’s progress.
- Combined with activity idempotency keys, duplicate side effects stay suppressed at the external service even if the activity runs twice.
- Callers must treat 409 as “someone else owns this attempt,” not as a transient retry of the same lease.
