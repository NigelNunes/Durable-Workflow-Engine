"use client";

import Link from "next/link";
import { useEffect, useState } from "react";
import { formatRecovery, messageFor } from "@/lib/messages";
import type { ApiFailure, DemoState, WorkflowRow } from "@/lib/types";

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
};

export function Demo() {
  const [state, setState] = useState<DemoState>(empty);
  const [notice, setNotice] = useState("");
  const [wake, setWake] = useState(false);
  const [seen, setSeen] = useState(false);

  async function load(first: boolean) {
    const started = performance.now();
    try {
      const res = await fetch("/api/state", { cache: "no-store" });
      if (first && (!res.ok || performance.now() - started > 2000)) {
        setWake(true);
      }
      if (!res.ok) {
        return;
      }
      setState((await res.json()) as DemoState);
    } catch {
      if (first) {
        setWake(true);
      }
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
      window.clearInterval(poll);
      document.removeEventListener("visibilitychange", onVisible);
    };
  }, []);

  async function startOrders() {
    const res = await fetch("/api/start", { method: "POST" });
    const body = (await res.json().catch(() => ({}))) as ApiFailure;
    if (!res.ok) {
      const why = messageFor(body.code);
      setNotice(body.started ? `Started ${body.started}. ${why}` : why);
      return;
    }
    setNotice("Started 12 orders.");
  }

  async function killWorker() {
    const res = await fetch("/api/kill", { method: "POST" });
    const body = (await res.json().catch(() => ({}))) as ApiFailure;
    if (!res.ok) {
      setNotice(messageFor(body.code));
      return;
    }
    setNotice("Kill sent. That worker exits on its next heartbeat.");
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
  }

  const total = state.counters.running + state.counters.completed + state.counters.failed;
  const alive = new Map(state.workers.map((worker) => [worker.name, worker.alive]));

  return (
    <main>
      <div className="controls">
        <button type="button" onClick={() => void startOrders()}>
          Start 12 orders
        </button>
        <button type="button" onClick={() => void killWorker()}>
          Kill a worker
        </button>
        <button type="button" aria-pressed={state.payments_failing} onClick={() => void togglePayments()}>
          Make payments fail
        </button>
      </div>
      {notice ? <p className="notice">{notice}</p> : null}
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
        {state.workers.map((worker) => (
          <article className="pill" key={worker.id}>
            <span className={worker.alive ? "dot alive" : "dot"} />
            <div>
              <strong>{worker.name}</strong>
              <div className="muted">
                {worker.alive ? "alive" : "down"} · {worker.running_tasks} running
              </div>
            </div>
          </article>
        ))}
        {seen && state.workers.length === 0 ? <p className="muted">No workers registered.</p> : null}
      </section>
      <p className="muted">Click Start, then Kill a worker, and watch orders recover.</p>
      <section className="grid" aria-label="Workflows">
        {state.workflows.map((workflow) => (
          <WorkflowCard key={workflow.id} workflow={workflow} ownerAlive={alive.get(workflow.owner_worker)} />
        ))}
      </section>
    </main>
  );
}

function WorkflowCard({ workflow, ownerAlive }: { workflow: WorkflowRow; ownerAlive: boolean | undefined }) {
  const waiting = workflow.status === "running" && (workflow.owner_worker === "" || ownerAlive === false);
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
  const steps = Math.max(workflow.total_steps, 1);
  const filled = workflow.status === "completed" || workflow.status === "failed" ? steps : Math.min(steps, workflow.step_index + 1);
  return (
    <Link className={classes.join(" ")} href={`/workflows/${encodeURIComponent(workflow.id)}`}>
      <header>
        <strong>{workflow.id}</strong>
        <span className="muted">Order</span>
      </header>
      <div className="muted">
        {workflow.owner_worker || "no owner"} · {workflow.current_step || "not started"}
      </div>
      <div className="bar" aria-hidden="true">
        {Array.from({ length: steps }, (_, index) => (
          <span className={index < filled ? "segment on" : "segment"} key={index} />
        ))}
      </div>
      <div className="tags">
        {workflow.status === "completed" ? <span className="tag">done</span> : null}
        {workflow.status === "failed" ? <span className="tag">failed</span> : null}
        {waiting ? <span className="tag">waiting</span> : null}
        {workflow.resumed ? <span className="tag">resumed</span> : null}
        {workflow.status === "running" && !waiting ? <span className="tag">running</span> : null}
      </div>
    </Link>
  );
}
