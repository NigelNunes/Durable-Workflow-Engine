import { engineFetch } from "@/lib/engine";

export const dynamic = "force-dynamic";

export async function POST() {
  const stamp = Date.now();
  let started = 0;
  for (let i = 1; i <= 12; i++) {
    const id = `ord-${stamp}-${i}`;
    const res = await engineFetch("/v1/workflows", {
      method: "POST",
      body: JSON.stringify({
        id,
        type: "order_fulfilment",
        input: {
          order_id: id,
          amount_cents: 2500,
          sku: "widget",
          qty: 1,
          email: "buyer@example.com",
        },
      }),
    });
    if (!res.ok) {
      const body = (await res.json().catch(() => ({}))) as { error?: string; code?: string };
      return Response.json({ started, error: body.error, code: body.code }, { status: res.status });
    }
    started++;
  }
  return Response.json({ started });
}
