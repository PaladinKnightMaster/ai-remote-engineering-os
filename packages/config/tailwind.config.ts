// Shared Tailwind v4 config — ADR-0047, design-system-governance preflight.
//
// Token Map per ../agency-kit/ai/core/design-system-governance.md §2. The
// seven categories the preflight names and where each one lives at T-007a:
//
//   colors           — explicit semantic tokens below (CSS custom properties).
//   radius           — explicit semantic tokens below.
//   semantic states  — encoded in the color tokens (primary/destructive/etc.)
//                      and extended once shadcn variants are vendored (T-007c+).
//   typography       — Tailwind v4 defaults stand in until a concrete consumer
//   spacing            (apps/web shell, the first shadcn component) needs an
//   elevation          override. This is an explicit decision in the T-007a
//   motion             review: speculative tokens for un-built consumers would
//                      violate "search-before-create" in
//                      design-system-governance.md §3. When the first SPA
//                      route ships in T-007c, audit which of these four need
//                      semantic tokens and add them here in the same diff.
//
// Concrete shade values for the color tokens are bound to CSS custom
// properties (--background, etc.) defined in apps/web/src/styles.css when
// the SPA shell lands (T-007c). This file is the token *surface*.
//
// Style Binding rule: app/UI code references semantic tokens only (e.g.
// bg-background, text-foreground); raw hex/rgb values are a lint violation
// (T-012 enforces).
import type { Config } from "tailwindcss";

const config: Config = {
  content: [
    "../../apps/*/index.html",
    "../../apps/*/src/**/*.{ts,tsx}",
    "../../packages/ui/src/**/*.{ts,tsx}",
  ],
  theme: {
    extend: {
      colors: {
        background: "hsl(var(--background))",
        foreground: "hsl(var(--foreground))",
        primary: {
          DEFAULT: "hsl(var(--primary))",
          foreground: "hsl(var(--primary-foreground))",
        },
        secondary: {
          DEFAULT: "hsl(var(--secondary))",
          foreground: "hsl(var(--secondary-foreground))",
        },
        muted: {
          DEFAULT: "hsl(var(--muted))",
          foreground: "hsl(var(--muted-foreground))",
        },
        accent: {
          DEFAULT: "hsl(var(--accent))",
          foreground: "hsl(var(--accent-foreground))",
        },
        destructive: {
          DEFAULT: "hsl(var(--destructive))",
          foreground: "hsl(var(--destructive-foreground))",
        },
        border: "hsl(var(--border))",
        input: "hsl(var(--input))",
        ring: "hsl(var(--ring))",
      },
      borderRadius: {
        lg: "var(--radius)",
        md: "calc(var(--radius) - 2px)",
        sm: "calc(var(--radius) - 4px)",
      },
    },
  },
  plugins: [],
};

export default config;
