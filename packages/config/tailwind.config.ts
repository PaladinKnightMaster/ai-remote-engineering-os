// Shared Tailwind v4 config — ADR-0047, design-system-governance preflight.
//
// Status at T-007c: this file is the ADR-0047 §Decision-named location for
// the design-system Token Map. With the move to Tailwind v4, the *canonical*
// token declarations live in `apps/web/src/styles.css` via the `@theme`
// directive — v4 reads tokens from CSS first, not from a JS config. This file
// remains as a **typed mirror** of the same tokens so:
//
//   - tooling that wants a Tailwind-typed token map (e.g. design-system
//     governance audits, future apps/admin migrations, IDE preview) can
//     import it,
//   - the ADR-0047-named location still resolves on disk,
//   - any drift between this file and `apps/web/src/styles.css` shows up in
//     review (the values must match by construction).
//
// Token Map per ../agency-kit/ai/core/design-system-governance.md §2.
// Coverage at T-007c:
//
//   colors           — explicit semantic tokens below.
//   radius           — explicit semantic token below.
//   semantic states  — encoded via the color tokens (primary, destructive,
//                      muted, accent) and extended once shadcn variants are
//                      vendored with their feature tickets.
//   typography       — Tailwind v4 defaults; audited at T-007c review and
//   spacing            kept as defaults until a concrete shadcn component or
//   elevation          apps/admin palette demands an override (per
//   motion             search-before-create in design-system-governance §3).
//
// Style Binding rule: app/UI code references semantic tokens only (e.g.
// bg-background, text-foreground); raw hex/rgb values are a lint violation
// (T-012 enforces).
//
// No `content:` array (T-132, VER-13). This file used to carry a v3-style
// `content: [...]` naming three scan roots — but Tailwind v4 is CSS-first
// (see the comment block two paragraphs up: "this file remains as a typed
// mirror... token declarations live in apps/web/src/styles.css") and never
// reads a JS config's `content` field at all. Nothing in this repo's build
// (`@tailwindcss/vite` in apps/web/vite.config.ts) or test suite imported
// this file for its `content` value either — grepped at T-132, zero
// consumers. A stale `content` array sitting next to a comment that already
// calls this file "a typed mirror only" is the same failure class VER-12
// hunts in eslint.config.js: something that reads as authoritative and
// silently isn't. The real, enforced scan roots are apps/web/src/styles.css's
// automatic per-app detection plus its explicit `@source
// "../../../packages/ui/src"` directive (T-116) — both covered by a VER-13
// test in packages/config/config-roots.test.ts. Deleted rather than kept in
// sync-by-hand: v4's automatic content detection and v3's explicit `content`
// globs aren't the same mechanism (v4 needs no apps/web entry at all — it
// auto-scans its own directory), so a "mirror" test binding the two would
// itself assert a false equivalence instead of a real one.

import type { Config } from "tailwindcss";

const config: Config = {
  theme: {
    extend: {
      colors: {
        background: "hsl(0 0% 100%)",
        foreground: "hsl(222.2 84% 4.9%)",
        primary: {
          DEFAULT: "hsl(222.2 47.4% 11.2%)",
          foreground: "hsl(210 40% 98%)",
        },
        secondary: {
          DEFAULT: "hsl(210 40% 96.1%)",
          foreground: "hsl(222.2 47.4% 11.2%)",
        },
        muted: {
          DEFAULT: "hsl(210 40% 96.1%)",
          foreground: "hsl(215.4 16.3% 46.9%)",
        },
        accent: {
          DEFAULT: "hsl(210 40% 96.1%)",
          foreground: "hsl(222.2 47.4% 11.2%)",
        },
        destructive: {
          DEFAULT: "hsl(0 84.2% 60.2%)",
          foreground: "hsl(210 40% 98%)",
        },
        border: "hsl(214.3 31.8% 91.4%)",
        input: "hsl(214.3 31.8% 91.4%)",
        ring: "hsl(222.2 84% 4.9%)",
      },
      borderRadius: {
        lg: "0.5rem",
        md: "calc(0.5rem - 2px)",
        sm: "calc(0.5rem - 4px)",
      },
    },
  },
  plugins: [],
};

export default config;
