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
- Imports customers, subscriptions, companies, and lifecycle stages.
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

Sync Stripe before HubSpot. The HubSpot sync matches the imported records and runs the detection rules.

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
├── internal/integrations/stripe/    Stripe connection and sync
├── internal/integrations/hubspot/   HubSpot connection, sync, and matching
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
| `POST` | `/api/v1/integrations/stripe` | Save a Stripe test key |
| `POST` | `/api/v1/integrations/stripe/sync` | Import Stripe data |
| `GET` | `/api/v1/integrations/hubspot` | Read HubSpot connection status |
| `POST` | `/api/v1/integrations/hubspot` | Save a HubSpot token |
| `POST` | `/api/v1/integrations/hubspot/sync` | Import, match, and compare companies |
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
