// Real browser acceptance for the P2-03 MFA, step-up and R3/R4 hardening on
// the dedicated task instance. Runs on the authorized remote host.
//
// Every check is an assertion: a wrong status, a wrong count, a missing
// guarantee level or a wrong error code fails the run. No cookie, CSRF token,
// password, TOTP secret or recovery code is written to the evidence.
import { chromium } from '/data/ops-ui-v12-http-20261022-r1/preview-r1/playwright-core/index.mjs';
import { readFileSync, writeFileSync } from 'node:fs';
import { createHmac } from 'node:crypto';

const origin = 'https://192.168.0.63:30464';
const root = '/data/ops-ui-v12-mfa-20261022-r1/preview';
const identity = JSON.parse(readFileSync(root + '/private/browser-identity.json', 'utf8'));
const adminPassword = identity.password;
const result = {
  origin, checks: [], requests: [], consoleErrors: [], failuresRetained: [],
  instance: { database: '127.0.0.1:15453', schemaVersion: 51, api: '127.0.0.1:18084' },
};
const browser = await chromium.launch({
  executablePath: '/opt/ops-assistant-browser-20261014/chrome-linux64/chrome',
  headless: true, args: ['--no-sandbox', '--disable-dev-shm-usage'],
});
const record = (path, status, requestId) => result.requests.push({ path, status, requestId });

function totpCode(secret, step) {
  const normalized = secret.toUpperCase().replace(/=+$/, '');
  const alphabet = 'ABCDEFGHIJKLMNOPQRSTUVWXYZ234567';
  let bits = '';
  for (const character of normalized) {
    const index = alphabet.indexOf(character);
    if (index < 0) throw new Error('provisioning secret is not base32');
    bits += index.toString(2).padStart(5, '0');
  }
  const bytes = [];
  for (let index = 0; index + 8 <= bits.length; index += 8) {
    bytes.push(parseInt(bits.slice(index, index + 8), 2));
  }
  const counter = Buffer.alloc(8);
  counter.writeBigUInt64BE(BigInt(step));
  const digest = createHmac('sha1', Buffer.from(bytes)).update(counter).digest();
  const offset = digest[digest.length - 1] & 0x0f;
  const value = (digest.readUInt32BE(offset) & 0x7fffffff) % 1000000;
  return String(value).padStart(6, '0');
}
const currentStep = () => Math.floor(Date.now() / 1000 / 30);

async function api(page, path, options = {}) {
  return page.evaluate(async ({ path, options }) => {
    const response = await fetch(path, { cache: 'no-store', ...options });
    let body = null;
    try { body = await response.json(); } catch { body = null; }
    return { status: response.status, body };
  }, { path, options });
}

