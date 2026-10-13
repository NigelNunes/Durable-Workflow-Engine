CREATE SCHEMA IF NOT EXISTS demo;

CREATE TABLE demo.charges (
  id               bigserial PRIMARY KEY,
  idempotency_key  text UNIQUE NOT NULL,
  order_id         text NOT NULL,
  amount_cents     int  NOT NULL,
  created_at       timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE demo.charge_requests (
  id               bigserial PRIMARY KEY,
  idempotency_key  text NOT NULL,
  order_id         text NOT NULL,
  deduplicated     boolean NOT NULL,
  created_at       timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE demo.inventory (
  sku   text PRIMARY KEY,
  stock int  NOT NULL
);

CREATE TABLE demo.reservations (
  idempotency_key text PRIMARY KEY,
  order_id        text NOT NULL,
  sku             text NOT NULL,
  qty             int  NOT NULL
);

CREATE TABLE demo.emails (
  idempotency_key text PRIMARY KEY,
  to_addr         text NOT NULL,
  template        text NOT NULL,
  sent_at         timestamptz DEFAULT now()
);

CREATE TABLE demo.accounts (
  id            text PRIMARY KEY,
  balance_cents bigint NOT NULL
);

CREATE TABLE demo.transfers_log (
  idempotency_key text PRIMARY KEY,
  account_id      text NOT NULL,
  delta_cents     bigint NOT NULL,
  kind            text NOT NULL
);

INSERT INTO demo.inventory (sku, stock) VALUES ('widget', 1000);
