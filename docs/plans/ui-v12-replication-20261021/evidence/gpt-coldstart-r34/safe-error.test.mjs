import { test } from 'node:test';
import assert from 'node:assert/strict';
import { safeErrorMessage } from './safe-error.mjs';
test('removes transport credentials while retaining the failure and URL', () => {
  const error = new Error('ECONNREFUSED https://example.invalid/login\n  - cookie: __Host-ops-session=fake-cookie\n  - authorization: Bearer fake-bearer\n  - X-CSRF-Token: fake-csrf');
  const safe = safeErrorMessage(error);
  for (const value of ['fake-cookie', 'fake-bearer', 'fake-csrf']) assert(!safe.includes(value));
  assert(safe.includes('ECONNREFUSED https://example.invalid/login'));
});
test('removes standalone cookies and caller-provided secrets', () => {
  const safe = safeErrorMessage(new Error('session __Host-ops-session=fake-token; password fake-password'), ['fake-password']);
  assert(!safe.includes('fake-token')); assert(!safe.includes('fake-password'));
});
test('preserves ordinary diagnostic messages', () => {
  assert.equal(safeErrorMessage(new Error('readyz returned 503')), 'readyz returned 503');
});
