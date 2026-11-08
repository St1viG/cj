-- name: GetPrincipalByAPIKeyHash :one
-- Bearer-token authentication: an unrevoked key, its user, and its org.
SELECT k.id AS api_key_id, k.last_used_at, sqlc.embed(u), sqlc.embed(o)
FROM api_keys k
JOIN users u ON u.id = k.user_id
JOIN orgs o ON o.id = k.org_id
JOIN memberships m ON m.org_id = k.org_id AND m.user_id = k.user_id
WHERE k.secret_hash = $1 AND k.revoked_at IS NULL;

-- name: TouchAPIKey :exec
-- At most once a minute, so authentication rarely writes.
UPDATE api_keys SET last_used_at = now()
WHERE id = $1 AND (last_used_at IS NULL OR last_used_at < now() - interval '1 minute');

-- name: CreateAPIKey :one
INSERT INTO api_keys (org_id, user_id, name, prefix, secret_hash)
VALUES ($1, $2, $3, $4, $5)
RETURNING *;

-- name: ListAPIKeys :many
SELECT * FROM api_keys WHERE org_id = $1 AND user_id = $2 ORDER BY created_at, id;

-- name: CountActiveAPIKeys :one
SELECT count(*) FROM api_keys WHERE org_id = $1 AND user_id = $2 AND revoked_at IS NULL;

-- name: RevokeAPIKey :one
UPDATE api_keys SET revoked_at = coalesce(revoked_at, now())
WHERE id = $1 AND org_id = $2 AND user_id = $3
RETURNING *;

-- name: AddSSHKey :one
INSERT INTO ssh_keys (org_id, user_id, name, public_key, fingerprint)
VALUES ($1, $2, $3, $4, $5)
RETURNING *;

-- name: ListSSHKeys :many
SELECT * FROM ssh_keys WHERE org_id = $1 ORDER BY created_at, id;

-- name: GetSSHKeysByIDs :many
SELECT * FROM ssh_keys WHERE org_id = $1 AND id = ANY(sqlc.arg(ids)::uuid[]);

-- name: DeleteSSHKey :execrows
DELETE FROM ssh_keys WHERE id = $1 AND org_id = $2;

-- name: GetOrgInstance :one
-- Org scoping: another org's instance is indistinguishable from none.
SELECT sqlc.embed(i), sqlc.embed(o), sqlc.embed(s), im.name AS image_name, im.version AS image_version, g.name AS org_name
FROM instances i
JOIN offers o ON o.id = i.offer_id
JOIN shapes s ON s.id = o.shape_id
JOIN images im ON im.id = i.image_id
JOIN orgs g ON g.id = i.org_id
WHERE i.id = $1 AND i.org_id = $2;

-- name: ListOrgInstances :many
-- Newest first, keyset-paginated on (created_at, id).
SELECT sqlc.embed(i), sqlc.embed(o), sqlc.embed(s), im.name AS image_name, im.version AS image_version, g.name AS org_name
FROM instances i
JOIN offers o ON o.id = i.offer_id
JOIN shapes s ON s.id = o.shape_id
JOIN images im ON im.id = i.image_id
JOIN orgs g ON g.id = i.org_id
WHERE i.org_id = sqlc.arg(org_id)
  AND (sqlc.arg(include_terminal)::boolean OR i.phase NOT IN ('terminated', 'lost', 'failed'))
  AND (sqlc.narg(before_created_at)::timestamptz IS NULL
       OR (i.created_at, i.id) < (sqlc.narg(before_created_at)::timestamptz, sqlc.narg(before_id)::uuid))
ORDER BY i.created_at DESC, i.id DESC
LIMIT sqlc.arg(max_rows)::integer;

-- name: ClaimIdempotencyKey :one
INSERT INTO idempotency_keys (org_id, method, key, request_hash)
VALUES ($1, $2, $3, $4)
ON CONFLICT DO NOTHING
RETURNING created_at;

-- name: GetIdempotencyKey :one
SELECT * FROM idempotency_keys WHERE org_id = $1 AND method = $2 AND key = $3;

-- name: DeleteIdempotencyKey :exec
DELETE FROM idempotency_keys WHERE org_id = $1 AND method = $2 AND key = $3;

-- name: SetIdempotencyResponse :exec
UPDATE idempotency_keys SET response = $4 WHERE org_id = $1 AND method = $2 AND key = $3;

-- name: PurgeIdempotencyKeys :execrows
DELETE FROM idempotency_keys WHERE created_at < @cutoff::timestamptz;

-- name: GetUserByEmail :one
SELECT * FROM users WHERE email = lower(@email::text);

-- name: GetOrgByName :one
SELECT * FROM orgs WHERE name = $1;
