# Ethiopia Navigation & Routing API

A high-performance, multi-modal routing, navigation, and distance matrix API for Ethiopia. Built with **Go** and powered by the **Valhalla** routing engine.

Engineered for ultra-low memory environments (running comfortably under **200 MB RAM**), high throughput, and sub-millisecond cached responses.

---

## Features

- **Multi-Modal Routing**: Native query-time routing for **Car**, **Motorcycle**, **Bicycle**, and **Pedestrian** modes on a unified hierarchical tile graph.
- **Clean REST API**: Simplified, flat JSON responses for road distances, travel durations, turn-by-turn steps, and GeoJSON geometries.
- **Sub-Millisecond In-Memory LRU Cache**: Thread-safe in-memory cache with 24-hour TTL serving repeated queries in **< 1 ms** with automatic profile isolation.
- **Smart Coordinate Auto-Correction**: Accepts both `lon,lat` and `lat,lon` formats, automatically detecting and correcting orientation for the Ethiopia geographic bounding box (~1.1m precision).
- **Time-Distance Matrix**: Fast N×N matrix calculation for origin-destination pairs.
- **Single-Binary Process Supervisor**: The Go server acts as PID 1, supervising the routing backend, conducting health probes, and handling graceful shutdowns.

---

## Supported Profiles

| Profile | Valhalla Costing | Aliases | Description |
| :--- | :--- | :--- | :--- |
| **`bicycle`** *(Default)* | `bicycle` | `bike`, `cycling` | Prioritizes cycle tracks, bike paths, and quiet roads |
| **`car`** | `auto` | `auto`, `driving`, `automobile` | Standard vehicle routing based on speed limits and road classes |
| **`motorcycle`** | `motorcycle` | `moto`, `motorbike` | Fast, agile routing with flexible road and track traversal |
| **`pedestrian`** | `pedestrian` | `walk`, `foot`, `walking` | Walking routes utilizing sidewalks, crosswalks, and footways |

---

## API Reference

### 1. `GET /health`

Health check endpoint verifying the HTTP server, routing backend status, and active cache count.

```bash
curl https://peculiar-rina-sharenex-b86b2492.koyeb.app/health
```

**Response (`200 OK`):**
```json
{
  "status": "ok",
  "backend": "online",
  "engine": "valhalla",
  "default_profile": "bicycle",
  "profiles": [
    "bicycle",
    "car",
    "motorcycle",
    "pedestrian"
  ],
  "cached_routes": 4
}
```

---

### 2. `GET /profiles`

Lists available routing profiles, their underlying costing models, aliases, and descriptions.

```bash
curl https://peculiar-rina-sharenex-b86b2492.koyeb.app/profiles
```

**Response (`200 OK`):**
```json
{
  "default_profile": "bicycle",
  "profiles": [
    {
      "name": "bicycle",
      "costing": "bicycle",
      "aliases": ["bike", "cycling"],
      "description": "Bicycle routing prioritizing cycling paths, tracks, and quiet roads"
    },
    {
      "name": "car",
      "costing": "auto",
      "aliases": ["auto", "driving", "automobile"],
      "description": "Automobile routing based on standard Ethiopian road classification and speed limits"
    },
    {
      "name": "motorcycle",
      "costing": "motorcycle",
      "aliases": ["moto", "motorbike"],
      "description": "Motorcycle routing with agile traversal and flexible road permissions"
    },
    {
      "name": "pedestrian",
      "costing": "pedestrian",
      "aliases": ["walk", "foot", "walking"],
      "description": "Pedestrian walking routes utilizing sidewalks, crosswalks, and footways"
    }
  ]
}
```

---

### 3. `GET /distance`

Calculates road distance and travel duration between two coordinates for any profile.

**Query Parameters:**
- `from` *(required)*: Starting point (`lon,lat` or `lat,lon`)
- `to` *(required)*: Destination point (`lon,lat` or `lat,lon`)
- `profile` *(optional)*: `car`, `motorcycle`, `bicycle`, or `pedestrian` (default: `bicycle`)

```bash
curl "https://peculiar-rina-sharenex-b86b2492.koyeb.app/distance?from=38.7577,9.0128&to=38.7891,9.0054&profile=car"
```

**Response (`200 OK`):**
```json
{
  "profile": "car",
  "distance_km": 4.82,
  "duration_minutes": 5.5,
  "distance_meters": 4822,
  "duration_seconds": 329,
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

### 4. `GET /route`

Full route calculation returning GeoJSON `LineString` geometry and optional turn-by-turn navigation instructions.

**Query Parameters:**
- `from` *(required)*: Starting point (`lon,lat` or `lat,lon`)
- `to` *(required)*: Destination point (`lon,lat` or `lat,lon`)
- `profile` *(optional)*: `car`, `motorcycle`, `bicycle`, or `pedestrian` (default: `bicycle`)
- `steps` *(optional)*: `true` or `false` (default: `false`)

```bash
curl "https://peculiar-rina-sharenex-b86b2492.koyeb.app/route?from=38.7577,9.0128&to=38.7891,9.0054&profile=bicycle&steps=true"
```

**Response (`200 OK`):**
```json
{
  "profile": "bicycle",
  "distance_km": 4.61,
  "duration_minutes": 16.9,
  "distance_meters": 4615,
  "duration_seconds": 1013,
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

### 5. `POST /matrix`

Calculates distance and travel duration matrices between multiple coordinate pairs.

**Request Body:**
```json
{
  "profile": "motorcycle",
  "coordinates": [
    [38.7577, 9.0128],
    [38.7891, 9.0054],
    [38.7700, 9.0200]
  ]
}
```

**Response (`200 OK`):**
```json
{
  "profile": "motorcycle",
  "distances_km": [
    [0.0, 4.82, 4.29],
    [5.4, 0.0, 4.28],
    [2.92, 3.91, 0.0]
  ],
  "durations_minutes": [
    [0.0, 5.5, 5.2],
    [6.5, 0.0, 5.2],
    [3.7, 4.5, 0.0]
  ],
  "distances_meters": [
    [0, 4822, 4287],
    [5395, 0, 4275],
    [2924, 3905, 0]
  ],
  "durations_seconds": [
    [0, 329, 314],
    [391, 0, 310],
    [223, 270, 0]
  ],
  "waypoints": [
    { "name": "", "location": [38.7577, 9.0128] },
    { "name": "", "location": [38.7891, 9.0054] },
    { "name": "", "location": [38.7700, 9.0200] }
  ]
}
```

---

## Configuration

The service is configured using standard environment variables:

| Variable | Default | Description |
| :--- | :--- | :--- |
| `PORT` | `3000` | HTTP port for the API server |
| `DEFAULT_PROFILE` | `bicycle` | Default profile when `profile` is omitted in requests |
| `VALHALLA_URL` | `http://127.0.0.1:8002` | Valhalla daemon listen URL |
| `VALHALLA_THREADS` | `1` | Worker thread count for Valhalla engine |
| `STRICT_BOUNDS` | `true` | Restrict coordinates to the Ethiopia bounding box |
| `GOMEMLIMIT` | `48MiB` | Go runtime memory ceiling to prevent GC spikes |
| `GOGC` | `50` | Go garbage collection frequency |

---

## Local Development

### Run with Docker Compose

```bash
docker compose up --build
```

### Run Unit Tests

```bash
cd api
go test -v ./...
```
