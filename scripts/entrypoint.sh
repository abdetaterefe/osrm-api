#!/bin/bash
set -e

PORT="${PORT:-3000}"
export PORT
export OSRM_URL="${OSRM_URL:-http://localhost:5000}"

echo "=== Starting OSRM Bicycle Routing Service ==="
echo "Port: $PORT"
echo "OSRM Backend URL: $OSRM_URL"

mkdir -p /data

# Check if /data has the map files. If not, copy from embedded /opt/osrm-data/
if [ ! -f "/data/ethiopia-bicycle.osrm.mldgr" ]; then
  if [ -d "/opt/osrm-data" ] && [ -f "/opt/osrm-data/ethiopia-bicycle.osrm.mldgr" ]; then
    echo "Copying pre-compiled map data into /data..."
    cp -a /opt/osrm-data/* /data/
  else
    echo "ERROR: Map data not found in /data or /opt/osrm-data!"
    exit 1
  fi
fi

# Start osrm-routed in background
echo "Starting osrm-routed (bicycle)..."
osrm-routed --algorithm mld --max-table-size 1000 /data/ethiopia-bicycle.osrm --port 5000 &
OSRM_PID=$!

# Wait for OSRM to be responsive
echo "Waiting for OSRM backend to initialize..."
for i in {1..30}; do
  if curl -s -f "http://localhost:5000/route/v1/driving/38.7577,9.0128;38.7578,9.0129?overview=false" > /dev/null 2>&1; then
    echo "OSRM backend is ready!"
    break
  fi
  sleep 0.5
done

# Start Go API server (takes over as PID 1)
echo "Starting Go API server on port $PORT..."
exec /app/api-server
