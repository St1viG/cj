-- +goose Up
-- What the node's driver can do, from the agent's Hello (SPEC §6.5: the
-- scheduler filters on the accelerator kinds the driver can attach).
ALTER TABLE nodes
  ADD COLUMN accelerator_kinds text[] NOT NULL DEFAULT '{}',
  ADD COLUMN max_instances integer NOT NULL DEFAULT 0 CHECK (max_instances >= 0);

-- +goose Down
ALTER TABLE nodes DROP COLUMN max_instances, DROP COLUMN accelerator_kinds;
