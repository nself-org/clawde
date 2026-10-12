/**
 * Purpose: Jest config for ClawDE desktop unit + component tests.
 * SPORT: T-E1-07
 */

const path = require("path");

// @nself/* are workspace links, so a bare "react" import inside them resolves
// to their own devDependency React (18). Pin every React import to this app's
// copy so hooks run against one React.
const reactDir = path.dirname(require.resolve("react/package.json"));
const reactDomDir = path.dirname(require.resolve("react-dom/package.json"));

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
    "^react$": reactDir,
    "^react/(.*)$": `${reactDir}/$1`,
    "^react-dom$": reactDomDir,
    "^react-dom/(.*)$": `${reactDomDir}/$1`,
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
