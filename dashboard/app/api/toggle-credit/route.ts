import { servicesURL, unavailable } from "@/lib/engine";

export const dynamic = "force-dynamic";

export async function POST(request: Request) {
  const body = (await request.json().catch(() => ({}))) as { enabled?: boolean };
  const enabled = Boolean(body.enabled);
  let res: Response;
  try {
    res = await fetch(servicesURL("/admin/credit/fail"), {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ enabled }),
      cache: "no-store",
    });
  } catch {
    return unavailable();
  }
  if (res.status !== 204) {
    return Response.json({ error: "credit toggle failed", code: "credit" }, { status: res.status || 502 });
  }
  return Response.json({ enabled });
}
