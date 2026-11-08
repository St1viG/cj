-- Internal CA for node identity (ADR-0024, roadmap P3.2). One row. The
-- private key is sealed with AES-256-GCM under a key derived from
-- KONDUKTER_MASTER_KEY, so a database dump alone cannot sign certificates.

-- +goose Up

CREATE TABLE pki_ca (
  id             smallint PRIMARY KEY DEFAULT 1 CHECK (id = 1),
  cert_pem       text NOT NULL,
  key_ciphertext bytea NOT NULL,
  created_at     timestamptz NOT NULL DEFAULT now()
);

-- +goose Down

DROP TABLE pki_ca;
