/**
 * Purpose: ESLint configuration for clawde/apps/mobile (React Native + TypeScript).
 * Inputs:  TypeScript files in src/.
 * Outputs: ESLint diagnostics.
 * Constraints: Aligns with jest-expo 53 / RN 0.79.7 / React 19.
 * SPORT: T-P3-E4-W4-S9-T02 — clawde/apps/mobile Expo 53 upgrade
 */
module.exports = {
  root: true,
  parser: '@typescript-eslint/parser',
  parserOptions: {
    ecmaVersion: 2020,
    sourceType: 'module',
    ecmaFeatures: { jsx: true },
  },
  plugins: ['@typescript-eslint', 'react', 'react-native'],
  extends: [
    'eslint:recommended',
    'plugin:@typescript-eslint/recommended',
    'plugin:react/recommended',
  ],
  settings: {
    react: { version: 'detect' },
  },
  env: {
    jest: true,
    'react-native/react-native': true,
  },
  rules: {
    '@typescript-eslint/no-explicit-any': 'warn',
    'react/react-in-jsx-scope': 'off',
  },
};
