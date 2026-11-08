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
