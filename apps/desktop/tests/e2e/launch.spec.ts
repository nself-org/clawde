/**
 * Purpose: E2E smoke tests — ClawDE desktop app launches and main shell is visible.
 * Inputs:  Vite dev server at :5173
 * Outputs: Playwright pass/fail assertions.
 * Constraints: Tauri APIs mocked by Vite plugin in dev; no Tauri binary required.
 * SPORT: T-P3-E6-W2-S6-T01
 */
import { test, expect } from "@playwright/test";

test("app loads within 5s", async ({ page }) => {
  const start = Date.now();
  await page.goto("/");
  // The app shell renders; check for the root element
  await expect(page.locator("#root")).toBeVisible({ timeout: 5000 });
  expect(Date.now() - start).toBeLessThan(5000);
});

test("document title contains ClawDE", async ({ page }) => {
  await page.goto("/");
  await expect(page).toHaveTitle(/ClawDE|clawde/i);
});
