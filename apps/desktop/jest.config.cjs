/**
 * Purpose: Jest config for ClawDE desktop unit + component tests.
 * SPORT: T-E1-07
 */

/** @type {import('jest').Config} */
const config = {
  preset: "ts-jest",
  testEnvironment: "jsdom",
  setupFilesAfterEnv: ["<rootDir>/src/__tests__/setup.ts"],
  setupFiles: [],
  globals: {
    "ts-jest": {
      tsconfig: {
        jsx: "react-jsx",
        esModuleInterop: true,
      },
    },
  },
  moduleNameMapper: {
    "^@/(.*)$": "<rootDir>/src/$1",
    // Mock Tauri API in tests (no native bridge)
    "^@tauri-apps/(.*)$": "<rootDir>/src/__tests__/__mocks__/tauri.ts",
    // Mock @nself/* workspace packages (not built in test env)
    "^@nself/ui$": "<rootDir>/src/__tests__/__mocks__/nself-ui.ts",
    "^@nself/errors$": "<rootDir>/src/__tests__/__mocks__/nself-errors.ts",
    "\\.css$": "identity-obj-proxy",
  },
  testMatch: ["**/__tests__/**/*.test.{ts,tsx}", "**/*.test.{ts,tsx}"],
  collectCoverageFrom: ["src/**/*.{ts,tsx}", "!src/**/*.d.ts"],
};

module.exports = config;
