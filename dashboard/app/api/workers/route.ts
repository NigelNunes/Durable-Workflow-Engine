import { spawn } from "node:child_process";
import path from "node:path";

export const dynamic = "force-dynamic";

const WORKER_SERVICES = ["demo-worker-1", "demo-worker-2", "demo-worker-3"] as const;

type WorkerSlot = (typeof WORKER_SERVICES)[number];

function composeRoot(): string {
  // dashboard/ → repo root
  return path.resolve(process.cwd(), "..");
}

function runCompose(args: string[]): Promise<{ ok: boolean; stdout: string; stderr: string }> {
  return new Promise((resolve) => {
    const child = spawn("docker", ["compose", "--profile", "demo", ...args], {
      cwd: composeRoot(),
      env: process.env,
    });
    let stdout = "";
    let stderr = "";
    child.stdout.on("data", (chunk: Buffer) => {
      stdout += chunk.toString();
    });
    child.stderr.on("data", (chunk: Buffer) => {
      stderr += chunk.toString();
    });
    child.on("error", (err) => {
      resolve({ ok: false, stdout, stderr: err.message });
    });
    child.on("close", (code) => {
      resolve({ ok: code === 0, stdout, stderr });
    });
  });
}

async function listRunningSlots(): Promise<{ running: WorkerSlot[]; error?: string }> {
  const result = await runCompose(["ps", "--status", "running", "--services"]);
  if (!result.ok && !result.stdout.trim()) {
    return {
      running: [],
      error: result.stderr.includes("docker") || result.stderr.includes("ENOENT")
        ? "Docker is not available. Start workers with docker compose --profile demo up."
        : result.stderr || "Could not list workers.",
    };
  }
  const running = result.stdout
    .split("\n")
    .map((line) => line.trim())
    .filter((line): line is WorkerSlot => (WORKER_SERVICES as readonly string[]).includes(line));
  return { running };
}

export async function GET() {
  const { running, error } = await listRunningSlots();
  if (error && running.length === 0) {
    return Response.json({ running: [], count: 0, max: WORKER_SERVICES.length, error, code: "docker_unavailable" }, { status: 503 });
  }
  return Response.json({
    running,
    count: running.length,
    max: WORKER_SERVICES.length,
    slots: WORKER_SERVICES,
  });
}

export async function POST(request: Request) {
  const body = (await request.json().catch(() => ({}))) as { action?: string };
  const action = body.action;
  if (action !== "add" && action !== "remove") {
    return Response.json({ error: "action must be add or remove", code: "bad_request" }, { status: 400 });
  }

  const { running, error } = await listRunningSlots();
  if (error && running.length === 0) {
    return Response.json({ error, code: "docker_unavailable" }, { status: 503 });
  }

  if (action === "add") {
    const next = WORKER_SERVICES.find((slot) => !running.includes(slot));
    if (!next) {
      return Response.json({ error: "All three worker slots are already running.", code: "at_capacity", running }, { status: 409 });
    }
    const up = await runCompose(["up", "-d", "--no-deps", next]);
    if (!up.ok) {
      return Response.json(
        { error: up.stderr || "Failed to start worker.", code: "docker_failed", running },
        { status: 503 },
      );
    }
    const after = await listRunningSlots();
    return Response.json({ action: "add", service: next, running: after.running, count: after.running.length });
  }

  if (running.length <= 1) {
    return Response.json(
      { error: "Keep at least one worker running.", code: "last_worker", running },
      { status: 409 },
    );
  }
  const target = running[running.length - 1];
  const stop = await runCompose(["stop", target]);
  if (!stop.ok) {
    return Response.json(
      { error: stop.stderr || "Failed to stop worker.", code: "docker_failed", running },
      { status: 503 },
    );
  }
  const after = await listRunningSlots();
  return Response.json({ action: "remove", service: target, running: after.running, count: after.running.length });
}
