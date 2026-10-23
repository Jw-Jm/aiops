// Run with the existing remote Chrome; no trace, cookies or response bodies saved.
import { chromium } from './playwright-core/index.mjs';
import { readFileSync, writeFileSync, existsSync } from 'node:fs';
import { randomBytes } from 'node:crypto';
import assert from 'node:assert/strict';
const root = '/data/ops-ui-v12-http-20261022-r1/preview-r1';
const origin = 'https://192.168.0.63:30462';
const result = { origin, checks: [], requests: [], consoleErrors: [] };
const browser = await chromium.launch({
  executablePath: '/opt/ops-assistant-browser-20261014/chrome-linux64/chrome',
  headless: true, args: ['--no-sandbox', '--disable-dev-shm-usage'],
});
try {
  result.browser = browser.version();
  const context = await browser.newContext({ ignoreHTTPSErrors: true,
    viewport: { width: 1440, height: 960 }, deviceScaleFactor: 1,
    locale: 'zh-CN', timezoneId: 'Asia/Shanghai' });
  const page = await context.newPage();
  page.on('pageerror', e => result.consoleErrors.push(e.message));
  page.on('response', response => {
    if (response.url().includes('/api/')) result.requests.push({
      path: new URL(response.url()).pathname, status: response.status(),
      requestId: response.headers()['x-request-id'],
    });
  });
  await page.goto(origin + '/login');
  await page.getByLabel('用户名', {exact: true}).waitFor();
  await page.screenshot({ path: root + '/logs/login-before.png' });
  result.checks.push('native login renders without OIDC');
  await page.getByLabel('用户名', {exact: true}).fill('v12.admin');
  const resumed = existsSync(root + '/private/administrator-current-password');
  let password = readFileSync(root + '/private/' + (resumed ? 'administrator-current-password' : 'administrator-password'), 'utf8');
  await page.getByLabel('密码', {exact: true}).fill(password);
  await page.getByRole('button', { name: /^登\s*录$/ }).click();
  await page.waitForURL(resumed ? '**/access/sources' : '**/auth/first-password');
  const firstSession = await page.evaluate(async () => (await fetch('/api/v1/auth/session')).json());
  if (!resumed) {
  await page.getByLabel('当前密码', {exact: true}).waitFor();
  await page.screenshot({ path: root + '/logs/first-password.png' });
  const cookies = await context.cookies();
  const sessionCookie = cookies.find(c => c.name === '__Host-ops-session');
  assert(sessionCookie?.httpOnly && sessionCookie.secure && sessionCookie.sameSite === 'Strict');
  assert.equal(firstSession.data.mode, 'first_password_only');
  assert.deepEqual(firstSession.data.authorizedClusters, []);
  result.checks.push('real cookie enforces Secure/HttpOnly/SameSite; first-only session');
  const firstForbidden = await context.request.get(origin + '/api/v1/overview?clusterUid=ungranted-preview-cluster');
  assert.equal(firstForbidden.status(), 403);
  password = randomBytes(24).toString('base64url');
  writeFileSync(root + '/private/administrator-current-password', password, {flag: 'wx', mode: 0o600});
  await page.getByLabel('当前密码', {exact: true}).fill(readFileSync(root + '/private/administrator-password', 'utf8'));
  await page.getByLabel('新密码', {exact: true}).fill(password);
  await page.getByLabel('确认新密码', {exact: true}).fill(password);
  await page.getByRole('button', { name: '设置密码并继续', exact: true }).click();
  await page.waitForURL('**/access/sources');
  await page.getByRole('button', {name:/退\s*出/}).waitFor();
  result.checks.push('first password commits and enters administrator shell');
  } else { result.checks.push('resumed with previously rotated password; first-password evidence retained separately'); }
  await page.screenshot({path: root + '/logs/admin-shell.png'});
  await page.reload();
  await page.getByRole('button', {name:/退\s*出/}).waitFor();
  const current = await page.evaluate(async () => (await fetch('/api/v1/auth/session')).json());
  assert.equal(current.data.mode, 'full');
  assert.equal(current.data.subject, firstSession.data.subject);
  assert(resumed ? current.data.revocation.credentialVersion === firstSession.data.revocation.credentialVersion : current.data.revocation.credentialVersion > firstSession.data.revocation.credentialVersion);
  result.subject = current.data.subject;
  result.checks.push('reload restores durable session and rotated credential version');
  const overview = await context.request.get(origin + '/api/v1/overview?clusterUid=ungranted-preview-cluster');
  assert.equal(overview.status(), 403);
  result.checks.push('administrator does not receive operator permission');
  const rejected = await context.request.post(origin + '/api/v1/auth/logout', {
    headers: { Origin: 'https://foreign.invalid' }, data: {expectedSessionVersion: current.data.revocation.sessionVersion},
  });
  assert.equal(rejected.status(), 403);
  result.checks.push('cross-origin/missing-CSRF logout rejected');
  const oldCookie = (await context.cookies()).find(c => c.name === '__Host-ops-session');
  const logoutResponse = page.waitForResponse(r => r.url().endsWith('/api/v1/auth/logout') && r.request().method() === 'POST');
  await page.getByRole('button', {name:/退\s*出/}).click();
  assert.equal((await logoutResponse).status(), 200);
  await page.waitForURL('**/login');
  await page.getByLabel('用户名', {exact: true}).waitFor();
  assert(!(await context.cookies()).some(c => c.name === '__Host-ops-session'));
  const replay = await context.request.get(origin + '/api/v1/auth/session', {
    headers: { Cookie: `${oldCookie.name}=${oldCookie.value}` },
  });
  assert.equal(replay.status(), 401);
  result.checks.push('logout deletes browser cookie and rejects replay of revoked cookie');
  await page.getByLabel('用户名', {exact: true}).fill('v12.admin');
  await page.getByLabel('密码', {exact: true}).fill(password);
  await page.getByRole('button', {name:/^登\s*录$/}).click();
  await page.waitForURL('**/access/sources');
  await page.getByRole('button', {name:/退\s*出/}).waitFor();
  const finalLogout = page.waitForResponse(r => r.url().endsWith('/api/v1/auth/logout') && r.request().method() === 'POST');
  await page.getByRole('button', {name:/退\s*出/}).click();
  assert.equal((await finalLogout).status(), 200);
  await page.waitForURL('**/login');
  result.checks.push('new password login succeeds after first-password rotation');
  assert.deepEqual(result.consoleErrors, []);
  result.status = 'PASS';
} catch (e) {
  result.status = 'FAIL'; result.failure = e.message;
  process.exitCode = 1;
} finally {
  writeFileSync(root + '/logs/browser-result.json', JSON.stringify(result, null, 2));
  await browser.close();
  console.log(JSON.stringify({status: result.status, checks: result.checks, failure: result.failure}));
}
