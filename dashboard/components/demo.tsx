"use client";

import Link from "next/link";
import { useRouter, useSearchParams } from "next/navigation";
import { Suspense, useEffect, useRef, useState } from "react";
import { formatRecovery, messageFor } from "@/lib/messages";
import {
  BATCH_OPTIONS,
  DEFAULT_BATCH,
  isScenarioType,
  scenarioByType,
  scenarios,
  type ScenarioType,
  typeLabel,
} from "@/lib/scenarios";
import type { ApiFailure, DemoState, WorkerRow, WorkflowRow } from "@/lib/types";

const empty: DemoState = {
  workers: [],
  workflows: [],
  counters: {
    running: 0,
    completed: 0,
    failed: 0,
    recovered_after_crash: 0,
    stale_completions_rejected: 0,
    avg_recovery_ms: 0,
  },
  duplicate_charges: 0,
  duplicate_requests_blocked: 0,
  total_charges: 0,
  payments_failing: false,
  credit_failing: false,
};

function newerHeartbeat(worker: WorkerRow, current: WorkerRow): boolean {
  const beat = Date.parse(worker.last_heartbeat ?? "");
  const currentBeat = Date.parse(current.last_heartbeat ?? "");
  return !Number.isNaN(beat) && (Number.isNaN(currentBeat) || beat >= currentBeat);
}

function visibleWorkers(workers: WorkerRow[], killed: ReadonlySet<string>): WorkerRow[] {
  const latestAlive = new Map<string, WorkerRow>();
  const latestDown = new Map<string, WorkerRow>();
  const justKilled = new Map<string, WorkerRow>();
  for (const worker of workers) {
    const shown = killed.has(worker.id) ? { ...worker, alive: false } : worker;
    if (killed.has(worker.id)) {
      const previous = justKilled.get(shown.name);
      if (!previous || newerHeartbeat(shown, previous)) {
        justKilled.set(shown.name, shown);
      }
    }
    const bucket = shown.alive ? latestAlive : latestDown;
    const current = bucket.get(shown.name);
    if (!current || newerHeartbeat(shown, current)) {
      bucket.set(shown.name, shown);
    }
  }
  const rows: WorkerRow[] = [];
  for (const name of [...new Set([...latestAlive.keys(), ...latestDown.keys(), ...justKilled.keys()])].sort()) {
    const alive = latestAlive.get(name);
    const down = justKilled.get(name) ?? latestDown.get(name);
    if (alive) {
      rows.push(alive);
    }
    if (down && down.id !== alive?.id) {
      rows.push(down);
    }
  }
  return rows;
}

