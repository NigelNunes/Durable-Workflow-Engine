"use client";

import Link from "next/link";
import { useEffect, useRef, useState } from "react";
import { Stamp } from "@/components/stamp";
import { formatRecovery, messageFor, typeLabel } from "@/lib/messages";
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

const scenarios = [
  { type: "order_fulfilment", label: "Orders", card: "Order", button: "Start 12 orders" },
  { type: "user_onboarding", label: "Onboarding", card: "Onboarding", button: "Start 12 onboarding" },
  { type: "money_transfer", label: "Transfer", card: "Transfer", button: "Start 12 transfers" },
  { type: "report_pipeline", label: "Report", card: "Report", button: "Start 12 reports" },
] as const;

const stepTotals: Record<string, number> = {
  order_fulfilment: 3,
  user_onboarding: 3,
  money_transfer: 2,
  report_pipeline: 4,
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
  const pills: WorkerRow[] = [];
  for (const name of [...new Set([...latestAlive.keys(), ...latestDown.keys(), ...justKilled.keys()])].sort()) {
    const alive = latestAlive.get(name);
    const down = justKilled.get(name) ?? latestDown.get(name);
    if (alive) {
      pills.push(alive);
    }
    if (down && down.id !== alive?.id) {
      pills.push(down);
    }
  }
  return pills;
}

export function Demo() {
  const [state, setState] = useState<DemoState>(empty);
  const [notice, setNotice] = useState("");
  const [wake, setWake] = useState(false);
  const [seen, setSeen] = useState(false);
  const [scenario, setScenario] = useState<(typeof scenarios)[number]["type"]>("order_fulfilment");
  const [filter, setFilter] = useState("all");
  const [killed, setKilled] = useState<string[]>([]);
  const [stamp, setStamp] = useState<{ id: number; kicker: string; text: string; tone: "go" | "kill" } | null>(null);
  const stampTimer = useRef(0);

  function pop(kicker: string, text: string, tone: "go" | "kill") {
    window.clearTimeout(stampTimer.current);
    setStamp({ id: Date.now(), kicker, text, tone });
    stampTimer.current = window.setTimeout(() => setStamp(null), 1700);
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
        setNotice(messageFor(body.code));
        return;
      }
      setState((await res.json()) as DemoState);
      setWake(false);
      setNotice((current) => (current === messageFor("unavailable") ? "" : current));
    } catch {
      if (first) {
        setWake(true);
      }
      setNotice(messageFor("unavailable"));
    } finally {
      if (first) {
        setSeen(true);
      }
    }
  }

  useEffect(() => {
    const timer = window.setTimeout(() => {
      void load(true);
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
      window.clearTimeout(stampTimer.current);
      window.clearInterval(poll);
      document.removeEventListener("visibilitychange", onVisible);
    };
  }, []);

  async function startOrders() {
    const res = await fetch("/api/start", {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ type: scenario }),
    });
    const body = (await res.json().catch(() => ({}))) as ApiFailure;
    if (!res.ok) {
      const why = messageFor(body.code);
      setNotice(body.started ? `Started ${body.started}. ${why}` : why);
      return;
    }
    const chosen = scenarios.find((item) => item.type === scenario);
    setNotice(`${chosen?.button.replace(/^Start/, "Started") ?? "Started 12 workflows"}.`);
    pop("Launched", chosen?.label ?? "Workflows", "go");
  }

  async function killWorker() {
    const res = await fetch("/api/kill", { method: "POST" });
    const body = (await res.json().catch(() => ({}))) as ApiFailure & { worker_id?: string };
    if (!res.ok) {
      setNotice(messageFor(body.code));
      return;
    }
    const workerID = body.worker_id;
    if (workerID) {
      setKilled((ids) => (ids.includes(workerID) ? ids : [...ids, workerID]));
    }
    const name = state.workers.find((worker) => worker.id === workerID)?.name;
    setNotice(name ? `${name} is exiting. A new process with that name comes back.` : "Kill sent. That worker exits on its next heartbeat.");
    pop("Exit 137", name ?? "Worker", "kill");
  }

  async function togglePayments() {
    const enabled = !state.payments_failing;
    const res = await fetch("/api/toggle-payments", {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ enabled }),
    });
    if (!res.ok) {
      setNotice(messageFor("payments"));
      return;
    }
    setState((current) => ({ ...current, payments_failing: enabled }));
    setNotice(enabled ? "Payments will fail until you turn this off." : "Payments are accepting charges.");
    pop(enabled ? "Down" : "Back", "Payments", enabled ? "kill" : "go");
  }

  async function toggleCredit() {
    const enabled = !state.credit_failing;
    const res = await fetch("/api/toggle-credit", {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ enabled }),
    });
    if (!res.ok) {
      setNotice(messageFor("credit"));
      return;
    }
    setState((current) => ({ ...current, credit_failing: enabled }));
    setNotice(enabled ? "Credit will fail until you turn this off." : "Credit is accepting transfers.");
    pop(enabled ? "Down" : "Back", "Credit", enabled ? "kill" : "go");
  }

  const total = state.counters.running + state.counters.completed + state.counters.failed;
  const aliveByID = new Map(state.workers.map((worker) => [worker.id, worker.alive]));
  const killedSet = new Set(killed);
  const pills = visibleWorkers(state.workers, killedSet);
  const shown = state.workflows.filter((workflow) => filter === "all" || workflow.type === filter);

  return (
    <main>
      <header className="page-head">
        <h1>
          Crash a worker.
          <br />
          <em>Watch the work finish.</em>
        </h1>
        <p className="lead">Start a scenario, then kill a worker while it is still running.</p>
      </header>
      <div className="panel">
        <div className="launch">
          <select aria-label="Scenario" value={scenario} onChange={(event) => setScenario(event.target.value as (typeof scenarios)[number]["type"])}>
            {scenarios.map((item) => (
              <option key={item.type} value={item.type}>
                {item.label}
              </option>
            ))}
          </select>
          <button className="primary" type="button" onClick={() => void startOrders()}>
            {scenarios.find((item) => item.type === scenario)?.button}
          </button>
        </div>
        <div className="switches">
          <button type="button" onClick={() => void killWorker()}>
            Kill a worker
          </button>
          <button type="button" aria-pressed={state.payments_failing} onClick={() => void togglePayments()}>
            Make payments fail
          </button>
          <button type="button" aria-pressed={state.credit_failing} onClick={() => void toggleCredit()}>
            Make credit fail
          </button>
        </div>
      </div>
      {stamp ? <Stamp key={stamp.id} kicker={stamp.kicker} text={stamp.text} tone={stamp.tone} /> : null}
      {notice ? <p key={notice} className="notice">{notice}</p> : null}
      {wake ? <p className="banner">Demo may take up to a minute to wake up on first load</p> : null}
      <section className="metrics" aria-label="Counters">
        <article className="metric">
          <div className="value">
            {state.counters.completed} / {total}
          </div>
          <div className="label">Completed</div>
        </article>
        <article className="metric">
          <div className="value">{state.counters.recovered_after_crash}</div>
          <div className="label">Recovered after crash</div>
        </article>
        <article className="metric">
          <div className={state.duplicate_charges === 0 ? "value zero" : "value"}>{state.duplicate_charges}</div>
          <div className="label">Duplicate charges</div>
        </article>
        <article className="metric">
          <div className="value">{state.duplicate_requests_blocked}</div>
          <div className="label">Duplicate requests blocked</div>
        </article>
        <article className="metric">
          <div className="value">{formatRecovery(state.counters.avg_recovery_ms)}</div>
          <div className="label">Avg recovery time</div>
        </article>
      </section>
      <section className="workers" aria-label="Workers">
        {pills.map((worker) => (
          <article className={killedSet.has(worker.id) ? "pill killed" : "pill"} key={worker.id}>
            <span className={worker.alive ? "dot alive" : "dot"} />
            <div>
              <strong>{worker.name}</strong>
              <div className="muted">
                {killedSet.has(worker.id) ? "killed" : worker.alive ? "alive" : "down"} · {worker.running_tasks} running
              </div>
            </div>
          </article>
        ))}
        {seen && pills.length === 0 ? <p className="muted">No workers registered.</p> : null}
      </section>
      <div className="filters" role="group" aria-label="Workflow type">
        <button type="button" aria-pressed={filter === "all"} onClick={() => setFilter("all")}>
          All
        </button>
        {scenarios.map((item) => (
          <button key={item.type} type="button" aria-pressed={filter === item.type} onClick={() => setFilter(item.type)}>
            {item.card}
          </button>
        ))}
      </div>
      <section className="grid" aria-label="Workflows">
        {shown.map((workflow) => (
          <WorkflowCard
            key={workflow.id}
            workflow={workflow}
            ownerAlive={workflow.owner_worker_id ? aliveByID.get(workflow.owner_worker_id) : undefined}
          />
        ))}
        {seen && filter !== "all" && shown.length === 0 ? (
          <p className="muted">No {typeLabel(filter)} workflows in the latest 50.</p>
        ) : null}
      </section>
    </main>
  );
}

