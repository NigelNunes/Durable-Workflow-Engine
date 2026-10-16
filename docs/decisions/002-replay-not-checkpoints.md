# 002. Replay instead of checkpointing process memory

## Status

Accepted

## Context

After a crash, a workflow must resume without re-doing completed steps. Options include dumping process memory, saving a custom checkpoint blob, or replaying deterministic code against an event log.

## Decision

Store an append-only event history. Workers always run workflow functions from the top. The SDK returns recorded results for steps that already completed and only emits commands for unfinished work.

## Consequences

- Workflow code must be deterministic (no `time.Now`, `rand`, UUID, goroutines, or map-order dependence in the workflow function).
- Replay needs only history plus the same code path—no process snapshot format.
- Non-determinism is detected when replay disagrees with history, instead of silently diverging.
- Activity side effects stay outside replay; they run only when scheduled as tasks.
