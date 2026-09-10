# OSRM Bicycle Routing API (Ethiopia)

Lightweight, high-performance routing and distance calculation service for Ethiopia, powered by [OSRM](https://github.com/Project-OSRM/osrm-backend) with a lean **Go** API. Optimized to run reliably even on small VPS instances with **512 MB RAM**.

## Features

- **Bicycle profile**: Tuned specifically for bike routing across urban and rural Ethiopia.
- **Ultra-low memory**: Go API uses only **~10–15 MB RAM** (replacing heavy Node.js runtimes).
- **Real road routing** using OpenStreetMap data for Ethiopia from Geofabrik.
- **True health checks**: `/health` actively verifies backend OSRM connectivity (returns 503 if backend is down).
- **Fast cold-starts**: Docker image built on minimal Alpine Linux.

---

## Memory Footprint (512 MB Plan Ready)

| Component | RAM Usage |
| :--- | :--- |
| OS / Kernel / Docker runtime | ~90 MB |
| `osrm-routed` (Ethiopia Bicycle MLD) | ~230 MB |
| Go API Server | **~12 MB** |
| **Total Active** | **~332 MB** |
| **Safety Headroom** | **~180 MB (Free buffer on 512MB VPS)** |

---

## Deploying on a 512 MB VPS

Because `osrm-extract` and `osrm-partition` require 2GB–4GB of RAM during map compilation, **do not compile the map on a 512 MB machine without swap**.

### Method 1: Pre-process on your PC and copy to VPS (Recommended)

1. **Extract map data on your local PC or laptop:**
   ```bash
   mkdir -p osrm-data
   docker compose run --rm init
   ```
   This downloads Ethiopia OSM data (~135MB) and compiles `osrm-data/ethiopia-bicycle.osrm.*` (~1–2 minutes).

2. **Copy the processed data directory to your VPS:**
   ```bash
   rsync -avzP ./osrm-data/ user@<vps-ip>:/path/to/osrm-api/osrm-data/
   ```

3. **Start the service on your VPS:**
   ```bash
   docker compose up -d
   ```
   The `init` container will detect that `.osrm.cells` and `.osrm.mldgr` already exist and skip compilation, starting `osrm-bicycle` and `api` immediately!

---

### Method 2: Process directly on VPS using a Swapfile

If you cannot pre-process locally, create a 2GB–4GB swapfile on your VPS first:

```bash
# 1. Create swapfile on VPS
sudo fallocate -l 3G /swapfile
sudo chmod 600 /swapfile
sudo mkswap /swapfile
sudo swapon /swapfile

# 2. Start services (init will compile using swap space)
docker compose up -d
```

---

## Stopping & Logs

```bash
# View live logs
docker compose logs -f api
docker compose logs -f osrm-bicycle

# Stop services
docker compose down
```

---

## API Endpoints

All endpoints run on `http://<vps-ip>:3000` (or `http://localhost:3000`).

### `GET /health`

Actively checks if the OSRM backend is healthy and responding.

```bash
curl http://localhost:3000/health
```

Healthy response (HTTP 200):
```json
{
  "status": "ok",
  "profiles": ["bicycle"],
  "backend_status": "online"
}
```

Degraded response (HTTP 503) if OSRM process died:
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

| Param | Required | Default | Description |
|---|---|---|---|
| `from` | yes | — | `longitude,latitude` |
| `to` | yes | — | `longitude,latitude` |
| `vehicle` | no | `bicycle` | `bicycle` |

### `GET /route`

Full route geometry and step-by-step turn instructions.

```bash
curl "http://localhost:3000/route?from=38.7577,9.0128&to=38.7891,9.0054&steps=true"
```

| Param | Required | Default | Description |
|---|---|---|---|
| `from` | yes | — | `longitude,latitude` |
| `to` | yes | — | `longitude,latitude` |
| `steps` | no | `false` | Turn-by-turn maneuvers |
| `alternatives` | no | `false` | Return alternative routes |

### `POST /matrix`

Distance/duration matrix for multiple coordinates.

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
├── docker-compose.yml        # Docker compose (init, osrm-bicycle, Go api)
├── Dockerfile.init            # Map pre-processing container image
├── profiles/
│   ├── bicycle.lua            # Bicycle routing profile
│   ├── car.lua
│   ├── foot.lua
│   └── motorcycle.lua
├── scripts/
│   ├── init.sh                # Downloads Ethiopia OSM data & processes bicycle
│   ├── update-map.sh          # Manual / scheduled map update script
│   └── scheduler.sh           # Optional cron scheduler
└── api/
    ├── main.go                # Ultra-light Go HTTP API (~12MB RAM)
    ├── main_test.go           # Unit tests
    ├── go.mod                 # Go module definition
    └── Dockerfile             # Multi-stage Alpine container
```
