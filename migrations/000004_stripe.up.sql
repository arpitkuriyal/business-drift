-- Integration credentials are encrypted by the application before storage.
-- The encryption key lives outside PostgreSQL in the process environment.
CREATE TABLE integrations (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    organization_id uuid NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    provider text NOT NULL CHECK (provider IN ('stripe')),
    api_key_ciphertext text NOT NULL,
    webhook_secret_ciphertext text NOT NULL,
    status text NOT NULL CHECK (status IN ('active', 'error', 'disconnected')),
    last_synced_at timestamptz,
    last_error text,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    UNIQUE (organization_id, provider),
    UNIQUE (organization_id, id)
);

-- Raw webhook events are the durable source record. external_event_id makes
-- Stripe retries idempotent for each organization.
CREATE TABLE processed_events (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    organization_id uuid NOT NULL,
    integration_id uuid NOT NULL,
    external_event_id text NOT NULL,
    event_type text NOT NULL,
    payload jsonb NOT NULL,
    status text NOT NULL CHECK (status IN ('pending', 'processed', 'failed')),
    last_error text,
    received_at timestamptz NOT NULL DEFAULT now(),
    processed_at timestamptz,
    FOREIGN KEY (organization_id, integration_id)
        REFERENCES integrations (organization_id, id) ON DELETE CASCADE,
    UNIQUE (organization_id, external_event_id),
    UNIQUE (organization_id, id)
);

-- PostgreSQL owns job state. Redis only notifies the worker that a job is ready.
CREATE TABLE integration_jobs (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    organization_id uuid NOT NULL,
    integration_id uuid NOT NULL,
    event_id uuid,
    kind text NOT NULL CHECK (kind IN ('stripe_sync', 'stripe_event')),
    status text NOT NULL CHECK (status IN ('pending', 'processing', 'completed', 'failed')),
    attempts integer NOT NULL DEFAULT 0 CHECK (attempts >= 0),
    available_at timestamptz NOT NULL DEFAULT now(),
    last_error text,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    FOREIGN KEY (organization_id, integration_id)
        REFERENCES integrations (organization_id, id) ON DELETE CASCADE,
    FOREIGN KEY (organization_id, event_id)
        REFERENCES processed_events (organization_id, id) ON DELETE CASCADE,
    UNIQUE (event_id)
);
CREATE INDEX integration_jobs_pending_idx
    ON integration_jobs (status, available_at, created_at)
    WHERE status IN ('pending', 'failed');
CREATE UNIQUE INDEX integration_jobs_one_active_sync_idx
    ON integration_jobs (integration_id, kind)
    WHERE kind = 'stripe_sync' AND status IN ('pending', 'processing');
