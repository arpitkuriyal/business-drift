DELETE FROM integration_jobs WHERE kind = 'hubspot_event';
ALTER TABLE integration_jobs DROP CONSTRAINT integration_jobs_kind_check;
ALTER TABLE integration_jobs ADD CONSTRAINT integration_jobs_kind_check
    CHECK (kind IN ('stripe_sync', 'stripe_event', 'hubspot_sync'));
