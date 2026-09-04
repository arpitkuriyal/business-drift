# Business Drift

Business Drift finds customer-status mismatches between Stripe and HubSpot.

Example: Stripe says a subscription is cancelled, but HubSpot still marks the company as a customer. Business Drift creates one finding with the values that caused the mismatch.

## What is included

- Registration, login, token refresh, and logout
- Argon2id password hashing and hashed opaque session tokens
- Organization-scoped data and owner/admin integration controls
- Encrypted Stripe test keys and HubSpot private-app tokens
- Manual Stripe customer/subscription sync
- Manual HubSpot company sync
- Safe customer matching by business domain
- One rule: cancelled in Stripe but active in HubSpot
- Findings with evidence in a React dashboard

The intentionally small flow is:

```text
Stripe sync -> HubSpot sync -> match by domain -> compare status -> show finding
```

Stripe customer emails and HubSpot company domains must match. For example, `person@acme.com` matches `acme.com`. Common personal email domains are not matched automatically.

## Stack

- Go and `net/http`
- PostgreSQL
- Redis for login rate limiting
- React, TypeScript, and Vite
- Docker Compose

## Run locally

Requirements: Go, Node.js, Docker, and Make.

```bash
make install
make up
make migrate-up
npm run dev --prefix web
```

Open <http://127.0.0.1:5173>.

## Demo setup

1. Register a Business Drift workspace.
2. In Stripe test mode, create a customer with a business email and a cancelled subscription.
3. Connect Stripe using an `sk_test_...` or restricted `rk_test_...` key, then click **Sync Stripe**.
4. In HubSpot, create a company whose domain matches the Stripe email domain and set its lifecycle stage to `Customer`.
5. Create a HubSpot private app with `crm.objects.companies.read`, connect its token, then click **Sync HubSpot companies**.
6. Open the finding created by the mismatch.

Never commit or share either token. Integration secrets are encrypted before PostgreSQL storage.

## Main API routes

| Method and path | Purpose |
|---|---|
| `POST /api/v1/auth/register` | Create a workspace and owner |
| `POST /api/v1/auth/login` | Sign in |
| `POST /api/v1/auth/refresh` | Rotate session tokens |
| `POST /api/v1/auth/logout` | Revoke the session |
| `GET /api/v1/auth/me` | Read the signed-in identity |
| `GET /api/v1/organization` | Read the current workspace |
| `POST /api/v1/integrations/stripe` | Save a Stripe test key |
| `POST /api/v1/integrations/stripe/sync` | Import Stripe data |
| `POST /api/v1/integrations/hubspot` | Save a HubSpot private-app token |
| `POST /api/v1/integrations/hubspot/sync` | Import and compare HubSpot companies |
| `GET /api/v1/findings` | List findings |
| `GET /api/v1/findings/{id}` | Read a finding and its evidence |
| `GET /live` | Process health |
| `GET /ready` | PostgreSQL and Redis health |

## Intentional limits

- Only test Stripe keys are accepted.
- Sync is started manually from the dashboard.
- HubSpot uses a private-app token, not OAuth.
- Customer matching uses a unique business domain only.
- HubSpot's standard `lifecyclestage=customer` value means active.
- There are no Stripe or HubSpot webhooks.
- There is one cross-system mismatch rule.
