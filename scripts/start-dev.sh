#!/bin/bash
# =============================================================================
# Open Lanka Payment — Development Environment Starter
# Starts all infrastructure, Go services (with hot reload), and frontends.
#
# Usage:
#   ./scripts/start-dev.sh          Start everything
#   ./scripts/start-dev.sh stop     Stop everything
#   ./scripts/start-dev.sh status   Show status of all services
# =============================================================================

set -e

ROOT_DIR="$(cd "$(dirname "$0")/.." && pwd)"
cd "$ROOT_DIR"

# Ensure GOPATH/bin is in PATH (where `go install` puts binaries)
GOBIN="${GOBIN:-$(go env GOPATH)/bin}"
export PATH="$GOBIN:$PATH"

PIDS_DIR="$ROOT_DIR/.pids"
LOGS_DIR="$ROOT_DIR/.logs"
mkdir -p "$PIDS_DIR" "$LOGS_DIR"

# Colors
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
BLUE='\033[0;34m'
MAGENTA='\033[0;35m'
CYAN='\033[0;36m'
BOLD='\033[1m'
DIM='\033[2m'
NC='\033[0m'

# Services definition: name:port (host-published 70xx scheme)
GO_SERVICES=(
    "gateway:7000"
    "payment:7001"
    "merchant:7002"
    "settlement:7003"
    "webhook:7004"
    "exchange:7005"
    "subscription:7006"
    "notification:7007"
    "admin:7008"
    "directdebit:7009"
)

FRONTEND_APPS=(
    "merchant-portal:7010"
    "admin-dashboard:7011"
)

print_banner() {
    echo ""
    echo -e "${BOLD}${BLUE}╔══════════════════════════════════════════════════╗${NC}"
    echo -e "${BOLD}${BLUE}║          Open Lanka Payment — Dev Mode           ║${NC}"
    echo -e "${BOLD}${BLUE}╚══════════════════════════════════════════════════╝${NC}"
    echo ""
}

print_summary() {
    echo ""
    echo -e "${BOLD}${GREEN}═══════════════════════════════════════════════════${NC}"
    echo -e "${BOLD}${GREEN}  All services are running!${NC}"
    echo -e "${BOLD}${GREEN}═══════════════════════════════════════════════════${NC}"
    echo ""
    echo -e "${BOLD}  Infrastructure:${NC}"
    echo -e "  ${CYAN}PostgreSQL${NC}       localhost:7020"
    echo -e "  ${CYAN}Redis${NC}            localhost:7021"
    echo -e "  ${CYAN}NATS${NC}             nats://localhost:7022"
    echo -e "  ${CYAN}NATS Monitor${NC}     http://localhost:7023"
    echo -e "  ${CYAN}MinIO Console${NC}    http://localhost:7025  ${DIM}(minioadmin / minioadmin123)${NC}"
    echo -e "  ${CYAN}MinIO API${NC}        http://localhost:7024"
    echo -e "  ${CYAN}Mailpit UI${NC}       http://localhost:7027  ${DIM}(catches all dev emails)${NC}"
    echo -e "  ${CYAN}Mailpit SMTP${NC}     localhost:7026"
    echo ""
    echo -e "${BOLD}  Go Services (hot reload via air):${NC}"
    for entry in "${GO_SERVICES[@]}"; do
        svc="${entry%%:*}"
        port="${entry##*:}"
        echo -e "  ${MAGENTA}${svc}${NC}$(printf '%*s' $((18 - ${#svc})) '')http://localhost:${port}"
    done
    echo ""
    echo -e "${BOLD}  Frontend Apps (Vite HMR):${NC}"
    echo -e "  ${YELLOW}Merchant Portal${NC}  http://localhost:7010"
    echo -e "  ${YELLOW}Admin Dashboard${NC}  http://localhost:7011"
    echo ""
    echo -e "${BOLD}  Logs:${NC}"
    echo -e "  ${DIM}Go services:   .logs/<service>.log${NC}"
    echo -e "  ${DIM}Frontends:     .logs/<app>.log${NC}"
    echo ""
    echo -e "${BOLD}  Commands:${NC}"
    echo -e "  ${DIM}Stop all:      make stop${NC}"
    echo -e "  ${DIM}Status:        make status${NC}"
    echo -e "  ${DIM}Logs:          tail -f .logs/<service>.log${NC}"
    echo ""
}

