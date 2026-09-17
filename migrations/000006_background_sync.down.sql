DELETE FROM integration_jobs WHERE kind = 'hubspot_sync';
DROP INDEX integration_jobs_one_active_sync_idx;
CREATE UNIQUE INDEX integration_jobs_one_active_sync_idx ON integration_jobs (integration_id, kind)
    WHERE kind = 'stripe_sync' AND status IN ('pending', 'processing');
ALTER TABLE integration_jobs DROP CONSTRAINT integration_jobs_kind_check;
ALTER TABLE integration_jobs ADD CONSTRAINT integration_jobs_kind_check
    CHECK (kind IN ('stripe_sync', 'stripe_event'));
