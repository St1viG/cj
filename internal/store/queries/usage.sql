-- name: InsertUsageRecord :execrows
-- Idempotent ingest: a replayed (instance_id, period_start) is ignored.
INSERT INTO usage_records (instance_id, period_start, period_end, running_seconds, node_id)
VALUES ($1, $2, $3, $4, $5)
ON CONFLICT (instance_id, period_start) DO NOTHING;

-- name: InsertOwnedUsageRecord :execrows
-- Gateway ingest: idempotent by (instance_id, period_start), and only for
-- instances placed on the reporting node.
INSERT INTO usage_records (instance_id, period_start, period_end, running_seconds, node_id)
SELECT @instance_id, @period_start, @period_end, @running_seconds, @node_id
WHERE EXISTS (SELECT 1 FROM instances WHERE id = @instance_id AND node_id = @node_id)
ON CONFLICT (instance_id, period_start) DO NOTHING;
