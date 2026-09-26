# Business Drift

Business Drift finds customer information that does not match between Stripe and HubSpot.

Stripe knows whether a customer is paying. HubSpot knows how the company is classified by the business. When one system changes and the other is not updated, reports and customer lists become incorrect.

For example:

```text
Stripe subscription: canceled
HubSpot lifecycle stage: Customer

Finding: Acme is cancelled in Stripe but active in HubSpot.
```

## What it does

- Connects to Stripe sandbox and HubSpot.
- Imports customers, subscriptions, companies, and lifecycle stages through durable background jobs.
- Verifies Stripe subscription webhooks, deduplicates deliveries, and automatically refreshes customer status.
- Matches Stripe customers with HubSpot companies by business domain.
- Detects incorrect customer status in both directions.
- Detects customers missing from either system.
- Shows findings with the source values that caused them.
- Includes workspace registration, login, roles, and organization-separated data.

## Architecture and flow

![Business Drift architecture and comparison flow](docs/architecture.svg)

```text
Stripe sync -> HubSpot sync -> normalize data -> match by domain
            -> run rules -> create or resolve findings -> show evidence
```

Wait for Stripe’s **Last sync** time to update before syncing HubSpot for the first time. Imports run in one background worker inside the API process. HubSpot matches the imported records; both sources run the shared status detector. Stripe subscription webhooks keep subsequent billing changes current. HubSpot changes still require reconciliation sync in this MVP.

## Customer matching

Business Drift extracts the domain from a Stripe customer email and compares it with the HubSpot company domain:

```text
Stripe email: person@acme.com  -> acme.com
HubSpot domain: www.acme.com   -> acme.com
Result: matched
```

Personal email domains such as Gmail, Outlook, Hotmail, Yahoo, and iCloud are not matched automatically because unrelated people can share those domains.

## Detection rules

| Stripe | HubSpot | Finding |
|---|---|---|
| Subscription is canceled | Company is a customer | Canceled in Stripe but active in HubSpot |
| Subscription is active | Company is not a customer | Active in Stripe but not a customer in HubSpot |
| Customer exists | No matching company | Customer is missing from HubSpot |
| No matching customer | Company exists | Company is missing from Stripe |

HubSpot's `lifecyclestage=customer` value is treated as active. Other lifecycle stages are treated as inactive.

## Project structure

```text
business-drift/
├── cmd/api/                         API entry point
├── internal/auth/                   registration, login, sessions, and roles
├── internal/detection/              comparison rules
├── internal/findings/               finding API and database queries
├── internal/integrations/stripe/    Stripe connection, webhooks, and sync
├── internal/integrations/hubspot/   HubSpot connection, sync, and matching
├── internal/integrations/jobs/      durable sync queue and job status
├── internal/platform/               database, encryption, HTTP, and logging
├── migrations/                      PostgreSQL migrations
├── web/                             React, TypeScript, and Tailwind application
├── docs/architecture.svg            architecture diagram
├── compose.yaml                     local services
└── LICENSE                          MIT License
```

## Run locally

Requirements: Go, Node.js, Docker, Docker Compose, and Make.

```bash
git clone https://github.com/arpitkuriyal/business-drift.git
cd business-drift
make install
make up
make migrate-up
npm run dev --prefix web
```

Open the address printed by Vite in the terminal.

Create a workspace using an organization name, your email, and a password of at least 12 characters. The first account becomes the workspace owner.

## Configuration

Docker Compose provides everything needed for local development, so no `.env` file is required for the commands above.

The backend supports these environment variables:

| Variable | Local default | Purpose |
|---|---|---|
| `APP_ENV` | `development` | Runtime mode: `development`, `test`, or `production` |
| `HTTP_ADDRESS` | `:8080` | Address and port used by the API |
| `DATABASE_URL` | Local PostgreSQL connection | PostgreSQL connection string |
| `REDIS_URL` | `redis://localhost:6379/0` | Redis connection string |
| `ENCRYPTION_KEY` | Development-only key | Base64-encoded 32-byte key used to encrypt integration credentials |

For production, set `APP_ENV=production` and provide `DATABASE_URL`, `REDIS_URL`, and `ENCRYPTION_KEY` explicitly. Generate a new encryption key with:

```bash
openssl rand -base64 32
```

The frontend reads one optional variable from `web/.env`:

```env
VITE_API_URL=https://api.example.com
```

Leave `VITE_API_URL` empty during local development because Vite proxies `/api` requests to the backend on port `8080`. The example is available in `web/.env.example`.

## Set up the demo

### Stripe

1. Create a Stripe account and stay in sandbox/test mode.
2. Create a customer with a business email, such as `demo@acme.com`.
3. Create a recurring product and subscription.
4. Cancel the subscription immediately so its status becomes `canceled`.
5. Copy a test key beginning with `sk_test_` or `rk_test_`.
6. In Business Drift, save the key and select **Sync Stripe**.

Never use a real card or live Stripe key for this demo.

### HubSpot

1. Create a HubSpot account.
2. Create a company whose domain matches the Stripe email domain. For `demo@acme.com`, use `acme.com`.
3. Set its lifecycle stage to `Customer`.
4. Create a private app for one account.
5. Give it the `crm.objects.companies.read` scope.
6. Copy its access token.
7. In Business Drift, save the token and select **Sync HubSpot companies**.
8. Open **Findings** to see the mismatch and its evidence.

