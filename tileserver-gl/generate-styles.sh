#!/usr/bin/env bash
set -euo pipefail

# Directory where this script resides
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
ROOT_DIR="$(cd "${SCRIPT_DIR}/.." && pwd)"

# Load .env file from repository root if present
if [ -f "${ROOT_DIR}/.env" ]; then
    set -a
    # shellcheck disable=SC1091
    source "${ROOT_DIR}/.env"
    set +a
fi

DB_NAME="${POSTGRES_DB:-wanderwell}"
DB_USER="${POSTGRES_USER:-postgres}"
DB_PASSWORD="${POSTGRES_PASSWORD:-}"
DB_HOST="${POSTGRES_HOST:-localhost}"
DB_PORT="${POSTGRES_PORT:-5432}"

ROUTES_TEMPLATE="${SCRIPT_DIR}/routes-style.template.json"
EXPLORER_TEMPLATE="${SCRIPT_DIR}/explorer-tiles-style.template.json"
CONFIG_TEMPLATE="${SCRIPT_DIR}/config.template.json"

if [ ! -f "${ROUTES_TEMPLATE}" ]; then
    echo "Error: Routes template not found at ${ROUTES_TEMPLATE}" >&2
    exit 1
fi

if [ ! -f "${EXPLORER_TEMPLATE}" ]; then
    echo "Error: Explorer tiles template not found at ${EXPLORER_TEMPLATE}" >&2
    exit 1
fi

# Fetch athlete/user IDs from database
fetch_user_ids() {
    # 1. Try via running postgis docker container if docker is available
    if command -v docker >/dev/null 2>&1; then
        local container_id
        container_id=$(docker ps -q -f "name=postgis" 2>/dev/null | head -n 1)
        if [ -n "${container_id}" ]; then
            docker exec -i "${container_id}" psql -U "${DB_USER}" -d "${DB_NAME}" -t -A -c "SELECT id FROM athlete;" 2>/dev/null && return 0
        fi
    fi

    # 2. Try via local psql
    if command -v psql >/dev/null 2>&1; then
        PGPASSWORD="${DB_PASSWORD}" psql -h "${DB_HOST}" -p "${DB_PORT}" -U "${DB_USER}" -d "${DB_NAME}" -t -A -c "SELECT id FROM athlete;" 2>/dev/null && return 0
    fi

    return 1
}

echo "Fetching user IDs from database..."
if ! USER_IDS=$(fetch_user_ids); then
    echo "Error: Failed to fetch user IDs from database." >&2
    echo "Ensure PostGIS container is running or psql is configured and accessible." >&2
    exit 1
fi

CONFIG_STYLES_BLOCK=""
USER_COUNT=0

for user_id in ${USER_IDS}; do
    # Verify user_id is a valid integer
    if ! [[ "${user_id}" =~ ^[0-9]+$ ]]; then
        continue
    fi

    echo "Generating style files for user ID: ${user_id}"

    ROUTES_FILE="routes-style-${user_id}.json"
    EXPLORER_FILE="explorer-tiles-style-${user_id}.json"

    # Generate routes-style.json for this user
    sed "s/{{USER_ID}}/${user_id}/g" "${ROUTES_TEMPLATE}" > "${SCRIPT_DIR}/${ROUTES_FILE}"
    sed "s/{{USER_ID}}/${user_id}/g" "${ROUTES_TEMPLATE}" > "${SCRIPT_DIR}/routes-style.json"

    # Generate explorer-tiles-style.json for this user
    sed "s/{{USER_ID}}/${user_id}/g" "${EXPLORER_TEMPLATE}" > "${SCRIPT_DIR}/${EXPLORER_FILE}"
    sed "s/{{USER_ID}}/${user_id}/g" "${EXPLORER_TEMPLATE}" > "${SCRIPT_DIR}/explorer-tiles-stle-${user_id}.json"
    sed "s/{{USER_ID}}/${user_id}/g" "${EXPLORER_TEMPLATE}" > "${SCRIPT_DIR}/explorer-tiles-style.json"

    # Build entry for config.json
    STYLE_ENTRY=$(cat <<EOF
    "routes-${user_id}": {
      "style": "${ROUTES_FILE}",
      "tilejson": {
        "type": "overlay",
        "format": "png",
        "bounds": [-180, -85.05112877980659, 180, 85.05112877980659]
      }
    },
    "explorer-tiles-${user_id}": {
      "style": "${EXPLORER_FILE}",
      "tilejson": {
        "type": "overlay",
        "format": "png",
        "bounds": [-180, -85.05112877980659, 180, 85.05112877980659]
      }
    }
EOF
)

    if [ -z "${CONFIG_STYLES_BLOCK}" ]; then
        CONFIG_STYLES_BLOCK="${STYLE_ENTRY}"
    else
        CONFIG_STYLES_BLOCK="${CONFIG_STYLES_BLOCK},
${STYLE_ENTRY}"
    fi

    USER_COUNT=$((USER_COUNT + 1))
done

# If template exists, update config.json with all per-user styles plus default fallbacks
if [ -f "${CONFIG_TEMPLATE}" ]; then
    DEFAULT_ENTRIES=$(cat <<EOF
    "routes": {
      "style": "routes-style.json",
      "tilejson": {
        "type": "overlay",
        "format": "png",
        "bounds": [-180, -85.05112877980659, 180, 85.05112877980659]
      }
    },
    "explorer-tiles": {
      "style": "explorer-tiles-style.json",
      "tilejson": {
        "type": "overlay",
        "format": "png",
        "bounds": [-180, -85.05112877980659, 180, 85.05112877980659]
      }
    }
EOF
)
    ALL_STYLES="${DEFAULT_ENTRIES}"
    if [ -n "${CONFIG_STYLES_BLOCK}" ]; then
        ALL_STYLES="${ALL_STYLES},
${CONFIG_STYLES_BLOCK}"
    fi

    # Read config template and replace {{STYLES}}
    node -e '
      const fs = require("fs");
      const template = fs.readFileSync(process.argv[1], "utf8");
      const styles = process.argv[2];
      const output = template.replace("{{STYLES}}", styles);
      fs.writeFileSync(process.argv[3], output);
    ' "${CONFIG_TEMPLATE}" "${ALL_STYLES}" "${SCRIPT_DIR}/config.json"
    echo "Updated config.json with per-user style endpoints."
fi

if [ "${USER_COUNT}" -eq 0 ]; then
    echo "No users found in database."
else
    echo "Successfully generated styles for ${USER_COUNT} user(s)."
fi
