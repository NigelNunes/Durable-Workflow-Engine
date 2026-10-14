export type WorkerRow = {
  id: string;
  name: string;
  last_heartbeat: string;
  alive: boolean;
  running_tasks: number;
};

export type WorkflowRow = {
  id: string;
  type: string;
  status: string;
  current_step: string;
  step_index: number;
  total_steps: number;
  owner_worker: string;
  owner_worker_id: string;
  resumed: boolean;
  created_at: string;
};

export type Counters = {
  running: number;
  completed: number;
  failed: number;
  recovered_after_crash: number;
  stale_completions_rejected: number;
  avg_recovery_ms: number;
};

export type DemoState = {
  workers: WorkerRow[];
  workflows: WorkflowRow[];
  counters: Counters;
  duplicate_charges: number;
  duplicate_requests_blocked: number;
  total_charges: number;
  payments_failing: boolean;
  credit_failing: boolean;
};

export type WorkflowDetail = {
  id: string;
  type: string;
  status: string;
  result: unknown;
  error: string | null;
};

export type HistoryEvent = {
  seq: number;
  type: string;
  data: Record<string, unknown>;
  created_at: string;
};

export type ApiFailure = {
  error?: string;
  code?: string;
  started?: number;
};
