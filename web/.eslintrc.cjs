// ESLint configuration for the BeerMate Display Manager frontend.
//
// The rules that matter here are Chromium-97 safety and catching real mistakes,
// not stylistic noise (Prettier-style formatting is left to the editor).
module.exports = {
  root: true,
  env: { browser: true, es2020: true },
  parser: '@typescript-eslint/parser',
  parserOptions: { ecmaVersion: 2019, sourceType: 'module', ecmaFeatures: { jsx: true } },
  plugins: ['@typescript-eslint'],
  extends: ['eslint:recommended', 'plugin:@typescript-eslint/recommended'],
  ignorePatterns: ['dist', 'node_modules', '*.config.ts', 'src/test/setup.ts'],
  rules: {
    // Chromium 97 lacks these; banning them at lint time stops a silent
    // white-screen on the Jetson.
    'no-restricted-properties': [
      'error',
      { object: 'window', property: 'structuredClone', message: 'structuredClone is Chrome 98+; the target is Chromium 97.' },
      { object: 'globalThis', property: 'structuredClone', message: 'structuredClone is Chrome 98+; the target is Chromium 97.' },
    ],
    'no-restricted-globals': [
      'error',
      { name: 'structuredClone', message: 'structuredClone is Chrome 98+; the target is Chromium 97.' },
    ],
    '@typescript-eslint/no-unused-vars': ['warn', { argsIgnorePattern: '^_' }],
    '@typescript-eslint/no-explicit-any': 'off',
    'no-console': ['warn', { allow: ['warn', 'error'] }],
  },
};
