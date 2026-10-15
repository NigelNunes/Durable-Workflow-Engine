import { engineFetch, unavailable } from "@/lib/engine";
import {
  DEFAULT_BATCH,
  ONBOARDING_DELAY_SECONDS,
  PACE_SECONDS,
  type ScenarioType,
  isScenarioType,
} from "@/lib/scenarios";

export const dynamic = "force-dynamic";

export async function POST(request: Request) {
  const body = (await request.json().catch(() => ({}))) as { type?: string; count?: number };
  const type = body.type ?? "order_fulfilment";
  if (!isScenarioType(type)) {
    return Response.json({ error: "unknown scenario", code: "bad_request" }, { status: 400 });
  }
  const count = clampCount(body.count);
  const stamp = Date.now();
  const prefix = prefixFor(type);
  let started = 0;
  try {
    for (let i = 1; i <= count; i++) {
      const id = `${prefix}-${stamp}-${i}`;
      const res = await engineFetch("/v1/workflows", {
        method: "POST",
        body: JSON.stringify({ id, type, input: inputFor(type, id) }),
      });
      if (!res.ok) {
        const failure = (await res.json().catch(() => ({}))) as { error?: string; code?: string };
        return Response.json({ started, error: failure.error, code: failure.code }, { status: res.status });
      }
      started++;
    }
  } catch {
    if (started > 0) {
      return Response.json({ started, error: "demo engine is not running", code: "unavailable" }, { status: 503 });
    }
    return unavailable();
  }
  return Response.json({ started });
}

function clampCount(value: number | undefined): number {
  if (typeof value !== "number" || !Number.isFinite(value)) {
    return DEFAULT_BATCH;
  }
  return Math.min(5, Math.max(1, Math.round(value)));
}

function prefixFor(type: ScenarioType): string {
  switch (type) {
    case "user_onboarding":
      return "onb";
    case "money_transfer":
      return "xfer";
    case "report_pipeline":
      return "rpt";
    default:
      return "ord";
  }
}

function inputFor(type: ScenarioType, id: string): Record<string, unknown> {
  switch (type) {
    case "user_onboarding":
      return { user_id: id, email: "new@example.com", delay_seconds: ONBOARDING_DELAY_SECONDS };
    case "money_transfer":
      return {
        transfer_id: id,
        from_account: "alice",
        to_account: "bob",
        amount_cents: 1000,
        pace_seconds: PACE_SECONDS,
      };
    case "report_pipeline":
      return { report_id: id, pace_seconds: PACE_SECONDS };
    default:
      return {
        order_id: id,
        amount_cents: 2500,
        sku: "widget",
        qty: 1,
        email: "buyer@example.com",
        pace_seconds: PACE_SECONDS,
      };
  }
}
