-- name: CreateEnrollmentToken :one
INSERT INTO enrollment_tokens (site_id, token_hash, created_by, expires_at)
VALUES ($1, $2, $3, $4)
RETURNING *;
