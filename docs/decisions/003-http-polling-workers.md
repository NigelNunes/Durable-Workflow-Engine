# 003. Workers poll the engine over HTTP

## Status

Accepted

## Context

Workers could open Postgres connections and claim tasks themselves, or receive pushed work from the engine.

## Decision

Workers register and poll the engine over HTTP/JSON with long-polling. The engine is the only process that writes durable state. Push/gRPC is out of scope for v1.

## Consequences

- Invariants live in one place (the engine).
- Dashboard and workers share the same API surface.
- Extra network hop versus direct DB access.
- Polling load-balances naturally across workers; long-poll avoids busy loops.
- Firewall-friendly for demos and simple deploys.
