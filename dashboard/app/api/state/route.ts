import { engineFetch, readJSON, servicesURL } from "@/lib/engine";
import type { Counters, DemoState, WorkerRow, WorkflowRow } from "@/lib/types";

export const dynamic = "force-dynamic";

type EngineState = {
  workers: WorkerRow[];
  workflows: WorkflowRow[];
  counters: Counters;
};

type ChargeStats = {
  duplicate_charges: number;
  duplicate_requests_blocked: number;
  total_charges: number;
};

export async function GET() {
  const [engineRes, statsRes, failRes] = await Promise.all([
    engineFetch("/v1/dashboard/state"),
    fetch(servicesURL("/stats"), { cache: "no-store" }),
    fetch(servicesURL("/admin/payments/fail"), { cache: "no-store" }),
  ]);
  if (!engineRes.ok) {
    const body = await engineRes.json().catch(() => ({ error: "engine unavailable" }));
    return Response.json(body, { status: engineRes.status });
  }
  if (!statsRes.ok) {
    return Response.json({ error: "stats unavailable", code: "stats" }, { status: statsRes.status });
  }
  const engine = await readJSON<EngineState>(engineRes);
  const stats = await readJSON<ChargeStats>(statsRes);
  let paymentsFailing = false;
  if (failRes.ok) {
    const fail = await readJSON<{ enabled: boolean }>(failRes);
    paymentsFailing = fail.enabled;
  }
  const state: DemoState = {
    workers: engine.workers ?? [],
    workflows: engine.workflows ?? [],
    counters: engine.counters,
    duplicate_charges: stats.duplicate_charges,
    duplicate_requests_blocked: stats.duplicate_requests_blocked,
    total_charges: stats.total_charges,
    payments_failing: paymentsFailing,
  };
  return Response.json(state);
}
