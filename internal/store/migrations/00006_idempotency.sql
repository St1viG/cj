-- +goose Up
-- Idempotency keys for mutating public RPCs (ADR-0006): per org and method,
-- the hash of the first request and its response, kept for 24 h.
CREATE TABLE idempotency_keys (
  org_id       uuid NOT NULL REFERENCES orgs (id),
  method       text NOT NULL,
  key          text NOT NULL CHECK (length(key) BETWEEN 1 AND 255),
  request_hash bytea NOT NULL,
  response     bytea,
  created_at   timestamptz NOT NULL DEFAULT now(),
  PRIMARY KEY (org_id, method, key)
);

CREATE INDEX idempotency_keys_created_at ON idempotency_keys (created_at);

-- +goose Down
DROP TABLE idempotency_keys;
