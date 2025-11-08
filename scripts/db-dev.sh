#!/usr/bin/env bash
# db-dev.sh - Database development management script for go-hookd
#
# This script manages the PostgreSQL development database container.
# Supports both Docker and Podman (auto-detects).
#
# Usage: ./scripts/db-dev.sh <command>
#
# Commands:
#   bootstrap  - Start database, run migrations, verify setup
#   start      - Start the database container
#   stop       - Stop the database container
#   teardown   - Stop and remove container + volumes (DESTRUCTIVE)
#   reset      - Teardown and bootstrap (fresh start)
#   connect    - Open psql connection to database
#   logs       - Show container logs (use -f to follow)
#   status     - Show container status
#   migrate-up - Run up migrations
#   migrate-down - Run down migrations
#   help       - Show this help message

set -e  # Exit on error

# =============================================================================
# CONFIGURATION
# =============================================================================

CONTAINER_NAME="go-hookd-postgres-dev"
DB_HOST="localhost"
DB_PORT="5433"
DB_USER="hookd_dev"
DB_PASSWORD="hookd_dev_password_change_in_production"
DB_NAME="hookd_dev"
CONNECTION_STRING="postgresql://${DB_USER}:${DB_PASSWORD}@${DB_HOST}:${DB_PORT}/${DB_NAME}?sslmode=disable"

# Colors for output
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
BLUE='\033[0;34m'
NC='\033[0m' # No Color

# =============================================================================
# HELPER FUNCTIONS
# =============================================================================

# Detect container runtime (docker or podman)
detect_runtime() {
    if command -v podman &> /dev/null; then
        echo "podman"
    elif command -v docker &> /dev/null; then
        echo "docker"
    else
        echo ""
    fi
}

# Print colored message
log_info() {
    echo -e "${BLUE}ℹ${NC} $1"
}

log_success() {
    echo -e "${GREEN}✓${NC} $1"
}

log_warning() {
    echo -e "${YELLOW}⚠${NC} $1"
}

log_error() {
    echo -e "${RED}✗${NC} $1"
}

# Check if container is running
is_container_running() {
    local runtime=$1
    $runtime ps --filter "name=${CONTAINER_NAME}" --format "{{.Names}}" 2>/dev/null | grep -q "${CONTAINER_NAME}"
}

# Check if container exists (running or stopped)
container_exists() {
    local runtime=$1
    $runtime ps -a --filter "name=${CONTAINER_NAME}" --format "{{.Names}}" 2>/dev/null | grep -q "${CONTAINER_NAME}"
}

# Wait for database to be ready
wait_for_db() {
    local runtime=$1
    log_info "Waiting for database to be ready..."

    local retries=30
    local count=0

    while [ $count -lt $retries ]; do
        if $runtime exec ${CONTAINER_NAME} pg_isready -U ${DB_USER} -d ${DB_NAME} &> /dev/null; then
            log_success "Database is ready!"
            return 0
        fi
        count=$((count + 1))
        echo -n "."
        sleep 1
    done

    echo ""
    log_error "Database did not become ready in time"
    return 1
}

# =============================================================================
# COMMAND FUNCTIONS
# =============================================================================

cmd_bootstrap() {
    local runtime=$(detect_runtime)
    if [ -z "$runtime" ]; then
        log_error "Neither docker nor podman found. Please install one of them."
        exit 1
    fi

    log_info "Using container runtime: ${runtime}"

    # Check if already running
    if is_container_running "$runtime"; then
        log_warning "Container is already running"
        log_info "Run './scripts/db-dev.sh reset' for a fresh start"
        return 0
    fi

    # Start with docker-compose/podman-compose
    log_info "Starting PostgreSQL container on port ${DB_PORT}..."

    if [ "$runtime" = "podman" ]; then
        if command -v podman-compose &> /dev/null; then
            podman-compose up -d
        else
            log_warning "podman-compose not found, using podman directly"
            podman run -d \
                --name ${CONTAINER_NAME} \
                -e POSTGRES_USER=${DB_USER} \
                -e POSTGRES_PASSWORD=${DB_PASSWORD} \
                -e POSTGRES_DB=${DB_NAME} \
                -p ${DB_PORT}:5432 \
                -v go-hookd-postgres-data:/var/lib/postgresql/data \
                postgres:16-alpine
        fi
    else
        docker compose up -d
    fi

    # Wait for database
    wait_for_db "$runtime"

    # Run migrations
    log_info "Running migrations..."
    cmd_migrate_up

    log_success "Bootstrap complete!"
    log_info "Connection string: ${CONNECTION_STRING}"
}

