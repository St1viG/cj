-- name: CreateNode :one
INSERT INTO nodes (site_id, hostname, agent_version, cert_fingerprint, cert_expires_at)
VALUES ($1, $2, $3, $4, $5)
RETURNING *;

-- name: GetNode :one
SELECT * FROM nodes WHERE id = $1;

-- name: GetNodeForUpdate :one
-- Serializes placements on one node (SPEC §6.5 commit step 1).
SELECT * FROM nodes WHERE id = $1 FOR UPDATE;

-- name: ListNodes :many
SELECT * FROM nodes ORDER BY hostname, id;

-- name: ListNodesBySite :many
SELECT * FROM nodes WHERE site_id = $1 ORDER BY hostname, id;

-- name: SetNodeStatus :one
UPDATE nodes SET status = $2, status_reason = $3, updated_at = now()
WHERE id = $1
RETURNING *;

-- name: BumpNodeDesiredGeneration :one
UPDATE nodes SET desired_generation = desired_generation + 1, updated_at = now()
WHERE id = $1
RETURNING desired_generation;

-- name: UpsertNodeCapacity :exec
INSERT INTO node_capacity (node_id, vcpu_total, vcpu_reserved, ram_mib_total, ram_mib_reserved, disk_gib_total, disk_gib_reserved)
VALUES ($1, $2, $3, $4, $5, $6, $7)
ON CONFLICT (node_id) DO UPDATE SET
  vcpu_total = EXCLUDED.vcpu_total, vcpu_reserved = EXCLUDED.vcpu_reserved,
  ram_mib_total = EXCLUDED.ram_mib_total, ram_mib_reserved = EXCLUDED.ram_mib_reserved,
  disk_gib_total = EXCLUDED.disk_gib_total, disk_gib_reserved = EXCLUDED.disk_gib_reserved,
  updated_at = now();

-- name: GetNodeCapacity :one
SELECT * FROM node_capacity WHERE node_id = $1;

-- name: UpsertAccelerator :one
INSERT INTO accelerators (node_id, local_id, kind, model, mem_mib, pci_address, numa_node, iommu_group, host_driver, health, health_reason)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11)
ON CONFLICT (node_id, local_id) DO UPDATE SET
  kind = EXCLUDED.kind, model = EXCLUDED.model, mem_mib = EXCLUDED.mem_mib,
  pci_address = EXCLUDED.pci_address, numa_node = EXCLUDED.numa_node,
  iommu_group = EXCLUDED.iommu_group, host_driver = EXCLUDED.host_driver,
  health = EXCLUDED.health, health_reason = EXCLUDED.health_reason,
  updated_at = now()
RETURNING *;

-- name: ListAcceleratorsByNode :many
SELECT * FROM accelerators WHERE node_id = $1 ORDER BY local_id;

-- name: UpdateNodeInventory :one
UPDATE nodes SET
  hostname = $2, arch = $3, os = $4, kernel = $5, driver = $6,
  advertise_address = $7, labels = $8, cached_images = $9, updated_at = now()
WHERE id = $1
RETURNING *;

-- name: CreateNodeWithID :one
-- Enrollment picks the id first: it goes into the certificate's SAN.
INSERT INTO nodes (id, site_id, hostname, agent_version, cert_fingerprint, cert_expires_at)
VALUES ($1, $2, $3, $4, $5, $6)
RETURNING *;

-- name: NodeHello :one
-- A Hello makes an enrolling or unreachable node ready; other statuses
-- (draining, maintenance, quarantined) are operator decisions and stay.
UPDATE nodes SET
  agent_version = sqlc.arg(agent_version), driver = sqlc.arg(driver),
  accelerator_kinds = coalesce(sqlc.arg(accelerator_kinds)::text[], '{}'),
  max_instances = sqlc.arg(max_instances)::integer,
  observed_generation = sqlc.arg(last_applied)::bigint,
  last_seen_at = now(), updated_at = now(),
  status = CASE WHEN status IN ('enrolling', 'unreachable') THEN 'ready'::node_status ELSE status END,
  status_reason = CASE WHEN status IN ('enrolling', 'unreachable') THEN '' ELSE status_reason END,
  -- An agent ahead of us (re-enrolled with an old local store) would ignore
  -- every snapshot as out of date; jump past its generation.
  desired_generation = CASE WHEN sqlc.arg(last_applied)::bigint > desired_generation
                            THEN sqlc.arg(last_applied)::bigint + 1 ELSE desired_generation END
WHERE id = sqlc.arg(id)
RETURNING *;

-- name: NodeHeartbeat :one
UPDATE nodes SET last_seen_at = now(), observed_generation = $2 WHERE id = $1
RETURNING status;

-- name: ListAcceleratorLocalIDsInUse :many
-- Accelerators of a node held by an active allocation.
SELECT a.local_id FROM accelerators a
JOIN allocations al ON al.accelerator_id = a.id AND al.released_at IS NULL
WHERE a.node_id = $1;

-- name: MarkAcceleratorMissing :exec
UPDATE accelerators SET health = 'missing', health_reason = 'no longer reported by the agent', updated_at = now()
WHERE node_id = $1 AND local_id = $2;

-- name: DeleteAccelerator :exec
DELETE FROM accelerators WHERE node_id = $1 AND local_id = $2;

-- name: ListSilentReadyNodesForUpdate :many
-- Heartbeat watchdog: READY nodes not heard from since the cutoff.
SELECT * FROM nodes
WHERE status = 'ready' AND coalesce(last_seen_at, created_at) < @cutoff::timestamptz
ORDER BY id
FOR UPDATE SKIP LOCKED;

-- name: ListEmptyDrainingNodesForUpdate :many
SELECT n.* FROM nodes n
WHERE n.status = 'draining'
  AND NOT EXISTS (SELECT 1 FROM allocations a WHERE a.node_id = n.id AND a.released_at IS NULL)
ORDER BY n.id
FOR UPDATE SKIP LOCKED;

-- name: SetNodeCapabilities :exec
UPDATE nodes SET accelerator_kinds = $2, max_instances = $3, updated_at = now() WHERE id = $1;
