/**
 * Purpose: Jest configuration for jest-expo with TypeScript path alias support.
 * Inputs:  jest-expo preset; babel-plugin-module-resolver alias @/*.
 * Outputs: Jest test runner configuration.
 * Constraints: Must match babel.config.js alias mapping. Expo SDK 53 / RN 0.79.7.
 * SPORT: T-P3-E4-W4-S9-T02 — clawde/apps/mobile Expo 53 upgrade
 */
module.exports = {
  preset: 'jest-expo',
  testEnvironment: 'node',
  moduleNameMapper: {
    '^@/(.*)$': '<rootDir>/src/$1',
    // Resolve @nself/* workspace packages by source (not compiled dist)
    '^@nself/(.*)$': '<rootDir>/../../../packages/@nself/$1/src/index.ts',
  },
  transformIgnorePatterns: [
    'node_modules/(?!.*node_modules)(?!((jest-)?react-native|@react-native(-community)?)|expo(nent)?|@expo(nent)?/.*|@expo-google-fonts/.*|react-navigation|@react-navigation/.*|@unimodules/.*|unimodules|sentry-expo|native-base|react-native-svg)',
  ],
  testMatch: [
    '**/__tests__/**/*.[jt]s?(x)',
    '**/?(*.)+(spec|test).[jt]s?(x)',
  ],
};
