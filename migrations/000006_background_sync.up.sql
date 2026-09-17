ALTER TABLE integration_jobs DROP CONSTRAINT integration_jobs_kind_check;
ALTER TABLE integration_jobs ADD CONSTRAINT integration_jobs_kind_check
    CHECK (kind IN ('stripe_sync', 'stripe_event', 'hubspot_sync'));

-- Failed syncs remain retryable. Collapse any legacy duplicates before extending
-- the uniqueness rule to both providers and retryable states.
WITH ranked AS (
    SELECT id, row_number() OVER (
        PARTITION BY integration_id, kind
        ORDER BY CASE status WHEN 'processing' THEN 0 WHEN 'pending' THEN 1 ELSE 2 END, created_at
    ) AS position
    FROM integration_jobs
    WHERE kind IN ('stripe_sync', 'hubspot_sync') AND status IN ('pending', 'processing', 'failed')
)
UPDATE integration_jobs SET status='completed', last_error='Superseded by another sync', updated_at=now()
WHERE id IN (SELECT id FROM ranked WHERE position > 1);
DROP INDEX integration_jobs_one_active_sync_idx;
CREATE UNIQUE INDEX integration_jobs_one_active_sync_idx ON integration_jobs (integration_id, kind)
    WHERE kind IN ('stripe_sync', 'hubspot_sync') AND status IN ('pending', 'processing', 'failed');