check_tool() {
    if ! command -v "$1" &> /dev/null; then
        echo -e "${RED}Error: '$1' is not installed.${NC}"
        echo -e "${DIM}$2${NC}"
        return 1
    fi
}

stop_all() {
    echo -e "${YELLOW}Stopping all services...${NC}"

    # Stop Go services
    for entry in "${GO_SERVICES[@]}"; do
        svc="${entry%%:*}"
        pidfile="$PIDS_DIR/$svc.pid"
        if [ -f "$pidfile" ]; then
            pid=$(cat "$pidfile")
            if kill -0 "$pid" 2>/dev/null; then
                kill "$pid" 2>/dev/null || true
                echo -e "  ${RED}Stopped${NC} $svc (PID $pid)"
            fi
            rm -f "$pidfile"
        fi
    done

    # Stop frontend apps
    for entry in "${FRONTEND_APPS[@]}"; do
        app="${entry%%:*}"
        pidfile="$PIDS_DIR/$app.pid"
        if [ -f "$pidfile" ]; then
            pid=$(cat "$pidfile")
            if kill -0 "$pid" 2>/dev/null; then
                kill "$pid" 2>/dev/null || true
                echo -e "  ${RED}Stopped${NC} $app (PID $pid)"
            fi
            rm -f "$pidfile"
        fi
    done

    # Stop docker infrastructure
    echo -e "  ${YELLOW}Stopping Docker containers...${NC}"
    docker compose down 2>/dev/null || true

    echo -e "${GREEN}All services stopped.${NC}"
    exit 0
}

