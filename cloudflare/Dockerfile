FROM golang:1.24-alpine AS go-builder
WORKDIR /build
COPY api/go.mod ./
COPY api/main.go ./
RUN CGO_ENABLED=0 GOOS=linux go build -ldflags="-s -w" -o api-server .

FROM ghcr.io/project-osrm/osrm-backend:v5.27.1
RUN apt-get update && apt-get install -y wget curl ca-certificates && rm -rf /var/lib/apt/lists/*

WORKDIR /app
COPY --from=go-builder /build/api-server /app/api-server
COPY profiles/ /profiles/
COPY cloudflare/start.sh /start.sh
RUN chmod +x /start.sh

EXPOSE 3000
CMD ["/start.sh"]