function DemoInner() {
  const router = useRouter();
  const search = useSearchParams();
  const queryScenario = search.get("scenario");
  const scenario: ScenarioType = isScenarioType(queryScenario) ? queryScenario : "order_fulfilment";

  const [state, setState] = useState<DemoState>(empty);
  const [notice, setNotice] = useState("");
  const [wake, setWake] = useState(false);
  const [seen, setSeen] = useState(false);
  const [batch, setBatch] = useState<(typeof BATCH_OPTIONS)[number]>(DEFAULT_BATCH);
  const [filter, setFilter] = useState("all");
  const [killed, setKilled] = useState<string[]>([]);
  const [workerSlots, setWorkerSlots] = useState<{ count: number; max: number; error?: string }>({ count: 0, max: 3 });
  const noticeTimer = useRef(0);

  function tell(message: string) {
    window.clearTimeout(noticeTimer.current);
    setNotice(message);
    noticeTimer.current = window.setTimeout(() => setNotice(""), 4000);
  }

  function selectScenario(type: ScenarioType) {
    router.replace(`/demo?scenario=${type}`, { scroll: false });
  }

  async function load(first: boolean) {
    const started = performance.now();
    try {
      const res = await fetch("/api/state", { cache: "no-store" });
      if (!res.ok) {
        if (first || performance.now() - started > 2000) {
          setWake(true);
        }
        const body = (await res.json().catch(() => ({}))) as ApiFailure;
        tell(messageFor(body.code));
        return;
      }
      setState((await res.json()) as DemoState);
      setWake(false);
    } catch {
      if (first) {
        setWake(true);
      }
      tell(messageFor("unavailable"));
    } finally {
      if (first) {
        setSeen(true);
      }
    }
  }

  async function loadWorkers() {
    try {
      const res = await fetch("/api/workers", { cache: "no-store" });
      const body = (await res.json().catch(() => ({}))) as {
        count?: number;
        max?: number;
        error?: string;
      };
      setWorkerSlots({
        count: body.count ?? 0,
        max: body.max ?? 3,
        error: res.ok ? undefined : body.error,
      });
    } catch {
      setWorkerSlots((current) => ({
        ...current,
        error: "Docker is not available. Start with: docker compose --profile demo up",
      }));
    }
  }

  useEffect(() => {
    const timer = window.setTimeout(() => {
      void load(true);
      void loadWorkers();
    }, 0);
    const poll = window.setInterval(() => {
      if (!document.hidden) {
        void load(false);
      }
    }, 1000);
    const onVisible = () => {
      if (!document.hidden) {
        void load(false);
      }
    };
    document.addEventListener("visibilitychange", onVisible);
    return () => {
      window.clearTimeout(timer);
      window.clearTimeout(noticeTimer.current);
      window.clearInterval(poll);
      document.removeEventListener("visibilitychange", onVisible);
    };
  }, []);

  async function startScenario() {
    const res = await fetch("/api/start", {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ type: scenario, count: batch }),
    });
    const body = (await res.json().catch(() => ({}))) as ApiFailure & { started?: number };
    if (!res.ok) {
      const why = messageFor(body.code);
      tell(body.started ? `Started ${body.started}. ${why}` : why);
      return;
    }
    const chosen = scenarioByType(scenario);
    tell(`Started ${body.started ?? batch} ${chosen.label.toLowerCase()} run${(body.started ?? batch) === 1 ? "" : "s"}.`);
  }

  async function killWorker() {
    const res = await fetch("/api/kill", { method: "POST" });
    const body = (await res.json().catch(() => ({}))) as ApiFailure & { worker_id?: string };
    if (!res.ok) {
      tell(messageFor(body.code));
      return;
    }
    const workerID = body.worker_id;
    if (workerID) {
      setKilled((ids) => (ids.includes(workerID) ? ids : [...ids, workerID]));
    }
    const name = state.workers.find((worker) => worker.id === workerID)?.name;
    tell(name ? `${name} crashed. Another worker will pick up the work.` : "Worker crash sent.");
  }

  async function changeWorkers(action: "add" | "remove") {
    const res = await fetch("/api/workers", {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ action }),
    });
    const body = (await res.json().catch(() => ({}))) as {
      error?: string;
      code?: string;
      count?: number;
      max?: number;
      service?: string;
    };
    if (!res.ok) {
      tell(body.error ?? messageFor(body.code));
      void loadWorkers();
      return;
    }
    setWorkerSlots({ count: body.count ?? 0, max: body.max ?? 3 });
    tell(action === "add" ? `Added ${body.service}.` : `Removed ${body.service}.`);
    window.setTimeout(() => void load(false), 800);
  }

  async function togglePayments() {
    const enabled = !state.payments_failing;
    const res = await fetch("/api/toggle-payments", {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ enabled }),
    });
    if (!res.ok) {
      tell(messageFor("payments"));
      return;
    }
    setState((current) => ({ ...current, payments_failing: enabled }));
    tell(enabled ? "Payments will fail until you turn this off." : "Payments are healthy again.");
  }

  async function toggleCredit() {
    const enabled = !state.credit_failing;
    const res = await fetch("/api/toggle-credit", {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ enabled }),
    });
    if (!res.ok) {
      tell(messageFor("credit"));
      return;
    }
    setState((current) => ({ ...current, credit_failing: enabled }));
    tell(enabled ? "Credit will fail until you turn this off." : "Credit is healthy again.");
  }

  const total = state.counters.running + state.counters.completed + state.counters.failed;
  const aliveByID = new Map(state.workers.map((worker) => [worker.id, worker.alive]));
  const killedSet = new Set(killed);
  const workers = visibleWorkers(state.workers, killedSet);
  const shown = state.workflows.filter((workflow) => filter === "all" || workflow.type === filter);
  const chosen = scenarioByType(scenario);
  const aliveCount = workers.filter((w) => w.alive && !killedSet.has(w.id)).length;

  return (
    <main className="demo-page">
      <header className="demo-intro">
        <div className="demo-intro-copy">
          <p className="kicker">Live demo</p>
          <h1>
            Crash a worker.
            <br />
            <em>Watch the work finish.</em>
          </h1>
        </div>
        <p className="lead">
          Run a real business flow, kill a process mid-flight, and see another worker finish it from the Postgres log.
        </p>
      </header>

      {wake ? (
        <p className="demo-alert">Engine is waking up — first load can take up to a minute. Need: docker compose --profile demo up</p>
      ) : null}
      {notice ? <p className="demo-flash">{notice}</p> : null}

      <section className="demo-scene" aria-label="Scenario">
        <div className="demo-scene-tabs" role="tablist" aria-label="Scenarios">
          {scenarios.map((item) => (
            <button
              key={item.type}
              type="button"
              role="tab"
              aria-selected={scenario === item.type}
              className={scenario === item.type ? "demo-tab on" : "demo-tab"}
              onClick={() => selectScenario(item.type)}
            >
              {item.label}
            </button>
          ))}
        </div>

        <div className="demo-scene-body">
          <div className="demo-scene-copy">
            <h2>{chosen.label}</h2>
            <p>{chosen.pitch}</p>
            <ol className="demo-steps">
              {chosen.pitch.split("→").map((step) => (
                <li key={step.trim()}>{step.trim()}</li>
              ))}
            </ol>
          </div>
          <div className="demo-scene-actions">
            <label className="demo-field">
              <span>How many</span>
              <select
                aria-label="How many to run"
                value={batch}
                onChange={(event) => setBatch(Number(event.target.value) as (typeof BATCH_OPTIONS)[number])}
              >
                {BATCH_OPTIONS.map((n) => (
                  <option key={n} value={n}>
                    {n}
                  </option>
                ))}
              </select>
            </label>
            <button className="demo-run" type="button" onClick={() => void startScenario()}>
              {chosen.button}
            </button>
          </div>
        </div>
      </section>

      <section className="demo-chaos" aria-label="Break something">
        <div className="demo-chaos-copy">
          <strong>Break something</strong>
          <p>This is the pitch moment — crash a worker or fail a dependency while work is still running.</p>
        </div>
        <div className="demo-chaos-actions">
          <button className="demo-kill" type="button" onClick={() => void killWorker()}>
            Kill a worker
          </button>
          <button
            type="button"
            className={state.payments_failing ? "demo-toggle on" : "demo-toggle"}
            aria-pressed={state.payments_failing}
            onClick={() => void togglePayments()}
          >
            {state.payments_failing ? "Payments broken" : "Break payments"}
          </button>
          <button
            type="button"
            className={state.credit_failing ? "demo-toggle on" : "demo-toggle"}
            aria-pressed={state.credit_failing}
            onClick={() => void toggleCredit()}
          >
            {state.credit_failing ? "Credit broken" : "Break credit"}
          </button>
        </div>
      </section>

      <section className="demo-board" aria-label="Live board">
        <div className="demo-stats" aria-label="Results">
          <div>
            <strong>
              {state.counters.completed}/{total || 0}
            </strong>
            <span>finished</span>
          </div>
          <div>
            <strong>{state.counters.recovered_after_crash}</strong>
            <span>recovered after crash</span>
          </div>
          <div>
            <strong className={state.duplicate_charges === 0 ? "ok" : undefined}>{state.duplicate_charges}</strong>
            <span>duplicate charges</span>
          </div>
          <div>
            <strong>{state.duplicate_requests_blocked}</strong>
            <span>duplicates blocked</span>
          </div>
          <div>
            <strong>{formatRecovery(state.counters.avg_recovery_ms)}</strong>
            <span>avg recovery</span>
          </div>
        </div>

        <div className="demo-board-split">
          <div className="demo-col demo-col-workers">
            <div className="demo-workers-head">
              <div>
                <h3>Workers</h3>
                <p className="muted">
                  {aliveCount} alive · {workerSlots.count}/{workerSlots.max} slots
                  {workerSlots.error ? ` · ${workerSlots.error}` : null}
                </p>
              </div>
              <div className="demo-worker-tools">
                <button type="button" onClick={() => void changeWorkers("add")} disabled={workerSlots.count >= workerSlots.max}>
                  Add
                </button>
                <button type="button" onClick={() => void changeWorkers("remove")} disabled={workerSlots.count <= 1}>
                  Remove
                </button>
              </div>
            </div>

            <ul className="demo-worker-list">
              {workers.map((worker) => {
                const status = killedSet.has(worker.id) ? "killed" : worker.alive ? "alive" : "down";
                return (
                  <li key={worker.id} className={`demo-worker ${status}`}>
                    <span className="demo-worker-dot" aria-hidden="true" />
                    <span className="demo-worker-name">{worker.name}</span>
                    <span className="demo-worker-meta">
                      {status} · {worker.running_tasks} running
                    </span>
                  </li>
                );
              })}
              {seen && workers.length === 0 ? <li className="muted demo-worker-empty">No workers registered yet.</li> : null}
            </ul>
          </div>

          <div className="demo-col demo-col-runs">
            <div className="demo-runs-head">
              <h3>Runs</h3>
              <div className="demo-filters" role="group" aria-label="Filter runs">
                <button type="button" aria-pressed={filter === "all"} onClick={() => setFilter("all")}>
                  All
                </button>
                {scenarios.map((item) => (
                  <button
                    key={item.type}
                    type="button"
                    aria-pressed={filter === item.type}
                    onClick={() => setFilter(item.type)}
                  >
                    {item.short}
                  </button>
                ))}
              </div>
            </div>

            <div className="demo-runs">
              {shown.map((workflow) => (
                <WorkflowRow
                  key={workflow.id}
                  workflow={workflow}
                  ownerAlive={workflow.owner_worker_id ? aliveByID.get(workflow.owner_worker_id) : undefined}
                />
              ))}
              {seen && filter !== "all" && shown.length === 0 ? (
                <p className="muted demo-runs-empty">No {typeLabel(filter)} runs in the latest batch.</p>
              ) : null}
              {seen && filter === "all" && shown.length === 0 ? (
                <p className="muted demo-runs-empty">No runs yet. Pick a scenario above and start one.</p>
              ) : null}
            </div>
          </div>
        </div>
      </section>
    </main>
  );
}

