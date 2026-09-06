# Business Drift

Business Drift is a small, rules-first risk monitor for B2B SaaS teams. It looks for places where operational systems disagree or signal a revenue problem, then turns those signals into reviewable findings with the evidence that caused them.

The problem is ordinary but expensive: a subscription is cancelled in Stripe while the CRM still calls the customer active, or an invoice keeps failing without reaching the right person. Those gaps are easy to miss when each team lives in a different tool. Business Drift gives them one queue to review.

This repository is an intentionally focused MVP. It includes multi-tenant authentication, a complete fixture-driven mismatch flow, a Stripe sandbox integration, background event processing, and a React dashboard. It does not pretend to be a finished revenue-operations platform; the current goal is to make the first vertical slice reliable, understandable, and easy to extend.

## What works today

- Organization registration and role-based access for owners, admins, analysts, and viewers
- Opaque access and refresh tokens with rotation, revocation, and reuse detection
- Tenant-scoped PostgreSQL queries and database constraints
- A development fixture that compares Stripe and HubSpot customer states
- Deduplicated findings that can open, resolve, and reopen without losing their evidence
- Encrypted Stripe sandbox credentials, signed webhook ingestion, and background jobs
- Stripe rules for cancelled subscriptions and risky invoices
- A responsive dashboard for onboarding, connection status, fixture ingestion, and finding review
- Local Docker Compose setup and a CI workflow for backend and frontend checks

## How the system fits together

Business Drift is a modular monolith: one Go process serves the API and runs the Stripe worker, while PostgreSQL owns durable state and Redis handles short-lived coordination.

The React dashboard talks to the Go API with JSON requests and bearer tokens. The API authenticates the user, adds the trusted organization identity to the request, and passes work to the relevant domain package. PostgreSQL stores application and job data. Redis supports login rate limits and wakes the background worker when Stripe work is ready.

Two boundaries keep the design manageable:

1. Provider payloads are normalized before a rule sees them. A rule works with stable business facts rather than Stripe-specific JSON.
2. Redis is a wake-up signal, not the source of truth. Events and job state remain in PostgreSQL, so restarting Redis does not erase work.

## Finding lifecycle

A finding represents a current condition, while its evidence records why that condition was detected. A stable fingerprint prevents the same issue from filling the dashboard with duplicates.

When a rule matches for the first time, the service opens a finding. Seeing the same condition again updates its last-detected time instead of creating another row. When the facts agree, the finding is resolved. If the condition returns later, the existing finding is reopened with fresh evidence.

For the cross-system demo, the data path is:

```text
fixture request
  -> canonical customer
  -> Stripe and HubSpot identities
  -> normalized customer facts
  -> status mismatch rule
  -> finding plus immutable evidence
  -> findings API
  -> dashboard
```

The Stripe path adds a durable inbox in front of the same rule-and-finding idea: verify the webhook, store its event and job in one transaction, notify the worker, normalize the payload, evaluate the rules, and mark the job complete.

## Domain model

Organizations are the tenant boundary. Users join them through memberships, and sessions retain the organization and user pair used for each login. Every customer, finding, integration, event, and job belongs to an organization.

The canonical customer is the internal identity shared by all sources. `customer_identities` maps Stripe, HubSpot, and future product records to it. `customer_facts` keeps the latest normalized state used by rules, while `finding_evidence` preserves the values behind an earlier decision even after current facts change.

## Technology choices

| Area | Choice | Reason for this stage |
|---|---|---|
| API | Go and `net/http` | A small dependency surface, explicit request flow, and straightforward concurrency |
| Database | PostgreSQL | Transactions, constraints, JSONB evidence, and durable job state in one place |
| Coordination | Redis | Fast login rate limits and inexpensive worker notifications |
| Web app | React, TypeScript, and Vite | A typed, compact review interface with a quick local feedback loop |
| Passwords | Argon2id | Memory-hard password hashing with a per-password salt |
| Sessions | Opaque random tokens | Immediate revocation and server-side control over current roles |
| Secrets | AES-GCM at rest | Stripe credentials are not stored as readable database values |
| Integration work | PostgreSQL-backed jobs | Webhook acknowledgement stays quick and processing can be retried |

## Repository map

```text
business-drift/
├── cmd/api/                         application entry point and wiring
├── internal/
│   ├── auth/                        passwords, sessions, middleware, and roles
│   ├── audit/                       organization-scoped activity history
│   ├── detection/                   provider-independent business rules
│   ├── findings/                    finding queries and HTTP responses
│   ├── fixtures/                    local cross-system demonstration flow
│   ├── integrations/stripe/         credentials, webhooks, sync, and worker
│   ├── organizations/               current organization endpoint
│   └── platform/                    config, database, encryption, HTTP, logging
├── migrations/                      ordered PostgreSQL schema changes
├── web/                             React and TypeScript dashboard
├── deployments/api.Dockerfile       production-style API image
├── compose.yaml                     API, PostgreSQL, Redis, and migration tool
├── Makefile                         common local and CI commands
└── .env.example                     documented development configuration
```

## Run it locally

### Prerequisites

- Go 1.25.13 or newer
- Node.js 24 with npm
- Docker with Docker Compose
- GNU Make

Install dependencies, start PostgreSQL and Redis, apply the schema, and run the API:

```bash
make install
make infra-up
make migrate-up
make run
```

In a second terminal, start the dashboard:

```bash
npm run dev --prefix web
```

