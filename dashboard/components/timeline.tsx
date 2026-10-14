"use client";

import Link from "next/link";
import { useEffect, useState } from "react";
import { relativeTime } from "@/lib/messages";
import type { HistoryEvent, WorkflowDetail } from "@/lib/types";

const labels: Record<string, string> = {
  WorkflowStarted: "Workflow started",
  ActivityScheduled: "Activity scheduled",
  ActivityCompleted: "Activity completed",
  ActivityFailed: "Activity failed",
  WorkflowCompleted: "Workflow completed",
  WorkflowFailed: "Workflow failed",
  TimerStarted: "Timer started",
  TimerFired: "Timer fired",
  ActivityRetryScheduled: "Retry scheduled",
  ActivityLeaseExpired: "Lease expired",
  WorkflowTaskLeaseExpired: "Workflow task lease expired",
  StaleCompletionRejected: "Stale completion rejected",
};

function tone(type: string): string {
  if (type === "ActivityRetryScheduled") {
    return "retry";
  }
  if (type === "ActivityLeaseExpired" || type === "WorkflowTaskLeaseExpired" || type === "StaleCompletionRejected") {
    return "bad";
  }
  if (type === "ActivityCompleted" || type === "WorkflowCompleted") {
    return "good";
  }
  return "";
}

function fields(data: Record<string, unknown>): string {
  const parts = Object.entries(data)
    .filter(([, value]) => value !== undefined && value !== null && value !== "")
    .map(([key, value]) => `${key}: ${typeof value === "string" ? value : JSON.stringify(value)}`);
  return parts.join(" · ");
}

function recoverySpan(events: HistoryEvent[]): { start: number; end: number; label: string } | null {
  const start = events.findIndex((event) => event.type === "ActivityLeaseExpired" || event.type === "WorkflowTaskLeaseExpired");
  if (start < 0) {
    return null;
  }
  const end = events.findIndex(
    (event, index) => index > start && (event.type === "ActivityCompleted" || event.type === "WorkflowCompleted"),
  );
  if (end < 0) {
    return null;
  }
  const ms = Date.parse(events[end].created_at) - Date.parse(events[start].created_at);
  const seconds = Math.max(0, ms / 1000);
  return { start, end, label: `Recovered in ${seconds.toFixed(1)} seconds` };
}

export function Timeline({ id }: { id: string }) {
  const [workflow, setWorkflow] = useState<WorkflowDetail | null>(null);
  const [events, setEvents] = useState<HistoryEvent[]>([]);
  const [error, setError] = useState("");

  useEffect(() => {
    let cancelled = false;
    async function load() {
      const [workflowRes, eventsRes] = await Promise.all([
        fetch(`/api/workflows/${encodeURIComponent(id)}`, { cache: "no-store" }),
        fetch(`/api/workflows/${encodeURIComponent(id)}/events`, { cache: "no-store" }),
      ]);
      if (cancelled) {
        return;
      }
      if (!workflowRes.ok || !eventsRes.ok) {
        setError("This workflow could not be loaded.");
        return;
      }
      setWorkflow((await workflowRes.json()) as WorkflowDetail);
      const history = (await eventsRes.json()) as HistoryEvent[];
      setEvents(Array.isArray(history) ? history : []);
    }
    void load();
    return () => {
      cancelled = true;
    };
  }, [id]);

  const span = recoverySpan(events);
  const rows = events.map((event, index) => {
    const row = (
      <article className={`event ${tone(event.type)}`} key={event.seq}>
        <header>
          <strong>{labels[event.type] ?? event.type}</strong>
          <span className="muted">{relativeTime(event.created_at)}</span>
        </header>
        <div className="muted">{fields(event.data ?? {})}</div>
      </article>
    );
    if (!span || index < span.start || index > span.end) {
      return row;
    }
    if (index === span.start) {
      return (
        <div className="recover" key={`recover-${event.seq}`}>
          <div className="muted">{span.label}</div>
          {row}
        </div>
      );
    }
    return row;
  });

  const grouped = span
    ? [
        ...rows.slice(0, span.start),
        <div className="recover" key="recover">
          <div className="muted">{span.label}</div>
          {events.slice(span.start, span.end + 1).map((event) => (
            <article className={`event ${tone(event.type)}`} key={event.seq}>
              <header>
                <strong>{labels[event.type] ?? event.type}</strong>
                <span className="muted">{relativeTime(event.created_at)}</span>
              </header>
              <div className="muted">{fields(event.data ?? {})}</div>
            </article>
          ))}
        </div>,
        ...rows.slice(span.end + 1),
      ]
    : rows;

  const result = workflow?.result ? JSON.stringify(workflow.result) : "";

  return (
    <main>
      <p>
        <Link href="/">Back to the demo</Link>
      </p>
      {error ? <p className="notice">{error}</p> : null}
      {workflow ? (
        <header className="card">
          <strong>{workflow.id}</strong>
          <div className="muted">
            {workflow.type} · {workflow.status}
          </div>
          {result ? <div>Result {result}</div> : null}
          {workflow.error ? <div>Error {workflow.error}</div> : null}
        </header>
      ) : null}
      <div className="timeline">{grouped}</div>
    </main>
  );
}
