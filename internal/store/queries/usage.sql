-- name: InsertUsageRecord :execrows
-- Idempotent ingest: a replayed (instance_id, period_start) is ignored.
INSERT INTO usage_records (instance_id, period_start, period_end, running_seconds, node_id)
VALUES ($1, $2, $3, $4, $5)
ON CONFLICT (instance_id, period_start) DO NOTHING;