cmd_start() {
    local runtime=$(detect_runtime)
    if [ -z "$runtime" ]; then
        log_error "Neither docker nor podman found"
        exit 1
    fi

    if is_container_running "$runtime"; then
        log_warning "Container is already running"
        return 0
    fi

    log_info "Starting container..."

    if [ "$runtime" = "podman" ] && command -v podman-compose &> /dev/null; then
        podman-compose start
    elif [ "$runtime" = "docker" ]; then
        docker compose start
    else
        $runtime start ${CONTAINER_NAME}
    fi

    wait_for_db "$runtime"
    log_success "Container started"
}

cmd_stop() {
    local runtime=$(detect_runtime)
    if [ -z "$runtime" ]; then
        log_error "Neither docker nor podman found"
        exit 1
    fi

    if ! is_container_running "$runtime"; then
        log_warning "Container is not running"
        return 0
    fi

    log_info "Stopping container..."

    if [ "$runtime" = "podman" ] && command -v podman-compose &> /dev/null; then
        podman-compose stop
    elif [ "$runtime" = "docker" ]; then
        docker compose stop
    else
        $runtime stop ${CONTAINER_NAME}
    fi

    log_success "Container stopped"
}

cmd_teardown() {
    local runtime=$(detect_runtime)
    if [ -z "$runtime" ]; then
        log_error "Neither docker nor podman found"
        exit 1
    fi

    log_warning "⚠️  DESTRUCTIVE OPERATION: This will remove all data!"
    read -p "Are you sure? (y/N): " -n 1 -r
    echo

    if [[ ! $REPLY =~ ^[Yy]$ ]]; then
        log_info "Teardown cancelled"
        return 0
    fi

    log_info "Tearing down database..."

    if [ "$runtime" = "podman" ] && command -v podman-compose &> /dev/null; then
        podman-compose down -v
    elif [ "$runtime" = "docker" ]; then
        docker compose down -v
    else
        if container_exists "$runtime"; then
            $runtime stop ${CONTAINER_NAME} 2>/dev/null || true
            $runtime rm -f ${CONTAINER_NAME} 2>/dev/null || true
        fi
        $runtime volume rm go-hookd-postgres-data 2>/dev/null || true
    fi

    log_success "Teardown complete"
}

cmd_reset() {
    log_info "Resetting database (teardown + bootstrap)..."
    cmd_teardown
    cmd_bootstrap
}

cmd_connect() {
    local runtime=$(detect_runtime)
    if [ -z "$runtime" ]; then
        log_error "Neither docker nor podman found"
        exit 1
    fi

    if ! is_container_running "$runtime"; then
        log_error "Container is not running. Run './scripts/db-dev.sh start' first"
        exit 1
    fi

    log_info "Connecting to database..."
    log_info "Connection: ${DB_USER}@${DB_HOST}:${DB_PORT}/${DB_NAME}"
    echo ""

    $runtime exec -it ${CONTAINER_NAME} psql -U ${DB_USER} -d ${DB_NAME}
}

cmd_logs() {
    local runtime=$(detect_runtime)
    if [ -z "$runtime" ]; then
        log_error "Neither docker nor podman found"
        exit 1
    fi

    if ! container_exists "$runtime"; then
        log_error "Container does not exist"
        exit 1
    fi

    # Check if -f flag is passed
    if [ "$1" = "-f" ] || [ "$1" = "--follow" ]; then
        $runtime logs -f ${CONTAINER_NAME}
    else
        $runtime logs ${CONTAINER_NAME}
    fi
}

cmd_status() {
    local runtime=$(detect_runtime)
    if [ -z "$runtime" ]; then
        log_error "Neither docker nor podman found"
        exit 1
    fi

    log_info "Container runtime: ${runtime}"
    log_info "Container name: ${CONTAINER_NAME}"
    log_info "Database port: ${DB_PORT}"
    echo ""

    if is_container_running "$runtime"; then
        log_success "Container is RUNNING"
        $runtime ps --filter "name=${CONTAINER_NAME}"
    elif container_exists "$runtime"; then
        log_warning "Container exists but is NOT running"
        $runtime ps -a --filter "name=${CONTAINER_NAME}"
    else
        log_warning "Container does NOT exist"
    fi
}

