// <Button> tests (T-116). jsdom cannot prove a hover/active state actually
// paints (see the browser-rendered evidence at
// tests/smoke/button.smoke.spec.ts, FE-42) — this file covers what jsdom
// CAN prove: rendering, the variant/size lookup producing distinct classes,
// disabled semantics, className composition, and click wiring.

import { cleanup, fireEvent, render, screen } from "@testing-library/react";
import { afterEach, describe, expect, it, vi } from "vitest";

import { Button } from "./Button";

afterEach(cleanup);

describe("Button", () => {
  it("renders its children as a real <button>", () => {
    render(<Button>Click me</Button>);
    const btn = screen.getByRole("button", { name: "Click me" });
    expect(btn).toBeTruthy();
    expect(btn.tagName).toBe("BUTTON");
  });

  it("defaults to variant=default size=md", () => {
    render(<Button>Default</Button>);
    const btn = screen.getByRole("button", { name: "Default" });
    expect(btn.className).toContain("border-border");
    expect(btn.className).toContain("px-3 py-2 text-sm");
  });

  it("each variant produces a distinct resting className", () => {
    render(
      <>
        <Button variant="default">Default</Button>
        <Button variant="primary">Primary</Button>
        <Button variant="destructive">Destructive</Button>
        <Button variant="ghost">Ghost</Button>
      </>,
    );
    const classes = ["Default", "Primary", "Destructive", "Ghost"].map(
      (name) => screen.getByRole("button", { name }).className,
    );
    expect(new Set(classes).size).toBe(4);
    expect(screen.getByRole("button", { name: "Primary" }).className).toContain("bg-primary");
    expect(screen.getByRole("button", { name: "Destructive" }).className).toContain("bg-destructive");
    // ghost preserves the pre-existing link idiom: muted text, no fill/border.
    const ghost = screen.getByRole("button", { name: "Ghost" }).className;
    expect(ghost).toContain("text-muted-foreground");
    expect(ghost).toContain("hover:underline");
    expect(ghost).not.toContain("text-primary");
    expect(ghost).not.toContain("bg-primary");
  });

  it("each size produces a distinct className for the same variant", () => {
    render(
      <>
        <Button size="xs">XS</Button>
        <Button size="sm">SM</Button>
        <Button size="md">MD</Button>
      </>,
    );
    const xs = screen.getByRole("button", { name: "XS" }).className;
    const sm = screen.getByRole("button", { name: "SM" }).className;
    const md = screen.getByRole("button", { name: "MD" }).className;
    expect(new Set([xs, sm, md]).size).toBe(3);
    expect(xs).toContain("text-xs");
    expect(sm).toContain("py-1.5");
    expect(md).toContain("py-2");
  });

  it("sets the disabled attribute and the disabled state classes", () => {
    render(<Button disabled>Disabled</Button>);
    const btn = screen.getByRole("button", { name: "Disabled" }) as HTMLButtonElement;
    expect(btn.disabled).toBe(true);
    expect(btn.className).toContain("disabled:opacity-60");
    expect(btn.className).toContain("disabled:pointer-events-none");
  });

  it("composes a consumer className rather than replacing the variant/size classes", () => {
    render(<Button className="w-full">Wide</Button>);
    const btn = screen.getByRole("button", { name: "Wide" });
    expect(btn.className).toContain("w-full");
    expect(btn.className).toContain("border-border");
  });

  it("fires onClick when enabled, and not when disabled", () => {
    const onClick = vi.fn();
    render(
      <>
        <Button onClick={onClick}>Enabled</Button>
        <Button onClick={onClick} disabled>
          Disabled
        </Button>
      </>,
    );
    fireEvent.click(screen.getByRole("button", { name: "Enabled" }));
    expect(onClick).toHaveBeenCalledOnce();
    fireEvent.click(screen.getByRole("button", { name: "Disabled" }));
    expect(onClick).toHaveBeenCalledOnce();
  });

  it("defaults type to undefined, letting a consumer opt into submit/button explicitly", () => {
    render(<Button type="submit">Submit</Button>);
    expect((screen.getByRole("button", { name: "Submit" }) as HTMLButtonElement).type).toBe("submit");
    render(<Button>No type</Button>);
    // Native <button> with no explicit type defaults to "submit" per the
    // HTML spec — Button doesn't override that, it passes props straight
    // through (ButtonHTMLAttributes), so callers control it explicitly.
    expect((screen.getByRole("button", { name: "No type" }) as HTMLButtonElement).type).toBe("submit");
  });
});
