# Dashboard UI

This directory contains the React-based dashboard for GitOps Promoter.

The built static files are embedded into the Go binary via the `ui/web/` directory.

### Build Everything

```bash
make build-all
```

### Run Dashboard Server

```bash
make run-dashboard
```

Open [http://localhost:8080](http://localhost:8080) in your browser.

### Run Vite dev server

```bash
make run-dashboard-dev
```

Open [http://localhost:5173](http://localhost:5173) in your browser. This will hit the Vite dev server which allows for hot module reloading, keeping up to date with the latest UI changes.

### Run Controller

```bash
make run
```

## Development

### Build Dashboard Only

```bash
make build-dashboard  # Installs dependencies and builds
```

### Local Development Server

```bash
cd ui/components-lib && npm install
cd ../dashboard && npm install
npm run dev
```

Open [http://localhost:5173](http://localhost:5173) in your browser.

### Mock data

Append `?mock=true` to a dashboard route (e.g. [http://localhost:5173/promotion-strategies?mock=true](http://localhost:5173/promotion-strategies?mock=true)) to render a stable, built-in set of `PromotionStrategy` fixtures instead of fetching live data. This is useful for building UI against specific states (in-flight promotions, history, PR states) without a running controller, and the fixtures are also importable from `@shared/fixtures/mockData` for unit tests.

The mock code is gated on `import.meta.env.DEV`, so it is dead-code-eliminated from production builds (`npm run build`) and never ships in the bundle.

## Production Build

### Build Everything (Dashboard + Go Binary)

```bash
make build-all
```

### Build Docker Image

```bash
make docker-build
```
