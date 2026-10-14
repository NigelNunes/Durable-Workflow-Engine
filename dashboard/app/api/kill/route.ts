import { engineFetch, unavailable } from "@/lib/engine";

export const dynamic = "force-dynamic";

export async function POST() {
  try {
    const res = await engineFetch("/v1/admin/chaos", {
      method: "POST",
      admin: true,
      body: JSON.stringify({ action: "crash" }),
    });
    const body = await res.json().catch(() => ({ error: "chaos failed" }));
    return Response.json(body, { status: res.status });
  } catch {
    return unavailable();
  }
}
