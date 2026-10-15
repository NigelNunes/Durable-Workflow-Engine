INSERT INTO demo.accounts (id, balance_cents) VALUES
  ('alice', 100000),
  ('bob', 0)
ON CONFLICT (id) DO NOTHING;

CREATE TABLE demo.report_steps (
  idempotency_key text PRIMARY KEY,
  report_id       text NOT NULL,
  name            text NOT NULL
);
