# Dashboard UI

This directory contains the React-based dashboard for GitOps Promoter.

## From Project Root (Recommended)
```bash
make build-dashboard  # Installs dependencies and builds
```

## Local Development (If you want to run dev server)
```bash
cd ui/components-lib && npm install
cd ../dashboard && npm install
npm run dev
```
Open [http://localhost:5173](http://localhost:5173) in your browser.

## Production Build & Embed
```bash
npm run build:embed
```
This will build the dashboard and copy static files to `../../web/static` for Go embedding.
