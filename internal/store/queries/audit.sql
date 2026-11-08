-- name: InsertAuditLog :one
INSERT INTO audit_log (actor_type, actor_id, actor_display, action, target, payload)
VALUES ($1, $2, $3, $4, $5, $6)
RETURNING *;
