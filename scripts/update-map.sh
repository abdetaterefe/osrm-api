#!/bin/bash
set -e

DATA_DIR="/data"
PROFILES_DIR="/profiles"
OSM_URL="https://download.geofabrik.de/africa/ethiopia-latest.osm.pbf"
OSM_FILE="$DATA_DIR/ethiopia-latest.osm.pbf"
PROFILES=("car" "bicycle" "foot" "motorcycle")
OSRM_CONTAINERS="osrm-car osrm-bicycle osrm-foot osrm-motorcycle"

echo "=== Map Update: $(date) ==="

# Download latest OSM data
echo "Downloading latest Ethiopia OSM data..."
cp "$OSM_FILE" "${OSM_FILE}.old" 2>/dev/null || true
wget -q --show-progress -O "$OSM_FILE" "$OSM_URL"

NEW_HASH=$(md5sum "$OSM_FILE" | cut -d' ' -f1)
OLD_HASH=$(md5sum "${OSM_FILE}.old" 2>/dev/null | cut -d' ' -f1)

if [ "$NEW_HASH" = "$OLD_HASH" ]; then
  echo "No changes in map data."
  rm -f "${OSM_FILE}.old"
  exit 0
fi

echo "New map data found, reprocessing..."

for profile in "${PROFILES[@]}"; do
  PREFIX="$DATA_DIR/ethiopia-${profile}"

  echo "[$profile] Extracting..."
  osrm-extract -p "$PROFILES_DIR/${profile}.lua" "$OSM_FILE"

  BASE=$(basename "$OSM_FILE" .osm.pbf)
  for f in ${DATA_DIR}/${BASE}.osrm.*; do
    SUFFIX="${f#${DATA_DIR}/${BASE}.osrm.}"
    mv "$f" "${PREFIX}.osrm.${SUFFIX}" 2>/dev/null || true
  done

  echo "[$profile] Partitioning..."
  osrm-partition "${PREFIX}.osrm"

  echo "[$profile] Customizing..."
  osrm-customize "${PREFIX}.osrm"

  echo "[$profile] Done."
done

rm -f "${OSM_FILE}.old"
touch "$DATA_DIR/.last-update"

echo "Restarting OSRM routing containers..."
for container in $OSRM_CONTAINERS; do
  docker restart "$container" 2>/dev/null && echo "  Restarted $container" || echo "  Could not restart $container"
done

echo "=== Update complete ==="
