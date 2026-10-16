# 005. Lease expiry does not consume a retry attempt

## Status

Accepted

## Context

Activities have a bounded retry policy for application failures. If lease expiry also burned an attempt, a flaky network or short TTL could exhaust retries without the activity function ever returning an error.

## Decision

Application failures (activity returns an error the engine classifies as retryable) decrement remaining attempts and schedule backoff. Lease expiry / worker crash returns the task to `pending` with a new fencing token and does **not** count as an attempt.

## Consequences

- Crash recovery stays orthogonal to business retries.
- Poison activities that crash the process still hit `MAX_REDELIVERIES` (redelivery cap), which is separate from the activity retry policy.
- Operators can set short `LEASE_TTL` for fast failover without starving legitimate retries.
