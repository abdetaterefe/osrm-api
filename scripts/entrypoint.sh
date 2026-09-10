#!/bin/bash
set -e

PORT="${PORT:-3000}"
export PORT
export OSRM_URL="${OSRM_URL:-http://localhost:5000}"

echo "=== Starting OSRM Bicycle Routing Service ==="
echo "Port: $PORT"
echo "OSRM Backend URL: $OSRM_URL"

# Find map file (checks /data for mounted volumes, falls back to embedded /opt/osrm-data)
if [ -f "/data/ethiopia-bicycle.osrm.mldgr" ]; then
  MAP_PATH="/data/ethiopia-bicycle.osrm"
  echo "Using map from /data: $MAP_PATH"
elif [ -f "/opt/osrm-data/ethiopia-bicycle.osrm.mldgr" ]; then
  MAP_PATH="/opt/osrm-data/ethiopia-bicycle.osrm"
  echo "Using embedded pre-compiled map: $MAP_PATH"
else
  echo "ERROR: Map data not found in /data or /opt/osrm-data!"
  ls -la /opt/osrm-data/ /data/ 2>/dev/null || true
  exit 1
fi

# Start osrm-routed in background (options must precede the positional map path)
echo "Starting osrm-routed (bicycle)..."
osrm-routed --algorithm mld --max-table-size 1000 --port 5000 "$MAP_PATH" &
OSRM_PID=$!

# Wait for OSRM to be responsive on port 5000 using bash built-in /dev/tcp
echo "Waiting for OSRM backend to initialize..."
READY=0
for i in {1..30}; do
  if (echo > /dev/tcp/localhost/5000) >/dev/null 2>&1; then
    echo "OSRM backend is ready and listening on port 5000!"
    READY=1
    break
  fi
  sleep 0.5
done

if [ "$READY" -ne 1 ]; then
  echo "WARNING: OSRM did not bind to port 5000 within 15 seconds."
fi

# Start Go API server (takes over as PID 1)
echo "Starting Go API server on port $PORT..."
exec /app/api-server