Open [http://127.0.0.1:5173](http://127.0.0.1:5173). Vite proxies `/api` to the Go server at `http://127.0.0.1:8080`.

To run the API and its dependencies entirely in containers instead:

```bash
make up
make migrate-up
```

The defaults in `.env.example` are for local development only. The API already uses those development values when variables are absent. Copy the file to `.env` when you need overrides, and load it into your shell before starting the process. Production refuses to start without explicit database, Redis, and encryption settings.

## Try the first complete flow

1. Open the web app and choose **Create workspace**.
2. Enter an organization name, email, and password.
3. From the dashboard, submit the prefilled Stripe/HubSpot mismatch.
4. Open the new finding and inspect the evidence from both systems.
5. Change the source states so they agree and submit again to resolve it.

The fixture route is intentionally available only in development and test environments. It demonstrates the domain flow without requiring third-party accounts and is never registered in production.

The equivalent request body is:

```json
{
  "customer_name": "Acme",
  "stripe_customer_id": "cus_acme",
  "hubspot_company_id": "company_acme",
  "stripe_subscription_status": "canceled",
  "hubspot_customer_status": "active"
}
```

## API guide

All protected routes expect:

```text
Authorization: Bearer <access-token>
```

### Health

| Method and path | Purpose |
|---|---|
| `GET /health` | Backward-compatible process health |
| `GET /live` | Confirms that the process is alive |
| `GET /ready` | Checks PostgreSQL and Redis readiness |

### Authentication and workspace

| Method and path | Access | Purpose |
|---|---|---|
| `POST /api/v1/auth/register` | Public | Create an organization and its first owner |
| `POST /api/v1/auth/login` | Public | Issue an access/refresh token pair |
| `POST /api/v1/auth/refresh` | Refresh token | Rotate the current token pair |
| `POST /api/v1/auth/logout` | Refresh token | Revoke the token family |
| `GET /api/v1/auth/me` | Signed in | Return the trusted identity and role |
| `POST /api/v1/auth/password/change` | Signed in | Change a password and revoke sessions |
| `POST /api/v1/auth/password/request-reset` | Public | Create a short-lived reset token |
| `POST /api/v1/auth/password/reset` | Reset token | Set a new password and revoke sessions |
| `GET /api/v1/organization` | Signed in | Read the current organization |
| `GET /api/v1/audit-events` | Owner or admin | List current-organization audit events |

Login attempts are rate-limited through Redis. Only token hashes are stored in PostgreSQL. Password reset tokens are returned in development because an email provider is outside this MVP; test and production responses never expose them.

### Findings and integrations

| Method and path | Access | Purpose |
|---|---|---|
| `POST /api/v1/dev/fixture-events` | Owner or admin | Ingest a local two-system snapshot |
| `GET /api/v1/findings` | Signed in | List the latest organization findings |
| `GET /api/v1/findings/{id}` | Signed in | Read one finding with its evidence |
| `POST /api/v1/integrations/stripe` | Owner or admin | Validate and save Stripe sandbox credentials |
| `GET /api/v1/integrations/stripe` | Signed in | Read Stripe connection and sync status |
| `POST /api/v1/integrations/stripe/sync` | Owner or admin | Queue a full Stripe synchronization |
| `POST /api/v1/webhooks/stripe/{integrationID}` | Stripe signature | Accept an allowlisted Stripe event |

Stripe credentials are encrypted before storage and are never returned by the API. The integration accepts supported event types only, treats repeated Stripe event IDs idempotently, and reconciles stale active integrations by queuing another full sync.

## Configuration

| Variable | Development default | Notes |
|---|---|---|
| `APP_ENV` | `development` | One of `development`, `test`, or `production` |
| `HTTP_ADDRESS` | `:8080` | Listen host and port |
| `DATABASE_URL` | Local PostgreSQL URL | Must be supplied in production |
| `REDIS_URL` | `redis://localhost:6379/0` | Supports `redis` and `rediss` URLs |
| `ENCRYPTION_KEY` | Local demonstration key | Base64 for exactly 32 bytes; replace outside local development |
| `VITE_API_URL` | empty | Web build only; empty uses the Vite proxy or same origin |

Never reuse the checked-in demonstration encryption key in a deployed environment.

## Development commands

Run `make help` to see every target. The commands used most often are:

```bash
make infra-up       # start PostgreSQL and Redis
make migrate-up     # apply pending migrations
make run            # run the API on the host
make lint           # format/static checks plus frontend lint
make test           # race-enabled Go package tests
make build          # build API and frontend assets
make ci             # run the local CI-equivalent gate
make down           # stop containers without deleting volumes
```

GitHub Actions repeats the main checks and adds vulnerability, dependency, and secret scanning. Dependabot watches Go modules, npm packages, and workflow actions.

## Security notes

- Every tenant-owned query is scoped with the organization ID from authenticated server context, never a client-supplied tenant ID.
- Passwords are hashed with Argon2id; opaque session and reset tokens are hashed with SHA-256 before storage.
- Refresh tokens rotate. Reusing an older token revokes its family.
- Stripe webhook signatures and timestamps are verified before events are accepted.
- Request bodies are size-limited and unknown JSON fields are rejected.
- Responses receive request IDs, defensive headers, and structured request logs.
- Integration secrets use authenticated encryption, with the key supplied outside the database.

These controls are a foundation, not a claim of production certification. A real deployment would still need managed secrets, TLS at every boundary, backups, monitoring, tested recovery, email delivery, and a formal security review.

## License

No license has been added yet. Until one is chosen, the repository should be treated as source-available for evaluation rather than open source.
