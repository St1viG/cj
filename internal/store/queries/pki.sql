-- name: InsertCA :exec
-- First writer wins; concurrent starters then read the same row.
INSERT INTO pki_ca (cert_pem, key_ciphertext) VALUES ($1, $2)
ON CONFLICT (id) DO NOTHING;

-- name: GetCA :one
SELECT * FROM pki_ca WHERE id = 1;
