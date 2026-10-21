import http from 'k6/http';
import { check } from 'k6';

export const options = {
  noConnectionReuse: true,
  scenarios: {
    orders: {
      executor: 'constant-arrival-rate',
      rate: 10,
      timeUnit: '1s',
      duration: __ENV.DURATION || '15m',
      preAllocatedVUs: 50,
      maxVUs: 50,
    },
  },
  thresholds: {
    http_req_failed: ['rate<0.01'],
    http_req_duration: ['p(95)<1000'],
    checks: ['rate>0.99'],
    dropped_iterations: ['count==0'],
  },
};

export default function () {
  const response = http.post(
    `${__ENV.BASE_URL}/order`,
    JSON.stringify({ item: 'load-test' }),
    { headers: { 'Content-Type': 'application/json' }, timeout: '5s' },
  );
  check(response, {
    'order created': (r) => r.status === 200 || r.status === 201,
  });
}
