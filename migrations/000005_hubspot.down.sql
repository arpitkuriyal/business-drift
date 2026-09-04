-- Removing HubSpot integrations also removes their dependent rows through the
-- existing cascading foreign keys before the Stripe-only constraint returns.
DELETE FROM integrations WHERE provider = 'hubspot';

ALTER TABLE integrations DROP COLUMN IF EXISTS config;

ALTER TABLE integrations
    DROP CONSTRAINT integrations_provider_check;

ALTER TABLE integrations
    ADD CONSTRAINT integrations_provider_check
    CHECK (provider IN ('stripe'));
