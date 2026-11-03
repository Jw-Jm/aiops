import { chromium } from '/data/ops-ui-v12-http-20261022-r1/preview-r1/playwright-core/index.mjs';
import { readFileSync, writeFileSync } from 'node:fs';
import { randomUUID } from 'node:crypto';

const root = '/data/ops-v12-completion-20261026-r1/evidence';
const browser = await chromium.launch({ executablePath: '/opt/ops-assistant-browser-20261014/chrome-linux64/chrome', headless: true, args: ['--no-sandbox', '--disable-dev-shm-usage'] });
const context = await browser.newContext({ ignoreHTTPSErrors: true, viewport: { width: 1440, height: 960 }, locale: 'zh-CN', timezoneId: 'Asia/Shanghai' });
const page = await context.newPage();
const result = { origin: 'https://192.168.0.63:30470', checks: [], errors: [] };
page.on('pageerror', error => result.errors.push(error.message));
try {
  await page.goto(result.origin + '/login');
  await page.getByRole('button', { name: /^登\s*录/ }).waitFor();
  await page.getByLabel('用户名', { exact: true }).fill('lin.wei');
  // Public prototype text is only used for matching the existing screenshot.
  await page.getByLabel('密码', { exact: true }).fill('DemoOps#2026');
  await page.getByRole('heading', { name: '登录智能运维平台' }).click();
  await page.screenshot({ path: root + '/login-current.png' });
  result.geometry = await page.locator('.login-card').evaluate(el => {
    const rect = node => { const r = node.getBoundingClientRect(); return { x: r.x, y: r.y, width: r.width, height: r.height }; };
    return { card: rect(el), title: rect(el.querySelector('h2')), form: rect(el.querySelector('form')), inputs: [...el.querySelectorAll('input')].map(rect), button: rect(el.querySelector('button[type=submit]')) };
  });
  await page.getByLabel('用户名', { exact: true }).fill('r23-absent-' + randomUUID());
  await page.getByLabel('密码', { exact: true }).fill(randomUUID());
  let release, arrived;
  const gate = new Promise(resolve => { release = resolve; });
  const intercepted = new Promise(resolve => { arrived = resolve; });
  await context.route('**/api/v1/auth/login', async route => { arrived(); await gate; await route.continue(); });
  const response = page.waitForResponse(r => new URL(r.url()).pathname === '/api/v1/auth/login').catch(() => null);
  await page.getByRole('button', { name: /^登\s*录/ }).click();
  try {
    await intercepted;
    if (!(await page.locator('button[type=submit]').isDisabled())) throw new Error('login submit remains enabled');
    await page.screenshot({ path: root + '/login-pending.png' });
    result.checks.push('pending real request disables duplicate submit');
  } finally { release(); }
  if ((await response)?.status() !== 401) throw new Error('unknown account must be rejected');
  await page.getByText('登录名或密码不正确。', { exact: true }).waitFor();
  await page.screenshot({ path: root + '/login-error.png' });
  result.checks.push('real API returns401; error remains on login');
  // Authenticate only against the new database copy. The source identity file
  // and every existing preview account remain unchanged.
  const fixture = JSON.parse(readFileSync('/data/ops-ui-v12-mfa-20261022-r1/gpt-dialog-r14/preview/private/browser-created-account.json', 'utf8'));
  await page.getByLabel('用户名', { exact: true }).fill(fixture.loginName);
  await page.getByLabel('密码', { exact: true }).fill(fixture.password);
  const loggedIn = page.waitForResponse(r => new URL(r.url()).pathname === '/api/v1/auth/login');
  await page.getByRole('button', { name: /^登\s*录/ }).click();
  if ((await loggedIn).status() !== 201) throw new Error('isolated copied account login rejected');
  await page.waitForURL(url => !['/login', '/auth/first-password'].includes(url.pathname));
  const clusters = await page.evaluate(async () => (await fetch('/api/v1/admin/clusters?limit=100')).status);
  if (clusters !== 200) throw new Error('native administrator cluster listing status=' + clusters);
  result.checks.push('real copied dual-role account login201; native administrator cluster list200');
  const loggedOut = page.waitForResponse(r => new URL(r.url()).pathname === '/api/v1/auth/logout');
  await page.getByRole('button', { name: /^退\s*出$/ }).click();
  if ((await loggedOut).status() !== 200) throw new Error('logout did not revoke session');
  await page.waitForURL('**/login');
  await page.reload();
  await page.getByLabel('用户名', { exact: true }).waitFor();
  const sessionStatus = await page.evaluate(async () => (await fetch('/api/v1/auth/session')).status);
  if (sessionStatus !== 401) throw new Error('logged-out session restored');
  result.checks.push('logout200; reload remains login; session401');
  if (result.errors.length) throw new Error('browser runtime errors');
  result.status = 'passed';
} catch (error) {
  result.status = 'failed';
  result.failure = error.message;
  process.exitCode = 1;
} finally {
  writeFileSync(root + '/login-browser.json', JSON.stringify(result, null, 2));
  await browser.close();
  console.log(JSON.stringify(result));
}
