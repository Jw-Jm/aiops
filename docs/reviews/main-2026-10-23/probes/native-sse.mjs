// Run from the repository root with node --experimental-strip-types.
import assert from 'node:assert/strict';
import { streamInvestigationEventsWithResume } from '../../../../web/src/api/sse-reader.ts';
let calls = 0;
globalThis.fetch = async () => { calls++; throw new Error('unexpected network request'); };
await assert.rejects(async () => {
  for await (const event of streamInvestigationEventsWithResume('/api/v1/investigations/test/events', {
    credentials: 'same-origin', headers: {},
  })) void event;
}, /requires Bearer authentication/);
assert.equal(calls, 0);
console.log('CONFIRMED: native cookie SSE is rejected before fetch (calls=0).');
