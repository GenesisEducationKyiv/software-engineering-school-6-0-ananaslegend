export const BASE_URL = __ENV.BASE_URL || 'http://localhost:8080';
export const REPO_POOL_SIZE = parseInt(__ENV.REPO_POOL_SIZE || '100', 10);

export function randomRepo() {
  const i = Math.floor(Math.random() * REPO_POOL_SIZE);
  return `owner-${i}/repo-${i}`;
}

export function uniqueEmail(scenarioTag) {
  const ts = Date.now();
  return `loadtest+${scenarioTag}-${__VU}-${__ITER}-${ts}@example.com`;
}
