# Multi-Profile Navigation Service (Ethiopia)

High-performance, ultra-lean multi-modal navigation, routing, and matrix calculation service for Ethiopia. Built with **Valhalla** and **100% Go**. Engineered specifically for 512 MB RAM environments on **Koyeb**, **Railway**, **Render**, or any low-resource VPS.

---

## Why Valhalla + Go?

Unlike traditional OSRM setups which require a separate ~500 MB–1 GB static graph per vehicle mode, **Valhalla compiles hierarchical vector tiles once for the entire map**. 

- **All 4 Profiles in One Process**: Simultaneously routes **Car**, **Motorcycle**, **Bicycle**, and **Pedestrian** dynamically at query time.
- **Ultra-Low Memory Footprint**: Uses only **~120–180 MB of RAM total**, comfortably running inside 512 MB free-tier containers with zero risk of OOM kills.
- **Clean Go API & Supervisor**: A single binary serves the HTTP API, manages the LRU cache, auto-corrects coordinates, and acts as PID 1 supervisor managing `valhalla_service`.

---

## Supported Routing Profiles

| Profile | Valhalla Costing | Aliases | Description |
| :--- | :--- | :--- | :--- |
| **`bicycle`** *(Default)* | `bicycle` | `bike`, `cycling` | Prioritizes cycling paths, tracks, and quiet streets |
| **`car`** | `auto` | `auto`, `driving`, `automobile` | Standard vehicle routing based on speed limits and road classes |
| **`motorcycle`** | `motorcycle` | `moto`, `motorbike` | Fast, agile routing with flexible road traversal |
| **`pedestrian`** | `pedestrian` | `walk`, `foot`, `walking` | Walking routes utilizing sidewalks, crosswalks, and footpaths |

---

## API Endpoints

### 1. `GET /health`

Active probe verifying backend connectivity, engine, and cache status.

```bash
curl https://your-domain.app/health
```

Response (`200 OK`):
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

Lists available routing profiles, costing models, and aliases.

```bash
curl https://your-domain.app/profiles
```

Response (`200 OK`):
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

Calculates road distance and travel duration between two points for any profile.

Query parameters:
- `from`: starting coordinates (`lon,lat` or `lat,lon`)
- `to`: destination coordinates (`lon,lat` or `lat,lon`)
- `profile` (optional): `car`, `motorcycle`, `bicycle`, or `pedestrian` (default: `bicycle`)

```bash
# Car distance & travel time
curl "https://your-domain.app/distance?from=38.7577,9.0128&to=38.7891,9.0054&profile=car"

# Motorcycle distance & travel time
curl "https://your-domain.app/distance?from=38.7577,9.0128&to=38.7891,9.0054&profile=motorcycle"
```

Response (`200 OK`):
```json
{
  "profile": "car",
  "distance_km": 4.09,
  "duration_minutes": 10.5,
  "distance_meters": 4088,
  "duration_seconds": 630,
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

Full route geometry (GeoJSON `LineString`) and turn-by-turn navigation instructions.

Query parameters:
- `from`: starting coordinates (`lon,lat` or `lat,lon`)
- `to`: destination coordinates (`lon,lat` or `lat,lon`)
- `profile` (optional): `car`, `motorcycle`, `bicycle`, `pedestrian` (default: `bicycle`)
- `steps`: `true` or `false` (optional, default: `false`)

```bash
curl "https://your-domain.app/route?from=38.7577,9.0128&to=38.7891,9.0054&profile=motorcycle&steps=true"
```

Response (`200 OK`):
```json
{
  "profile": "motorcycle",
  "distance_km": 4.09,
  "duration_minutes": 8.5,
  "distance_meters": 4088,
  "duration_seconds": 510,
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
      "duration_seconds": 45,
      "type": "depart",
      "modifier": "right"
    },
    {
      "instruction": "Turn left onto Jomo Kenyatta Avenue",
      "street_name": "Jomo Kenyatta Avenue",
      "distance_meters": 1058,
      "duration_seconds": 150,
      "type": "turn",
      "modifier": "left"
    }
  ]
}
```

---

### 5. `POST /matrix`

Compute distance and travel duration matrices between multiple coordinate pairs.

Request body:
```json
{
  "profile": "car",
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
  "profile": "car",
  "distances_km": [
    [0.0, 4.09, 2.91],
    [4.33, 0.0, 3.38],
    [2.60, 3.30, 0.0]
  ],
  "durations_minutes": [
    [0.0, 10.5, 7.2],
    [11.2, 0.0, 8.4],
    [6.8, 8.1, 0.0]
  ],
  "distances_meters": [
    [0, 4088, 2906],
    [4327, 0, 3377],
    [2599, 3299, 0]
  ],
  "durations_seconds": [
    [0, 630, 432],
    [672, 0, 504],
    [408, 486, 0]
  ],
  "waypoints": [
    { "name": "", "location": [38.75770, 9.01280] },
    { "name": "", "location": [38.78910, 9.00540] },
    { "name": "", "location": [38.77000, 9.02000] }
  ]
}
```

---

## Smart Features

1. **Profile Isolation in LRU Cache**:
   - Cache keys are strictly namespaced by profile (`dist:car:...` vs `dist:bicycle:...`), preventing cross-profile collisions while maintaining **< 1 ms** responses for repeated routes.
2. **Smart Coordinate Auto-Correction**:
   - Seamlessly accepts either `lon,lat` or `lat,lon` and auto-corrects them for Ethiopia (lat: 3.0°–15.5°N, lon: 32.0°–48.5°E), rounded to 5 decimal places (~1.1m precision).
3. **PID 1 Process Supervisor**:
   - The Go binary handles signals (`SIGINT`, `SIGTERM`), supervises `valhalla_service`, conducts startup health probes, and performs graceful shutdowns.

---

## Deployment

### Railway / Koyeb / Render (512 MB Free Tier)

Deploy directly using the repository Dockerfile. The container uses multi-stage builds and runs under 200 MB RAM:

- `PORT`: `3000`
- `DEFAULT_PROFILE`: `bicycle` (or `car`)
- `VALHALLA_THREADS`: `1`
- `GOMEMLIMIT`: `48MiB`
- `GOGC`: `50`