show_status() {
    local running_docker=0
    local total_docker=0
    local running_go=0
    local total_go=0
    local running_fe=0
    local total_fe=0

    echo ""
    echo -e "${BOLD}${BLUE}╔══════════════════════════════════════════════════╗${NC}"
    echo -e "${BOLD}${BLUE}║        Open Lanka Payment — Service Status       ║${NC}"
    echo -e "${BOLD}${BLUE}╚══════════════════════════════════════════════════╝${NC}"
    echo ""

    # ─── Infrastructure ───
    echo -e "${BOLD}  Infrastructure (Docker):${NC}"
    echo ""

    _infra_status() {
        local container="$1" label="$2" url="$3" note="$4"
        local padding=$((18 - ${#label}))
        total_docker=$((total_docker + 1))

        if docker compose ps --status running 2>/dev/null | grep -q "$container"; then
            running_docker=$((running_docker + 1))
            printf "    ${GREEN}●${NC} ${CYAN}%s${NC}%*s%s" "$label" "$padding" "" "$url"
            [ -n "$note" ] && printf "  ${DIM}(%s)${NC}" "$note"
            echo ""
        else
            printf "    ${RED}●${NC} ${CYAN}%s${NC}%*s${DIM}not running${NC}\n" "$label" "$padding" ""
        fi
    }

    _infra_status "postgres" "PostgreSQL"    "localhost:7020"           ""
    _infra_status "redis"    "Redis"         "localhost:7021"           ""
    _infra_status "nats"     "NATS"          "nats://localhost:7022"    "monitor: http://localhost:7023"
    _infra_status "minio"    "MinIO Console" "http://localhost:7025"    "minioadmin / minioadmin123"
    _infra_status "mailpit"  "Mailpit UI"    "http://localhost:7027"    "catches all dev emails"

    echo ""

    # ─── Go Services ───
    echo -e "${BOLD}  Go Services (hot reload via air):${NC}"
    echo ""

    for entry in "${GO_SERVICES[@]}"; do
        svc="${entry%%:*}"
        port="${entry##*:}"
        total_go=$((total_go + 1))
        pidfile="$PIDS_DIR/$svc.pid"
        local padding=$((18 - ${#svc}))

        if [ -f "$pidfile" ] && kill -0 "$(cat "$pidfile")" 2>/dev/null; then
            running_go=$((running_go + 1))
            printf "    ${GREEN}●${NC} ${MAGENTA}%s${NC}%*shttp://localhost:%s\n" "$svc" "$padding" "" "$port"
        else
            printf "    ${RED}●${NC} ${MAGENTA}%s${NC}%*s${DIM}not running  (port %s)${NC}\n" "$svc" "$padding" "" "$port"
        fi
    done

    echo ""

    # ─── Frontend Apps ───
    echo -e "${BOLD}  Frontend Apps (Vite HMR):${NC}"
    echo ""

    _fe_status() {
        local app="$1" port="$2" label="$3"
        local padding=$((18 - ${#label}))
        total_fe=$((total_fe + 1))
        local pidfile="$PIDS_DIR/$app.pid"

        if [ -f "$pidfile" ] && kill -0 "$(cat "$pidfile")" 2>/dev/null; then
            running_fe=$((running_fe + 1))
            printf "    ${GREEN}●${NC} ${YELLOW}%s${NC}%*shttp://localhost:%s\n" "$label" "$padding" "" "$port"
        else
            printf "    ${RED}●${NC} ${YELLOW}%s${NC}%*s${DIM}not running  (port %s)${NC}\n" "$label" "$padding" "" "$port"
        fi
    }

    _fe_status "merchant-portal" "7010" "Merchant Portal"
    _fe_status "admin-dashboard" "7011" "Admin Dashboard"

    echo ""

    # ─── Production URLs ───
    echo -e "${BOLD}  Production:${NC}"
    echo ""
    echo -e "    ${CYAN}API Gateway${NC}      https://olp-api.nipuntheekshana.com"
    echo -e "    ${YELLOW}Merchant Portal${NC}  https://olp-merchant.nipuntheekshana.com"
    echo -e "    ${YELLOW}Admin Dashboard${NC}  https://olp-admin.nipuntheekshana.com"
    echo ""

    # ─── Summary ───
    local total=$((total_docker + total_go + total_fe))
    local running=$((running_docker + running_go + running_fe))

    if [ "$running" -eq "$total" ]; then
        echo -e "  ${GREEN}${BOLD}All ${total} services running${NC}"
    elif [ "$running" -eq 0 ]; then
        echo -e "  ${RED}${BOLD}No services running${NC}  ${DIM}(run: make start)${NC}"
    else
        echo -e "  ${YELLOW}${BOLD}${running}/${total} services running${NC}  ${DIM}(${running_docker}/${total_docker} infra, ${running_go}/${total_go} go, ${running_fe}/${total_fe} frontend)${NC}"
    fi

    echo ""
    echo -e "  ${DIM}Commands:  make start  |  make stop  |  make status${NC}"
    echo -e "  ${DIM}Logs:      tail -f .logs/<service>.log${NC}"
    echo ""

    exit 0
}

# Handle commands
case "${1:-}" in
    stop)  stop_all ;;
    status) show_status ;;
esac

print_banner

# ─── Check prerequisites ───
echo -e "${BOLD}Checking prerequisites...${NC}"
MISSING=0
check_tool "docker" "Install Docker: https://docs.docker.com/get-docker/" || MISSING=1
check_tool "go" "Install Go: https://go.dev/dl/" || MISSING=1
check_tool "air" "Install air: go install github.com/air-verse/air@latest" || MISSING=1
check_tool "pnpm" "Install pnpm: npm install -g pnpm" || MISSING=1
if [ "$MISSING" -eq 1 ]; then
    echo -e "${RED}Please install missing tools and try again.${NC}"
    exit 1
fi
echo -e "${GREEN}All prerequisites found.${NC}"
echo ""

# ─── Copy .env if missing ───
if [ ! -f "$ROOT_DIR/.env" ]; then
    echo -e "${YELLOW}No .env file found — copying from .env.example${NC}"
    cp "$ROOT_DIR/.env.example" "$ROOT_DIR/.env"
fi

# Source .env
set -a
source "$ROOT_DIR/.env"
set +a

# ─── Start Infrastructure ───
echo -e "${BOLD}Starting infrastructure...${NC}"
docker compose up -d postgres redis nats minio minio-init mailpit

echo -e "  Waiting for PostgreSQL..."
TRIES=0
until docker compose exec -T postgres pg_isready -U olp 2>/dev/null; do
    TRIES=$((TRIES + 1))
    if [ "$TRIES" -ge 30 ]; then
        echo -e "${RED}PostgreSQL failed to start!${NC}"
        exit 1
    fi
    sleep 1
done
echo -e "  ${GREEN}PostgreSQL ready.${NC}"

echo -e "  Waiting for other services..."
sleep 2
echo -e "  ${GREEN}Infrastructure ready.${NC}"
echo ""

# ─── Run Migrations ───
if command -v migrate &> /dev/null; then
    echo -e "${BOLD}Running database migrations...${NC}"
    DB_URL_BASE="postgres://${DB_USER:-olp}:${DB_PASSWORD:-olp_dev_password}@${DB_HOST:-localhost}:${DB_PORT:-7020}"
    for db in merchant payment settlement exchange webhook subscription admin notification directdebit; do
        db_name="${DB_NAME_PREFIX:-}${db}${DB_NAME_SUFFIX-_db}"
        migrate -path "migrations/$db" -database "${DB_URL_BASE}/${db_name}?sslmode=${DB_SSLMODE:-disable}" up 2>/dev/null || true
    done
    echo -e "  ${GREEN}Migrations complete.${NC}"
    echo ""
else
    echo -e "${YELLOW}Skipping migrations — 'migrate' CLI not found.${NC}"
    echo -e "${DIM}  Install: go install -tags 'postgres' github.com/golang-migrate/migrate/v4/cmd/migrate@latest${NC}"
    echo ""
fi

# ─── Start Go Services with Air (hot reload) ───
echo -e "${BOLD}Starting Go services with hot reload...${NC}"
for entry in "${GO_SERVICES[@]}"; do
    svc="${entry%%:*}"
    port="${entry##*:}"

    # Kill existing if running
    if [ -f "$PIDS_DIR/$svc.pid" ]; then
        old_pid=$(cat "$PIDS_DIR/$svc.pid")
        kill "$old_pid" 2>/dev/null || true
        rm -f "$PIDS_DIR/$svc.pid"
    fi

    # Start with air for hot reload
    PORT=$port air \
        --build.cmd "go build -o ./tmp/air-$svc ./services/$svc/cmd/" \
        --build.bin "./tmp/air-$svc" \
        --build.include_ext "go" \
        --build.exclude_dir "bin,tmp,node_modules,dist,apps,migrations,scripts,config,e2e,.git,.pids,.logs" \
        > "$LOGS_DIR/$svc.log" 2>&1 &

    echo $! > "$PIDS_DIR/$svc.pid"
    echo -e "  ${GREEN}Started${NC} $svc on :$port (PID $!)"
done
echo ""

# ─── Start Frontend Apps ───
echo -e "${BOLD}Starting frontend apps with Vite HMR...${NC}"
for entry in "${FRONTEND_APPS[@]}"; do
    app="${entry%%:*}"
    port="${entry##*:}"

    # Kill existing if running
    if [ -f "$PIDS_DIR/$app.pid" ]; then
        old_pid=$(cat "$PIDS_DIR/$app.pid")
        kill "$old_pid" 2>/dev/null || true
        rm -f "$PIDS_DIR/$app.pid"
    fi

    # Install deps if needed
    if [ ! -d "$ROOT_DIR/apps/$app/node_modules" ]; then
        echo -e "  ${YELLOW}Installing deps for $app...${NC}"
        (cd "$ROOT_DIR/apps/$app" && pnpm install) > "$LOGS_DIR/$app-install.log" 2>&1
    fi

    # Start Vite dev server
    (cd "$ROOT_DIR/apps/$app" && pnpm dev) > "$LOGS_DIR/$app.log" 2>&1 &
    echo $! > "$PIDS_DIR/$app.pid"
    echo -e "  ${GREEN}Started${NC} $app on :$port (PID $!)"
done

print_summary
