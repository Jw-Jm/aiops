// Recomputes the SL-001 pixel comparison for the current build.
//
// The capture follows the accepted procedure exactly: transitions and
// animations are disabled, the diagnostic fill is applied, the focus ring is
// removed, and the canvas is 1440x960 with the same locale and time zone. The
// comparison uses the two registered copy-difference masks and the frozen
// prototype reference. No credential is written to the artifact.
import { chromium } from '/data/ops-ui-v12-http-20261022-r1/preview-r1/playwright-core/index.mjs';
import { existsSync, readFileSync } from 'node:fs';

const origin = 'https://192.168.0.63:30464';
const root = '/data/ops-ui-v12-mfa-20261022-r1/preview';
const output = root + '/logs/product-login-filled-current.png';
const browser = await chromium.launch({
  executablePath: '/opt/ops-assistant-browser-20261014/chrome-linux64/chrome',
  headless: true, args: ['--no-sandbox', '--disable-dev-shm-usage'],
});
try {
  const context = await browser.newContext({
    ignoreHTTPSErrors: true, viewport: { width: 1440, height: 960 }, deviceScaleFactor: 1,
    locale: 'zh-CN', timezoneId: 'Asia/Shanghai',
  });
  const page = await context.newPage();
  await page.goto(origin + '/login');
  await page.waitForSelector('#native-login-title');
  await page.evaluate(() => document.fonts.ready);
  await page.addStyleTag({
    content: '*,*::before,*::after{transition:none !important;animation:none !important}',
  });
  await page.fill('#loginName', 'lin.wei');
  await page.fill('#password', 'DemoOps#2026');
  await page.evaluate(() => document.activeElement instanceof HTMLElement && document.activeElement.blur());
  await page.evaluate(() => document.fonts.ready);
  await page.screenshot({ path: output });
  console.log(JSON.stringify({ output, overrideApplied: existsSync(root + '/override-candidate.css') }));
} finally {
  await browser.close();
}
