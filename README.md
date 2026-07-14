# OSRM Routing API

Routing and distance calculation service for Ethiopia, powered by [OSRM](https://github.com/Project-OSRM/osrm-backend). Built for food delivery apps, ride-sharing, or any service that needs real road distance and duration.

## Features

- **4 vehicle profiles**: car, motorcycle, bicycle, foot
- **Real road routing** using OpenStreetMap data for Ethiopia
- **Monthly auto-update** for map data
- **Run anywhere**: Docker (self-hosted) or Cloudflare Containers (serverless)
- **Same API** for both — pick whichever fits your setup

## Choose Your Platform

| | Docker | Cloudflare Containers |
|---|---|---|
| **Cost** | Your own server | Workers Paid plan |
| **Setup** | `docker compose up` | `npx wrangler deploy` |
| **Cold start** | Instant | ~5 min (first time) |
| **Monthly update** | Cron container | Worker cron trigger |
| **Scaling** | Manual | Automatic |

---

## Docker (Self-Hosted)

### Quick Start

```bash
docker compose up -d
```

On first run, it downloads Ethiopia OSM data (~133MB) and processes it for all profiles (~5 min). After that, services start automatically.

### Monthly Map Updates

Automatic on the 1st of every month at 3:00 AM. The `map-updater` container downloads fresh data from [Geofabrik](https://download.geofabrik.de/africa/ethiopia-latest.osm.pbf), reprocesses all profiles, and restarts the routing services.

To run manually:
```bash
docker exec osrm-map-updater /usr/local/bin/update-map.sh
```

### Stopping

```bash
docker compose down
```

---

## Cloudflare Containers (Serverless)

### Prerequisites

- [Wrangler CLI](https://developers.cloudflare.com/workers/wrangler/) installed
- Cloudflare account (Workers Paid plan required for Containers)

### Setup & Deploy

```bash
cd cloudflare
npm install
npx wrangler deploy
```

This will:
1. Build a single Docker image with all 4 OSRM profiles + the API
2. Push it to Cloudflare's container registry
3. Deploy the Worker that routes to the container

### How it Works

Everything runs in **one container** (no docker-compose):

```
┌──────────────────────────────────────────────┐
│            Cloudflare Container              │
│                                              │
│  ┌─────────┐ ┌─────────┐ ┌─────────┐        │
│  │ OSRM    │ │ OSRM    │ │ OSRM    │  ...   │
│  │ car     │ │ bike    │ │ foot    │        │
│  │ :5001   │ │ :5002   │ │ :5003   │        │
│  └────┬────┘ └────┬────┘ └────┬────┘        │
│       └───────────┼───────────┘              │
│             ┌─────┴─────┐                    │
│             │ Hono API  │                    │
│             │   :3000   │                    │
│             └───────────┘                    │
└──────────────────────────────────────────────┘
                     ▲
                     │
            ┌────────┴────────┐
            │ Cloudflare      │
            │ Worker          │
            └─────────────────┘
```

### Monthly Map Updates

Automatic via Cloudflare Workers cron trigger (`0 3 1 * *` — 1st of every month at 3:00 AM). The Worker restarts the container, which re-downloads and re-processes the map data.

### Instance Type

Uses `standard-4` (4 vCPU, 12GB RAM, 20GB disk). Edit `wrangler.jsonc` to change.

---

## API Endpoints

All endpoints run on `http://localhost:3000` (Docker) or your Worker URL (Cloudflare).

### `GET /health`

```bash
curl http://localhost:3000/health
```

### `GET /distance`

Quick distance and duration between two points.

```bash
curl "http://localhost:3000/distance?from=38.7577,9.0128&to=38.7891,9.0054&vehicle=car"
```

| Param | Required | Default | Description |
|-------|----------|---------|-------------|
| `from` | yes | — | `longitude,latitude` |
| `to` | yes | — | `longitude,latitude` |
| `vehicle` | no | `car` | `car`, `motorcycle`, `bicycle`, `foot` |

Response:
```json
{
  "vehicle": "car",
  "distance_meters": 4804.6,
  "distance_km": 4.8,
  "duration_seconds": 298,
  "duration_minutes": 5.0
}
```

### `GET /route`

Full route with geometry and step-by-step directions.

```bash
curl "http://localhost:3000/route?from=38.7577,9.0128&to=38.7891,9.0054&vehicle=motorcycle&steps=true"
```

| Param | Required | Default | Description |
|-------|----------|---------|-------------|
| `from` | yes | — | `longitude,latitude` |
| `to` | yes | — | `longitude,latitude` |
| `vehicle` | no | `car` | `car`, `motorcycle`, `bicycle`, `foot` |
| `steps` | no | `false` | Include turn-by-turn instructions |
| `alternatives` | no | `false` | Return alternative routes |

### `GET /compare`

Compare distance and duration across all vehicle types at once.

```bash
curl "http://localhost:3000/compare?from=38.7577,9.0128&to=38.7891,9.0054"
```

Response:
```json
{
  "from": [38.7577, 9.0128],
  "to": [38.7891, 9.0054],
  "results": {
    "car":        { "distance_km": 4.8,  "duration_minutes": 5.0 },
    "motorcycle": { "distance_km": 4.8,  "duration_minutes": 4.5 },
    "bicycle":    { "distance_km": 4.09, "duration_minutes": 18.0 },
    "foot":       { "distance_km": 4.06, "duration_minutes": 48.9 }
  }
}
```

### `POST /matrix`

Distance/duration matrix for multiple coordinates. Useful for finding the nearest driver.

```bash
curl -X POST http://localhost:3000/matrix \
  -H "Content-Type: application/json" \
  -d '{
    "coordinates": [[38.7577,9.0128], [38.7891,9.0054], [38.77,9.02]],
    "vehicle": "motorcycle"
  }'
```

Response:
```json
{
  "vehicle": "motorcycle",
  "distances": [[0, 4804, 3200], [4804, 0, 2100], [3200, 2100, 0]],
  "durations": [[0, 268, 180], [268, 0, 120], [180, 120, 0]],
  "coordinates": [[38.7577,9.0128], [38.7891,9.0054], [38.77,9.02]]
}
```

---

## Project Structure

```
├── docker-compose.yml        # Docker deployment (multi-container)
├── Dockerfile.init            # Init container image for Docker
├── wrangler.jsonc             # Cloudflare Containers config
├── profiles/
│   ├── car.lua                # Car routing profile
│   ├── motorcycle.lua         # Motorcycle profile
│   ├── bicycle.lua            # Bicycle profile
│   └── foot.lua               # Walking profile
├── scripts/
│   ├── init.sh                # Downloads OSM data + processes profiles
│   ├── update-map.sh          # Monthly map update script
│   └── scheduler.sh           # Cron scheduler for auto-updates
├── api/
│   ├── Dockerfile
│   ├── package.json
│   ├── tsconfig.json
│   └── src/
│       └── server.ts          # Hono API server (shared by both)
└── cloudflare/
    ├── Dockerfile             # Single-container image for CF
    ├── start.sh               # Startup script
    ├── package.json
    ├── tsconfig.json
    └── src/
        └── index.ts           # Cloudflare Worker
```

The API code (`api/src/server.ts`) is **shared** between Docker and Cloudflare. Only the deployment mechanism differs.
