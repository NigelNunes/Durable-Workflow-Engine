# Guarantees

## What the engine guarantees

1. **Durable workflow state.** Once the engine accepts a command, it is persisted in Postgres in the same transaction that enqueues follow-up work. A crash of any worker, or of the engine process itself, does not lose workflow progress.
2. **Each activity's result is recorded at most once.** The history never contains two `ActivityCompleted` events for the same command.
3. **Crash recovery.** If a worker dies mid-workflow-task or mid-activity, the lease expires and another worker takes over. Time to recover is bounded by `LEASE_TTL` plus poll interval.
4. **Stale workers cannot corrupt state.** Fencing tokens reject late completions from workers that lost their lease.
5. **Retries with backoff** for failed activities, up to a configured limit.
6. **Durable timers** that fire even if everything restarts during the sleep.
7. **Detection of non-deterministic workflow code** instead of silently producing wrong behavior.

## What it does not guarantee

- **Not exactly-once execution of activities.** It is at-least-once. If a worker crashes after the external side effect happens but before the result is recorded, the activity runs again. The correct claim is "no duplicate side effects when activities honor the idempotency key", not "no duplicate executions".
- **Not highly available.** One Postgres instance is a single point of failure. One engine process is a single point of failure for new task dispatch (workers and history remain safe).
- **Not horizontally scaled engine.** The engine could run multiple replicas (all state is in Postgres and locking is row-based), but v1 targets one.
- **Not production grade.** No auth beyond a shared key, no multi-tenancy, no workflow versioning, no history archival.

## Explicit non-goals for v1

- Multiple language SDKs (Go only).
- Workflow versioning and migration of in-flight workflows.
- Signals, queries, child workflows.
- Parallel activity fan-out (v1 workflows are sequential).
- A clustered engine with sharding.

## Chaos results (Phase 7)

Phase 7 recorded these results. Each scenario ran 20 times against Compose Postgres. Workers and the engine were OS processes. A failed iteration stops that scenario. None failed in the run below.

The fake charge service is in the test process. It is not the Phase 8 demo schema. The same idempotency rule is what the assertions check: one key, one charge.

Command: `DATABASE_URL=postgres://durable:durable@localhost:5433/durable?sslmode=disable go test -p 1 -count=1 -timeout 40m ./...`

The chaos package passed in 462.5 seconds. `go vet ./...` passed.

- **F1. Kill before the charge.** 20/20. The activity signals ready and sleeps. SIGKILL arrives before `POST /charge`. After the lease expires, the replacement worker charges once. Status `completed`. One `ActivityCompleted`. Charges 1. Deduplicated requests 0.
- **F2. Kill after the charge.** 20/20. The activity charges, signals ready, then blocks. SIGKILL. The replacement sends the same idempotency key. Charges 1. Deduplicated requests 1. One `ActivityCompleted`.
- **F3. Kill during a workflow task.** 20/20. `BeforeWorkflowReplay` blocks before any command is sent. SIGKILL. History at that moment is only `WorkflowStarted`. The replacement completes the workflow. One `WorkflowStarted`, one `ActivityScheduled`, one `ActivityCompleted`, one `WorkflowCompleted`.
- **F4. Stall, then a late complete.** 20/20. The victim stops heartbeats and stops leasing more tasks, sleeps 3 seconds, then reports. The other worker completes after the reaper. Result `other`. One `ActivityCompleted`. One `StaleCompletionRejected`.
- **F5. Activity failures.** 20/20. Failure mode on, four attempts, 50ms initial backoff. After two `ActivityRetryScheduled` events, failure mode turns off and the workflow completes with one `ActivityCompleted` and no `ActivityFailed`. A second workflow left in failure mode with two attempts ends `failed` with one `ActivityFailed`.
- **F6. Engine process killed.** 20/20. SIGKILL the engine during an activity, then start it again on the same port. The worker process stays up. The workflow completes. One `WorkflowStarted`, one `ActivityCompleted`, one `WorkflowCompleted`. A lease-expired event is allowed.
- **F7. Postgres restarted.** 20/20. `docker compose restart postgres` while an activity is in progress. The workflow row is still there afterward. The workflow completes. One `WorkflowStarted`, one `ActivityCompleted`, one `WorkflowCompleted`.
- **F8. Engine down past a timer.** 20/20. Sleep is 1 second. The engine is stopped before `fire_at` and started after it. Completion is within 2 seconds of the new process becoming healthy. One `TimerFired`.
- **F9. Stale activity completion.** 20/20. Worker A holds the activity with heartbeats paused. The test expires the lease. Worker B completes with result `from-b`. Worker A's later report is one `StaleCompletionRejected`. One `ActivityCompleted`.
- **F10. Non-deterministic swap.** 20/20. Version 1 is inside activity StepA. Workers then restart on version 2, which runs StepB first. The workflow fails. The error contains `non-determinism`. `WorkflowFailed` has `non_determinism` true. StepB is not in history.
- **F11. Poison activity.** 20/20. The activity calls `os.Exit(1)`. The harness starts a new process so the abandoned lease can be counted. `MAX_REDELIVERIES` is 2. The third expiry writes `ActivityFailed` with `TooManyRedeliveries`. No activity task stays open. The workflow ends `failed`. A later ordinary workflow completes, and the engine is still serving `/healthz`.
- **F12. Load and kill.** 20/20. Three workers, 200 workflows, a charge activity of 150ms. Every 10 seconds one worker is killed and replaced. All 200 complete. Charges 200. `duplicate_charges` 0. No workflow has more than one `ActivityCompleted`. No task left `leased`.
