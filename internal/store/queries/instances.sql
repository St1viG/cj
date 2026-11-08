-- name: CreateInstance :one
INSERT INTO instances (org_id, offer_id, image_id, name, spec, created_by)
VALUES ($1, $2, $3, $4, $5, $6)
RETURNING *;

-- name: GetInstance :one
SELECT * FROM instances WHERE id = $1;

-- name: GetInstanceForUpdate :one
SELECT * FROM instances WHERE id = $1 FOR UPDATE;

-- name: InsertInstanceEvent :exec
-- Written in the same transaction as every phase change (P3.6).
INSERT INTO instance_events (instance_id, from_phase, to_phase, reason)
VALUES ($1, $2, $3, $4);

-- name: ListInstanceEvents :many
SELECT * FROM instance_events WHERE instance_id = $1 ORDER BY id;

-- name: ListSnapshotInstances :many
-- Everything the node should run: placed, still desired, not terminal,
-- with the concrete accelerators chosen by the scheduler.
SELECT
  i.id, i.spec,
  coalesce(array_agg(a.local_id ORDER BY a.local_id) FILTER (WHERE a.local_id IS NOT NULL), '{}')::text[] AS accelerator_ids
FROM instances i
LEFT JOIN allocations al ON al.instance_id = i.id AND al.released_at IS NULL AND al.accelerator_id IS NOT NULL
LEFT JOIN accelerators a ON a.id = al.accelerator_id
WHERE i.node_id = $1 AND i.desired AND i.phase NOT IN ('terminated', 'lost', 'failed')
GROUP BY i.id
ORDER BY i.created_at, i.id;

-- name: SetInstancePhase :one
UPDATE instances SET
  phase = @phase, phase_reason = @phase_reason, phase_changed_at = now(),
  running_at = CASE WHEN @phase = 'running'::instance_phase AND running_at IS NULL THEN now() ELSE running_at END,
  terminated_at = CASE WHEN @phase IN ('terminated'::instance_phase, 'lost'::instance_phase, 'failed'::instance_phase) THEN now() ELSE terminated_at END,
  desired = CASE WHEN @phase IN ('terminated'::instance_phase, 'lost'::instance_phase, 'failed'::instance_phase) THEN false ELSE desired END
WHERE id = @id
RETURNING *;

-- name: SetInstanceAgentStatus :exec
UPDATE instances SET agent_status_at = $2, connection = $3 WHERE id = $1;

-- name: PlaceInstance :one
-- The scheduler's commit step 4 (SPEC §6.5); the caller bumps the node
-- generation in the same transaction and passes the new value.
UPDATE instances SET
  node_id = @node_id, phase = 'scheduled', phase_reason = '', phase_changed_at = now(),
  placement_generation = @placement_generation, scheduled_at = now()
WHERE id = @id AND phase = 'pending'
RETURNING *;

-- name: SetInstanceUndesired :exec
UPDATE instances SET desired = false WHERE id = $1;
