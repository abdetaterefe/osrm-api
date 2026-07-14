#!/bin/bash
set -e

DATA_DIR="/data"
PROFILES_DIR="/profiles"
OSM_URL="https://download.geofabrik.de/africa/ethiopia-latest.osm.pbf"
OSM_FILE="$DATA_DIR/ethiopia-latest.osm.pbf"
PROFILES=("car" "bicycle" "foot" "motorcycle")

mkdir -p "$DATA_DIR"

# Download OSM data if not present
if [ ! -f "$OSM_FILE" ]; then
  echo "[init] Downloading Ethiopia OSM data..."
  wget -q -O "$OSM_FILE" "$OSM_URL"
  echo "[init] Download complete."
fi

# Process each profile if not already done
for profile in "${PROFILES[@]}"; do
  PREFIX="$DATA_DIR/ethiopia-${profile}"

  if [ -f "${PREFIX}.osrm.mldgr" ] && [ -f "${PREFIX}.osrm.cells" ]; then
    echo "[$profile] Already processed."
    continue
  fi

  cp "$OSM_FILE" "${PREFIX}.osm.pbf"

  echo "[$profile] Extracting..."
  osrm-extract -p "$PROFILES_DIR/${profile}.lua" "${PREFIX}.osm.pbf" 2>&1 | tail -1

  echo "[$profile] Partitioning..."
  osrm-partition "${PREFIX}.osrm" 2>&1 | tail -1

  echo "[$profile] Customizing..."
  osrm-customize "${PREFIX}.osrm" 2>&1 | tail -1

  rm -f "${PREFIX}.osm.pbf"
  echo "[$profile] Done."
done

echo "[init] Starting OSRM instances..."

osrm-routed --algorithm mld --max-table-size 10000 /data/ethiopia-car.osrm        --port 5001 &
osrm-routed --algorithm mld --max-table-size 10000 /data/ethiopia-bicycle.osrm    --port 5002 &
osrm-routed --algorithm mld --max-table-size 10000 /data/ethiopia-foot.osrm       --port 5003 &
osrm-routed --algorithm mld --max-table-size 10000 /data/ethiopia-motorcycle.osrm --port 5004 &

echo "[init] Waiting for OSRM instances..."
sleep 3

echo "[init] Starting API server..."
exec node /app/api/dist/server.js
