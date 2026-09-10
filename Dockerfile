# Stage 1: Build the Go API binary & download Ethiopia OSM map
FROM golang:1.24-alpine AS builder
WORKDIR /build

# Copy and compile Go API
COPY api/go.mod ./
COPY api/main.go ./
RUN CGO_ENABLED=0 GOOS=linux go build -ldflags="-s -w" -o api-server .

# Download Ethiopia OSM data from openstreetmap.fr (public unmetered mirror, unblocked by firewalls)
RUN echo "=== Downloading Ethiopia OSM map data from OSM France mirror ===" && \
    wget -O ethiopia-bicycle.osm.pbf https://download.openstreetmap.fr/extracts/africa/ethiopia-latest.osm.pbf

# Stage 2: Unified OSRM Bicycle runtime container
FROM ghcr.io/project-osrm/osrm-backend:v5.27.1

WORKDIR /app

# Copy the compiled Go API binary
COPY --from=builder /build/api-server /app/api-server

# Copy bicycle profile and entrypoint script
COPY profiles/ /profiles/
COPY scripts/entrypoint.sh /app/entrypoint.sh
RUN chmod +x /app/entrypoint.sh

# Copy downloaded map from builder stage into /opt/osrm-data
RUN mkdir -p /opt/osrm-data
COPY --from=builder /build/ethiopia-bicycle.osm.pbf /opt/osrm-data/ethiopia-bicycle.osm.pbf

# Pre-compile the Ethiopia bicycle map at Docker build time
RUN echo "=== Extracting bicycle routing profile ===" && \
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
