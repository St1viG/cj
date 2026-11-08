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
