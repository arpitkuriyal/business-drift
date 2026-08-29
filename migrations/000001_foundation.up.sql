CREATE TABLE system_metadata (
    key text PRIMARY KEY,
    value jsonb NOT NULL,
    updated_at timestamptz NOT NULL DEFAULT now()
);

COMMENT ON TABLE system_metadata IS 'Non-secret application metadata and migration smoke-test table.';
