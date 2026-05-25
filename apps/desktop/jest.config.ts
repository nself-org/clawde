/**
 * Purpose: Jest config for ClawDE desktop unit + component tests.
 * SPORT: T-E1-07
 */
import type { Config } from "jest";

const config: Config = {
  preset: "ts-jest",
  testEnvironment: "jsdom",
  setupFilesAfterFramework: [],
  setupFilesAfterFramework: [],
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
    "\\.css$": "identity-obj-proxy",
  },
  testMatch: ["**/__tests__/**/*.test.{ts,tsx}", "**/*.test.{ts,tsx}"],
  collectCoverageFrom: ["src/**/*.{ts,tsx}", "!src/**/*.d.ts"],
};

export default config;
