# OSRM Routing API

Routing and distance calculation service for Ethiopia, powered by [OSRM](https://github.com/Project-OSRM/osrm-backend). Built for food delivery apps, ride-sharing, or any service that needs real road distance and duration.

## Features

- **4 vehicle profiles**: car, motorcycle, bicycle, foot
- **Real road routing** using OpenStreetMap data for Ethiopia
- **Monthly auto-update** for map data
- **Zero config** — one command to run everything

## Quick Start

```bash
docker compose up -d
```

On first run, it downloads Ethiopia OSM data (~133MB) and processes it for all profiles (~5 min). After that, services start automatically.

## API Endpoints

All endpoints run on `http://localhost:3000`.

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

## Architecture

```
                 ┌─────────────┐
                 │  Your App   │
                 │  Backend    │
                 └──────┬──────┘
                        │ :3000
                 ┌──────┴──────┐
                 │  Hono API   │
                 │  (server.js)│
                 └──────┬──────┘
            ┌───────────┼───────────┐
            │           │           │
     ┌──────┴──┐ ┌──────┴──┐ ┌──────┴──┐
     │ OSRM    │ │ OSRM    │ │ OSRM    │  ...
     │ car     │ │ bike    │ │ foot    │
     │ :5001   │ │ :5002   │ │ :5003   │
     └─────────┘ └─────────┘ └─────────┘
```

Each OSRM instance runs the MLD (Multi-Level Dijkstra) algorithm with preprocessed Ethiopia road data.

## Project Structure

```
├── docker-compose.yml        # Docker deployment (multi-container)
├── Dockerfile.init            # Init container image
├── wrangler.jsonc             # Cloudflare Containers config
├── profiles/
│   ├── car.lua                # Car routing profile
│   ├── motorcycle.lua         # Motorcycle profile (faster urban speeds)
│   ├── bicycle.lua            # Bicycle profile
│   └── foot.lua               # Walking profile
├── scripts/
│   ├── init.sh                # Downloads OSM data + processes profiles
│   ├── update-map.sh          # Monthly map update script
│   └── scheduler.sh           # Cron scheduler for auto-updates
├── api/
│   ├── Dockerfile
│   ├── package.json
│   └── server.js              # Hono API server
└── cloudflare/
    ├── Dockerfile             # Single-container image for CF
    ├── start.sh               # Startup script (processes + runs all)
    ├── package.json
    └── src/
        └── index.js           # Cloudflare Worker (routes to container)
```

## Map Updates

Map data updates automatically on the 1st of every month at 3:00 AM. The `map-updater` container downloads fresh data from [Geofabrik](https://download.geofabrik.de/africa/ethiopia-latest.osm.pbf), reprocesses all profiles, and restarts the routing services.

To run manually:
```bash
docker exec osrm-map-updater /usr/local/bin/update-map.sh
```

## Updating the API Code

```bash
docker compose up -d --build api
```

## Stopping

```bash
docker compose down
```

To also remove processed map data:
```bash
docker compose down
rm -rf osrm-data/
```

---

## Deploy to Cloudflare Containers

Run the same OSRM stack on [Cloudflare Containers](https://developers.cloudflare.com/containers/) — serverless, no infrastructure to manage.

### Prerequisites

- [Wrangler CLI](https://developers.cloudflare.com/workers/wrangler/) installed
- Cloudflare account (Workers Paid plan required for Containers)

### Setup

```bash
cd cloudflare
npm install
```

### Deploy

```bash
npx wrangler deploy
```

This will:
1. Build a single Docker image with all 4 OSRM profiles + the API
2. Push it to Cloudflare's container registry
3. Deploy the Worker that routes to the container

### How it works

On Cloudflare, everything runs in **one container** (no docker-compose):

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
                     │ :3000
            ┌────────┴────────┐
            │ Cloudflare      │
            │ Worker          │
            │ (routes/health) │
            └─────────────────┘
```

### Instance type

Uses `standard-4` (4 vCPU, 12GB RAM, 20GB disk) to fit all 4 OSRM profiles. First request takes ~5 min while data is downloaded and processed. Subsequent requests are fast.

### Configuration

Edit `wrangler.jsonc` to change:
- `instance_type` — scale up/down (see [limits](https://developers.cloudflare.com/containers/platform-details/limits/))
- `max_instances` — number of container instances
- `sleepAfter` in `src/index.js` — idle timeout before container sleeps

### Manual deploy

```bash
cd cloudflare
npx wrangler deploy
```

### Test

```bash
# After deployment, your Worker URL will be:
# https://osrm-api.<your-subdomain>.workers.dev

curl "https://osrm-api.<your-subdomain>.workers.dev/distance?from=38.7577,9.0128&to=38.7891,9.0054&vehicle=car"
```
