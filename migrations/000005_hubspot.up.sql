-- HubSpot uses an encrypted private-app token for the demo integration.
ALTER TABLE integrations
    DROP CONSTRAINT integrations_provider_check;

ALTER TABLE integrations
    ADD CONSTRAINT integrations_provider_check
    CHECK (provider IN ('stripe', 'hubspot'));
