import { engineFetch, unavailable } from "@/lib/engine";

export const dynamic = "force-dynamic";

const scenarios = ["order_fulfilment", "user_onboarding", "money_transfer", "report_pipeline"] as const;

type Scenario = (typeof scenarios)[number];

export async function POST(request: Request) {
  const body = (await request.json().catch(() => ({}))) as { type?: string };
  const type = body.type ?? "order_fulfilment";
  if (!scenarios.includes(type as Scenario)) {
    return Response.json({ error: "unknown scenario", code: "bad_request" }, { status: 400 });
  }
  const stamp = Date.now();
  const prefix = prefixFor(type as Scenario);
  let started = 0;
  try {
    for (let i = 1; i <= 12; i++) {
      const id = `${prefix}-${stamp}-${i}`;
      const res = await engineFetch("/v1/workflows", {
        method: "POST",
        body: JSON.stringify({ id, type, input: inputFor(type as Scenario, id) }),
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

function prefixFor(type: Scenario): string {
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

function inputFor(type: Scenario, id: string): Record<string, unknown> {
  switch (type) {
    case "user_onboarding":
      return { user_id: id, email: "new@example.com", delay_seconds: 30 };
    case "money_transfer":
      return { transfer_id: id, from_account: "alice", to_account: "bob", amount_cents: 1000 };
    case "report_pipeline":
      return { report_id: id };
    default:
      return { order_id: id, amount_cents: 2500, sku: "widget", qty: 1, email: "buyer@example.com" };
  }
}
