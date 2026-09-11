# OSRM Bicycle Routing Service (Ethiopia)

High-performance, ultra-lean bicycle routing and distance calculation service for Ethiopia, built with **OSRM** and **100% Go**. Designed to run effortlessly on **Koyeb**, **Railway**, **Render**, **Fly.io**, or **any VPS with 512 MB RAM**.

---

## Features

- **100% Go + OSRM**: Zero Node.js, zero pnpm, zero npm, zero shell script dependencies.
- **Dedicated Bicycle Routing**: Simplified API dedicated specifically to bicycle navigation, speeds, and cycleways.
- **In-Memory LRU Cache with TTL**: Built-in thread-safe cache stores up to 10,000 routes for 24 hours. Repeated queries respond in **< 1 ms**.
- **Smart Coordinate Auto-Correction**: Intelligently detects whether coordinates are passed as `lon,lat` or `lat,lon` and auto-corrects them for Ethiopia, rounding to 5 decimal places (~1.1m precision).
- **HTTP Edge Caching**: Responses include `Cache-Control` and `X-Cache: HIT/MISS` headers for seamless CDN / edge caching.
- **Go Child Process Supervisor**: The Go server acts as PID 1, launching and supervising `osrm-routed --mmap`, ensuring graceful restarts and shutdown.
- **Ultra-Lean Memory (< 100 MB Active)**: Uses memory-mapped files (`--mmap`) so `osrm-routed` and the Go API together run comfortably in 512 MB RAM containers.

---

## Live Endpoints

| Endpoint | Method | Description |
| :--- | :--- | :--- |
| `/health` | GET | Active health probe & backend status + cache item count |
| `/distance` | GET | Quick road distance, duration, and km for bicycles |
| `/route` | GET | Turn-by-turn maneuvers & full GeoJSON geometry |
| `/matrix` | POST | Distance & duration matrix between multiple coordinates |

---

## API Documentation

### `GET /health`

Actively probes backend connectivity and reports cache statistics.

```bash
curl http://localhost:3000/health
```

Healthy response (`200 OK`):
```json
{
  "status": "ok",
  "vehicle": "bicycle",
  "backend_status": "online",
  "cached_routes": 42
}
```

---

### `GET /distance`

Calculates road distance and duration between two coordinates for bicycles.

- Coordinates can be provided as `lon,lat` OR `lat,lon` (auto-detected).

```bash
curl "http://localhost:3000/distance?from=38.7577,9.0128&to=38.7891,9.0054"
```

Response:
```json
{
  "vehicle": "bicycle",
  "distance_meters": 4804.6,
  "distance_km": 4.8,
  "duration_seconds": 298,
  "duration_minutes": 5.0
}
```

Response Headers:
```http
Cache-Control: public, max-age=86400, stale-while-revalidate=3600
X-Cache: HIT
Content-Type: application/json; charset=utf-8
```

---

### `GET /route`

Returns full route geometry (GeoJSON) and turn-by-turn maneuvers.

Query Parameters:
- `from`: starting coordinates (`lon,lat` or `lat,lon`)
- `to`: destination coordinates (`lon,lat` or `lat,lon`)
- `steps`: `true` / `false` (include step maneuvers, default `false`)
- `alternatives`: `true` / `false` (return alternative routes, default `false`)

```bash
curl "http://localhost:3000/route?from=38.7577,9.0128&to=38.7891,9.0054&steps=true"
```

---

### `POST /matrix`

Compute distance and duration tables for multiple points in Ethiopia.

```bash
curl -X POST http://localhost:3000/matrix \
  -H "Content-Type: application/json" \
  -d '{
    "coordinates": [
      [38.7577, 9.0128],
      [38.7891, 9.0054],
      [38.7700, 9.0200]
    ]
  }'
```

---

## Deployment Options

### Koyeb (Recommended)

1. Connect your GitHub repository to Koyeb.
2. Select **Dockerfile** as the build type.
3. Koyeb will automatically build the container and deploy it with a public HTTPS URL.

### VPS (Docker Compose)

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

## Project Structure

```
├── Dockerfile                # Multi-stage container (Go builder + OSRM runtime)
├── docker-compose.yml        # Compose service configuration
├── profiles/
│   └── bicycle.lua           # Custom bicycle routing profile for Ethiopia
├── api/
│   ├── main.go               # Go API & Process Supervisor with LRU Cache
│   ├── main_test.go          # Unit tests (cache, auto-correction, endpoints)
│   ├── go.mod                # Pure Go stdlib module
│   └── Dockerfile            # Standalone API image (optional)
└── README.md
```

