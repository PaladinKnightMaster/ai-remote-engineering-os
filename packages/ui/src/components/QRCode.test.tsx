// QRCode component contract — jsdom-level unit coverage of the wiring around
// the vendored encoder (../vendor/qrcodegen.ts). Per the project DoD this does
// NOT satisfy the browser-rendered-evidence requirement for apps/web/
// packages/ui rendering changes (FE-42) — tests/smoke/pairing.smoke.spec.ts
// (T-115a) is what does, by rendering the real <Pairing> route in a real
// Chromium. This file only proves the component renders without a canvas
// (jsdom has none) and that a known input produces a stable, non-empty
// module grid.

import { cleanup, render } from "@testing-library/react";
import { afterEach, describe, expect, it } from "vitest";
import { QRCode } from "./QRCode";
import { qrcodegen } from "../vendor/qrcodegen";

afterEach(cleanup);

const VALUE = "https://example.test/pair#11111111-1111-1111-1111-111111111111.abc123";

describe("QRCode", () => {
  it("renders an SVG with a non-empty set of dark-module rects, no canvas involved", () => {
    const { container } = render(<QRCode value={VALUE} />);
    const svg = container.querySelector("svg");
    expect(svg).toBeTruthy();
    expect(container.querySelector("canvas")).toBeNull();

    const rects = container.querySelectorAll("rect");
    // One background rect + one rect per dark module.
    expect(rects.length).toBeGreaterThan(1);
  });

  it("produces a stable module count for a known input (matches the vendored encoder directly)", () => {
    const expected = qrcodegen.QrCode.encodeText(VALUE, qrcodegen.QrCode.Ecc.MEDIUM);
    const { container } = render(<QRCode value={VALUE} />);
    const svg = container.querySelector("svg")!;
    const viewBox = svg.getAttribute("viewBox");
    // dimension = qr.size + 2*margin(default 4)
    expect(viewBox).toBe(`0 0 ${expected.size + 8} ${expected.size + 8}`);

    let darkCount = 0;
    for (let y = 0; y < expected.size; y++) {
      for (let x = 0; x < expected.size; x++) {
        if (expected.getModule(x, y)) darkCount++;
      }
    }
    // +1 for the white background rect.
    expect(container.querySelectorAll("rect").length).toBe(darkCount + 1);
  });

  it("gives the SVG a text alternative (role=img + aria-label) for the payload it encodes", () => {
    const { container } = render(<QRCode value={VALUE} label="Pairing link" />);
    const svg = container.querySelector("svg")!;
    expect(svg.getAttribute("role")).toBe("img");
    expect(svg.getAttribute("aria-label")).toBe("Pairing link");
  });

  it("defaults the accessible label to the raw encoded value when no label is given", () => {
    const { container } = render(<QRCode value={VALUE} />);
    const svg = container.querySelector("svg")!;
    expect(svg.getAttribute("aria-label")).toBe(VALUE);
  });
});
