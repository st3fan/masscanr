package service

import (
	"context"

	"github.com/jackc/pgx/v5"
)

// domainSchema is initialized only on a fresh development database. Earlier
// databases are deliberately unsupported; no backfill is performed.
const domainSchema = `
CREATE TABLE IF NOT EXISTS masscanr.targets (
    id text PRIMARY KEY,
    name text NOT NULL CHECK (length(trim(name)) > 0),
    revision bigint NOT NULL CHECK (revision > 0),
    created_at timestamptz NOT NULL,
    updated_at timestamptz NOT NULL,
    archived_at timestamptz
);
CREATE TABLE IF NOT EXISTS masscanr.target_entries (
    id text PRIMARY KEY,
    target_id text NOT NULL REFERENCES masscanr.targets(id),
    position integer NOT NULL CHECK (position >= 0),
    kind text NOT NULL CHECK (kind IN ('ip', 'cidr', 'as')),
    selector text NOT NULL CHECK (length(selector) > 0),
    input text NOT NULL CHECK (length(input) > 0),
    UNIQUE (target_id, position),
    UNIQUE (target_id, selector)
);
CREATE INDEX IF NOT EXISTS target_entries_order ON masscanr.target_entries(target_id, position);
CREATE TABLE IF NOT EXISTS masscanr.scan_plans (
    id text PRIMARY KEY,
    target_id text NOT NULL REFERENCES masscanr.targets(id),
    name text NOT NULL CHECK (length(trim(name)) > 0),
    revision bigint NOT NULL CHECK (revision > 0),
    provider text NOT NULL CHECK (provider IN ('vultr', 'digitalocean')),
    settings jsonb NOT NULL CHECK (jsonb_typeof(settings) = 'object'),
    created_at timestamptz NOT NULL,
    updated_at timestamptz NOT NULL
);
CREATE INDEX IF NOT EXISTS scan_plans_target ON masscanr.scan_plans(target_id, id);
CREATE TABLE IF NOT EXISTS masscanr.scan_preflights (
    id text PRIMARY KEY,
    target_id text NOT NULL REFERENCES masscanr.targets(id),
    scan_plan_id text NOT NULL REFERENCES masscanr.scan_plans(id),
    target_revision bigint NOT NULL CHECK (target_revision > 0),
    scan_plan_revision bigint NOT NULL CHECK (scan_plan_revision > 0),
    expires_at timestamptz NOT NULL,
    data jsonb NOT NULL CHECK (jsonb_typeof(data) = 'object')
);
CREATE INDEX IF NOT EXISTS scan_preflights_plan ON masscanr.scan_preflights(scan_plan_id, expires_at);
CREATE TABLE IF NOT EXISTS masscanr.runs (
    id text PRIMARY KEY,
    target_id text NOT NULL REFERENCES masscanr.targets(id),
    scan_plan_id text NOT NULL REFERENCES masscanr.scan_plans(id),
    request_key text NOT NULL UNIQUE,
    active boolean NOT NULL DEFAULT true,
    attempted boolean NOT NULL DEFAULT false,
    workflow_id text NOT NULL DEFAULT '',
    stop boolean NOT NULL DEFAULT false,
    result_checkpoint bigint NOT NULL DEFAULT 0 CHECK (result_checkpoint >= 0),
    result_state text NOT NULL DEFAULT 'none' CHECK (result_state IN ('none', 'live', 'partial', 'complete', 'error')),
    shard_count bigint NOT NULL DEFAULT 0 CHECK (shard_count >= 0),
    requested_workers integer NOT NULL DEFAULT 0 CHECK (requested_workers >= 0),
    effective_workers integer NOT NULL DEFAULT 0 CHECK (effective_workers >= 0),
    completed_shards bigint NOT NULL DEFAULT 0 CHECK (completed_shards >= 0),
    execution_epoch bigint NOT NULL DEFAULT 0 CHECK (execution_epoch >= 0),
    data jsonb NOT NULL
);
CREATE UNIQUE INDEX IF NOT EXISTS one_active_run ON masscanr.runs(active) WHERE active;
CREATE INDEX IF NOT EXISTS runs_target ON masscanr.runs(target_id, id);
CREATE INDEX IF NOT EXISTS runs_scan_plan ON masscanr.runs(scan_plan_id, id);
CREATE TABLE IF NOT EXISTS masscanr.run_worker_slots (
    run_id text NOT NULL REFERENCES masscanr.runs(id),
    slot integer NOT NULL CHECK (slot > 0),
    assigned_shards bigint NOT NULL CHECK (assigned_shards > 0),
    completed_shards bigint NOT NULL DEFAULT 0 CHECK (completed_shards >= 0),
    next_shard bigint NOT NULL CHECK (next_shard > 0),
    current_instance_id text,
    state text NOT NULL DEFAULT 'pending',
    PRIMARY KEY (run_id, slot)
);
CREATE TABLE IF NOT EXISTS masscanr.run_workers (
    run_id text NOT NULL REFERENCES masscanr.runs(id),
    instance_id text NOT NULL,
    slot integer NOT NULL,
    ip text NOT NULL DEFAULT '',
    state text NOT NULL,
    cleanup text NOT NULL DEFAULT 'pending',
    detail text NOT NULL DEFAULT '',
    created_at timestamptz NOT NULL,
    updated_at timestamptz NOT NULL,
    PRIMARY KEY (run_id, instance_id),
    FOREIGN KEY (run_id, slot) REFERENCES masscanr.run_worker_slots(run_id, slot)
);
CREATE INDEX IF NOT EXISTS run_workers_slot ON masscanr.run_workers(run_id, slot, created_at);
CREATE TABLE IF NOT EXISTS masscanr.run_shards (
    run_id text NOT NULL REFERENCES masscanr.runs(id),
    shard_index bigint NOT NULL CHECK (shard_index > 0),
    slot integer NOT NULL,
    state text NOT NULL CHECK (state IN ('running', 'failed', 'completed')),
    attempts integer NOT NULL CHECK (attempts BETWEEN 1 AND 3),
    current_instance_id text NOT NULL,
    progress_percent double precision,
    progress_at timestamptz,
    updated_at timestamptz NOT NULL,
    PRIMARY KEY (run_id, shard_index),
    FOREIGN KEY (run_id, slot) REFERENCES masscanr.run_worker_slots(run_id, slot)
);
CREATE INDEX IF NOT EXISTS run_shards_slot ON masscanr.run_shards(run_id, slot, shard_index);
CREATE TABLE IF NOT EXISTS masscanr.run_shard_attempts (
    run_id text NOT NULL,
    shard_index bigint NOT NULL,
    attempt integer NOT NULL CHECK (attempt BETWEEN 1 AND 3),
    instance_id text NOT NULL,
    state text NOT NULL CHECK (state IN ('running', 'failed', 'completed')),
    detail text NOT NULL DEFAULT '',
    journal_seq bigint NOT NULL DEFAULT 0 CHECK (journal_seq >= 0),
    started_at timestamptz NOT NULL,
    finished_at timestamptz,
    PRIMARY KEY (run_id, shard_index, attempt),
    FOREIGN KEY (run_id, shard_index) REFERENCES masscanr.run_shards(run_id, shard_index),
    FOREIGN KEY (run_id, instance_id) REFERENCES masscanr.run_workers(run_id, instance_id)
);
CREATE TABLE IF NOT EXISTS masscanr.scan_results (
    run_id text NOT NULL REFERENCES masscanr.runs(id),
    target_id text NOT NULL REFERENCES masscanr.targets(id),
    ip inet NOT NULL CHECK (family(ip) = 4),
    port integer NOT NULL CHECK (port BETWEEN 0 AND 65535),
    protocol text NOT NULL CHECK (protocol = 'tcp'),
    first_seen numeric(20,0) NOT NULL CHECK (first_seen >= 0),
    last_seen numeric(20,0) NOT NULL CHECK (last_seen >= first_seen),
    observations numeric(20,0) NOT NULL CHECK (observations >= 1),
    selector text NOT NULL DEFAULT '',
    origin_prefix text NOT NULL DEFAULT '',
    PRIMARY KEY (run_id, ip, port, protocol)
);
CREATE INDEX IF NOT EXISTS scan_results_target_ip_port ON masscanr.scan_results(target_id, ip, port);
CREATE INDEX IF NOT EXISTS scan_results_ip_port ON masscanr.scan_results(ip, port);
`

func initDomainSchema(ctx context.Context, tx pgx.Tx) error {
	_, err := tx.Exec(ctx, domainSchema)
	return err
}
