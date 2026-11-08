-- name: CreateAllocation :one
INSERT INTO allocations (instance_id, node_id, vcpu, ram_mib, disk_gib, accelerator_id)
VALUES ($1, $2, $3, $4, $5, $6)
RETURNING *;

-- name: ReleaseInstanceAllocations :execrows
UPDATE allocations SET released_at = now()
WHERE instance_id = $1 AND released_at IS NULL;

-- name: NodeAllocatedResources :one
-- Free = total - reserved - this, recomputed inside the placement
-- transaction (ADR-0009).
SELECT
  coalesce(sum(vcpu), 0)::integer   AS vcpu,
  coalesce(sum(ram_mib), 0)::bigint AS ram_mib,
  coalesce(sum(disk_gib), 0)::bigint AS disk_gib
FROM allocations
WHERE node_id = $1 AND released_at IS NULL;

-- name: ListActiveAllocationsByNode :many
SELECT * FROM allocations WHERE node_id = $1 AND released_at IS NULL ORDER BY created_at, id;
