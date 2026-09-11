# Stage 1: Build the Go API binary & fetch pre-compiled map
FROM golang:1.24-alpine AS builder
WORKDIR /build

# Copy and compile Go API
COPY api/go.mod ./
COPY api/main.go ./
RUN CGO_ENABLED=0 GOOS=linux go build -ldflags="-s -w" -o api-server .

# Download pre-compiled Ethiopia bicycle map from GitHub Release (CDN-backed, unmetered, fast)
RUN echo "=== Downloading pre-compiled Ethiopia bicycle map data ===" && \
    wget -O ethiopia-bicycle-osrm.tar.gz https://github.com/abdetaterefe/osrm-api/releases/download/v1.0-data/ethiopia-bicycle-osrm.tar.gz

# Stage 2: Unified OSRM Bicycle runtime container
FROM ghcr.io/project-osrm/osrm-backend:v5.27.1

WORKDIR /app

# Copy the compiled Go API binary
COPY --from=builder /build/api-server /app/api-server

# Copy bicycle profile and entrypoint script
COPY profiles/ /profiles/
COPY scripts/entrypoint.sh /app/entrypoint.sh
RUN chmod +x /app/entrypoint.sh

# Unpack pre-compiled map data directly into /opt/osrm-data
RUN mkdir -p /opt/osrm-data
COPY --from=builder /build/ethiopia-bicycle-osrm.tar.gz /opt/osrm-data/
RUN tar -xzf /opt/osrm-data/ethiopia-bicycle-osrm.tar.gz -C /opt/osrm-data && \
    rm -f /opt/osrm-data/ethiopia-bicycle-osrm.tar.gz && \
    echo "=== Map unpack complete ==="

ENV PORT=3000
ENV OSRM_URL=http://127.0.0.1:5000

EXPOSE 3000

ENTRYPOINT ["/app/entrypoint.sh"]
