/**
 * Purpose: ESLint configuration for clawde/apps/desktop (Tauri 2 + React + TypeScript).
 * Inputs:  TypeScript files in src/.
 * Outputs: ESLint diagnostics.
 * Constraints: ESLint 8 legacy config (the lint script passes --ext); .cjs because
 *              the package is "type": "module". Mirrors apps/mobile/.eslintrc.js.
 */
module.exports = {
  root: true,
  parser: '@typescript-eslint/parser',
  parserOptions: {
    ecmaVersion: 2021,
    sourceType: 'module',
    ecmaFeatures: { jsx: true },
  },
  plugins: ['@typescript-eslint', 'react', 'react-hooks'],
  extends: [
    'eslint:recommended',
    'plugin:@typescript-eslint/recommended',
    'plugin:react/recommended',
    'plugin:react-hooks/recommended',
  ],
  settings: {
    react: { version: 'detect' },
  },
  env: {
    browser: true,
    es2021: true,
    jest: true,
  },
  rules: {
    '@typescript-eslint/no-explicit-any': 'warn',
    // A leading underscore marks a deliberately unused binding (mock signatures, render props).
    '@typescript-eslint/no-unused-vars': ['error', { argsIgnorePattern: '^_', varsIgnorePattern: '^_' }],
    'react/react-in-jsx-scope': 'off',
  },
};