cmd_migrate_up() {
    local runtime=$(detect_runtime)
    if [ -z "$runtime" ]; then
        log_error "Neither docker nor podman found"
        exit 1
    fi

    if ! is_container_running "$runtime"; then
        log_error "Container is not running. Run './scripts/db-dev.sh start' first"
        exit 1
    fi

    log_info "Running UP migrations..."

    # Copy migration file to container and execute
    for migration in migrations/postgres/*_up.sql; do
        if [ -f "$migration" ]; then
            log_info "Applying: $(basename $migration)"
            $runtime exec -i ${CONTAINER_NAME} psql -U ${DB_USER} -d ${DB_NAME} < "$migration"
        fi
    done

    log_success "Migrations applied"
}

cmd_migrate_down() {
    local runtime=$(detect_runtime)
    if [ -z "$runtime" ]; then
        log_error "Neither docker nor podman found"
        exit 1
    fi

    if ! is_container_running "$runtime"; then
        log_error "Container is not running. Run './scripts/db-dev.sh start' first"
        exit 1
    fi

    log_warning "⚠️  DESTRUCTIVE OPERATION: This will drop all tables!"
    read -p "Are you sure? (y/N): " -n 1 -r
    echo

    if [[ ! $REPLY =~ ^[Yy]$ ]]; then
        log_info "Migration rollback cancelled"
        return 0
    fi

    log_info "Running DOWN migrations..."

    # Run down migrations in reverse order
    for migration in $(ls -r migrations/postgres/*_down.sql 2>/dev/null); do
        if [ -f "$migration" ]; then
            log_info "Reverting: $(basename $migration)"
            $runtime exec -i ${CONTAINER_NAME} psql -U ${DB_USER} -d ${DB_NAME} < "$migration"
        fi
    done

    log_success "Migrations reverted"
}

cmd_help() {
    cat << EOF
${BLUE}go-hookd Database Development Tool${NC}

${GREEN}Usage:${NC}
  ./scripts/db-dev.sh <command>

${GREEN}Commands:${NC}
  ${YELLOW}bootstrap${NC}     Start database, run migrations, verify setup
  ${YELLOW}start${NC}         Start the database container
  ${YELLOW}stop${NC}          Stop the database container
  ${YELLOW}teardown${NC}      Stop and remove container + volumes (DESTRUCTIVE)
  ${YELLOW}reset${NC}         Teardown and bootstrap (fresh start)
  ${YELLOW}connect${NC}       Open psql connection to database
  ${YELLOW}logs${NC}          Show container logs (use -f to follow)
  ${YELLOW}status${NC}        Show container status
  ${YELLOW}migrate-up${NC}    Run up migrations
  ${YELLOW}migrate-down${NC}  Run down migrations (DESTRUCTIVE)
  ${YELLOW}help${NC}          Show this help message

${GREEN}Configuration:${NC}
  Container: ${CONTAINER_NAME}
  Port:      ${DB_PORT}
  Database:  ${DB_NAME}
  User:      ${DB_USER}

${GREEN}Examples:${NC}
  ./scripts/db-dev.sh bootstrap          # Initial setup
  ./scripts/db-dev.sh connect            # Open psql
  ./scripts/db-dev.sh logs -f            # Follow logs
  ./scripts/db-dev.sh reset              # Fresh start

${BLUE}Excellence. Always.${NC}
EOF
}

# =============================================================================
# MAIN
# =============================================================================

# Check if command provided
if [ $# -eq 0 ]; then
    cmd_help
    exit 0
fi

# Execute command
case "$1" in
    bootstrap)
        cmd_bootstrap
        ;;
    start)
        cmd_start
        ;;
    stop)
        cmd_stop
        ;;
    teardown)
        cmd_teardown
        ;;
    reset)
        cmd_reset
        ;;
    connect)
        cmd_connect
        ;;
    logs)
        cmd_logs "$2"
        ;;
    status)
        cmd_status
        ;;
    migrate-up)
        cmd_migrate_up
        ;;
    migrate-down)
        cmd_migrate_down
        ;;
    help|--help|-h)
        cmd_help
        ;;
    *)
        log_error "Unknown command: $1"
        echo ""
        cmd_help
        exit 1
        ;;
esac
