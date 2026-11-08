-- name: UpsertPartner :one
INSERT INTO partners (name, legal_name, contact_email)
VALUES ($1, $2, $3)
ON CONFLICT (name) DO UPDATE SET
  legal_name = EXCLUDED.legal_name, contact_email = EXCLUDED.contact_email
RETURNING *;

-- name: UpsertSite :one
INSERT INTO sites (partner_id, name, region, country_code)
VALUES ($1, $2, $3, $4)
ON CONFLICT (partner_id, name) DO UPDATE SET
  region = EXCLUDED.region, country_code = EXCLUDED.country_code
RETURNING *;

-- name: GetSite :one
SELECT * FROM sites WHERE id = $1;

-- name: ListSites :many
SELECT * FROM sites ORDER BY name;

-- name: ListSitesByPartner :many
SELECT * FROM sites WHERE partner_id = $1 ORDER BY name;

-- name: GetSiteByName :one
SELECT s.* FROM sites s JOIN partners p ON p.id = s.partner_id
WHERE p.name = @partner_name AND s.name = @site_name;
