package service

import (
	"context"

	"github.com/jackc/pgx/v5"
)

// domainSchema is initialized only on a fresh development database. Version 1
// databases are deliberately unsupported; no legacy backfill is performed.
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
    provider text NOT NULL CHECK (provider = 'vultr'),
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
ALTER TABLE masscanr.runs ADD COLUMN IF NOT EXISTS target_id text REFERENCES masscanr.targets(id);
ALTER TABLE masscanr.runs ADD COLUMN IF NOT EXISTS scan_plan_id text REFERENCES masscanr.scan_plans(id);
ALTER TABLE masscanr.runs ADD COLUMN IF NOT EXISTS result_checkpoint bigint NOT NULL DEFAULT 0 CHECK (result_checkpoint >= 0);
ALTER TABLE masscanr.runs ADD COLUMN IF NOT EXISTS result_state text NOT NULL DEFAULT 'none' CHECK (result_state IN ('none', 'live', 'partial', 'complete', 'error'));
CREATE INDEX IF NOT EXISTS runs_target ON masscanr.runs(target_id, id);
CREATE INDEX IF NOT EXISTS runs_scan_plan ON masscanr.runs(scan_plan_id, id);
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