function WorkflowCard({ workflow, ownerAlive }: { workflow: WorkflowRow; ownerAlive: boolean | undefined }) {
  const ownerID = workflow.owner_worker_id ?? "";
  const waiting = workflow.status === "running" && (ownerID === "" || ownerAlive !== true);
  const classes = ["card"];
  if (workflow.status === "completed") {
    classes.push("done");
  } else if (workflow.status === "failed") {
    classes.push("failed");
  } else if (waiting) {
    classes.push("waiting");
  }
  if (workflow.resumed) {
    classes.push("resumed");
  }
  const steps = Math.max(stepTotals[workflow.type] ?? workflow.total_steps, workflow.step_index + 1, 1);
  const filled = workflow.status === "completed" ? steps : Math.min(steps, workflow.step_index + 1);
  const badge = workflow.status === "failed" ? "failed" : waiting ? "waiting" : workflow.resumed ? "resumed" : workflow.status === "completed" ? "done" : "running";
  return (
    <Link className={classes.join(" ")} href={`/workflows/${encodeURIComponent(workflow.id)}`}>
      <header>
        <div>
          <div className="type">{typeLabel(workflow.type)}</div>
          <strong>{workflow.id}</strong>
        </div>
        <span className={`badge ${badge}`}>{badge}</span>
      </header>
      <div className="muted">
        {workflow.status === "running"
          ? `${workflow.owner_worker || "no owner"} · ${workflow.current_step || "not started"}`
          : workflow.current_step || "not started"}
      </div>
      <div className="progress">
        <div className="bar" aria-hidden="true">
          {Array.from({ length: steps }, (_, index) => (
            <span className={index < filled ? "segment on" : "segment"} key={index} />
          ))}
        </div>
        <span className="count">
          {filled} / {steps}
        </span>
      </div>
    </Link>
  );
}
