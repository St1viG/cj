-- name: CreateEnrollmentToken :one
INSERT INTO enrollment_tokens (site_id, token_hash, created_by, expires_at)
VALUES ($1, $2, $3, $4)
RETURNING *;

-- name: GetEnrollmentTokenByHashForUpdate :one
SELECT * FROM enrollment_tokens WHERE token_hash = $1 FOR UPDATE;

-- name: MarkEnrollmentTokenUsed :exec
UPDATE enrollment_tokens SET used_at = now(), node_id = $2 WHERE id = $1;
