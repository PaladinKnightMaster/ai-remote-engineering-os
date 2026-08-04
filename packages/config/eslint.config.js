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
      // Block fetch / new Request and their member-expression bypasses.
      // T-012 review pass surfaced globalThis.fetch / window.fetch as a
      // real bypass vector — selectors below cover all known forms.
      "no-restricted-syntax": [
        "error",
        // Bare-call form: fetch(...) / new Request(...).
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
        // Bypass form: globalThis.fetch / window.fetch / self.fetch.
        {
          selector:
            "CallExpression[callee.type='MemberExpression'][callee.property.name='fetch'][callee.object.name=/^(globalThis|window|self)$/]",
          message:
            "globalThis.fetch / window.fetch / self.fetch is forbidden — same reason as bare fetch(). Use createApiClient (ADR-0047 SEC-7).",
        },
        {
          selector:
            "NewExpression[callee.type='MemberExpression'][callee.property.name='Request'][callee.object.name=/^(globalThis|window|self)$/]",
          message:
            "globalThis.Request / window.Request / self.Request is forbidden — same reason as bare `new Request()` (ADR-0047 SEC-7).",
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
      // Belt-and-suspenders: catch the window.localStorage / globalThis.localStorage
      // bypass forms. no-restricted-globals only catches bare references; member-
      // expression access slips past it.
      "no-restricted-properties": [
        "error",
        {
          object: "window",
          property: "localStorage",
          message: "window.localStorage is forbidden — same reasoning as bare localStorage (ADR-0047 SEC-7).",
        },
        {
          object: "window",
          property: "sessionStorage",
          message: "window.sessionStorage is forbidden — same reasoning as bare sessionStorage (ADR-0047 SEC-7).",
        },
        {
          object: "globalThis",
          property: "localStorage",
          message: "globalThis.localStorage is forbidden — same reasoning as bare localStorage (ADR-0047 SEC-7).",
        },
        {
          object: "globalThis",
          property: "sessionStorage",
          message: "globalThis.sessionStorage is forbidden — same reasoning as bare sessionStorage (ADR-0047 SEC-7).",
        },
      ],
      // === Orthogonal XSS escape hatches ===
      // dangerouslySetInnerHTML is the React-specific XSS surface.
      "react/no-danger": "error",
      "react/no-danger-with-children": "error",
      // <a target="_blank"> without rel="noopener noreferrer" is the
      // tab-nabbing vector. Standard React lint covers it.
      "react/jsx-no-target-blank": "error",
      // eval / new Function() are the JS-runtime XSS escape hatches.
      // Operator-controllable content (terminal output, agent outputs)
      // must never reach a code-evaluator.
      "no-eval": "error",
      "no-new-func": "error",
      "no-implied-eval": "error",
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

  // === Vendored third-party source (ADR-0068 §D7, FE-39, T-115a). ===
  // packages/ui/src/vendor/qrcodegen.ts is Nayuki's upstream QR encoder,
  // vendored byte-faithful (see its header for provenance) rather than
  // rewritten to this repo's style. It uses TS's namespace-merging feature
  // (upstream's own module shape, predating ESM), carries a top-level
  // `@ts-nocheck` (see the file header, adaptation note 2), reassigns some
  // `let`s upstream never converted to `const`, and has one upstream regex
  // with a redundant escape — every rule below is silenced ONLY for this one
  // file so upstream's exact source needn't be reformatted to satisfy house
  // lint rules it was never written against. (`**/vendor/qrcodegen.ts`, not a
  // full repo-root-relative path: ESLint's flat-config `files` globs resolve
  // against the *importing* package's own eslint.config.js — packages/ui's is
  // a one-line re-export of this shared file — so a root-relative pattern
  // silently never matches; verified via `eslint --print-config`.)
  {
    files: ["**/vendor/qrcodegen.ts"],
    rules: {
      "@typescript-eslint/no-namespace": "off",
      "@typescript-eslint/no-unused-vars": "off",
      "@typescript-eslint/no-explicit-any": "off",
      "@typescript-eslint/ban-ts-comment": "off",
      "prefer-const": "off",
      "no-useless-escape": "off",
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
