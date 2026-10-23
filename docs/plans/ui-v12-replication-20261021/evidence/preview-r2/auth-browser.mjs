// Real browser acceptance for the P2-02/P2-04 preview. Runs on the authorized
// remote host. No cookies, CSRF tokens or passwords are written to evidence.
import { chromium } from '/data/ops-ui-v12-http-20261022-r1/preview-r1/playwright-core/index.mjs';
import { readFileSync, writeFileSync } from 'node:fs';

const origin = 'https://192.168.0.63:30463';
const root = '/data/ops-ui-v12-http-20261022-r1/preview-r2';
const adminPassword = readFileSync(root + '/private/administrator-current-password', 'utf8').trim();
const result = { origin, checks: [], requests: [], consoleErrors: [], failuresRetained: [] };
const browser = await chromium.launch({
  executablePath: '/opt/ops-assistant-browser-20261014/chrome-linux64/chrome',
  headless: true, args: ['--no-sandbox', '--disable-dev-shm-usage'],
});
const record = (path, status, requestId) => result.requests.push({ path, status, requestId });
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

  await page.goto(origin + '/login');
  await page.getByLabel('用户名', { exact: true }).waitFor();
  await page.evaluate(() => document.fonts.ready);
  await page.screenshot({ path: root + '/logs/login-state-empty.png' });
  result.checks.push('native login renders for an unauthenticated browser');

  await page.getByRole('button', { name: /^登\s*录$/ }).click();
  await page.getByText('请输入登录名').waitFor();
  await page.screenshot({ path: root + '/logs/login-state-validation.png' });
  result.checks.push('blank submit shows field validation without a request');
  if (result.requests.some((r) => r.path === '/api/v1/auth/login'))
    throw new Error('blank submit reached the server');

  await page.getByLabel('用户名', { exact: true }).fill('v12.probe.unknown');
  await page.getByLabel('密码', { exact: true }).fill('not-a-real-password');
  const rejected = page.waitForResponse((r) => r.url().endsWith('/api/v1/auth/login'));
  await page.getByRole('button', { name: /^登\s*录$/ }).click();
  const rejectedStatus = (await rejected).status();
  await page.getByRole('alert').first().waitFor();
  await page.screenshot({ path: root + '/logs/login-state-credentials.png' });
  result.credentialsFailures = { status: rejectedStatus, alert: await page.getByRole('alert').first().innerText() };
  if (rejectedStatus !== 401) throw new Error('unknown login must be rejected with 401');
  result.checks.push('unknown login is rejected with a constant 401 and inline error');

  // The only slow request is the login POST; the delay makes the real loading
  // state visible without replacing the request or the response.
  await page.route('**/api/v1/auth/login', async (route) => {
    await new Promise((resolve) => setTimeout(resolve, 900));
    await route.continue();
  });
  await page.getByLabel('用户名', { exact: true }).fill('v12.admin');
  await page.getByLabel('密码', { exact: true }).fill(adminPassword);
  await page.getByRole('button', { name: /^登\s*录$/ }).click();
  await page.waitForTimeout(300);
  const submitting = await page
    .getByRole('button', { name: /登\s*录/ })
    .evaluate((button) => ({ disabled: button.disabled, busy: button.getAttribute('aria-busy') }));
  await page.screenshot({ path: root + '/logs/login-state-loading.png' });
  result.loading = submitting;
  if (submitting.busy !== 'true') throw new Error('loading state did not expose aria-busy');
  await page.waitForURL('**/access/sources');
  await page.unroute('**/api/v1/auth/login');
  result.checks.push('submit exposes an explicit busy state and completes into the administrator shell');

  const session = await page.evaluate(async () => {
    const response = await fetch('/api/v1/auth/session', { cache: 'no-store' });
    return { status: response.status, body: await response.json() };
  });
  if (session.status !== 200) throw new Error('session is not readable after login');
  result.session = {
    subject: session.body.data.subject,
    mode: session.body.data.mode,
    acr: session.body.data.acr,
    roles: session.body.data.authorization.grants.map((g) => g.role),
    revocation: session.body.data.revocation,
  };

  const clusters = await page.evaluate(async () => {
    const response = await fetch('/api/v1/admin/clusters', { cache: 'no-store' });
    return { status: response.status, body: await response.json().catch(() => null) };
  });
  result.adminClusters = {
    status: clusters.status,
    count: Array.isArray(clusters.body?.data) ? clusters.body.data.length : null,
    code: clusters.body?.code ?? null,
  };

  const overview = await page.evaluate(async () => {
    const response = await fetch('/api/v1/overview?clusterUid=ungranted-preview-cluster', { cache: 'no-store' });
    return { status: response.status, body: await response.json().catch(() => null) };
  });
  result.operatorOverview = { status: overview.status, code: overview.body?.code ?? null };

  const bindings = await page.evaluate(async () => {
    const response = await fetch('/api/v1/admin/role-bindings', { cache: 'no-store' });
    return { status: response.status, body: await response.json().catch(() => null) };
  });
  result.legacyBindings = {
    status: bindings.status,
    count: Array.isArray(bindings.body?.data) ? bindings.body.data.length : null,
  };

  await page.screenshot({ path: root + '/logs/admin-shell-r2.png' });
  await page.reload();
  await page.getByRole('button', { name: '退出', exact: true }).waitFor();
  const reloadSession = await page.evaluate(async () =>
    (await fetch('/api/v1/auth/session', { cache: 'no-store' })).status);
  if (reloadSession !== 200) throw new Error('reload did not restore the session');
  result.checks.push('reload restores the durable native session');

  const oldCookie = (await context.cookies()).find((c) => c.name === '__Host-ops-session');
  const logout = page.waitForResponse(
    (r) => r.url().endsWith('/api/v1/auth/logout') && r.request().method() === 'POST');
  await page.getByRole('button', { name: '退出', exact: true }).click();
  const logoutStatus = (await logout).status();
  await page.waitForURL('**/login');
  await page.getByLabel('用户名', { exact: true }).waitFor();
  if (logoutStatus !== 200) throw new Error('logout did not revoke the session');
  if ((await context.cookies()).some((c) => c.name === '__Host-ops-session'))
    throw new Error('logout left the session cookie behind');
  const replay = await context.request.get(origin + '/api/v1/auth/session', {
    headers: { Cookie: `${oldCookie.name}=${oldCookie.value}` },
  });
  if (replay.status() !== 401) throw new Error('revoked cookie was still accepted');
  result.checks.push('logout revokes server-side state and the old cookie is rejected');
  result.status = 'PASS';
} catch (error) {
  result.status = 'FAIL';
  result.failure = error.message;
  process.exitCode = 1;
} finally {
  writeFileSync(root + '/logs/auth-browser-result.json', JSON.stringify(result, null, 2));
  await browser.close();
  console.log(JSON.stringify({
    status: result.status, failure: result.failure, checks: result.checks,
    adminClusters: result.adminClusters, operatorOverview: result.operatorOverview,
    legacyBindings: result.legacyBindings, loading: result.loading,
  }, null, 2));
}
