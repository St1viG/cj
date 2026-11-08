-- Last-writer-wins guard for agent InstanceStatus ingest (roadmap P3.3):
-- the agent timestamp of the newest status applied to the instance.

-- +goose Up

ALTER TABLE instances ADD COLUMN agent_status_at timestamptz;

-- +goose Down

ALTER TABLE instances DROP COLUMN agent_status_at;
