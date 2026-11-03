// r30 repair 2: the only capture helper used by the r30 harness.
//
// Policy (see screenshot-policy.md): a screenshot may never contain a factor
// secret. Before every capture the page is inspected for
//
//   * an otpauth:// enrolment URI or its base32 secret,
//   * a recovery code shaped like XXXX-XXXX,
//   * a 6 digit one-time code sitting in a non-password input,
//   * a base32 secret sitting in a non-password input,
//
// and the capture is refused when any of them is present. States that
// inherently show secrets (the one-time recovery-code panel, the pending
// enrolment URI) are therefore recorded as JSON assertions only, never as an
// image; the safe state after reload/expiry/cancel is what gets captured.
//
// Capture protocol (revised after the r30 review):
//   1. settle()  — park the pointer, wait for modal transform/animation to end,
//   2. probe     — inspect the *settled* DOM immediately before the capture,
//   3. capture,
//   4. re-probe  — if the frame changed into a secret state, delete the file.
// A probe taken before settle() cannot describe what is on screen at capture
// time, because the DOM is still allowed to change during the wait. The probe
// therefore runs after settle(), in the same state the screenshot records, and
// the post-capture probe makes a late secret fail closed instead of silently
// leaving an image on disk.
import { rm } from 'node:fs/promises';
import { setTimeout as delay } from 'node:timers/promises';

const SECRET_PROBES = [
  { name: 'otpauth-uri', re: /otpauth:\/\/totp\//i },
  { name: 'recovery-code', re: /\b[A-Z0-9]{4}-[A-Z0-9]{4}\b/ },
];

export async function inspectPage(page) {
  return page.evaluate((probes) => {
    const found = [];
    const text = document.body ? document.body.innerText : '';
    for (const probe of probes) {
      if (new RegExp(probe.re, 'i').test(text)) found.push(probe.name);
    }
    for (const field of document.querySelectorAll('input, textarea')) {
      if ((field.getAttribute('type') || 'text') === 'password') continue;
      const value = field.value || '';
      if (/^\d{6}$/.test(value.trim())) found.push('otp-input');
      if (/^[A-Z2-7]{16,}$/i.test(value.trim())) found.push('base32-input');
    }
    return found;
  }, SECRET_PROBES.map((probe) => ({ name: probe.name, re: probe.re.source })));
}

export async function settle(page) {
  await page.mouse.move(1438, 4);
  await page.waitForFunction(() => {
    const modal = document.querySelector('.ant-modal');
    if (!modal) return true;
    const style = getComputedStyle(modal);
    return style.opacity === '1' && style.transform === 'none';
  });
  await page.evaluate(async () => {
    const running = document.getAnimations().filter((animation) => {
      const timing = animation.effect && animation.effect.getComputedTiming ? animation.effect.getComputedTiming() : null;
      return animation.playState === 'running' && timing && Number.isFinite(timing.endTime) && timing.endTime > 0;
    });
    await Promise.race([
      Promise.all(running.map((animation) => animation.finished.catch(() => undefined))),
      new Promise((resolve) => setTimeout(resolve, 3000)),
    ]);
  });
  await delay(120);
}

// Captured only when both the settled pre-capture probe and the post-capture
// probe are empty. `describeSecretState` is what the caller records in JSON
// instead of an image when the state cannot be shown.
export async function safeShot(page, file, { allowSecretFree = true } = {}) {
  if (!allowSecretFree) {
    throw new Error('refusing to capture ' + file + ': capture disabled for this state');
  }
  await settle(page);
  const before = await inspectPage(page);
  if (before.length) {
    throw new Error('refusing to capture ' + file + ': secret UI present after settle (' + before.join(',') + ')');
  }
  await page.screenshot({ path: file });
  const after = await inspectPage(page);
  if (after.length) {
    await rm(file, { force: true });
    throw new Error('discarded ' + file + ': secret UI appeared during capture (' + after.join(',') + ')');
  }
  return { file, probes: [], postProbes: [], capturedAt: new Date().toISOString() };
}

// Replaces a screenshot with a machine-checkable statement about the state.
export async function describeSecretState(page, label) {
  return page.evaluate((name) => {
    const text = document.body ? document.body.innerText : '';
    return {
      state: name,
      hasOtpauthUri: /otpauth:\/\/totp\//i.test(text),
      recoveryCodeCount: (text.match(/\b[A-Z0-9]{4}-[A-Z0-9]{4}\b/g) || []).length,
      otpFilledInputs: [...document.querySelectorAll('input')].filter((field) => (field.getAttribute('type') || 'text') !== 'password' && /^\d{6}$/.test((field.value || '').trim())).length,
      captured: false,
      reason: 'secret-bearing state is asserted, never captured',
    };
  }, label);
}
