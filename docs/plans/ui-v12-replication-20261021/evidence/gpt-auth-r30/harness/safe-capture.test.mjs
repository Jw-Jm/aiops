// r30 repair 2 self-check: proves the capture protocol fails closed when a
// secret shows up *during* the settle wait or *between* the probe and the
// capture. Uses a fake page (no browser, no real factor data): the probe
// results are synthetic strings, and no image is ever produced from them.
import assert from 'node:assert/strict';
import { mkdtemp, readFile, rm } from 'node:fs/promises';
import { existsSync } from 'node:fs';
import { tmpdir } from 'node:os';
import { join } from 'node:path';
import test from 'node:test';

import { safeShot } from './safe-capture.mjs';

// evaluate() is called once by settle() and once per probe. The queue below
// therefore starts with the settle placeholder, followed by the synthetic probe
// results for the settled pre-capture probe and the post-capture probe.
function fakePage(queue, { onScreenshot } = {}) {
  const results = [...queue];
  return {
    mouse: { move: async () => {} },
    waitForFunction: async () => {},
    evaluate: async () => {
      if (!results.length) return [];
      return results.shift();
    },
    screenshot: async ({ path }) => {
      if (onScreenshot) onScreenshot(path);
      await (await import('node:fs/promises')).writeFile(path, 'synthetic-frame');
    },
  };
}

test('a secret that appears during the settle wait is refused and writes no image', async () => {
  const dir = await mkdtemp(join(tmpdir(), 'safe-capture-'));
  const file = join(dir, 'during-settle.png');
  let captured = 0;
  const page = fakePage([undefined, ['otpauth-uri']], { onScreenshot: () => (captured += 1) });
  await assert.rejects(() => safeShot(page, file), /secret UI present after settle/);
  assert.equal(captured, 0, 'screenshot must not be taken when the settled DOM holds a secret');
  assert.equal(existsSync(file), false, 'no file may be written for a refused capture');
  await rm(dir, { recursive: true, force: true });
});

test('a secret that appears only after the pre-capture probe is deleted again', async () => {
  const dir = await mkdtemp(join(tmpdir(), 'safe-capture-'));
  const file = join(dir, 'late-secret.png');
  const page = fakePage([undefined, [], ['otp-input']]);
  await assert.rejects(() => safeShot(page, file), /secret UI appeared during capture/);
  assert.equal(existsSync(file), false, 'the late-probe failure must remove the written image');
  await rm(dir, { recursive: true, force: true });
});

test('a settled secret-free state is captured once and kept', async () => {
  const dir = await mkdtemp(join(tmpdir(), 'safe-capture-'));
  const file = join(dir, 'clean.png');
  let captured = 0;
  const page = fakePage([undefined, [], []], { onScreenshot: () => (captured += 1) });
  const receipt = await safeShot(page, file);
  assert.equal(captured, 1);
  assert.equal(receipt.probes.length, 0);
  assert.equal(receipt.postProbes.length, 0);
  assert.equal(await readFile(file, 'utf8'), 'synthetic-frame');
  await rm(dir, { recursive: true, force: true });
});

test('capture can be disabled for a state that cannot be shown', async () => {
  const dir = await mkdtemp(join(tmpdir(), 'safe-capture-'));
  const file = join(dir, 'disabled.png');
  const page = fakePage([undefined, []]);
  await assert.rejects(() => safeShot(page, file, { allowSecretFree: false }), /capture disabled/);
  assert.equal(existsSync(file), false);
  await rm(dir, { recursive: true, force: true });
});
