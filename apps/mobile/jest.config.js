/**
 * Purpose: Jest configuration for jest-expo with TypeScript path alias support.
 * Inputs:  jest-expo preset; babel-plugin-module-resolver alias @/*.
 * Outputs: Jest test runner configuration.
 * Constraints: Must match babel.config.js alias mapping.
 * SPORT: T-E1-06 — React Native Expo migration
 */
module.exports = {
  preset: 'jest-expo',
  testEnvironment: 'node',
  moduleNameMapper: {
    '^@/(.*)$': '<rootDir>/src/$1',
  },
  transformIgnorePatterns: [
    'node_modules/(?!((jest-)?react-native|@react-native(-community)?)/|expo(nent)?|@expo(nent)?/.*|@expo-google-fonts/.*|react-navigation|@react-navigation/.*|@unimodules/.*|unimodules|sentry-expo|native-base|react-native-svg)',
  ],
  testPathPattern: 'src/__tests__',
};
