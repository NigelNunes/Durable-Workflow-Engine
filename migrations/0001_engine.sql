CREATE TABLE workflows (
  id           text PRIMARY KEY,
  type         text NOT NULL,
  status       text NOT NULL CHECK (status IN ('running','completed','failed','canceled')),
  input        jsonb NOT NULL DEFAULT 'null',
  result       jsonb,
  error        text,
  history_len  int NOT NULL DEFAULT 0,
  created_at   timestamptz NOT NULL DEFAULT now(),
  updated_at   timestamptz NOT NULL DEFAULT now(),
  closed_at    timestamptz
);
CREATE INDEX workflows_status_created_idx ON workflows (status, created_at DESC);

CREATE TABLE workflow_events (
  workflow_id  text NOT NULL REFERENCES workflows(id) ON DELETE CASCADE,
  seq          int  NOT NULL,
  type         text NOT NULL,
  data         jsonb NOT NULL DEFAULT '{}',
  created_at   timestamptz NOT NULL DEFAULT now(),
  PRIMARY KEY (workflow_id, seq)
);

CREATE TABLE tasks (
  id                       bigserial PRIMARY KEY,
  workflow_id              text NOT NULL REFERENCES workflows(id) ON DELETE CASCADE,
  kind                     text NOT NULL CHECK (kind IN ('workflow','activity')),
  command_seq              int,
  activity_name            text,
  payload                  jsonb NOT NULL DEFAULT '{}',
  status                   text NOT NULL CHECK (status IN ('pending','leased','done')),
  run_at                   timestamptz NOT NULL DEFAULT now(),
  attempt                  int NOT NULL DEFAULT 1,
  max_attempts             int NOT NULL DEFAULT 1,
  redeliveries             int NOT NULL DEFAULT 0,
  lease_owner              text,
  lease_token              bigint NOT NULL DEFAULT 0,
  lease_expires_at         timestamptz,
  history_len_at_dispatch  int,
  last_error               text,
  created_at               timestamptz NOT NULL DEFAULT now(),
  updated_at               timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX tasks_poll_idx ON tasks (kind, run_at) WHERE status = 'pending';
CREATE INDEX tasks_lease_expiry_idx ON tasks (lease_expires_at) WHERE status = 'leased';

CREATE UNIQUE INDEX one_open_workflow_task
  ON tasks (workflow_id) WHERE kind = 'workflow' AND status IN ('pending','leased');

CREATE UNIQUE INDEX one_open_activity_task
  ON tasks (workflow_id, command_seq) WHERE kind = 'activity' AND status IN ('pending','leased');

CREATE TABLE timers (
  workflow_id  text NOT NULL REFERENCES workflows(id) ON DELETE CASCADE,
  command_seq  int  NOT NULL,
  fire_at      timestamptz NOT NULL,
  fired        boolean NOT NULL DEFAULT false,
  PRIMARY KEY (workflow_id, command_seq)
);
CREATE INDEX timers_due_idx ON timers (fire_at) WHERE NOT fired;

CREATE TABLE workers (
  id              text PRIMARY KEY,
  name            text NOT NULL,
  started_at      timestamptz NOT NULL DEFAULT now(),
  last_heartbeat  timestamptz NOT NULL DEFAULT now(),
  pending_chaos   text            -- demo mode only: 'crash' | 'stall:<seconds>' | NULL
);
