import http from 'k6/http';
import { check, sleep } from 'k6';
import { Trend, Rate, Counter } from 'k6/metrics';

const routeLatency = new Trend('route_duration_ms');
const distanceLatency = new Trend('distance_duration_ms');
const errorRate = new Rate('api_errors');
const cacheHits = new Counter('cache_hits');

export const options = {
  scenarios: {
    realistic_load: {
      executor: 'constant-vus',
      vus: 3, // 3 concurrent users constantly requesting
      duration: '45s',
    },
  },
  thresholds: {
    api_errors: ['rate<0.01'], // < 1% errors
  },
};

const BASE_URL = __ENV.API_URL || 'http://127.0.0.1:3080';

const LOCATIONS = [
  { lon: 38.7636, lat: 9.0105 },
  { lon: 38.7891, lat: 8.9954 },
  { lon: 38.7512, lat: 9.0347 },
  { lon: 38.7456, lat: 9.0118 },
  { lon: 38.8354, lat: 9.0210 },
  { lon: 38.8045, lat: 8.9902 },
];

const PROFILES = ['car', 'bicycle', 'motorcycle', 'pedestrian'];

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

  const res = http.get(
    `${BASE_URL}/distance?from=${p1.lon},${p1.lat}&to=${p2.lon},${p2.lat}&profile=${profile}`
  );
  distanceLatency.add(res.timings.duration);

  const ok = check(res, {
    'status is 200': (r) => r.status === 200,
  });

  if (!ok) {
    errorRate.add(1);
  } else {
    errorRate.add(0);
    if (res.headers['X-Cache'] === 'HIT') {
      cacheHits.add(1);
    }
  }

  sleep(0.1);
}
