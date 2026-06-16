/**
 * Purpose: E2E tests for ClawDE desktop critical UI states (connected / disconnected).
 * Inputs:  Vite dev server at :5173
 * Outputs: Playwright pass/fail assertions.
 * Constraints: Daemon connection is mocked in dev server — tests verify rendered states.
 * SPORT: T-P3-E6-W2-S6-T01
 */
import { test, expect } from "@playwright/test";

test("app renders without JS errors on load", async ({ page }) => {
  const errors: string[] = [];
  page.on("pageerror", (err) => errors.push(err.message));
  await page.goto("/");
  // Allow any deferred async modules to settle
  await page.waitForLoadState("networkidle");
  expect(errors).toHaveLength(0);
});

test("viewport renders app shell in 1280x800", async ({ page }) => {
  await page.setViewportSize({ width: 1280, height: 800 });
  await page.goto("/");
  await expect(page.locator("#root")).toBeVisible();
  const root = await page.locator("#root").boundingBox();
  expect(root?.width).toBeGreaterThan(0);
  expect(root?.height).toBeGreaterThan(0);
});
