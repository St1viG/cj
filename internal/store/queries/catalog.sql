-- name: UpsertRegion :exec
INSERT INTO regions (name, description) VALUES ($1, $2)
ON CONFLICT (name) DO UPDATE SET description = EXCLUDED.description;

-- name: ListRegions :many
SELECT * FROM regions ORDER BY name;

-- name: UpsertShape :one
INSERT INTO shapes (name, vcpu, ram_mib, disk_gib, accel_kind, accel_model, accel_count)
VALUES ($1, $2, $3, $4, $5, $6, $7)
ON CONFLICT (name) DO UPDATE SET
  vcpu = EXCLUDED.vcpu, ram_mib = EXCLUDED.ram_mib, disk_gib = EXCLUDED.disk_gib,
  accel_kind = EXCLUDED.accel_kind, accel_model = EXCLUDED.accel_model,
  accel_count = EXCLUDED.accel_count
RETURNING *;

-- name: ListShapes :many
SELECT * FROM shapes ORDER BY name;

-- name: UpsertImage :one
INSERT INTO images (name, version, arch, os, accel_stack, compatible_models, digest, url, size_bytes, format, status)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11)
ON CONFLICT (name, version, arch) DO UPDATE SET
  os = EXCLUDED.os, accel_stack = EXCLUDED.accel_stack,
  compatible_models = EXCLUDED.compatible_models, digest = EXCLUDED.digest,
  url = EXCLUDED.url, size_bytes = EXCLUDED.size_bytes, format = EXCLUDED.format,
  status = EXCLUDED.status
RETURNING *;

-- name: GetImage :one
SELECT * FROM images WHERE id = $1;

-- name: ListImages :many
SELECT * FROM images ORDER BY name, version, arch;

-- name: UpsertOffer :one
INSERT INTO offers (shape_id, region, capacity_class, price_per_hour_minor, currency, active)
VALUES ($1, $2, $3, $4, $5, $6)
ON CONFLICT (shape_id, region, capacity_class) DO UPDATE SET
  price_per_hour_minor = EXCLUDED.price_per_hour_minor,
  currency = EXCLUDED.currency, active = EXCLUDED.active
RETURNING *;

-- name: GetOffer :one
SELECT sqlc.embed(offers), sqlc.embed(shapes)
FROM offers JOIN shapes ON shapes.id = offers.shape_id
WHERE offers.id = $1;

-- name: ListOffers :many
SELECT sqlc.embed(offers), sqlc.embed(shapes)
FROM offers JOIN shapes ON shapes.id = offers.shape_id
ORDER BY shapes.accel_count, shapes.name, offers.region;
