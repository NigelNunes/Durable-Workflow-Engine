function required(name: string): string {
  const value = process.env[name];
  if (!value) {
    throw new Error(`${name} is required`);
  }
  return value;
}

export function engineURL(path: string): string {
  return new URL(path, required("ENGINE_URL")).toString();
}

export function servicesURL(path: string): string {
  return new URL(path, required("DEMO_SERVICES_URL")).toString();
}

export async function engineFetch(path: string, init: RequestInit & { admin?: boolean } = {}) {
  const headers = new Headers(init.headers);
  headers.set("X-Demo-Key", required("DEMO_KEY"));
  if (init.admin) {
    headers.set("X-Admin-Token", required("ADMIN_TOKEN"));
  }
  if (init.body && !headers.has("Content-Type")) {
    headers.set("Content-Type", "application/json");
  }
  return fetch(engineURL(path), {
    method: init.method,
    body: init.body,
    headers,
    cache: "no-store",
  });
}

export async function readJSON<T>(res: Response): Promise<T> {
  return (await res.json()) as T;
}

export function unavailable(): Response {
  return Response.json({ error: "demo engine is not running", code: "unavailable" }, { status: 503 });
}
