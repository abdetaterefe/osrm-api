# OSRM Bicycle Routing Service (Ethiopia)

High-performance, ultra-lean bicycle routing and distance calculation service for Ethiopia, built with **OSRM** and **100% Go**. Optimized for 512 MB RAM environments on **Koyeb**, **Railway**, or any standard VPS.

---

## Features

- **Clean, Modern API**: No nested arrays, no legacy fields, zero fluff. Everything is right at the top level.
- **Dedicated Bicycle Routing**: Accurate Ethiopian cycling paths, road surfaces, and bicycle speeds.
- **In-Memory LRU Cache with TTL**: Built-in cache stores up to 10,000 routes for 24 hours with **< 1 ms** response times.
- **Smart Coordinate Auto-Correction**: Seamlessly accepts `lon,lat` or `lat,lon` and auto-corrects them for Ethiopia (lat: 3.0°–15.5°N, lon: 32.0°–48.5°E), rounded to 5 decimal places (~1.1m precision).
- **HTTP Edge Caching**: Responses include `Cache-Control: public, max-age=86400, stale-while-revalidate=3600` and `X-Cache: HIT/MISS` headers.
- **Go Child Process Supervisor**: Single binary serves HTTP and manages `osrm-routed --mmap` as PID 1 with clean graceful shutdowns.

---

## API Endpoints

### 1. `GET /health`

Active probe verifying backend connectivity and cache status.

```bash
curl https://peculiar-rina-sharenex-b86b2492.koyeb.app/health
```

Response (`200 OK`):
```json
{
  "status": "ok",
  "backend": "online",
  "cached_routes": 4
}
```

---

### 2. `GET /distance`

Quick road distance and duration calculation between two points.

Query parameters:
- `from`: starting coordinates (`lon,lat` or `lat,lon`)
- `to`: destination coordinates (`lon,lat` or `lat,lon`)

```bash
curl "https://peculiar-rina-sharenex-b86b2492.koyeb.app/distance?from=38.7577,9.0128&to=38.7891,9.0054"
```

Response (`200 OK`):
```json
{
  "distance_km": 4.09,
  "duration_minutes": 18.0,
  "distance_meters": 4088,
  "duration_seconds": 1080,
  "origin": {
    "name": "Ras Desta Damtew Street",
    "location": [38.75739, 9.01284]
  },
  "destination": {
    "name": "BL_03_573 Street",
    "location": [38.78909, 9.00543]
  }
}
```

---

### 3. `GET /route`

Full route geometry and turn-by-turn cycling instructions.

Query parameters:
- `from`: starting coordinates (`lon,lat` or `lat,lon`)
- `to`: destination coordinates (`lon,lat` or `lat,lon`)
- `steps`: `true` or `false` (optional, default `false`)

```bash
curl "https://peculiar-rina-sharenex-b86b2492.koyeb.app/route?from=38.7577,9.0128&to=38.7891,9.0054&steps=true"
```

Response (`200 OK`):
```json
{
  "distance_km": 4.09,
  "duration_minutes": 18.0,
  "distance_meters": 4088,
  "duration_seconds": 1080,
  "origin": {
    "name": "Ras Desta Damtew Street",
    "location": [38.75739, 9.01284]
  },
  "destination": {
    "name": "BL_03_573 Street",
    "location": [38.78909, 9.00543]
  },
  "geometry": {
    "type": "LineString",
    "coordinates": [
      [38.75739, 9.01284],
      [38.75753, 9.01369],
      [38.75802, 9.01316]
    ]
  },
  "steps": [
    {
      "instruction": "Head right on Ras Desta Damtew Street",
      "street_name": "Ras Desta Damtew Street",
      "distance_meters": 313,
      "duration_seconds": 81,
      "type": "depart",
      "modifier": "right"
    },
    {
      "instruction": "Turn left onto Jomo Kenyatta Avenue",
      "street_name": "Jomo Kenyatta Avenue",
      "distance_meters": 1058,
      "duration_seconds": 294,
      "type": "turn",
      "modifier": "left"
    }
  ]
}
```

---

### 4. `POST /matrix`

Distance and duration tables for multiple points.

Request body:
```json
{
  "coordinates": [
    [38.7577, 9.0128],
    [38.7891, 9.0054],
    [38.7700, 9.0200]
  ]
}
```

Response (`200 OK`):
```json
{
  "distances_km": [
    [0.0, 4.09, 2.91],
    [4.33, 0.0, 3.38],
    [2.60, 3.30, 0.0]
  ],
  "durations_minutes": [
    [0.0, 18.0, 12.5],
    [18.9, 0.0, 14.5],
    [11.6, 13.8, 0.0]
  ],
  "distances_meters": [
    [0, 4088, 2906],
    [4327, 0, 3377],
    [2599, 3299, 0]
  ],
  "durations_seconds": [
    [0, 1080, 749],
    [1137, 0, 871],
    [698, 829, 0]
  ],
  "waypoints": [
    { "name": "Ras Desta Damtew Street", "location": [38.75739, 9.01284] },
    { "name": "BL_03_573 Street", "location": [38.78909, 9.00543] },
    { "name": "", "location": [38.77000, 9.02000] }
  ]
}
```

---

## Project Structure

```
├── Dockerfile                # Multi-stage container (Go builder + OSRM runtime)
├── docker-compose.yml        # Docker Compose configuration for VPS
├── profiles/
│   └── bicycle.lua           # Custom bicycle routing profile for Ethiopia
├── api/
│   ├── main.go               # Pure Go HTTP API & Process Supervisor with LRU Cache
│   ├── main_test.go          # Unit tests (cache, auto-correction, endpoints)
│   ├── go.mod                # Pure Go module definition
│   └── Dockerfile            # Standalone API image (optional)
└── README.md
```
