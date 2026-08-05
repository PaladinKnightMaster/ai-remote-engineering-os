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
  //
  // The pattern must contain ONLY segments that appear in the path ESLint
  // actually matches against. Flat-config `files` globs resolve against the
  // config file ESLint loads for the run, and every package's own
  // eslint.config.js is a one-line re-export of this shared array — so
  // `pnpm --filter @aireos/shared-types run lint` runs `eslint .` from
  // packages/shared-types/, where this file's relative path is plain
  // `src/client.ts`. A "packages/shared-types/src/client.ts" pattern therefore
  // never matches, and neither does "**/shared-types/src/client.ts" — the
  // `shared-types` segment is not in the relative path at all. (The vendored-
  // source block below works because `vendor/` IS in its relative path,
  // `src/vendor/qrcodegen.ts`.) `**/src/client.ts` matches under both
  // invocations: `**` absorbs zero segments per-package, and
  // `packages/shared-types` from the repo root.
  //
  // This block was dead from the day it was written — verified via
  // `eslint --print-config`, which reported no-restricted-syntax at severity 2
  // (still fully enforced) — and went unnoticed only because client.ts reaches
  // the network through openapi-fetch's createClient, which trips neither AST
  // selector. Re-verify with --print-config after touching these patterns; a
  // silently-dead exception looks identical to a working one.
  {
    files: ["**/src/client.ts", "**/src/client.test.ts"],
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

  // === ADR-0068 §D8 scoped override: the E-04 first-run checklist's
  // per-browser dismissal (ONB-1..2, T-118a). ===
  // The SEC-7 localStorage ban above already names its own escape hatch:
  // "For legitimate non-credential UI state, file a follow-up ADR and add a
  // scoped override." ADR-0068 §D8 (Accepted) IS that follow-up ADR — it
  // names localStorage, by name, as the mechanism for hiding the onboarding
  // panel without the server recording anything about onboarding (ONB-2).
  // This is presentation state, not a credential: no PASETO token, no
  // session secret, nothing SEC-7 was written to keep out of client storage
  // ever touches this key.
  //
  // Scoped to exactly the two files that use it — packages/ui and every
  // other apps/web route are untouched, so the SEC-7 ban still applies to
  // them in full. `**/components/...` (not a `apps/web/...`-rooted pattern)
  // for the same reason the `src/client.ts` block above uses `**/src/...`:
  // this shared array is re-exported as-is by apps/web's own
  // eslint.config.js, so the pattern must match the path relative to THAT
  // package's `eslint .` invocation (`src/components/OnboardingChecklist.tsx`),
  // not a repo-root-relative one.
  //
  // Narrowed to exactly what ONB-2 needs (Commander review): re-declare the
  // two rules rather than turn them fully off, so only `localStorage` is
  // carved out here — `sessionStorage` stays banned even in these two files.
  // ADR-0068 §D8 names localStorage specifically; sessionStorage was never
  // part of that grant, and leaving it off entirely would silently permit a
  // later addition to this same component to reach for session-scoped state
  // without re-litigating SEC-7. Verified with `eslint --print-config` (see
  // the T-118a implementation report) that `no-restricted-globals` still
  // flags `sessionStorage` here while `localStorage` passes.
  {
    files: ["**/components/OnboardingChecklist.tsx", "**/components/OnboardingChecklist.test.tsx"],
    rules: {
      "no-restricted-globals": [
        "error",
        {
          name: "sessionStorage",
          message:
            "sessionStorage is forbidden in apps/* and packages/ui/* — same reasoning as ADR-0047 SEC-7. Only localStorage is carved out here, for ONB-2's per-browser onboarding-panel dismissal (ADR-0068 §D8).",
        },
      ],
      "no-restricted-properties": [
        "error",
        {
          object: "window",
          property: "sessionStorage",
          message:
            "window.sessionStorage is forbidden — same reasoning as ADR-0047 SEC-7. Only localStorage is carved out here (ADR-0068 §D8).",
        },
        {
          object: "globalThis",
          property: "sessionStorage",
          message:
            "globalThis.sessionStorage is forbidden — same reasoning as ADR-0047 SEC-7. Only localStorage is carved out here (ADR-0068 §D8).",
        },
      ],
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
  //
  // NB: the `**/` prefix is not itself the cure — this pattern works because
  // `vendor/` really is a segment of the relative path ESLint matches against
  // (`src/vendor/qrcodegen.ts`). A pattern naming a segment that is NOT in the
  // relative path fails no matter how it is prefixed; see the SEC-7 block above,
  // where `**/shared-types/src/client.ts` was tried and still never matched.
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
