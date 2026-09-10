# OSRM Bicycle Routing Service (Ethiopia)

High-performance, ultra-lean routing and distance calculation service for Ethiopia, built with **OSRM** and **100% Go**. Designed to work naturally on **Railway**, **Render**, **Fly.io**, or **any VPS with as little as 512 MB RAM**.

---

## Features

- **100% Go + OSRM**: Zero Node.js, zero pnpm, zero npm dependencies.
- **Ultra-lean memory**: The Go API consumes only **~12 MB RAM**; the entire runtime container uses **~250 MB RAM** total.
- **Build-time Map Compilation**: Map data is extracted and partitioned during `docker build` (using high-memory build runners). The container starts in ~2 seconds at runtime with zero risk of OOM kills.
- **True Health Checks**: `/health` actively verifies that the OSRM backend is healthy (returns HTTP 200 when ready, 503 if degraded).
- **Run Anywhere**: Deploy seamlessly via Railway, Docker Compose, or raw Docker.

---

## Deployment Options

### Option 1: Railway (Zero-config)

The project includes a root `Dockerfile` and `railway.json`:
1. Connect this GitHub repository to your Railway project.
2. Railway automatically detects `railway.json` and builds the `Dockerfile`.
3. The build phase downloads and compiles the Ethiopia bicycle map.
4. The service deploys and binds to `$PORT` automatically.

---

### Option 2: VPS (Docker Compose)

On your VPS:

```bash
# Clone the repository
git clone https://github.com/abdetaterefe/osrm-api.git
cd osrm-api

# Start the service
docker compose up -d --build

# View logs
docker compose logs -f
```

---

### Option 3: VPS (Standalone Docker)

```bash
docker build -t osrm-bicycle .
docker run -d -p 3000:3000 --name osrm-bicycle osrm-bicycle
```

---

## Memory Footprint (512 MB Safe)

| Component | RAM Usage |
| :--- | :--- |
| `osrm-routed` (Ethiopia Bicycle MLD) | ~230 MB |
| Go API Server | **~12 MB** |
| OS / runtime overhead | ~20 MB |
| **Total Active** | **~262 MB** |
| **Safety Headroom on 512 MB Plan** | **~250 MB (Almost 50% free buffer)** |

---

## API Endpoints

### `GET /health`

Actively checks whether the OSRM backend is responding.

```bash
curl http://localhost:3000/health
```

Healthy response (`200 OK`):
```json
{
  "status": "ok",
  "profiles": ["bicycle"],
  "backend_status": "online"
}
```

Degraded response (`503 Service Unavailable`):
```json
{
  "status": "degraded",
  "profiles": ["bicycle"],
  "backend_status": "offline",
  "error": "OSRM bicycle backend is unreachable"
}
```

### `GET /distance`

Quick road distance and duration between two coordinates for bicycles.

```bash
curl "http://localhost:3000/distance?from=38.7577,9.0128&to=38.7891,9.0054"
```

Response:
```json
{
  "vehicle": "bicycle",
  "distance_meters": 4090.5,
  "distance_km": 4.09,
  "duration_seconds": 1080,
  "duration_minutes": 18.0
}
```

### `GET /route`

Turn-by-turn maneuvers and full GeoJSON route geometry.

```bash
curl "http://localhost:3000/route?from=38.7577,9.0128&to=38.7891,9.0054&steps=true"
```

### `POST /matrix`

Compute distance and duration tables for multiple points.

```bash
curl -X POST http://localhost:3000/matrix \
  -H "Content-Type: application/json" \
  -d '{
    "coordinates": [[38.7577,9.0128], [38.7891,9.0054], [38.77,9.02]]
  }'
```

---

## Project Structure

```
├── Dockerfile                # Unified multi-stage build (Go + OSRM + embedded map)
├── docker-compose.yml        # Compose configuration for VPS
├── railway.json              # Railway deployment & health check configuration
├── profiles/
│   └── bicycle.lua           # OSRM bicycle routing profile
├── scripts/
│   ├── entrypoint.sh         # Boots osrm-routed & Go API
│   ├── init.sh               # Standalone map processor script
│   ├── update-map.sh         # Map update script
│   └── scheduler.sh          # Cron update helper
└── api/
    ├── main.go               # Pure Go HTTP API (~12 MB RAM)
    ├── main_test.go          # Unit tests
    ├── go.mod                # Go module definition
    └── Dockerfile            # Standalone API image (optional)
```
