# Dashboard UI

This directory contains the React-based dashboard for GitOps Promoter.

### Build Everything
```bash
make build-all
```

### Run Dashboard Server
```bash
make run-dashboard
```
Open [http://localhost:8080](http://localhost:8080) in your browser.

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

## Production Build

### Build Everything (Dashboard + Go Binary)
```bash
make build-all
```

### Build Docker Image
```bash
make docker-build
```
