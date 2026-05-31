import js from '@eslint/js'
import globals from 'globals'
import reactHooks from 'eslint-plugin-react-hooks'
import reactRefresh from 'eslint-plugin-react-refresh'
import tseslint from 'typescript-eslint'
import { defineConfig, globalIgnores } from 'eslint/config'

export default defineConfig([
  globalIgnores(['dist']),
  {
    files: ['**/*.{ts,tsx}'],
    extends: [
      js.configs.recommended,
      tseslint.configs.recommended,
      reactHooks.configs.flat.recommended,
      reactRefresh.configs.vite,
    ],
    languageOptions: {
      globals: globals.browser,
    },
    rules: {
      // React Compiler rules — too strict for this codebase's patterns.
      // Standard useEffect(() => setState(x), [dep]) is flagged incorrectly.
      'react-hooks/set-state-in-effect': 'off',
      'react-hooks/immutability': 'off',
      'react-hooks/preserve-manual-memoization': 'off',
      'react-hooks/refs': 'off',
      // Allow exporting hooks alongside components (standard Provider pattern)
      'react-refresh/only-export-components': 'off',
      // Allow underscore-prefixed unused vars (intentional destructure ignores)
      '@typescript-eslint/no-unused-vars': ['error', { argsIgnorePattern: '^_', varsIgnorePattern: '^_' }],
    },
  },
  {
    files: ['src/test/**', 'tests/**', '**/*.test.{ts,tsx}'],
    rules: {
      // Tests can use any for mocks
      '@typescript-eslint/no-explicit-any': 'off',
    },
  },
])