try {
  result.browser = browser.version();
  const context = await browser.newContext({
    ignoreHTTPSErrors: true, viewport: { width: 1440, height: 960 }, deviceScaleFactor: 1,
    locale: 'zh-CN', timezoneId: 'Asia/Shanghai', colorScheme: 'light',
  });
  const page = await context.newPage();
  page.on('pageerror', (e) => result.consoleErrors.push(e.message));
  page.on('response', (response) => {
    if (response.url().includes('/api/'))
      record(new URL(response.url()).pathname, response.status(), response.headers()['x-request-id']);
  });

  // 1) The login page renders for an unauthenticated browser.
  await page.goto(origin + '/login');
  await page.getByLabel('用户名', { exact: true }).waitFor();
  await page.evaluate(() => document.fonts.ready);
  await page.screenshot({ path: root + '/logs/login-empty.png' });
  result.checks.push('login renders for an unauthenticated browser');

  // 2) The very first login of a fresh administrator is first-password-only.
  await page.getByLabel('用户名', { exact: true }).fill(identity.loginName);
  await page.getByLabel('密码', { exact: true }).fill(adminPassword);
  await page.getByRole('button', { name: /^登\s*录$/ }).click();
  await page.waitForURL('**/auth/first-password', { timeout: 15000 });
  const firstSession = await api(page, '/api/v1/auth/session');
  result.firstOnly = { status: firstSession.status, mode: firstSession.body?.data?.mode, url: page.url() };
  if (firstSession.status !== 200) throw new Error('the first-password session is not readable');
  if (firstSession.body.data.mode !== 'first_password_only')
    throw new Error('a fresh administrator must be first-password-only');
  if (firstSession.body.data.authorization.grants.length !== 0)
    throw new Error('a first-password session exposed business grants');
  await page.screenshot({ path: root + '/logs/first-password-only.png' });
  result.checks.push('the first login of a fresh administrator is first-password-only without grants');

  // 3) The password rotation produces a full business session.
  const newPassword = 'mfa-preview-' + Math.random().toString(36).slice(2, 14);
  const currentInput = page.getByLabel('当前密码', { exact: true });
  if (await currentInput.count()) {
    await currentInput.fill(adminPassword);
    await page.getByLabel('新密码', { exact: true }).fill(newPassword);
    await page.getByLabel('确认新密码', { exact: true }).fill(newPassword);
    await page.getByRole('button', { name: '设置密码并继续' }).first().click();
    await page.waitForURL(/\/(login|access\/sources)/, { timeout: 15000 });
  } else {
    const change = await api(page, '/api/v1/auth/password:change', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json', 'X-CSRF-Token': firstSession.body.data.csrf.token },
      body: JSON.stringify({
        currentPassword: adminPassword, newPassword,
        expectedCredentialVersion: firstSession.body.data.revocation.credentialVersion,
      }),
    });
    if (change.status !== 201) throw new Error('password rotation failed: ' + change.status);
  }
  if (page.url().endsWith('/login')) {
    await page.getByLabel('用户名', { exact: true }).fill(identity.loginName);
    await page.getByLabel('密码', { exact: true }).fill(newPassword);
    await page.getByRole('button', { name: /^登\s*录$/ }).click();
    await page.waitForURL('**/access/sources', { timeout: 15000 });
  }
  if (!page.url().endsWith('/access/sources')) throw new Error('the administrator shell was not reached');
  result.checks.push('the rotated password signs in and reaches the administrator shell');

  // 4) The shared authorization answers with exact semantics.
  const clusters = await api(page, '/api/v1/admin/clusters');
  const overview = await api(page, '/api/v1/overview?clusterUid=ungranted-preview-cluster');
  const bindings = await api(page, '/api/v1/admin/role-bindings');
  result.authorization = {
    adminClusters: { status: clusters.status, count: Array.isArray(clusters.body?.data) ? clusters.body.data.length : null, code: clusters.body?.code ?? null },
    operatorOverview: { status: overview.status, code: overview.body?.code ?? null },
    legacyBindings: { status: bindings.status, count: Array.isArray(bindings.body?.data) ? bindings.body.data.length : null },
  };
  if (clusters.status !== 200) throw new Error('admin cluster listing must be 200, got ' + clusters.status);
  if (!Array.isArray(clusters.body.data)) throw new Error('admin cluster listing is not a list');
  if (overview.status !== 403) throw new Error('an ungranted operator scope must be 403, got ' + overview.status);
  if (overview.body?.code !== 'FORBIDDEN') throw new Error('the operator denial must carry the FORBIDDEN code');
  if (bindings.status !== 200) throw new Error('the legacy binding listing must be 200, got ' + bindings.status);
  if (!Array.isArray(bindings.body.data) || bindings.body.data.length !== 0)
    throw new Error('the native instance must not carry legacy role bindings');
  result.checks.push('admin clusters 200, ungranted operator scope 403 FORBIDDEN, legacy bindings 0 rows');

  // 5) The session projection is LoA1 before any second factor.
  const sessionBefore = await api(page, '/api/v1/auth/session');
  result.sessionBefore = { acr: sessionBefore.body?.data?.acr, authMethods: sessionBefore.body?.data?.authMethods };
  if (sessionBefore.status !== 200) throw new Error('the session projection is not readable');
  if (sessionBefore.body.data.acr !== 'urn:ops:loa:1')
    throw new Error('a password-only session must stay LoA1, got ' + sessionBefore.body.data.acr);
  if (sessionBefore.body.data.authMethods.join(',') !== 'password')
    throw new Error('a password-only session must report only the password method');

  // 6) The account security page is reachable and shows the real state.
  await page.goto(origin + '/account/security');
  await page.getByText('双因素状态').waitFor({ timeout: 15000 });
  await page.screenshot({ path: root + '/logs/account-security.png' });
  const statusBefore = await api(page, '/api/v1/auth/mfa/status');
  result.mfaStatusBefore = statusBefore.body?.data;
  if (statusBefore.status !== 200) throw new Error('the MFA status is not readable');
  if (statusBefore.body.data.state !== 'none')
    throw new Error('a fresh account must have no second factor, got ' + statusBefore.body.data.state);
  result.checks.push('the account security route renders and reports the server-side MFA state');

  // 7) The enrolment returns the provisioning URI exactly once.
  const enrolment = await api(page, '/api/v1/auth/mfa/enrollments', {
    method: 'POST',
    headers: { 'Content-Type': 'application/json', 'X-CSRF-Token': sessionBefore.body.data.csrf.token },
    body: JSON.stringify({ method: 'totp', expectedCredentialVersion: sessionBefore.body.data.revocation.credentialVersion }),
  });
  if (enrolment.status !== 201) throw new Error('the enrolment must be 201, got ' + enrolment.status);
  const enrollmentId = enrolment.body.data.enrollmentId;
  const provisioningUri = enrolment.body.data.otpauthUri;
  if (!provisioningUri.startsWith('otpauth://totp/')) throw new Error('the enrolment did not return a provisioning URI');
  const secret = new URL(provisioningUri).searchParams.get('secret');
  if (!secret || secret.length < 16) throw new Error('the provisioning URI carries no usable secret');
  const enrollmentView = await api(page, '/api/v1/auth/mfa/enrollments/' + enrollmentId);
  result.enrollment = { created: enrolment.status, view: enrollmentView.status, state: enrollmentView.body?.data?.state };
  if (enrollmentView.status !== 200) throw new Error('the enrolment view must be 200');
  if (enrollmentView.body.data.state !== 'pending_confirmation')
    throw new Error('the enrolment must be pending_confirmation');
  if (JSON.stringify(enrollmentView.body).includes('otpauth'))
    throw new Error('the enrolment view leaked the provisioning URI');
  result.checks.push('the enrolment URI is returned once and the status view never repeats it');

  // 8) A challenge alone stays LoA1, a wrong password is rejected, and
  //    password plus TOTP reaches LoA2.
  const challenge = await api(page, '/api/v1/auth/step-up-challenges', {
    method: 'POST',
    headers: { 'Content-Type': 'application/json', 'X-CSRF-Token': sessionBefore.body.data.csrf.token },
    body: JSON.stringify({
      operation: 'mfa_enrollment',
      target: { kind: 'mfa_enrollment', enrollmentId, revision: 1 },
      expectedSessionVersion: sessionBefore.body.data.revocation.sessionVersion,
    }),
  });
  if (challenge.status !== 201) throw new Error('the challenge must be 201, got ' + challenge.status);
  const challengeId = challenge.body.data.challengeId;
  if (challenge.body.data.requiredMethods.join(',') !== 'password,totp')
    throw new Error('the server must require password and totp');
  const wrongPassword = await api(page, '/api/v1/auth/step-up-challenges/' + challengeId + ':verify', {
    method: 'POST',
    headers: { 'Content-Type': 'application/json', 'X-CSRF-Token': sessionBefore.body.data.csrf.token },
    body: JSON.stringify({ method: 'password', credential: 'not-the-password', expectedChallengeRevision: challenge.body.data.revision }),
  });
  if (wrongPassword.status !== 200) throw new Error('a rejected password must still answer 200 with a result');
  if (wrongPassword.body.data.verified !== false) throw new Error('a wrong password must not verify the challenge');
  if (wrongPassword.body.data.acr !== 'urn:ops:loa:1') throw new Error('a rejected factor must stay LoA1');
  if (wrongPassword.body.data.failureCode !== 'invalid_credential')
    throw new Error('a rejected factor must report invalid_credential');
  const passwordStep = await api(page, '/api/v1/auth/step-up-challenges/' + challengeId + ':verify', {
    method: 'POST',
    headers: { 'Content-Type': 'application/json', 'X-CSRF-Token': sessionBefore.body.data.csrf.token },
    body: JSON.stringify({ method: 'password', credential: newPassword, expectedChallengeRevision: wrongPassword.body.data.revision }),
  });
  if (passwordStep.body.data.verified !== false) throw new Error('one factor must not verify the challenge');
  if (passwordStep.body.data.stepUpSession !== null) throw new Error('one factor must not mint a step-up session');
  const totpStep = await api(page, '/api/v1/auth/step-up-challenges/' + challengeId + ':verify', {
    method: 'POST',
    headers: { 'Content-Type': 'application/json', 'X-CSRF-Token': sessionBefore.body.data.csrf.token },
    body: JSON.stringify({ method: 'totp', credential: totpCode(secret, currentStep()), expectedChallengeRevision: passwordStep.body.data.revision }),
  });
  if (totpStep.body.data.verified !== true) throw new Error('password plus totp must verify the challenge');
  if (totpStep.body.data.acr !== 'urn:ops:loa:2') throw new Error('password plus totp must reach LoA2');
  if (!totpStep.body.data.stepUpSession) throw new Error('a verified challenge must mint a step-up session');
  result.checks.push('a wrong password stays LoA1, password plus totp reaches LoA2 with a step-up session');

  // 9) The confirmation consumes the bound step-up and issues the codes once.
  const confirmation = await api(page, '/api/v1/auth/mfa/enrollments/' + enrollmentId + ':verify', {
    method: 'POST',
    headers: { 'Content-Type': 'application/json', 'X-CSRF-Token': sessionBefore.body.data.csrf.token },
    body: JSON.stringify({
      totpCode: totpCode(secret, currentStep()),
      expectedEnrollmentRevision: 1,
      stepUp: { challengeId, sessionRef: sessionBefore.body.data.sessionRef },
    }),
  });
  if (confirmation.status !== 200) throw new Error('the confirmation must be 200, got ' + confirmation.status);
  const codes = confirmation.body.data.recoveryCodes.codes;
  if (!Array.isArray(codes) || codes.length !== 10) throw new Error('the confirmation must return ten recovery codes');
  const statusAfter = await api(page, '/api/v1/auth/mfa/status');
  if (statusAfter.body.data.state !== 'confirmed') throw new Error('the factor must be confirmed');
  if (statusAfter.body.data.recoveryCodesRemaining !== 10) throw new Error('the recovery code count must be ten');
  const sessionAfter = await api(page, '/api/v1/auth/session');
  if (sessionAfter.body.data.acr !== 'urn:ops:loa:2') throw new Error('the session must report LoA2 after the step-up');
  result.checks.push('the enrolment is confirmed, ten recovery codes are issued once and the session reports LoA2');

  // 10) The account security page shows the confirmed factor.
  await page.goto(origin + '/account/security');
  await page.getByText('双因素状态').waitFor({ timeout: 15000 });
  await page.reload();
  await page.getByText('双因素状态').waitFor({ timeout: 15000 });
  await page.getByText('已启用').waitFor({ timeout: 15000 });
  const confirmedTag = await page.getByText('已启用').count();
  if (confirmedTag < 1) throw new Error('the account security page does not show the confirmed factor');
  const loa2Tag = await page.getByText('LoA 2（密码 + 动态码）').count();
  if (loa2Tag < 1) throw new Error('the account security page does not show the LoA2 session');
  await page.screenshot({ path: root + '/logs/account-security-confirmed.png' });
  result.checks.push('the account security page shows the confirmed second factor');

  // 11) R3: a failed server revocation is reported and can be retried.
  await page.goto(origin + '/access/sources');
  await page.getByRole('button', { name: '退出', exact: true }).waitFor({ timeout: 15000 });
  await page.route('**/api/v1/auth/logout', async (route) => {
    await route.fulfill({
      status: 500, contentType: 'application/json',
      body: JSON.stringify({ code: 'INTERNAL', message: 'injected revocation failure', requestId: 'injected', retryable: true }),
    });
  });
  await page.getByRole('button', { name: '退出', exact: true }).click();
  await page.waitForURL('**/login', { timeout: 15000 });
  const notice = page.getByText('上次退出的服务器会话尚未确认撤销');
  await notice.waitFor({ timeout: 15000 });
  const retry = page.getByRole('button', { name: '重试撤销' });
  if ((await retry.count()) < 1) throw new Error('the unconfirmed revocation offers no retry');
  await page.screenshot({ path: root + '/logs/logout-unconfirmed.png' });
  const cookieAfterFailure = (await context.cookies()).find((c) => c.name === '__Host-ops-session');
  result.revocation = { notice: true, retry: true, cookieKept: Boolean(cookieAfterFailure) };
  if (!cookieAfterFailure) throw new Error('the unconfirmed revocation dropped the cookie before the server confirmed');
  result.checks.push('a failed revocation is reported with a controlled retry and keeps the local identity cleared');

  // 12) The controlled retry revokes the session and clears the notice.
  await page.unroute('**/api/v1/auth/logout');
  await retry.click();
  await page.waitForTimeout(1200);
  const noticeAfter = await page.getByText('上次退出的服务器会话尚未确认撤销').count();
  const cookiesAfterRetry = (await context.cookies()).filter((c) => c.name === '__Host-ops-session');
  result.revocation.noticeAfterRetry = noticeAfter;
  result.revocation.cookieAfterRetry = cookiesAfterRetry.length;
  if (noticeAfter !== 0) throw new Error('the retry did not clear the unconfirmed notice');
  if (cookiesAfterRetry.length !== 0) throw new Error('the retry did not remove the session cookie');
  const oldCookie = cookieAfterFailure;
  const replay = await context.request.get(origin + '/api/v1/auth/session', {
    headers: { Cookie: `${oldCookie.name}=${oldCookie.value}` },
  });
  if (replay.status() !== 401) throw new Error('the revoked cookie was still accepted');
  result.checks.push('the controlled retry revokes the server session and the old cookie is rejected');

  // 13) The entry rate limit answers 429 with a stable code. This runs last
  //     because the fence is entry wide and this instance is task owned.
  let limited = null;
  for (let attempt = 0; attempt < 70; attempt += 1) {
    const response = await context.request.post(origin + '/api/v1/auth/login', {
      data: { loginName: 'mfa.probe.lock', password: 'not-a-real-password' },
      headers: { 'Content-Type': 'application/json', 'Sec-Fetch-Site': 'same-origin' },
    });
    if (response.status() === 429) { limited = { status: 429, body: await response.json().catch(() => null) }; break; }
  }
  result.rateLimit = limited;
  if (!limited) throw new Error('the entry never answered 429 after repeated failures');
  if (!limited.body || !limited.body.code) throw new Error('the 429 answer carries no error code');
  result.checks.push('the entry rate limit answers 429 with an error code');

  result.status = 'PASS';
} catch (error) {
  result.status = 'FAIL';
  result.failure = error.message;
  process.exitCode = 1;
} finally {
  writeFileSync(root + '/logs/mfa-browser-result.json', JSON.stringify(result, null, 2));
  await browser.close();
  console.log(JSON.stringify({
    status: result.status, failure: result.failure, checks: result.checks,
    authorization: result.authorization, sessionBefore: result.sessionBefore,
    mfaStatusBefore: result.mfaStatusBefore, revocation: result.revocation,
    rateLimit: result.rateLimit && { status: result.rateLimit.status, code: result.rateLimit.body?.code },
  }, null, 2));
}
