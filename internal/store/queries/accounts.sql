-- name: UpsertOrg :one
INSERT INTO orgs (name, kind, partner_id) VALUES ($1, $2, $3)
ON CONFLICT (name) DO UPDATE SET kind = EXCLUDED.kind, partner_id = EXCLUDED.partner_id
RETURNING *;

-- name: GetOrg :one
SELECT * FROM orgs WHERE id = $1;

-- name: UpsertUser :one
INSERT INTO users (email, display_name, password_hash) VALUES (lower(@email::text), @display_name, @password_hash)
ON CONFLICT (email) DO UPDATE SET
  display_name = EXCLUDED.display_name, password_hash = EXCLUDED.password_hash
RETURNING *;

-- name: AddMembership :exec
INSERT INTO memberships (org_id, user_id) VALUES ($1, $2)
ON CONFLICT DO NOTHING;
