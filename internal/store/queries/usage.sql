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

-- name: ListEventsForInstances :many
SELECT * FROM instance_events WHERE instance_id = ANY(sqlc.arg(ids)::uuid[]) ORDER BY instance_id, id;

-- name: SumAgentUsage :many
-- Agent-reported running seconds per instance over periods inside the window.
SELECT instance_id, coalesce(sum(running_seconds), 0)::bigint AS seconds,
  min(period_start)::timestamptz AS first_start, max(period_end)::timestamptz AS last_end
FROM usage_records
WHERE instance_id = ANY(sqlc.arg(ids)::uuid[])
  AND period_start >= sqlc.arg(window_start)::timestamptz AND period_end <= sqlc.arg(window_end)::timestamptz
GROUP BY instance_id;

-- name: ListOrgUsageInstances :many
-- Instances of an org that were RUNNING at some point in the window.
SELECT sqlc.embed(i), sqlc.embed(o), sh.name AS shape_name
FROM instances i JOIN offers o ON o.id = i.offer_id JOIN shapes sh ON sh.id = o.shape_id
WHERE i.org_id = sqlc.arg(org_id)
  AND (sqlc.narg(instance_id)::uuid IS NULL OR i.id = sqlc.narg(instance_id)::uuid)
  AND i.running_at IS NOT NULL AND i.running_at < sqlc.arg(window_end)::timestamptz
  AND (i.terminated_at IS NULL OR i.terminated_at > sqlc.arg(window_start)::timestamptz)
ORDER BY i.created_at DESC, i.id;

-- name: ListMeteredInstances :many
-- Cross-check candidates: instances with agent usage records since the cutoff.
SELECT DISTINCT instance_id FROM usage_records WHERE period_end > sqlc.arg(cutoff)::timestamptz;
