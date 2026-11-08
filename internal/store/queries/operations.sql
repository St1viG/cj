-- name: CreateSession :one
INSERT INTO sessions (token_hash, user_id, org_id, expires_at)
VALUES ($1, $2, $3, $4)
RETURNING *;

-- name: GetPrincipalBySessionHash :one
-- Cookie authentication: an unexpired session, its user, and its org.
SELECT ss.id AS session_id, ss.last_seen_at, sqlc.embed(u), sqlc.embed(o)
FROM sessions ss
JOIN users u ON u.id = ss.user_id
JOIN orgs o ON o.id = ss.org_id
JOIN memberships m ON m.org_id = ss.org_id AND m.user_id = ss.user_id
WHERE ss.token_hash = $1 AND ss.expires_at > now();

-- name: TouchSession :exec
-- Sliding expiry, written at most once a minute.
UPDATE sessions SET last_seen_at = now(), expires_at = @expires_at::timestamptz
WHERE id = @id AND last_seen_at < now() - interval '1 minute';

-- name: DeleteSession :exec
DELETE FROM sessions WHERE token_hash = $1;

-- name: ListUserOrgs :many
SELECT o.* FROM orgs o JOIN memberships m ON m.org_id = o.id
WHERE m.user_id = $1
ORDER BY m.created_at, o.name;

-- name: ListSitesView :many
-- Sites of one partner (all when partner_id is null) with node counts.
SELECT sqlc.embed(s), p.name AS partner_name,
  (SELECT count(*) FROM nodes n WHERE n.site_id = s.id)::bigint AS node_count
FROM sites s JOIN partners p ON p.id = s.partner_id
WHERE sqlc.narg(partner_id)::uuid IS NULL OR s.partner_id = sqlc.narg(partner_id)::uuid
ORDER BY p.name, s.name;

-- name: CountNodesBySiteStatus :many
SELECT site_id, status, count(*)::bigint AS count FROM nodes
WHERE site_id = ANY(sqlc.arg(site_ids)::uuid[])
GROUP BY site_id, status
ORDER BY site_id, status;

-- name: ListNodesView :many
-- Nodes with their site, partner, capacity and current allocations. Filters
-- are optional: partner, site, one node, statuses (empty = all).
SELECT sqlc.embed(n), s.name AS site_name, s.partner_id, p.name AS partner_name,
  coalesce(c.vcpu_total, 0)::bigint AS vcpu_total, coalesce(c.vcpu_reserved, 0)::bigint AS vcpu_reserved,
  coalesce(c.ram_mib_total, 0)::bigint AS ram_mib_total, coalesce(c.ram_mib_reserved, 0)::bigint AS ram_mib_reserved,
  coalesce(c.disk_gib_total, 0)::bigint AS disk_gib_total, coalesce(c.disk_gib_reserved, 0)::bigint AS disk_gib_reserved,
  coalesce(al.vcpu, 0)::bigint AS vcpu_allocated, coalesce(al.ram_mib, 0)::bigint AS ram_mib_allocated,
  coalesce(al.disk_gib, 0)::bigint AS disk_gib_allocated,
  (SELECT count(*) FROM instances i
    WHERE i.node_id = n.id AND i.phase NOT IN ('terminated', 'lost', 'failed'))::bigint AS active_instances
FROM nodes n
JOIN sites s ON s.id = n.site_id
JOIN partners p ON p.id = s.partner_id
LEFT JOIN node_capacity c ON c.node_id = n.id
LEFT JOIN LATERAL (
  SELECT sum(a.vcpu) AS vcpu, sum(a.ram_mib) AS ram_mib, sum(a.disk_gib) AS disk_gib
  FROM allocations a WHERE a.node_id = n.id AND a.released_at IS NULL
) al ON true
WHERE (sqlc.narg(partner_id)::uuid IS NULL OR s.partner_id = sqlc.narg(partner_id)::uuid)
  AND (sqlc.narg(site_id)::uuid IS NULL OR n.site_id = sqlc.narg(site_id)::uuid)
  AND (sqlc.narg(node_id)::uuid IS NULL OR n.id = sqlc.narg(node_id)::uuid)
  AND (cardinality(sqlc.arg(statuses)::text[]) = 0 OR n.status::text = ANY(sqlc.arg(statuses)::text[]))
ORDER BY p.name, s.name, n.hostname, n.id;

-- name: ListAcceleratorsView :many
-- Accelerators of the given nodes, with whether an active allocation holds them.
SELECT sqlc.embed(a),
  EXISTS (SELECT 1 FROM allocations al WHERE al.accelerator_id = a.id AND al.released_at IS NULL) AS allocated
FROM accelerators a
WHERE a.node_id = ANY(sqlc.arg(node_ids)::uuid[])
ORDER BY a.node_id, a.local_id;

-- name: NodeInstanceSeconds :many
-- Instance running seconds per node over usage periods overlapping the window.
SELECT node_id, coalesce(sum(running_seconds), 0)::bigint AS seconds
FROM usage_records
WHERE node_id = ANY(sqlc.arg(node_ids)::uuid[])
  AND period_start < sqlc.arg(period_end)::timestamptz AND period_end > sqlc.arg(period_start)::timestamptz
GROUP BY node_id;

-- name: ListAllInstances :many
-- Operator view across orgs; same shape as ListOrgInstances.
SELECT sqlc.embed(i), sqlc.embed(o), sqlc.embed(s), im.name AS image_name, im.version AS image_version, g.name AS org_name
FROM instances i
JOIN offers o ON o.id = i.offer_id
JOIN shapes s ON s.id = o.shape_id
JOIN images im ON im.id = i.image_id
JOIN orgs g ON g.id = i.org_id
WHERE (sqlc.narg(org_id)::uuid IS NULL OR i.org_id = sqlc.narg(org_id)::uuid)
  AND (sqlc.narg(node_id)::uuid IS NULL OR i.node_id = sqlc.narg(node_id)::uuid)
  AND (sqlc.arg(include_terminal)::boolean OR i.phase NOT IN ('terminated', 'lost', 'failed'))
  AND (sqlc.narg(before_created_at)::timestamptz IS NULL
       OR (i.created_at, i.id) < (sqlc.narg(before_created_at)::timestamptz, sqlc.narg(before_id)::uuid))
ORDER BY i.created_at DESC, i.id DESC
LIMIT sqlc.arg(max_rows)::integer;

-- name: GetAnyInstance :one
SELECT sqlc.embed(i), sqlc.embed(o), sqlc.embed(s), im.name AS image_name, im.version AS image_version, g.name AS org_name
FROM instances i
JOIN offers o ON o.id = i.offer_id
JOIN shapes s ON s.id = o.shape_id
JOIN images im ON im.id = i.image_id
JOIN orgs g ON g.id = i.org_id
WHERE i.id = $1;

-- name: ListAuditLog :many
-- Newest first, keyset-paginated on id.
SELECT * FROM audit_log
WHERE (sqlc.arg(actor_id)::text = '' OR actor_id = sqlc.arg(actor_id)::text)
  AND (sqlc.arg(action)::text = '' OR action = sqlc.arg(action)::text)
  AND (sqlc.narg(before_id)::bigint IS NULL OR id < sqlc.narg(before_id)::bigint)
ORDER BY id DESC
LIMIT sqlc.arg(max_rows)::integer;
