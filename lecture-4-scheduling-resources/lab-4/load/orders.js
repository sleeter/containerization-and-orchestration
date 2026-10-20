import http from 'k6/http';
import { check } from 'k6';

export const options = {
  scenarios: {
    orders: {
      executor: 'constant-arrival-rate',
      rate: 10,
      timeUnit: '1s',
      duration: '15m',
      preAllocatedVUs: 5,
      maxVUs: 20,
    },
  },
  thresholds: {
    http_req_failed: ['rate<0.01'],
    http_req_duration: ['p(95)<300'],
    checks: ['rate>0.99'],
  },
};

export default function () {
  const response = http.post(
    'http://host.docker.internal:8080/order',
    JSON.stringify({ item: 'load-test' }),
    { headers: { 'Content-Type': 'application/json' }, timeout: '5s' },
  );
  check(response, {
    'order created': (r) => r.status === 200 || r.status === 201,
  });
}
