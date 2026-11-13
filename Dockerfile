# Stage 1: Build the Go API binary & fetch pre-compiled map tiles
FROM golang:1.24-alpine AS builder
WORKDIR /build

# Install curl to reliably follow GitHub release redirects
RUN apk add --no-cache curl ca-certificates

# Copy and compile Go API
COPY api/go.mod ./
COPY api/main.go ./
RUN CGO_ENABLED=0 GOOS=linux go build -ldflags="-s -w" -o api-server .

# Download pre-compiled Ethiopia Valhalla tiles from GitHub Release v2.0-valhalla-data
# If the release tag is not yet published, gracefully proceed (tiles can be mounted at runtime)
RUN echo "=== Attempting download of pre-compiled Ethiopia Valhalla tiles ===" && \
    (curl -fsSL -o ethiopia-valhalla-tiles.tar.gz https://github.com/abdetaterefe/osrm-api/releases/download/v2.0-valhalla-data/ethiopia-valhalla-tiles.tar.gz || \
     echo "Note: Release v2.0-valhalla-data not yet present on GitHub; tiles will be mounted or unpacked at runtime")

# Stage 2: Unified Valhalla Multi-Profile runtime container
FROM ghcr.io/gis-ops/docker-valhalla/valhalla:latest

WORKDIR /app
USER root

# Copy the compiled Go API binary
COPY --from=builder /build/api-server /app/api-server

# Unpack pre-compiled Valhalla tiles into /custom_files if downloaded
RUN mkdir -p /custom_files /data/valhalla && chmod -R 777 /custom_files /data/valhalla
COPY --from=builder /build/ethiopia-valhalla-tiles.tar.gz* /tmp/
RUN if [ -f /tmp/ethiopia-valhalla-tiles.tar.gz ] && [ -s /tmp/ethiopia-valhalla-tiles.tar.gz ]; then \
      tar -xzf /tmp/ethiopia-valhalla-tiles.tar.gz -C /custom_files && \
      rm -f /tmp/ethiopia-valhalla-tiles.tar.gz && \
      chmod -R 777 /custom_files && \
      echo "=== Valhalla tiles unpack complete ==="; \
    else \
      rm -f /tmp/ethiopia-valhalla-tiles.tar.gz*; \
    fi

ENV PORT=3000
ENV VALHALLA_URL=http://127.0.0.1:8002
ENV DEFAULT_PROFILE=bicycle
ENV VALHALLA_THREADS=1
ENV GOMEMLIMIT=48MiB
ENV GOGC=50

EXPOSE 3000

ENTRYPOINT ["/app/api-server"]
