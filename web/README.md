# Business Drift web

This React and TypeScript app is the review dashboard for the Business Drift API.

```bash
npm install
npm run dev
```

The local Vite server opens on `http://127.0.0.1:5173` and proxies `/api` to the Go API on port `8080`.

Use `npm run lint` and `npm run build` before committing frontend changes. Set `VITE_API_URL` only when the API is hosted separately; leave it empty for local development and same-origin deployments.