Do not share or commit the Stripe key or HubSpot token.

## Automatic Stripe updates

This MVP uses one Go worker, one PostgreSQL job table, and a fixed retry delay. No separate queue service or worker deployment is needed. It handles one job at a time, so a long import can delay webhook processing.

```text
Stripe webhook -> verify signature -> save event + job -> return success
Background worker -> fetch current Stripe state -> save facts -> run detection
Manual Sync button -> save job -> same background worker imports the source
```

1. Apply migrations with `make migrate-up` and restart the API. The worker starts automatically with the API; PostgreSQL is the durable queue, so Redis notifications are not required.
2. Save a Stripe test API key. Copy the integration ID from the Stripe screen or `GET /api/v1/integrations/stripe`.
3. Register `https://<backend-host>/api/v1/webhooks/stripe/<integration-id>` in Stripe for your account's test-mode `customer.subscription.created`, `customer.subscription.updated`, and `customer.subscription.deleted` snapshot events.
4. Save that endpoint's `whsec_...` signing secret alongside your API key in the Stripe screen. It is encrypted in the database and never returned by the API. Leaving the field blank preserves the existing secret.
5. Complete an initial Stripe sync, then a HubSpot sync. Subsequent subscription changes arrive automatically; the dashboard refreshes findings and integration status every ten seconds while visible.

For local forwarding, run:

```bash
stripe listen --events customer.subscription.created,customer.subscription.updated,customer.subscription.deleted \
  --forward-to localhost:8080/api/v1/webhooks/stripe/<integration-id>
```

Use the signing secret printed by this command for local testing. See [Stripe's webhook setup guide](https://docs.stripe.com/webhooks).

The receiver verifies the original request bytes and signature timestamp before ingestion. Supported test-account events and their jobs commit in one transaction before HTTP 204 is returned. Repeated event IDs within an organization are acknowledged without another job. Invalid signatures receive 400; oversized bodies receive 413; storage failures receive 503 so delivery can be retried. Other signed event types are acknowledged without work. Live-mode and Connect events are outside this test-mode MVP.

A worker locks one job with `FOR UPDATE SKIP LOCKED`, fetches the latest Stripe subscription/customer state, and commits facts, detection evidence, and completion together. Fetches and writes serialize per organization with reconciliation updates, preventing an old event snapshot from overwriting newer state. If a customer has multiple subscriptions, active/trialing subscriptions take precedence; otherwise the newest subscription determines status. Failed jobs retry after 30 seconds. A crash releases transaction locks and leaves the job available for replay.

Manual sync endpoints return HTTP 202 with a job ID. Repeated requests reuse an outstanding sync, including a failed job awaiting retry. Read `/api/v1/integration-jobs/<id>` to check `status`, `attempts`, and `last_error`; only members of the owning organization can read it. The UI confirms the request, then refreshes findings and the last sync time automatically. The worker holds a transaction lock while running, so the job remains pending (or failed during a retry) until completion. Sync imports are idempotent and may replay after a crash; HubSpot may have committed some company updates before a failed attempt.

Use manual sync for initial imports and occasional reconciliation. HubSpot webhook notifications are a follow-up; HubSpot sync already uses the shared detector to resolve findings after CRM status changes. Tenant isolation remains application-level plus tenant-aware foreign keys; this change does not introduce PostgreSQL RLS or claim a full security audit.

## API routes

| Method | Path | Purpose |
|---|---|---|
| `POST` | `/api/v1/auth/register` | Create a workspace and owner |
| `POST` | `/api/v1/auth/login` | Sign in |
| `POST` | `/api/v1/auth/refresh` | Refresh the session |
| `POST` | `/api/v1/auth/logout` | Sign out |
| `GET` | `/api/v1/auth/me` | Read the signed-in identity |
| `GET` | `/api/v1/organization` | Read the current workspace |
| `GET` | `/api/v1/integrations/stripe` | Read Stripe connection status |
| `POST` | `/api/v1/integrations/stripe` | Save a Stripe test key and optional webhook signing secret |
| `POST` | `/api/v1/integrations/stripe/sync` | Queue Stripe import (202 + job) |
| `GET` | `/api/v1/integrations/hubspot` | Read HubSpot connection status |
| `POST` | `/api/v1/integrations/hubspot` | Save a HubSpot token |
| `POST` | `/api/v1/integrations/hubspot/sync` | Queue HubSpot import and comparison (202 + job) |
| `POST` | `/api/v1/webhooks/stripe/{integrationID}` | Receive signed Stripe subscription events |
| `GET` | `/api/v1/integration-jobs/{id}` | Read an organization-scoped job status |
| `GET` | `/api/v1/findings` | List findings |
| `GET` | `/api/v1/findings/{id}` | Read a finding and its evidence |
| `GET` | `/live` | Check that the API is running |
| `GET` | `/ready` | Check the API, PostgreSQL, and Redis |

Protected routes use an access token in the `Authorization: Bearer <token>` header. Owners and admins can change integrations and start syncs.

## Useful commands

```bash
make test          # Run backend tests
make lint          # Run formatting, vet, and frontend lint checks
make build         # Build the backend and frontend
make ci            # Run all project checks
make logs          # Follow local service logs
make down          # Stop local services
```

## License

Licensed under the [MIT License](LICENSE).
