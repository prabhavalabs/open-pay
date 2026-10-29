# Local Development Guide

Complete guide to set up, run, and develop the Open Lanka Payment platform locally.

---

## Prerequisites

| Tool | Version | Install |
|------|---------|---------|
| **Go** | 1.24+ | `brew install go` |
| **Node.js** | 20+ | `brew install node` |
| **pnpm** | 10+ | `npm install -g pnpm` |
| **Docker** | 24+ | [Docker Desktop](https://www.docker.com/products/docker-desktop/) |
| **golang-migrate** | latest | `brew install golang-migrate` |

Verify installation:

```bash
go version        # go1.24+
node --version    # v20+
pnpm --version    # 10+
docker --version  # Docker 24+
migrate -version  # golang-migrate
```

---

## Quick Start (5 minutes)

```bash
# 1. Clone and enter the project
git clone https://github.com/theetaz/open-pay.git
cd open-pay

# 2. Start infrastructure (PostgreSQL, Redis, NATS)
make dev

# 3. Run database migrations
make migrate

# 4. Install frontend dependencies
pnpm install

# 5. Start all 9 backend services
./scripts/start-services.sh

# 6. Start Merchant Portal (Terminal 2)
pnpm --filter merchant-portal dev

# 7. Start Admin Dashboard (Terminal 3)
pnpm --filter admin-dashboard dev --port 3001
```

**URLs:**
- Gateway API: http://localhost:8080
- Merchant Portal: http://localhost:3000
- Admin Dashboard: http://localhost:3001

**Default Admin Login:**
- Email: `admin@openlankapay.lk`
- Password: `Admin@2024`

---

## Detailed Setup

### Step 1: Infrastructure

Start PostgreSQL (port 7020), Redis (port 6379), and NATS (port 4222):

```bash
make dev
```

This runs `docker compose up -d` for the three infrastructure containers.

**What gets created:**
- PostgreSQL 16 with 8 databases (merchant_db, payment_db, settlement_db, exchange_db, webhook_db, subscription_db, admin_db, notification_db)
- Redis 7 for caching
- NATS 2 with JetStream for messaging

**Verify infrastructure is running:**

```bash
docker compose ps
```

### Step 2: Database Migrations

Run all migrations across all 8 databases:

```bash
make migrate
```

This creates all tables and seeds:
- Merchant, user, API key, branch tables
- Payment table with indexes
- Settlement balances and withdrawals
- Exchange rates (seeded with 1 USDT = 325 LKR)
- Webhook configs and deliveries
- Subscription plans and subscriptions
- Audit logs
- **Admin roles** (SUPER_ADMIN, ADMIN, VIEWER) with permissions
- **Default admin user** (admin@openlankapay.lk / Admin@2024)
- Notification table

### Step 3: Install Frontend Dependencies

```bash
pnpm install
```

This installs dependencies for both `apps/merchant-portal` and `apps/admin-dashboard`.

### Step 4: Start Backend Services

```bash
./scripts/start-services.sh
```

This starts all 9 Go microservices in the background:

| Service | Port | Database |
|---------|------|----------|
| Gateway | 8080 | — (proxy only) |
| Payment | 8081 | payment_db |
| Merchant | 8082 | merchant_db |
| Settlement | 8083 | settlement_db |
| Webhook | 8084 | webhook_db |
| Exchange | 8085 | exchange_db |
| Subscription | 8086 | subscription_db |
| Notification | 8087 | notification_db |
| Admin | 8088 | admin_db |

**Logs** are written to `/tmp/openpay-{service}.log`.

### Step 5: Start Frontend Dev Servers

**Merchant Portal** (port 3000):

```bash
pnpm --filter merchant-portal dev
```

**Admin Dashboard** (port 3001):

```bash
pnpm --filter admin-dashboard dev --port 3001
```

---

## Managing Services

### Start All Backend Services

```bash
./scripts/start-services.sh
```

### Stop All Backend Services

```bash
./scripts/start-services.sh stop
```

### Restart All Backend Services

```bash
./scripts/start-services.sh stop && ./scripts/start-services.sh
```

### Force Kill (if ports are stuck)

```bash
# Kill all service ports
for port in 8080 8081 8082 8083 8084 8085 8086 8087 8088; do
  lsof -ti:$port | xargs kill -9 2>/dev/null
done

# Kill frontend ports
lsof -ti:3000 | xargs kill -9 2>/dev/null  # merchant portal
lsof -ti:3001 | xargs kill -9 2>/dev/null  # admin dashboard
```

### View Service Logs

```bash
# All services
tail -f /tmp/openpay-*.log

# Specific service
tail -f /tmp/openpay-gateway.log
tail -f /tmp/openpay-payment.log
tail -f /tmp/openpay-merchant.log
tail -f /tmp/openpay-settlement.log
tail -f /tmp/openpay-admin.log
```

### Run a Single Service Manually

```bash
PORT=8082 go run ./services/merchant/cmd/
```

### Check Service Health

```bash
curl http://localhost:8080/healthz
# {"status":"ok"}
```

---

## Database Management

### Run All Migrations

```bash
make migrate
```

### Rollback Last Migration (all databases)

```bash
make migrate-down
```

### Create a New Migration

```bash
make migrate-create svc=payment name=add_refund_table
```

### Run Migration for a Specific Database

```bash
migrate -path migrations/merchant -database "postgres://olp:olp_dev_password@localhost:7020/merchant_db?sslmode=disable" up
```

### Connect to Database Directly

```bash
# Connect to any database
docker compose exec postgres psql -U olp -d merchant_db
docker compose exec postgres psql -U olp -d payment_db
docker compose exec postgres psql -U olp -d admin_db

# List all tables in a database
docker compose exec postgres psql -U olp -d merchant_db -c "\dt"

# Run a query
docker compose exec postgres psql -U olp -d merchant_db -c "SELECT id, business_name, kyc_status FROM merchants;"
```

### Reset All Data (start fresh)

```bash
make down-clean   # Removes all Docker volumes (deletes all data)
make dev          # Recreate infrastructure
make migrate      # Run migrations fresh
```

---

## Building & Testing

### Build All Go Services

```bash
make build
```

Binaries are placed in `bin/`.

### Run Unit Tests

```bash
make test           # Quick (short mode)
make test-v         # Verbose
make test-all       # All tests
make test-coverage  # With HTML coverage report
```

### Run Linter

```bash
make lint    # Requires: brew install golangci-lint
make vet     # Go vet only
make fmt     # Format code
```

### TypeScript Type Check

```bash
pnpm --filter merchant-portal exec tsc --noEmit
pnpm --filter admin-dashboard exec tsc --noEmit
```

### Run E2E Integration Tests

With all services running:

```bash
bash scripts/e2e-test.sh   # (if available)

# Or manually test the full flow:
# 1. Register merchant
curl -s http://localhost:8080/v1/auth/register \
  -H "Content-Type: application/json" \
  -d '{"businessName":"Test Shop","email":"test@example.com","password":"TestPass1","name":"Test User"}'

# 2. Admin login
curl -s http://localhost:8080/v1/admin/auth/login \
  -H "Content-Type: application/json" \
  -d '{"email":"admin@openlankapay.lk","password":"Admin@2024"}'
```

---

## Infrastructure Management

### Start Infrastructure Only

```bash
make dev
```

### Stop Infrastructure

```bash
make down
```

### Stop Infrastructure + Delete Data

```bash
make down-clean
```

### Start Observability Stack (Prometheus + Grafana)

```bash
make up-observability
```

- Prometheus: http://localhost:9090
- Grafana: http://localhost:3000 (admin/admin) — use port 3001 for Grafana if merchant portal is running

### View Docker Container Status

```bash
docker compose ps
```

---

## Environment Variables

All services use sensible defaults for local development. Override via environment variables:

| Variable | Default | Description |
|----------|---------|-------------|
| `JWT_SECRET` | `dev-jwt-secret-change-in-production-min32chars` | JWT signing secret |
| `DATABASE_URL` | unset | Full DB URL for one service; overrides every `DB_*` variable below |
| `DB_HOST` | `localhost` | Postgres host |
| `DB_PORT` | `7020` | Postgres port |
| `DB_USER` | `olp` | Postgres role |
| `DB_PASSWORD` | `olp_dev_password` | Postgres password |
| `DB_NAME_PREFIX` | empty | Database name = prefix + service + suffix |
| `DB_NAME_SUFFIX` | `_db` | Set it to an empty value to drop the suffix (`DB_NAME_SUFFIX=`) |
| `DB_SSLMODE` | `disable` | libpq `sslmode` |
| `DB_POOL_MAX_CONNS` | `10` | Connection pool size per service |
| `PORT` | Service-specific (8080-8088) | HTTP listen port |
| `LOG_LEVEL` | `info` | Log level (debug, info, warn, error) |
| `MERCHANT_SERVICE_URL` | `http://localhost:8082` | Gateway proxy target |
| `PAYMENT_SERVICE_URL` | `http://localhost:8081` | Gateway proxy target |
| `EXCHANGE_SERVICE_URL` | `http://localhost:8085` | Gateway proxy target |
| `SETTLEMENT_SERVICE_URL` | `http://localhost:8083` | Gateway proxy target |
| `WEBHOOK_SERVICE_URL` | `http://localhost:8084` | Gateway proxy target |
| `SUBSCRIPTION_SERVICE_URL` | `http://localhost:8086` | Gateway proxy target |
| `NOTIFICATION_SERVICE_URL` | `http://localhost:8087` | Gateway proxy target |
| `ADMIN_SERVICE_URL` | `http://localhost:8088` | Gateway proxy target |
| `VITE_API_URL` | `http://localhost:8080` | Frontend API base URL |

See `.env.example` for a complete template.

---

## Project Structure

```
open-lanka-payment/
├── apps/
│   ├── merchant-portal/     # Merchant dashboard (React + TanStack Start)
│   └── admin-dashboard/     # Admin dashboard (React + TanStack Start)
├── services/
│   ├── gateway/             # API gateway (port 8080)
│   ├── payment/             # Payment processing (port 8081)
│   ├── merchant/            # Merchant management (port 8082)
│   ├── settlement/          # Balance & withdrawals (port 8083)
│   ├── webhook/             # Webhook delivery (port 8084)
│   ├── exchange/            # Exchange rates (port 8085)
│   ├── subscription/        # Recurring payments (port 8086)
│   ├── notification/        # Email/SMS notifications (port 8087)
│   └── admin/               # Audit logs & admin auth (port 8088)
├── pkg/                     # Shared Go packages
│   ├── auth/                # JWT, HMAC, ED25519, permissions
│   ├── money/               # Currency math
│   ├── database/            # PostgreSQL connection pool
│   └── observability/       # Structured logging
├── migrations/              # SQL migrations per service
├── scripts/                 # Helper scripts
├── config/                  # Prometheus/Grafana config
├── docker-compose.yml       # Infrastructure containers
└── Makefile                 # Development commands
```

---

## Common Issues

### Port Already in Use

```bash
# Find and kill the process
lsof -ti:8082 | xargs kill -9

# Or kill all service ports
for port in 8080 8081 8082 8083 8084 8085 8086 8087 8088; do
  lsof -ti:$port | xargs kill -9 2>/dev/null
done
```

### Database Does Not Exist

If a database wasn't created during initial Docker setup:

```bash
docker compose exec postgres psql -U olp -d olp_default -c "CREATE DATABASE notification_db;"
```

### Migration Dirty State

If a migration fails halfway:

```bash
# Force set version (replace N with the version number)
migrate -path migrations/merchant -database "postgres://olp:olp_dev_password@localhost:7020/merchant_db?sslmode=disable" force N
```

### Frontend "localStorage is not defined"

This is an SSR issue. All `localStorage` access must be wrapped with:

```typescript
if (typeof window !== 'undefined') {
  localStorage.getItem('key')
}
```

### Services Not Picking Up Code Changes

The start script uses `go run` which compiles on the fly. You must restart services after code changes:

```bash
./scripts/start-services.sh stop
./scripts/start-services.sh
```

---

## Makefile Reference

```bash
make help              # Show all available commands
make dev               # Start infrastructure
make down              # Stop infrastructure
make down-clean        # Stop + delete all data
make migrate           # Run all migrations
make migrate-down      # Rollback migrations
make build             # Build all Go binaries
make test              # Run unit tests
make test-v            # Verbose tests
make test-coverage     # Tests with coverage report
make lint              # Run golangci-lint
make fmt               # Format Go code
make vet               # Run go vet
make tidy              # Run go mod tidy
make clean             # Remove build artifacts
```
