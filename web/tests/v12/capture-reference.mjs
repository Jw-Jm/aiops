// Reference-only renderer. The browser must run in the authorized remote environment.
import { chromium } from '@playwright/test';
import { readFile, mkdir, writeFile } from 'node:fs/promises';
import { resolve, join } from 'node:path';
import { parseArgs } from 'node:util';
import { createHash } from 'node:crypto';
import assert from 'node:assert/strict';

const { values } = parseArgs({ options: {
  cdp: { type: 'string' }, base: { type: 'string' },
  design: { type: 'string' }, output: { type: 'string' },
  slices: { type: 'string' },
} });
assert(values.cdp && values.base && values.design && values.output,
  '--cdp --base --design --output are required; use the remote browser');
const design = resolve(values.design);
const manifest = JSON.parse(await readFile(join(design, 'manifest.json'), 'utf8'));
const source = await readFile(join(design, manifest.source));
assert.equal(createHash('sha256').update(source).digest('hex'), manifest.sourceSHA256);
const selected = values.slices ? new Set(values.slices.split(',')) : undefined;
const slices = manifest.slices.filter(s => !selected || selected.has(s.id));
assert.equal(slices.length, selected?.size ?? 458, 'unknown, duplicate, or missing slice');
assert.equal(new Set(manifest.slices.map(s => s.id)).size, 458);
const output = resolve(values.output);
await mkdir(output, { recursive: false });
const browser = await chromium.connectOverCDP(values.cdp);
const context = await browser.newContext({
  viewport: { width: 1440, height: 960 }, deviceScaleFactor: 1,
  locale: 'zh-CN', timezoneId: 'Asia/Shanghai', colorScheme: 'light',
});
const report = {
  kind: 'design-reference-only', productAcceptance: false,
  sourceSHA256: manifest.sourceSHA256, capturedAt: new Date().toISOString(),
  browser: browser.version(), canvas: manifest.canvas,
  environment: { locale: 'zh-CN', timezone: 'Asia/Shanghai', deviceScaleFactor: 1 },
  masks: [], slices: [],
};
try {
  const page = await context.newPage();
  for (const slice of slices) {
    const errors = [];
    const onError = error => errors.push(error.message);
    page.on('pageerror', onError);
    try {
      const response = await page.goto(new URL(slice.path, values.base).href);
      assert.equal(response?.status(), 200);
      await page.waitForFunction(() => document.documentElement.dataset.sliceReady === 'true');
      await page.evaluate(() => document.fonts.ready);
      assert.equal(await page.locator('html').getAttribute('data-slice-surface'),
        await page.evaluate(() => {
          return document.activeElement?.closest('dialog[open]')?.id ||
            document.documentElement.dataset.sliceSurface;
        }));
      const env = await page.evaluate(() => ({
        userAgent: navigator.userAgent, platform: navigator.platform,
        font: getComputedStyle(document.body).fontFamily,
        deviceScaleFactor: devicePixelRatio, width: innerWidth, height: innerHeight,
      }));
      assert.match(env.userAgent, /Linux/, 'browser must run on the remote Linux development host');
      assert.equal(env.deviceScaleFactor, 1);
      assert.equal(env.width, 1440);
      assert.equal(env.height, 960);
      report.environment = { ...report.environment, ...env };
      const files = [];
      const dimensions = [];
      const capture = async suffix => {
        const name = `${slice.id}-${suffix}.png`;
        const fullPage = suffix === 'full';
        const png = await page.screenshot({ path: join(output, name), fullPage, animations: 'disabled' });
        const width = png.readUInt32BE(16), height = png.readUInt32BE(20);
        if (!fullPage) {
          assert.equal(width, 1440);
          assert.equal(height, 960);
        }
        files.push(name);
        dimensions.push({ file: name, width, height, fullPage });
      };
      await capture('viewport');
      if (await page.evaluate(() => document.documentElement.scrollHeight > innerHeight))
        await capture('full');
      // Inspect independently scrolling bodies without hiding any dynamic region.
      const scrollers = await page.locator('body *').evaluateAll(elements => elements
        .filter(e => e.scrollHeight > e.clientHeight + 4 && e.clientHeight > 0 &&
          ['auto', 'scroll'].includes(getComputedStyle(e).overflowY))
        .map(e => { e.dataset.referenceScroll = 'true'; return {
          tag: e.tagName, id: e.id, className: e.className,
          scrollHeight: e.scrollHeight, clientHeight: e.clientHeight,
        }; }));
      for (let index = 0; index < scrollers.length; index++) {
        const scroller = page.locator('[data-reference-scroll]').nth(index);
        const previous = await scroller.evaluate(e => {
          const previous = e.scrollTop;
          e.scrollTop = e.scrollHeight;
          return previous;
        });
        await capture(`scroll-bottom-${index}`);
        await scroller.evaluate((e, previous) => { e.scrollTop = previous; }, previous);
      }
      assert.deepEqual(errors, []);
      report.slices.push({ id: slice.id, route: slice.route, status: 'captured', files, dimensions, scrollers });
    } catch (error) {
      report.slices.push({ id: slice.id, status: 'failed', error: error.message, errors });
    } finally {
      page.off('pageerror', onError);
      await writeFile(join(output, 'capture.json'), JSON.stringify(report, null, 2) + '\n');
    }
    if (report.slices.length % 25 === 0)
      console.log(`${report.slices.length}/${slices.length} reference states processed`);
  }
} finally {
  await context.close();
  await browser.close();
}
const failed = report.slices.filter(s => s.status !== 'captured');
console.log(JSON.stringify({ kind: report.kind, captured: report.slices.length - failed.length,
  failed: failed.map(s => s.id), output }));
if (failed.length) process.exitCode = 1;
