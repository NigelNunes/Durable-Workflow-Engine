import { engineFetch } from "@/lib/engine";

export const dynamic = "force-dynamic";

export async function GET(_request: Request, context: { params: Promise<{ id: string }> }) {
  const { id } = await context.params;
  const res = await engineFetch(`/v1/workflows/${encodeURIComponent(id)}`);
  const body = await res.json().catch(() => ({ error: "workflow unavailable" }));
  return Response.json(body, { status: res.status });
}
