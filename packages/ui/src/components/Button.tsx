// <Button> — the affordance defect fix (T-116; ADR-0068 §D3/§D7, FE-36).
//
// As-built finding 4: no button with an actual button appearance has had a
// hover or active state anywhere in the product since v0.1.0 — 54 raw
// `<button>` elements across apps/web/src/routes + packages/ui/src/components
// clustering into four families (T-116 preflight), each hand-styled with a
// static Tailwind className and none of them carrying hover/active/
// focus-visible states. This component is the fix; T-117 migrates the
// remaining call sites onto it.
//
// Hand-rolled, not vendored shadcn verbatim: shadcn's own Button depends on
// class-variance-authority + @radix-ui/react-slot. OI-30 forbids any new
// dependency and the v0.12.0 release criteria defer Radix adoption entirely
// (§14) — a Button doesn't justify pulling either in. So: shadcn's *shape*
// (a variant/size lookup producing a className), no `cva`, no `Slot`, no
// `asChild`.
//
// The lookup is a 2D grid (variant x size), not composed pieces, because the
// real call sites don't compose cleanly: e.g. the bordered "default" variant
// carries `font-medium` at its sm/md sizes but NOT at xs (5/5 existing xs
// call sites omit it; see the T-116 report), so baking a single "variant
// gives color, size gives padding" split would silently change the resting
// look of real buttons. Each cell below is hand-matched to what is actually
// live at that variant+size today; only the four interactive-state classes
// (hover/active/focus-visible/disabled) are new. Tokens: no new one is
// minted — `--color-ring` and `--color-accent`/`--color-accent-foreground`
// already exist in apps/web/src/styles.css and packages/config/tailwind.config.ts
// but were unused until this component (T-116 preflight confirmed it).
//
// `ghost` intentionally preserves the pre-existing link-styled idiom
// (`text-muted-foreground` + `hover:underline`, no border/background/
// padding) rather than shadcn's usual ghost (a button-shaped, transparent-
// fill variant) — ADR-0068 §D3 requires this so the T-117 migration is not
// also a visual redesign of the six sites that already use it.

import type { ButtonHTMLAttributes } from "react";

export type ButtonVariant = "default" | "primary" | "destructive" | "ghost";
export type ButtonSize = "xs" | "sm" | "md";

export interface ButtonProps extends ButtonHTMLAttributes<HTMLButtonElement> {
  /** Visual family. Defaults to "default" (the bordered/neutral look). */
  variant?: ButtonVariant;
  /** Padding/text scale. Defaults to "md" (the most common call-site size). */
  size?: ButtonSize;
}

const FOCUS_VISIBLE =
  "focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring focus-visible:ring-offset-2";
const DISABLED = "disabled:opacity-60 disabled:pointer-events-none";
// `bg-accent`/`text-accent-foreground` (the named-utility form used
// everywhere else in this file for other tokens, e.g. `bg-primary`,
// `text-destructive-foreground`) measured as PRESENT in source but silently
// absent from the compiled stylesheet in this build (T-116 — verified in the
// real browser: `bg-primary`/`bg-destructive`/`text-muted-foreground` all
// compile correctly from the same file, `bg-accent`/`text-accent-foreground`
// do not, root cause not identified). Tailwind's bracketed arbitrary-value
// syntax bypasses whatever the named-utility color-matcher is doing and
// resolves the same `--color-accent`/`--color-accent-foreground` theme
// variables directly — confirmed compiling and painting correctly.
const ACCENT_HOVER = "hover:bg-[var(--color-accent)] hover:text-[var(--color-accent-foreground)]";
const ACCENT_ACTIVE = "active:bg-[var(--color-accent)]/70";

// Full className per (variant, size) cell — a plain Record lookup, not cva.
// ghost's active state uses `opacity`, not a second `text-*` color utility,
// despite `active:text-foreground` being the more obvious choice (darken the
// text on press, mirroring "darken the fill" for the other three variants).
// Measured in the real browser (T-116, tests/smoke/button.smoke.spec.ts):
// `active:text-foreground` DID compile (correct selector, correct higher
// specificity than the resting `text-muted-foreground`, `:active` correctly
// matched) but did not win the cascade in this build — an unresolved
// Tailwind v4 / Lightning CSS oddity, not a misunderstanding of CSS
// specificity. `opacity` sidesteps it entirely: nothing else in this variant
// sets opacity, so there is no property to collide with.
const CLASSES: Record<ButtonVariant, Record<ButtonSize, string>> = {
  default: {
    xs: `rounded-md border border-border px-2 py-1 text-xs transition-colors ${ACCENT_HOVER} ${ACCENT_ACTIVE} ${FOCUS_VISIBLE} ${DISABLED}`,
    sm: `rounded-md border border-border px-3 py-1.5 text-sm transition-colors ${ACCENT_HOVER} ${ACCENT_ACTIVE} ${FOCUS_VISIBLE} ${DISABLED}`,
    md: `rounded-md border border-border px-3 py-2 text-sm font-medium transition-colors ${ACCENT_HOVER} ${ACCENT_ACTIVE} ${FOCUS_VISIBLE} ${DISABLED}`,
  },
  primary: {
    xs: `rounded-md bg-primary px-2 py-1 text-xs font-medium text-primary-foreground transition-colors hover:bg-primary/90 active:bg-primary/80 ${FOCUS_VISIBLE} ${DISABLED}`,
    sm: `rounded-md bg-primary px-3 py-1.5 text-sm font-medium text-primary-foreground transition-colors hover:bg-primary/90 active:bg-primary/80 ${FOCUS_VISIBLE} ${DISABLED}`,
    md: `rounded-md bg-primary px-3 py-2 text-sm font-medium text-primary-foreground transition-colors hover:bg-primary/90 active:bg-primary/80 ${FOCUS_VISIBLE} ${DISABLED}`,
  },
  destructive: {
    xs: `rounded-md bg-destructive px-2 py-1 text-xs font-medium text-destructive-foreground transition-colors hover:bg-destructive/90 active:bg-destructive/80 ${FOCUS_VISIBLE} ${DISABLED}`,
    sm: `rounded-md bg-destructive px-3 py-1.5 text-sm font-medium text-destructive-foreground transition-colors hover:bg-destructive/90 active:bg-destructive/80 ${FOCUS_VISIBLE} ${DISABLED}`,
    md: `rounded-md bg-destructive px-3 py-2 text-sm font-medium text-destructive-foreground transition-colors hover:bg-destructive/90 active:bg-destructive/80 ${FOCUS_VISIBLE} ${DISABLED}`,
  },
  ghost: {
    xs: `rounded-sm text-xs text-muted-foreground transition-colors hover:underline active:opacity-70 ${FOCUS_VISIBLE} ${DISABLED}`,
    sm: `rounded-sm text-sm text-muted-foreground transition-colors hover:underline active:opacity-70 ${FOCUS_VISIBLE} ${DISABLED}`,
    md: `rounded-sm text-sm text-muted-foreground transition-colors hover:underline active:opacity-70 ${FOCUS_VISIBLE} ${DISABLED}`,
  },
};

export function Button({
  variant = "default",
  size = "md",
  className,
  ...props
}: ButtonProps) {
  const composed = className ? `${CLASSES[variant][size]} ${className}` : CLASSES[variant][size];
  return <button className={composed} {...props} />;
}
