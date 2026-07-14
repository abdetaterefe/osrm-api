import { Hono } from "hono";
import { serve } from "@hono/node-server";
import http from "http";

const OSRM_URLS = {
  car: process.env.OSRM_CAR || "http://localhost:5001",
  motorcycle: process.env.OSRM_MOTORCYCLE || "http://localhost:5004",
  bicycle: process.env.OSRM_BICYCLE || "http://localhost:5002",
  foot: process.env.OSRM_FOOT || "http://localhost:5003",
};

function osrmRequest(osrmUrl, path) {
  return new Promise((resolve, reject) => {
    const url = new URL(path, osrmUrl);
    http
      .get(url.toString(), (res) => {
        let data = "";
        res.on("data", (chunk) => (data += chunk));
        res.on("end", () => {
          try {
            resolve(JSON.parse(data));
          } catch {
            reject(new Error("Invalid OSRM response"));
          }
        });
      })
      .on("error", reject);
  });
}

function parseCoords(value) {
  const parts = value.split(",").map(Number);
  if (parts.length !== 2 || parts.some(isNaN)) return null;
  return parts;
}

function getOsrmUrl(vehicle) {
  const url = OSRM_URLS[vehicle];
  if (!url) return null;
  return url;
}

const app = new Hono();

app.get("/health", (c) =>
  c.json({
    status: "ok",
    profiles: Object.keys(OSRM_URLS),
  })
);

app.get("/route", async (c) => {
  const from = c.req.query("from");
  const to = c.req.query("to");
  const vehicle = c.req.query("vehicle") || "car";
  const alternatives = c.req.query("alternatives") === "true";
  const steps = c.req.query("steps") === "true";

  if (!from || !to) {
    return c.json({ error: "from and to query params required" }, 400);
  }

  const osrmUrl = getOsrmUrl(vehicle);
  if (!osrmUrl) {
    return c.json(
      { error: `unknown vehicle '${vehicle}'. supported: ${Object.keys(OSRM_URLS).join(", ")}` },
      400
    );
  }

  const fromCoords = parseCoords(from);
  const toCoords = parseCoords(to);
  if (!fromCoords || !toCoords) {
    return c.json({ error: "coords must be lon,lat numbers" }, 400);
  }

  const params = new URLSearchParams({
    overview: "full",
    alternatives: String(alternatives),
    steps: String(steps),
    geometries: "geojson",
  });

  try {
    const path = `/route/v1/driving/${fromCoords[0]},${fromCoords[1]};${toCoords[0]},${toCoords[1]}?${params}`;
    const result = await osrmRequest(osrmUrl, path);

    if (result.code !== "Ok" || !result.routes?.length) {
      return c.json({ error: "No route found" }, 404);
    }

    return c.json({
      vehicle,
      routes: result.routes.map((route) => ({
        distance_meters: route.distance,
        distance_km: parseFloat((route.distance / 1000).toFixed(2)),
        duration_seconds: Math.round(route.duration),
        duration_minutes: parseFloat((route.duration / 60).toFixed(1)),
        geometry: route.geometry,
        legs: route.legs.map((leg) => ({
          distance_meters: leg.distance,
          distance_km: parseFloat((leg.distance / 1000).toFixed(2)),
          duration_seconds: Math.round(leg.duration),
          duration_minutes: parseFloat((leg.duration / 60).toFixed(1)),
          steps: leg.steps?.map((step) => ({
            distance_meters: step.distance,
            duration_seconds: Math.round(step.duration),
            instruction: step.maneuver
              ? { type: step.maneuver.type, modifier: step.maneuver.modifier }
              : null,
            name: step.name,
            geometry: step.geometry,
          })),
        })),
      })),
      waypoints: result.waypoints.map((wp) => ({
        location: wp.location,
        name: wp.name,
      })),
    });
  } catch {
    return c.json({ error: "Failed to calculate route" }, 500);
  }
});

