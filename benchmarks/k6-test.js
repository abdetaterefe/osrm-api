import http from 'k6/http';
import { check, sleep } from 'k6';
import { Trend, Rate, Counter } from 'k6/metrics';

// Custom metrics
const routeLatency = new Trend('route_duration_ms');
const distanceLatency = new Trend('distance_duration_ms');
const matrixLatency = new Trend('matrix_duration_ms');
const errorRate = new Rate('api_errors');
const cacheHits = new Counter('cache_hits');

export const options = {
  scenarios: {
    // 1-minute ramp & saturation test
    burst_test: {
      executor: 'ramping-vus',
      startVUs: 1,
      stages: [
        { duration: '20s', target: 5 },   // Warm up with light load
        { duration: '30s', target: 20 },  // Moderate traffic
        { duration: '30s', target: 50 },  // Stress / saturation test
        { duration: '20s', target: 0 },   // Cool down
      ],
      gracefulRampDown: '5s',
    },
  },
  thresholds: {
    // P95 latency should stay under 500ms even on 0.1 vCPU
    http_req_duration: ['p(95)<500'],
    api_errors: ['rate<0.05'], // < 5% errors
  },
};

const BASE_URL = __ENV.API_URL || 'http://localhost:3000';

// Real coordinate pairs in Ethiopia
const LOCATIONS = [
  // Addis Ababa coordinates
  { name: 'Meskel Square', lon: 38.7636, lat: 9.0105 },
  { name: 'Bole Medhanealem', lon: 38.7891, lat: 8.9954 },
  { name: 'Piazza', lon: 38.7512, lat: 9.0347 },
  { name: 'Mexico Square', lon: 38.7456, lat: 9.0118 },
  { name: 'CMC', lon: 38.8354, lat: 9.0210 },
  { name: 'Gerji', lon: 38.8045, lat: 8.9902 },
  { name: 'Sarbet', lon: 38.7360, lat: 8.9980 },
  { name: 'Megenagna', lon: 38.8010, lat: 9.0205 },
];

const PROFILES = ['bicycle', 'car', 'motorcycle', 'pedestrian'];

function getRandomItem(arr) {
  return arr[Math.floor(Math.random() * arr.length)];
}

export default function () {
  const p1 = getRandomItem(LOCATIONS);
  let p2 = getRandomItem(LOCATIONS);
  while (p2 === p1) {
    p2 = getRandomItem(LOCATIONS);
  }

  const profile = getRandomItem(PROFILES);

  // Test 1: GET /distance
  const distRes = http.get(
    `${BASE_URL}/distance?from=${p1.lon},${p1.lat}&to=${p2.lon},${p2.lat}&profile=${profile}`
  );
  distanceLatency.add(distRes.timings.duration);

  const distSuccess = check(distRes, {
    'distance status 200': (r) => r.status === 200,
    'distance has km': (r) => {
      try {
        const body = JSON.parse(r.body);
        return body.distance_km > 0 && body.profile === profile;
      } catch (e) {
        return false;
      }
    },
  });

  if (!distSuccess) {
    errorRate.add(1);
  } else {
    errorRate.add(0);
    if (distRes.headers['X-Cache'] === 'HIT') {
      cacheHits.add(1);
    }
  }

  sleep(0.05);

  // Test 2: GET /route (turn-by-turn steps)
  const routeRes = http.get(
    `${BASE_URL}/route?from=${p1.lon},${p1.lat}&to=${p2.lon},${p2.lat}&profile=${profile}&steps=true`
  );
  routeLatency.add(routeRes.timings.duration);

  const routeSuccess = check(routeRes, {
    'route status 200': (r) => r.status === 200,
    'route has geometry': (r) => {
      try {
        const body = JSON.parse(r.body);
        return body.geometry !== undefined && body.distance_meters > 0;
      } catch (e) {
        return false;
      }
    },
  });

  if (!routeSuccess) {
    errorRate.add(1);
  } else {
    errorRate.add(0);
    if (routeRes.headers['X-Cache'] === 'HIT') {
      cacheHits.add(1);
    }
  }

  sleep(0.05);

  // Test 3: POST /matrix (3x3 points)
  const matrixPoints = [
    [p1.lon, p1.lat],
    [p2.lon, p2.lat],
    [38.7700, 9.0200],
  ];

  const matrixPayload = JSON.stringify({
    profile: profile,
    coordinates: matrixPoints,
  });

  const matrixRes = http.post(`${BASE_URL}/matrix`, matrixPayload, {
    headers: { 'Content-Type': 'application/json' },
  });
  matrixLatency.add(matrixRes.timings.duration);

  const matrixSuccess = check(matrixRes, {
    'matrix status 200': (r) => r.status === 200,
    'matrix has 3x3 distances': (r) => {
      try {
        const body = JSON.parse(r.body);
        return (
          body.distances_km &&
          body.distances_km.length === 3 &&
          body.profile === profile
        );
      } catch (e) {
        return false;
      }
    },
  });

  if (!matrixSuccess) {
    errorRate.add(1);
  } else {
    errorRate.add(0);
    if (matrixRes.headers['X-Cache'] === 'HIT') {
      cacheHits.add(1);
    }
  }

  sleep(0.1);
}
