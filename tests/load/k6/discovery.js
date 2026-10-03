import http from 'k6/http';
import { check } from 'k6';
import { BASE_URL, randomRepo, uniqueEmail } from './lib/env.js';

export const options = {
  scenarios: {
    ramp: {
      executor: 'ramping-arrival-rate',
      startRate: 5,
      timeUnit: '1s',
      preAllocatedVUs: 50,
      maxVUs: 800,
      stages: [
        { target: 5,   duration: '30s' },
        { target: 25,  duration: '1m' },
        { target: 50,  duration: '1m' },
        { target: 100, duration: '1m' },
        { target: 200, duration: '1m' },
        { target: 400, duration: '1m' },
      ],
      tags: { scenario: 'discovery' },
    },
  },
};

export default function () {
  const payload = JSON.stringify({
    email: uniqueEmail('disc'),
    repository: randomRepo(),
  });
  const res = http.post(`${BASE_URL}/api/subscribe`, payload, {
    headers: { 'Content-Type': 'application/json' },
    tags: { scenario: 'discovery' },
  });
  check(res, { 'status is 2xx': (r) => r.status >= 200 && r.status < 300 });
}
