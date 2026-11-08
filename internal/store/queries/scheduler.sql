-- name: ListPlacementNodes :many
-- Scheduler candidates (SPEC §6.5): READY nodes of a region with their free
-- resources (total - reserved - active allocations), active instance count,
-- and when their partner last received a placement. With node_id set it
-- returns just that node, for the recompute inside the commit transaction.
SELECT
  n.id, n.hostname, n.arch, n.capacity_class, n.accelerator_kinds, n.max_instances, n.cached_images,
  s.partner_id, s.region,
  (c.vcpu_total - c.vcpu_reserved - coalesce(al.vcpu, 0))::bigint             AS free_vcpu,
  (c.ram_mib_total - c.ram_mib_reserved - coalesce(al.ram_mib, 0))::bigint    AS free_ram_mib,
  (c.disk_gib_total - c.disk_gib_reserved - coalesce(al.disk_gib, 0))::bigint AS free_disk_gib,
  (SELECT count(*) FROM instances i
    WHERE i.node_id = n.id AND i.phase NOT IN ('terminated', 'lost', 'failed'))::bigint AS active_instances,
  coalesce((SELECT max(i.scheduled_at) FROM instances i
    JOIN nodes pn ON pn.id = i.node_id JOIN sites ps ON ps.id = pn.site_id
    WHERE ps.partner_id = s.partner_id), 'epoch'::timestamptz)::timestamptz AS partner_last_placed_at
FROM nodes n
JOIN sites s ON s.id = n.site_id
JOIN node_capacity c ON c.node_id = n.id
LEFT JOIN LATERAL (
  SELECT sum(a.vcpu) AS vcpu, sum(a.ram_mib) AS ram_mib, sum(a.disk_gib) AS disk_gib
  FROM allocations a WHERE a.node_id = n.id AND a.released_at IS NULL
) al ON true
WHERE n.status = 'ready' AND s.region = sqlc.arg(region)::text
  AND (sqlc.narg(node_id)::uuid IS NULL OR n.id = sqlc.narg(node_id)::uuid)
ORDER BY n.id;

-- name: ListFreeAccelerators :many
-- Healthy accelerators of the given nodes that no active allocation holds.
SELECT a.* FROM accelerators a
WHERE a.node_id = ANY(sqlc.arg(node_ids)::uuid[]) AND a.health = 'ok'
  AND NOT EXISTS (
    SELECT 1 FROM allocations al WHERE al.accelerator_id = a.id AND al.released_at IS NULL
  )
ORDER BY a.node_id, a.local_id;

-- name: ListPendingInstances :many
-- Oldest first, so the scheduler is FIFO within a pass.
SELECT * FROM instances
WHERE phase = 'pending' AND desired
ORDER BY created_at, id
LIMIT sqlc.arg(max_rows)::integer;
