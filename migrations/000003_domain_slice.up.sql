-- A canonical customer is Business Drift's internal customer record. Source
-- identities connect Stripe, HubSpot, and later product accounts to this row.
CREATE TABLE canonical_customers (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    organization_id uuid NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    name text NOT NULL CHECK (char_length(name) BETWEEN 1 AND 200),
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    UNIQUE (organization_id, id)
);
CREATE INDEX canonical_customers_org_name_idx
    ON canonical_customers (organization_id, name);

CREATE TABLE customer_identities (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    organization_id uuid NOT NULL,
    customer_id uuid NOT NULL,
    source text NOT NULL CHECK (source IN ('stripe', 'hubspot', 'product')),
    external_id text NOT NULL CHECK (external_id <> ''),
    match_method text NOT NULL CHECK (match_method IN ('explicit', 'external_id', 'domain', 'email')),
    created_at timestamptz NOT NULL DEFAULT now(),
    FOREIGN KEY (organization_id, customer_id)
        REFERENCES canonical_customers (organization_id, id) ON DELETE CASCADE,
    UNIQUE (organization_id, source, external_id)
);
CREATE INDEX customer_identities_customer_idx
    ON customer_identities (organization_id, customer_id);

-- customer_facts stores the latest normalized value used by rules. The JSONB
-- value preserves type information while fact_type gives it a stable meaning.
CREATE TABLE customer_facts (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    organization_id uuid NOT NULL,
    customer_id uuid NOT NULL,
    source text NOT NULL,
    fact_type text NOT NULL,
    value jsonb NOT NULL,
    observed_at timestamptz NOT NULL,
    schema_version integer NOT NULL CHECK (schema_version > 0),
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    FOREIGN KEY (organization_id, customer_id)
        REFERENCES canonical_customers (organization_id, id) ON DELETE CASCADE,
    UNIQUE (organization_id, customer_id, source, fact_type)
);
CREATE INDEX customer_facts_customer_idx
    ON customer_facts (organization_id, customer_id, observed_at DESC);

CREATE TABLE findings (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    organization_id uuid NOT NULL,
    customer_id uuid NOT NULL,
    rule_name text NOT NULL,
    rule_version integer NOT NULL CHECK (rule_version > 0),
    fingerprint text NOT NULL,
    status text NOT NULL CHECK (status IN ('open', 'resolved')),
    risk text NOT NULL CHECK (risk IN ('low', 'medium', 'high')),
    title text NOT NULL,
    explanation text NOT NULL,
    first_detected_at timestamptz NOT NULL,
    last_detected_at timestamptz NOT NULL,
    resolved_at timestamptz,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    FOREIGN KEY (organization_id, customer_id)
        REFERENCES canonical_customers (organization_id, id) ON DELETE CASCADE,
    UNIQUE (organization_id, fingerprint),
    UNIQUE (organization_id, id)
);
CREATE INDEX findings_dashboard_idx
    ON findings (organization_id, status, last_detected_at DESC);

-- Evidence is an immutable snapshot of the inputs that produced a finding.
-- Updating a current fact never changes evidence attached to an older decision.
CREATE TABLE finding_evidence (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    organization_id uuid NOT NULL,
    finding_id uuid NOT NULL,
    source text NOT NULL,
    fact_type text NOT NULL,
    value jsonb NOT NULL,
    observed_at timestamptz NOT NULL,
    fingerprint text NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now(),
    FOREIGN KEY (organization_id, finding_id)
        REFERENCES findings (organization_id, id) ON DELETE CASCADE,
    UNIQUE (organization_id, finding_id, fingerprint)
);
CREATE INDEX finding_evidence_finding_idx
    ON finding_evidence (organization_id, finding_id, observed_at);

