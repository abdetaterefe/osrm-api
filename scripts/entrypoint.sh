#!/bin/bash
set -e

PORT="${PORT:-3000}"
export PORT
export OSRM_URL="${OSRM_URL:-http://127.0.0.1:5000}"

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

# Ensure read-write permissions on map files (required by --mmap)
chmod -R 777 /opt/osrm-data /data 2>/dev/null || true

# Start osrm-routed in background with --mmap (memory-mapped from disk to fit in 512MB RAM)
echo "Starting osrm-routed with --mmap on 0.0.0.0:5000 with map: $MAP_PATH ..."
osrm-routed --algorithm mld --mmap --max-table-size 100 --ip 0.0.0.0 --port 5000 "$MAP_PATH" &
OSRM_PID=$!

# Wait for OSRM to be responsive on 127.0.0.1:5000 using bash built-in /dev/tcp
echo "Waiting for OSRM backend to initialize..."
READY=0
for i in {1..40}; do
  if (echo > /dev/tcp/127.0.0.1/5000) >/dev/null 2>&1; then
    echo "OSRM backend is ready and listening on 127.0.0.1:5000!"
    READY=1
    break
  fi

  if ! kill -0 $OSRM_PID 2>/dev/null; then
    echo "FATAL: osrm-routed process ($OSRM_PID) exited unexpectedly!"
    wait $OSRM_PID || true
    exit 1
  fi

  sleep 0.5
done

if [ "$READY" -ne 1 ]; then
  echo "WARNING: OSRM did not bind to port 5000 within 20 seconds. Starting API anyway..."
fi

# Start Go API server (takes over as PID 1)
echo "Starting Go API server on port $PORT..."
exec /app/api-server
