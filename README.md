# Business Drift

Business Drift finds customer mismatches between Stripe and HubSpot, such as a canceled subscription for a company still marked as a customer.

![Business Drift architecture](docs/architecture.svg)

## Features

- Imports Stripe customers and subscriptions, and HubSpot companies.
- Matches records by company domain.
- Finds status mismatches and records missing from either system.
- Shows findings with the source data behind them.
- Supports multiple organizations, user roles, and encrypted integration credentials.
- Uses signed webhooks and background jobs to keep data up to date.

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

Open the address printed by Vite. Create a workspace with an organization name, email, and password of at least 12 characters. The first user becomes the owner.

## Connect Stripe and HubSpot

In **Stripe**, add a sandbox API key, create a customer with a business email, and select **Sync Stripe**.

In **HubSpot**, create a private app with `crm.objects.companies.read` access. Add its token in Business Drift and select **Sync HubSpot companies**. A matching company domain, such as `acme.com`, links `person@acme.com` to the company.

Findings appear under **Findings**. Personal email domains are not matched automatically.

## Webhooks and background jobs

The integration screens show each webhook endpoint. Add the endpoint and signing secret in Stripe or HubSpot, then subscribe to the relevant customer or company changes. Business Drift verifies webhook signatures and queues reconciliation in its background worker. Repeated event IDs are ignored, and failed jobs retry after 30 seconds.

For Stripe, subscribe to `customer.subscription.created`, `customer.subscription.updated`, and `customer.subscription.deleted`. In HubSpot, subscribe to company changes and provide the app secret in the HubSpot settings form.

Webhook setup requires a public backend URL. For local Stripe testing, run:

```bash
stripe listen --events customer.subscription.created,customer.subscription.updated,customer.subscription.deleted \
  --forward-to localhost:8080/api/v1/webhooks/stripe/<integration-id>
```

Use the `whsec_...` secret printed by Stripe CLI. Never use a live Stripe key or real card for the demo.

## Configuration

Docker Compose sets local defaults. For production, set `APP_ENV=production` and provide `DATABASE_URL`, `REDIS_URL`, and `ENCRYPTION_KEY`.

Generate a 32-byte encryption key with:

```bash
openssl rand -base64 32
```

The frontend can use `VITE_API_URL` in `web/.env` when the API is hosted separately. See `web/.env.example`.

## Useful commands

```bash
make test
make lint
make build
make ci
make down
```

## License

Licensed under the [MIT License](LICENSE).
