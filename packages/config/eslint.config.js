// @aireos/config/eslint.config.js — shared flat ESLint config (ESLint 9).
//
// This file IS the v0.1.0 release-criteria SEC-7 enforcement mechanism.
// Every rule here closes a documented ADR-0047 verification gap OR a
// v0.1.0-release-criteria.md SEC-7 / SEC-8 sub-bullet. If you find
// yourself eslint-disabling a rule from this file, the disabling comment
// MUST cite which doctrine you're temporarily overriding and why.
//
// Authored as plain JS (not .ts) because ESLint config TS loading
// requires a transpiler hook (jiti); plain JS keeps the supply chain
// smaller and matches "smallest viable tooling kit" doctrine (ADR-0042).
// JSDoc types document the shape.

import js from "@eslint/js";
import tseslint from "typescript-eslint";
import reactPlugin from "eslint-plugin-react";
import reactHooks from "eslint-plugin-react-hooks";
import globals from "globals";

/** @type {import("eslint").Linter.Config[]} */
const config = [
  // Universal ignores. Generated code and build output are NOT linted —
  // generated OpenAPI types violate stylistic rules by design (codegen
  // produces what oapi-codegen / openapi-typescript emit), and dist
  // folders are not authored source.
  {
    ignores: [
      "**/dist/**",
      "**/build/**",
      "**/.vite/**",
      "**/node_modules/**",
      "**/src/generated/**",
      "**/__fixtures__/**",
      "tools/forbidden-deps.fixtures/**",
    ],
  },

  // Base recommended rules.
  js.configs.recommended,
  ...tseslint.configs.recommended,

  // TypeScript + React rules for apps/* and packages/*.
  {
    files: ["**/*.{ts,tsx}"],
    languageOptions: {
      parser: tseslint.parser,
      parserOptions: {
        ecmaVersion: "latest",
        sourceType: "module",
        ecmaFeatures: { jsx: true },
      },
      globals: {
        ...globals.browser,
        ...globals.node,
        ...globals.es2022,
      },
    },
    plugins: {
      react: reactPlugin,
      "react-hooks": reactHooks,
    },
    settings: {
      react: { version: "18" },
    },
    rules: {
      // === ADR-0047 SEC-7: PASETO must stay opaque to JS ===
      // Block direct fetch() and new Request() everywhere; the exception
      // for the createApiClient wrapper lives below as a scoped override.
      "no-restricted-syntax": [
        "error",
        {
          selector: "CallExpression[callee.name='fetch']",
          message:
            "Direct fetch() is forbidden. All API calls MUST go through createApiClient from @aireos/shared-types (ADR-0047 SEC-7) so they pick up the CSRF + 401 chokepoint. If you have a legitimate reason to bypass, file a follow-up ADR.",
        },
        {
          selector: "NewExpression[callee.name='Request']",
          message:
            "Direct `new Request()` is forbidden. The createApiClient wrapper constructs Requests; bypassing it skips CSRF auto-attach (ADR-0047 SEC-7).",
        },
      ],
      // === ADR-0047 SEC-7: tokens stay in HttpOnly cookies, never in storage ===
      "no-restricted-globals": [
        "error",
        {
          name: "localStorage",
          message:
            "localStorage is forbidden in apps/* and packages/ui/* — PASETO session tokens stay in HttpOnly cookies (ADR-0047 SEC-2 / SEC-7). For legitimate non-credential UI state, file a follow-up ADR and add a scoped override.",
        },
        {
          name: "sessionStorage",
          message:
            "sessionStorage is forbidden in apps/* and packages/ui/* — same reasoning as localStorage. PASETO tokens stay in HttpOnly cookies (ADR-0047 SEC-2 / SEC-7).",
        },
      ],
      // === ADR-0047 §Decision: XSS escape hatches forbidden ===
      // The cockpit renders operator-controllable content (logs, terminal
      // output, agent outputs); dangerouslySetInnerHTML is the XSS surface
      // we explicitly do not permit.
      "react/no-danger": "error",
      "react/no-danger-with-children": "error",
      // === React hooks correctness ===
      "react-hooks/rules-of-hooks": "error",
      "react-hooks/exhaustive-deps": "warn",
      // === TypeScript tightening ===
      // `any` is allowed but flagged; explicit acknowledgment via the rule
      // helps reviewers catch unintended type-erasure.
      "@typescript-eslint/no-explicit-any": "warn",
      // Unused imports/vars are warnings, not errors, so a contributor's
      // mid-development PR doesn't fail lint while they're iterating.
      "@typescript-eslint/no-unused-vars": [
        "warn",
        { argsIgnorePattern: "^_", varsIgnorePattern: "^_" },
      ],
      // === Style hygiene (low-noise) ===
      "no-console": ["warn", { allow: ["warn", "error"] }],
      "prefer-const": "warn",
      "no-var": "error",
    },
  },

  // === SEC-7 exception: the createApiClient wrapper IS the chokepoint. ===
  // Direct fetch() / new Request() are not only allowed inside the wrapper —
  // they're required for it to do its job. Same exception for the wrapper's
  // own test file.
  {
    files: [
      "packages/shared-types/src/client.ts",
      "packages/shared-types/src/client.test.ts",
    ],
    rules: {
      "no-restricted-syntax": "off",
    },
  },

  // === Test files: relaxed rules. ===
  // Tests legitimately need vi.fn<any>(), console.log for debugging,
  // and mock objects that don't satisfy real types.
  {
    files: ["**/*.test.{ts,tsx}", "**/*.spec.{ts,tsx}"],
    rules: {
      "@typescript-eslint/no-explicit-any": "off",
      "@typescript-eslint/no-unused-vars": "off",
      "no-console": "off",
    },
  },

  // === Node-context files (vite.config.ts, vitest.config.ts, scripts). ===
  // These run in Node, not the browser; localStorage doesn't exist here,
  // and process.env is legitimate.
  {
    files: [
      "**/vite.config.ts",
      "**/vitest.config.ts",
      "tools/**/*.{mjs,js,ts}",
    ],
    languageOptions: {
      globals: { ...globals.node },
    },
    rules: {
      "no-restricted-globals": "off",
      "no-console": "off",
    },
  },
];

export default config;
