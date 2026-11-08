-- Initial control-plane schema: SPEC §6.2 without ledger_entries (ADR-0023).
--
-- Conventions:
--   * ids are uuid v4 from gen_random_uuid();
--   * timestamps are timestamptz, written in UTC;
--   * strings the API always has are NOT NULL DEFAULT '' so the Go side
--     needs no null handling; NULL means "not happened yet" for timestamps;
--   * free capacity is never stored: it is total - reserved - the sum of
--     active allocations, recomputed in the placement transaction (ADR-0009).

-- +goose Up

CREATE TYPE instance_phase AS ENUM (
  'pending', 'scheduled', 'provisioning', 'running', 'terminating',
  'terminated', 'unreachable', 'lost', 'failed'
);

CREATE TYPE node_status AS ENUM (
  'enrolling', 'ready', 'draining', 'maintenance', 'unreachable',
  'quarantined', 'retired'
);

CREATE TABLE regions (
  name        text PRIMARY KEY,
  description text NOT NULL DEFAULT ''
);

CREATE TABLE partners (
  id               uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  name             text NOT NULL UNIQUE,
  legal_name       text NOT NULL DEFAULT '',
  contact_email    text NOT NULL DEFAULT '',
  commercial_model text NOT NULL DEFAULT '',
  created_at       timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE sites (
  id             uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  partner_id     uuid NOT NULL REFERENCES partners (id),
  name           text NOT NULL,
  region         text NOT NULL REFERENCES regions (name),
  country_code   text NOT NULL DEFAULT '',
  network_mode   text NOT NULL DEFAULT 'port-map'
                 CHECK (network_mode IN ('port-map', 'public-ip', 'overlay')),
  public_ip_pool text NOT NULL DEFAULT '',
  created_at     timestamptz NOT NULL DEFAULT now(),
  UNIQUE (partner_id, name)
);

CREATE TABLE nodes (
  id                  uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  site_id             uuid NOT NULL REFERENCES sites (id),
  hostname            text NOT NULL DEFAULT '',
  arch                text NOT NULL DEFAULT '' CHECK (arch IN ('', 'amd64', 'arm64')),
  os                  text NOT NULL DEFAULT '',
  kernel              text NOT NULL DEFAULT '',
  driver              text NOT NULL DEFAULT '',
  agent_version       text NOT NULL DEFAULT '',
  status              node_status NOT NULL DEFAULT 'enrolling',
  status_reason       text NOT NULL DEFAULT '',
  capacity_class      text NOT NULL DEFAULT 'dedicated'
                      CHECK (capacity_class IN ('dedicated', 'preemptible')),
  -- SHA-256 of the DER client certificate issued at enrollment (ADR-0024).
  cert_fingerprint    text UNIQUE,
  cert_expires_at     timestamptz,
  labels              jsonb NOT NULL DEFAULT '{}',
  -- Address customers use for port mappings (the tailnet IP in the demo).
  advertise_address   text NOT NULL DEFAULT '',
  -- [{"name":…,"version":…,"digest":…}] from the last Inventory.
  cached_images       jsonb NOT NULL DEFAULT '[]',
  -- Bumped by every change that affects the node's snapshot (SPEC §6.6).
  -- Starts at 1 so the first, possibly empty, snapshot is newer than an
  -- agent's initial last_applied_generation of 0.
  desired_generation  bigint NOT NULL DEFAULT 1,
  observed_generation bigint NOT NULL DEFAULT 0,
  last_seen_at        timestamptz,
  created_at          timestamptz NOT NULL DEFAULT now(),
  updated_at          timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX nodes_site_id ON nodes (site_id);

CREATE TABLE node_capacity (
  node_id           uuid PRIMARY KEY REFERENCES nodes (id) ON DELETE CASCADE,
  vcpu_total        integer NOT NULL CHECK (vcpu_total >= 0),
  vcpu_reserved     integer NOT NULL DEFAULT 0 CHECK (vcpu_reserved >= 0),
  ram_mib_total     bigint NOT NULL CHECK (ram_mib_total >= 0),
  ram_mib_reserved  bigint NOT NULL DEFAULT 0 CHECK (ram_mib_reserved >= 0),
  disk_gib_total    bigint NOT NULL CHECK (disk_gib_total >= 0),
  disk_gib_reserved bigint NOT NULL DEFAULT 0 CHECK (disk_gib_reserved >= 0),
  updated_at        timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE accelerators (
  id            uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  node_id       uuid NOT NULL REFERENCES nodes (id) ON DELETE CASCADE,
  -- The agent's id for the device (Accelerator.id in the agent protocol):
  -- the PCI address for passthrough GPUs. Sent back in InstanceSpec.
  local_id      text NOT NULL,
  kind          text NOT NULL,
  model         text NOT NULL,
  mem_mib       bigint NOT NULL DEFAULT 0,
  pci_address   text NOT NULL DEFAULT '',
  numa_node     integer NOT NULL DEFAULT -1,
  iommu_group   integer NOT NULL DEFAULT -1,
  host_driver   text NOT NULL DEFAULT '',
  -- 'missing': no longer reported but still held by an allocation, so the
  -- row is kept rather than deleted.
  health        text NOT NULL DEFAULT 'ok' CHECK (health IN ('ok', 'unavailable', 'missing')),
  health_reason text NOT NULL DEFAULT '',
  updated_at    timestamptz NOT NULL DEFAULT now(),
  UNIQUE (node_id, local_id)
);

CREATE TABLE images (
  id                uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  name              text NOT NULL,
  version           text NOT NULL,
  arch              text NOT NULL CHECK (arch IN ('amd64', 'arm64')),
  os                text NOT NULL DEFAULT '',
  -- Empty for CPU images; e.g. 'nvidia-580-cuda12' for GPU images.
  accel_stack       text NOT NULL DEFAULT '',
  compatible_models text[] NOT NULL DEFAULT '{}',
  digest            text NOT NULL,
  url               text NOT NULL,
  size_bytes        bigint NOT NULL DEFAULT 0,
  format            text NOT NULL DEFAULT 'qcow2' CHECK (format IN ('qcow2', 'raw')),
  status            text NOT NULL DEFAULT 'active' CHECK (status IN ('active', 'deprecated')),
  created_at        timestamptz NOT NULL DEFAULT now(),
  UNIQUE (name, version, arch)
);

CREATE TABLE shapes (
  id          uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  name        text NOT NULL UNIQUE,
  vcpu        integer NOT NULL CHECK (vcpu > 0),
  ram_mib     bigint NOT NULL CHECK (ram_mib > 0),
  disk_gib    bigint NOT NULL CHECK (disk_gib > 0),
  accel_kind  text NOT NULL DEFAULT '',
  accel_model text NOT NULL DEFAULT '',
  accel_count integer NOT NULL DEFAULT 0 CHECK (accel_count >= 0),
  CHECK ((accel_count = 0) = (accel_kind = ''))
);

CREATE TABLE offers (
  id                   uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  shape_id             uuid NOT NULL REFERENCES shapes (id),
  region               text NOT NULL REFERENCES regions (name),
  capacity_class       text NOT NULL DEFAULT 'dedicated'
                       CHECK (capacity_class IN ('dedicated', 'preemptible')),
  price_per_hour_minor bigint NOT NULL CHECK (price_per_hour_minor >= 0),
  currency             text NOT NULL CHECK (currency ~ '^[A-Z]{3}$'),
  active               boolean NOT NULL DEFAULT true,
  created_at           timestamptz NOT NULL DEFAULT now(),
  UNIQUE (shape_id, region, capacity_class)
);

-- Orgs are customers, partners (linked to a partners row) or the operator.
-- A user's role comes from the kind of the org it belongs to (ADR-0021).
CREATE TABLE orgs (
  id         uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  name       text NOT NULL UNIQUE,
  kind       text NOT NULL CHECK (kind IN ('customer', 'partner', 'operator')),
  partner_id uuid REFERENCES partners (id),
  created_at timestamptz NOT NULL DEFAULT now(),
  CHECK ((kind = 'partner') = (partner_id IS NOT NULL))
);

CREATE TABLE users (
  id            uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  email         text NOT NULL UNIQUE CHECK (email = lower(email)),
  display_name  text NOT NULL DEFAULT '',
  -- bcrypt.
  password_hash text NOT NULL DEFAULT '',
  created_at    timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE memberships (
  org_id     uuid NOT NULL REFERENCES orgs (id),
  user_id    uuid NOT NULL REFERENCES users (id),
  created_at timestamptz NOT NULL DEFAULT now(),
  PRIMARY KEY (org_id, user_id)
);

CREATE INDEX memberships_user_id ON memberships (user_id);

CREATE TABLE api_keys (
  id           uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  org_id       uuid NOT NULL REFERENCES orgs (id),
  user_id      uuid NOT NULL REFERENCES users (id),
  name         text NOT NULL,
  -- Leading characters of the secret, for display ("kdk_ab12…").
  prefix       text NOT NULL,
  -- SHA-256 of the full secret.
  secret_hash  bytea NOT NULL UNIQUE,
  created_at   timestamptz NOT NULL DEFAULT now(),
  last_used_at timestamptz,
  revoked_at   timestamptz
);

CREATE INDEX api_keys_org_id ON api_keys (org_id);

CREATE TABLE ssh_keys (
  id          uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  org_id      uuid NOT NULL REFERENCES orgs (id),
  user_id     uuid NOT NULL REFERENCES users (id),
  name        text NOT NULL,
  public_key  text NOT NULL,
  fingerprint text NOT NULL,
  created_at  timestamptz NOT NULL DEFAULT now(),
  UNIQUE (org_id, fingerprint)
);

CREATE TABLE sessions (
  id           uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  -- SHA-256 of the cookie value.
  token_hash   bytea NOT NULL UNIQUE,
  user_id      uuid NOT NULL REFERENCES users (id),
  org_id       uuid NOT NULL REFERENCES orgs (id),
  created_at   timestamptz NOT NULL DEFAULT now(),
  last_seen_at timestamptz NOT NULL DEFAULT now(),
  expires_at   timestamptz NOT NULL
);

CREATE TABLE instances (
  id                   uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  org_id               uuid NOT NULL REFERENCES orgs (id),
  offer_id             uuid NOT NULL REFERENCES offers (id),
  image_id             uuid NOT NULL REFERENCES images (id),
  node_id              uuid REFERENCES nodes (id),
  name                 text NOT NULL DEFAULT '',
  -- false once termination was requested; the node snapshot omits it.
  desired              boolean NOT NULL DEFAULT true,
  phase                instance_phase NOT NULL DEFAULT 'pending',
  phase_reason         text NOT NULL DEFAULT '',
  phase_changed_at     timestamptz NOT NULL DEFAULT now(),
  -- The node's desired_generation at placement; agent statuses produced
  -- under an older snapshot do not drive transitions (P3.3).
  placement_generation bigint NOT NULL DEFAULT 0,
  -- Agent-facing InstanceSpec fields: vcpu, ram_mib, disk_gib, image ref,
  -- ssh_public_keys, user_data, ports, hostname.
  spec                 jsonb NOT NULL,
  -- Host, ports and the ssh command once RUNNING.
  connection           jsonb NOT NULL DEFAULT '{}',
  created_by           uuid REFERENCES users (id),
  created_at           timestamptz NOT NULL DEFAULT now(),
  scheduled_at         timestamptz,
  running_at           timestamptz,
  terminated_at        timestamptz,
  CHECK (node_id IS NOT NULL OR phase IN ('pending', 'failed'))
);

CREATE INDEX instances_org_created ON instances (org_id, created_at DESC);
CREATE INDEX instances_active_node ON instances (node_id)
  WHERE phase NOT IN ('terminated', 'lost', 'failed');
CREATE INDEX instances_open_phase ON instances (phase)
  WHERE phase NOT IN ('terminated', 'lost', 'failed', 'running');

-- One active row per instance carries its vCPU / RAM / disk with
-- accelerator_id NULL; every accelerator gets its own row with zero
-- resources. Sums over active rows are therefore exact, and the partial
-- unique index guards each device.
CREATE TABLE allocations (
  id             uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  instance_id    uuid NOT NULL REFERENCES instances (id),
  node_id        uuid NOT NULL REFERENCES nodes (id),
  vcpu           integer NOT NULL DEFAULT 0 CHECK (vcpu >= 0),
  ram_mib        bigint NOT NULL DEFAULT 0 CHECK (ram_mib >= 0),
  disk_gib       bigint NOT NULL DEFAULT 0 CHECK (disk_gib >= 0),
  accelerator_id uuid REFERENCES accelerators (id),
  created_at     timestamptz NOT NULL DEFAULT now(),
  released_at    timestamptz,
  CHECK (accelerator_id IS NULL OR (vcpu = 0 AND ram_mib = 0 AND disk_gib = 0))
);

-- The over-allocation safety net (SPEC §6.2, ADR-0009).
CREATE UNIQUE INDEX one_active_lease_per_accelerator
  ON allocations (accelerator_id)
  WHERE released_at IS NULL AND accelerator_id IS NOT NULL;

CREATE INDEX allocations_active_node ON allocations (node_id) WHERE released_at IS NULL;
CREATE INDEX allocations_instance ON allocations (instance_id);

-- Append-only.
CREATE TABLE instance_events (
  id          bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
  instance_id uuid NOT NULL REFERENCES instances (id),
  from_phase  instance_phase,
  to_phase    instance_phase NOT NULL,
  reason      text NOT NULL DEFAULT '',
  at          timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX instance_events_instance ON instance_events (instance_id, id);

-- Agent metering, idempotent by (instance_id, period_start) (ADR-0023).
CREATE TABLE usage_records (
  instance_id     uuid NOT NULL REFERENCES instances (id),
  period_start    timestamptz NOT NULL,
  period_end      timestamptz NOT NULL,
  running_seconds integer NOT NULL CHECK (running_seconds BETWEEN 0 AND 3600),
  node_id         uuid NOT NULL REFERENCES nodes (id),
  received_at     timestamptz NOT NULL DEFAULT now(),
  PRIMARY KEY (instance_id, period_start),
  CHECK (period_end > period_start)
);

CREATE TABLE enrollment_tokens (
  id         uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  site_id    uuid NOT NULL REFERENCES sites (id),
  -- SHA-256 of the plaintext token.
  token_hash bytea NOT NULL UNIQUE,
  created_by uuid REFERENCES users (id),
  created_at timestamptz NOT NULL DEFAULT now(),
  expires_at timestamptz NOT NULL,
  used_at    timestamptz,
  node_id    uuid REFERENCES nodes (id)
);

-- Append-only.
CREATE TABLE audit_log (
  id            bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
  actor_type    text NOT NULL CHECK (actor_type IN ('user', 'api_key', 'system')),
  actor_id      text NOT NULL DEFAULT '',
  actor_display text NOT NULL DEFAULT '',
  action        text NOT NULL,
  target        text NOT NULL DEFAULT '',
  payload       jsonb NOT NULL DEFAULT '{}',
  at            timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX audit_log_at ON audit_log (at DESC);

-- +goose Down

DROP TABLE audit_log;
DROP TABLE enrollment_tokens;
DROP TABLE usage_records;
DROP TABLE instance_events;
DROP TABLE allocations;
DROP TABLE instances;
DROP TABLE sessions;
DROP TABLE ssh_keys;
DROP TABLE api_keys;
DROP TABLE memberships;
DROP TABLE users;
DROP TABLE orgs;
DROP TABLE offers;
DROP TABLE shapes;
DROP TABLE images;
DROP TABLE accelerators;
DROP TABLE node_capacity;
DROP TABLE nodes;
DROP TABLE sites;
DROP TABLE partners;
DROP TABLE regions;
DROP TYPE node_status;
DROP TYPE instance_phase;
