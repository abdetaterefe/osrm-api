# Stage 1: Build the lightweight Go API binary
FROM golang:1.24-alpine AS go-builder
WORKDIR /build
COPY api/go.mod ./
COPY api/main.go ./
RUN CGO_ENABLED=0 GOOS=linux go build -ldflags="-s -w" -o api-server .

# Stage 2: Unified OSRM Bicycle runtime container
FROM ghcr.io/project-osrm/osrm-backend:v5.27.1

RUN apt-get update && apt-get install -y wget curl ca-certificates && rm -rf /var/lib/apt/lists/*

WORKDIR /app

# Copy the compiled Go API binary
COPY --from=go-builder /build/api-server /app/api-server

# Copy bicycle profile and entrypoint script
COPY profiles/ /profiles/
COPY scripts/entrypoint.sh /app/entrypoint.sh
RUN chmod +x /app/entrypoint.sh

# Download and compile the Ethiopia bicycle map at Docker build time.
# Stored in /opt/osrm-data so it is embedded in the image and automatically
# copied to /data at runtime (even if /data is mounted as an empty host volume).
RUN mkdir -p /opt/osrm-data && \
    echo "=== Downloading Ethiopia OSM data ===" && \
    wget -q --show-progress -O /opt/osrm-data/ethiopia-bicycle.osm.pbf https://download.geofabrik.de/africa/ethiopia-latest.osm.pbf && \
    echo "=== Extracting bicycle routing profile ===" && \
    osrm-extract -p /profiles/bicycle.lua /opt/osrm-data/ethiopia-bicycle.osm.pbf && \
    echo "=== Partitioning ===" && \
    osrm-partition /opt/osrm-data/ethiopia-bicycle.osrm && \
    echo "=== Customizing ===" && \
    osrm-customize /opt/osrm-data/ethiopia-bicycle.osrm && \
    rm -f /opt/osrm-data/*.osm.pbf && \
    echo "=== Build-time map compilation complete ==="

ENV PORT=3000
ENV OSRM_URL=http://localhost:5000

EXPOSE 3000

ENTRYPOINT ["/app/entrypoint.sh"]