app.get("/distance", async (c) => {
  const from = c.req.query("from");
  const to = c.req.query("to");
  const vehicle = c.req.query("vehicle") || "car";

  if (!from || !to) {
    return c.json({ error: "from and to query params required" }, 400);
  }

  const osrmUrl = getOsrmUrl(vehicle);
  if (!osrmUrl) {
    return c.json(
      { error: `unknown vehicle '${vehicle}'. supported: ${Object.keys(OSRM_URLS).join(", ")}` },
      400
    );
  }

  const fromCoords = parseCoords(from);
  const toCoords = parseCoords(to);
  if (!fromCoords || !toCoords) {
    return c.json({ error: "coords must be lon,lat numbers" }, 400);
  }

  try {
    const path = `/route/v1/driving/${fromCoords[0]},${fromCoords[1]};${toCoords[0]},${toCoords[1]}?overview=false`;
    const result = await osrmRequest(osrmUrl, path);

    if (result.code !== "Ok" || !result.routes?.length) {
      return c.json({ error: "No route found" }, 404);
    }

    const route = result.routes[0];
    return c.json({
      vehicle,
      distance_meters: route.distance,
      distance_km: parseFloat((route.distance / 1000).toFixed(2)),
      duration_seconds: Math.round(route.duration),
      duration_minutes: parseFloat((route.duration / 60).toFixed(1)),
    });
  } catch {
    return c.json({ error: "Failed to calculate distance" }, 500);
  }
});

app.get("/compare", async (c) => {
  const from = c.req.query("from");
  const to = c.req.query("to");

  if (!from || !to) {
    return c.json({ error: "from and to query params required" }, 400);
  }

  const fromCoords = parseCoords(from);
  const toCoords = parseCoords(to);
  if (!fromCoords || !toCoords) {
    return c.json({ error: "coords must be lon,lat numbers" }, 400);
  }

  const results = {};
  for (const [vehicle, osrmUrl] of Object.entries(OSRM_URLS)) {
    try {
      const path = `/route/v1/driving/${fromCoords[0]},${fromCoords[1]};${toCoords[0]},${toCoords[1]}?overview=false`;
      const result = await osrmRequest(osrmUrl, path);

      if (result.code === "Ok" && result.routes?.length) {
        const route = result.routes[0];
        results[vehicle] = {
          distance_meters: route.distance,
          distance_km: parseFloat((route.distance / 1000).toFixed(2)),
          duration_seconds: Math.round(route.duration),
          duration_minutes: parseFloat((route.duration / 60).toFixed(1)),
        };
      } else {
        results[vehicle] = { error: "No route found" };
      }
    } catch {
      results[vehicle] = { error: "Service unavailable" };
    }
  }

  return c.json({ from: fromCoords, to: toCoords, results });
});

app.post("/matrix", async (c) => {
  const body = await c.req.json();
  const { coordinates, vehicle } = body;

  if (!Array.isArray(coordinates) || coordinates.length < 2) {
    return c.json({ error: "coordinates array with at least 2 [lon,lat] pairs required" }, 400);
  }

  const v = vehicle || "car";
  const osrmUrl = getOsrmUrl(v);
  if (!osrmUrl) {
    return c.json(
      { error: `unknown vehicle '${v}'. supported: ${Object.keys(OSRM_URLS).join(", ")}` },
      400
    );
  }

  try {
    const coords = coordinates.map((coord) => `${coord[0]},${coord[1]}`).join(";");
    const indices = coordinates.map((_, i) => i).join(";");

    const path = `/table/v1/driving/${coords}?sources=${indices}&destinations=${indices}&annotations=distance,duration`;
    const result = await osrmRequest(osrmUrl, path);

    if (result.code !== "Ok") {
      return c.json({ error: "Could not compute matrix" }, 404);
    }

    return c.json({
      vehicle: v,
      distances: result.distances,
      durations: result.durations,
      coordinates: result.waypoints.map((wp) => wp.location),
    });
  } catch {
    return c.json({ error: "Failed to calculate matrix" }, 500);
  }
});

const PORT = parseInt(process.env.PORT || "3000");
serve({ fetch: app.fetch, port: PORT }, () => {
  console.log(`OSRM API running on port ${PORT}`);
  console.log(`Profiles: ${Object.entries(OSRM_URLS).map(([k, v]) => `${k}=${v}`).join(", ")}`);
});
