#!/bin/bash
set -e

DATA_DIR="/data"
PROFILES_DIR="/profiles"
OSM_URL="https://download.geofabrik.de/africa/ethiopia-latest.osm.pbf"
OSM_FILE="$DATA_DIR/ethiopia-latest.osm.pbf"
PROFILES=("car" "bicycle" "foot" "motorcycle")

echo "=== OSRM Init ==="

# Download OSM data if not present
if [ ! -f "$OSM_FILE" ]; then
  echo "Downloading Ethiopia OSM data..."
  wget -q --show-progress -O "$OSM_FILE" "$OSM_URL"
  echo "Download complete."
else
  echo "OSM data already exists, skipping download."
fi

# Extract + partition + customize for each profile
for profile in "${PROFILES[@]}"; do
  PREFIX="$DATA_DIR/ethiopia-${profile}"

  if [ -f "${PREFIX}.osrm.mldgr" ] && [ -f "${PREFIX}.osrm.cells" ]; then
    echo "[$profile] Already processed, skipping."
    continue
  fi

  cp "$OSM_FILE" "${PREFIX}.osm.pbf"

  echo "[$profile] Extracting..."
  osrm-extract -p "$PROFILES_DIR/${profile}.lua" "${PREFIX}.osm.pbf"

  echo "[$profile] Partitioning..."
  osrm-partition "${PREFIX}.osrm"

  echo "[$profile] Customizing..."
  osrm-customize "${PREFIX}.osrm"

  rm -f "${PREFIX}.osm.pbf"

  echo "[$profile] Done."
done

echo "=== Init complete ==="
touch "$DATA_DIR/.init-done"
