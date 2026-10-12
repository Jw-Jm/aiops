import { test as base, expect, chromium, type Page } from "@playwright/test";
import { readFile } from "node:fs/promises";
import type { CommandRiskAssessmentRequest } from "../../src/api/generated/models";
import { createHmac } from "node:crypto";
export interface LiveInput {
  baseURL: string;
  username: string;
  passwordFile: string;
  otpFile: string;
  incidentId: string;
  namespaceProfile: string;
  clusterProfile: string;
  readCommand: string;
  clusterCommand: string;
  modelJobId?: string;
  screenshotDirectory: string;
  roleIdentitiesFile?: string;
  roleGrantHelper?: string;
  riskRequest?: CommandRiskAssessmentRequest;
}
export async function input(): Promise<LiveInput> {
  const path = process.env.OPS_WEB_QA_INPUT;
  if (!path)
    throw Error(
      "Provide external QA input; credentials and environment IDs must remain outside the repository",
    );
  return JSON.parse(await readFile(path, "utf8"));
}
export async function passwordLogin(
  page: Page,
  config: LiveInput,
  otpRequired = false,
) {
  await page.locator("#password").waitFor();
  if (await page.locator("#username").count())
    await page.locator("#username").fill(config.username);
  const privateValues = JSON.parse(await readFile(config.passwordFile, "utf8"));
  await page.locator("#password").fill(privateValues.subjectPassword);
  await page.locator("#kc-login").click();
  if (otpRequired) {
    await page.locator("#otp").waitFor();
    const key = (await readFile(config.otpFile, "utf8")).trim();
    const counter = Buffer.alloc(8);
    counter.writeBigUInt64BE(BigInt(Math.floor(Date.now() / 30000)));
    const bytes = createHmac("sha1", Buffer.from(key, "utf8"))
      .update(counter)
      .digest();
    await page
      .locator("#otp")
      .fill(
        String(
          (bytes.readUInt32BE(bytes[19]! & 15) & 0x7fffffff) % 1000000,
        ).padStart(6, "0"),
      );
    await page.locator("#kc-login").click();
  }
  await page.waitForURL(
    (url) =>
      url.origin === new URL(config.baseURL).origin &&
      !url.pathname.startsWith("/oidc") &&
      !url.pathname.startsWith("/auth/"),
  );
}
export const test = base.extend<{ config: LiveInput }>({
  config: async ({}, use) => {
    await use(await input());
  },
  page: async ({ config }, use) => {
    const endpoint = process.env.OPS_WEB_QA_CDP;
    if (!endpoint)
      throw Error(
        "Use CA-trusted, sandboxed QA Chromium with explicit CDP endpoint",
      );
    const browser = await chromium.connectOverCDP(endpoint);
    const context = await browser.newContext({
      viewport: { width: 1440, height: 900 },
    });
    const page = await context.newPage();
    await page.goto(config.baseURL + "/overview");
    await page.getByRole("button", { name: "使用 OIDC 登录" }).click();
    await passwordLogin(page, config);
    await expect(
      page.getByText("运行总览", { exact: true }).last(),
    ).toBeVisible();
    try {
      await use(page);
    } finally {
      await context.close();
      await browser.close();
    }
  },
});
export { expect };