export function Demo() {
  return (
    <Suspense fallback={<main className="demo-page" />}>
      <DemoInner />
    </Suspense>
  );
}

function WorkflowRow({ workflow, ownerAlive }: { workflow: WorkflowRow; ownerAlive: boolean | undefined }) {
  const ownerID = workflow.owner_worker_id ?? "";
  const waiting = workflow.status === "running" && (ownerID === "" || ownerAlive !== true);
  const chosen = scenarioByType(workflow.type);
  const steps = Math.max(chosen.steps, workflow.total_steps, workflow.step_index + 1, 1);
  const filled = workflow.status === "completed" ? steps : Math.min(steps, workflow.step_index + 1);
  const status =
    workflow.status === "failed"
      ? "failed"
      : waiting
        ? "waiting"
        : workflow.resumed
          ? "resumed"
          : workflow.status === "completed"
            ? "done"
            : "running";
  const detail =
    workflow.status === "running"
      ? `${workflow.owner_worker || "unassigned"} · ${workflow.current_step || "starting"}`
      : workflow.current_step || "—";

  return (
    <Link className={`demo-run-row ${status}`} href={`/workflows/${encodeURIComponent(workflow.id)}`}>
      <div className="demo-run-main">
        <span className="demo-run-type">{typeLabel(workflow.type)}</span>
        <strong>{workflow.id}</strong>
        <span className="demo-run-detail">{detail}</span>
      </div>
      <div className="demo-run-side">
        <span className={`demo-run-status ${status}`}>{status}</span>
        <span className="demo-run-count">
          {filled}/{steps}
        </span>
        <span className="demo-run-bar" aria-hidden="true">
          <span style={{ width: `${(filled / steps) * 100}%` }} />
        </span>
      </div>
    </Link>
  );
}
