/**
 * Purpose: Playwright e2e config for ClawDE desktop critical paths.
 * Inputs:  tests/e2e/**\/*.spec.ts
 * Outputs: E2E test results against pnpm dev server at :5173.
 * Constraints: Tests run against the Vite SPA (no Tauri binary required);
 *              Tauri-specific APIs are mocked via __mocks__/tauri.ts.
 * SPORT: T-P3-E6-W2-S6-T01
 */
import { defineConfig, devices } from "@playwright/test";

export default defineConfig({
  testDir: "./tests/e2e",
  fullyParallel: false,
  forbidOnly: !!process.env.CI,
  retries: process.env.CI ? 2 : 0,
  workers: 1,
  reporter: process.env.CI ? "github" : "list",
  timeout: 30_000,
  use: {
    baseURL: "http://localhost:5173",
    trace: "on-first-retry",
    screenshot: "only-on-failure",
  },
  projects: [
    { name: "chromium", use: { ...devices["Desktop Chrome"] } },
  ],
  webServer: {
    command: "pnpm dev",
    url: "http://localhost:5173",
    timeout: 60_000,
    reuseExistingServer: !process.env.CI,
  },
});
