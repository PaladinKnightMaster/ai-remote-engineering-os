// <QRCode> — renders a QR code as inline SVG from the vendored Nayuki encoder
// (../vendor/qrcodegen.ts; ADR-0068 §D7, FE-39). Landed at T-115a with a
// concrete consumer (the desk pairing surface, apps/web's <Pairing> route) —
// per design-system-governance §3's "search-before-create" discipline, a
// vendored/hand-rolled component only belongs in this barrel once something
// actually renders it, which this ticket supplies.
//
// SVG, not <canvas>: jsdom has no canvas (see the pre-existing
// HTMLCanvasElement.getContext noise around the xterm tests), so an SVG is
// both assertable in a jsdom test and crisp at any physical size for a phone
// camera. Each dark module is one <rect> — simple, and directly countable in
// a test (`container.querySelectorAll("rect")`).
//
// Accessibility: the QR is a visual encoding of data the operator cannot read
// by eye, so the <svg> carries role="img" + aria-label as its text
// alternative. The encoded payload should ALSO be shown as separate,
// selectable text by the consumer for a device that cannot scan (e.g. the
// pairing page renders the same URL in a <code> element) — this component
// only owns the graphic.

import { useMemo } from "react";
import { qrcodegen } from "../vendor/qrcodegen";

export interface QRCodeProps {
  /** The text/URL to encode. */
  value: string;
  /** Rendered pixel size (square). Defaults to 240. */
  size?: number;
  /** Quiet-zone border, in modules, on each side. Defaults to 4 (the spec minimum). */
  margin?: number;
  /** Accessible text alternative for the encoded image. Defaults to the raw value. */
  label?: string;
  className?: string;
}

export function QRCode({ value, size = 240, margin = 4, label, className }: QRCodeProps) {
  const qr = useMemo(
    () => qrcodegen.QrCode.encodeText(value, qrcodegen.QrCode.Ecc.MEDIUM),
    [value],
  );

  const dimension = qr.size + margin * 2;
  const darkModules: Array<{ x: number; y: number }> = [];
  for (let y = 0; y < qr.size; y++) {
    for (let x = 0; x < qr.size; x++) {
      if (qr.getModule(x, y)) darkModules.push({ x, y });
    }
  }

  return (
    <svg
      role="img"
      aria-label={label ?? value}
      viewBox={`0 0 ${dimension} ${dimension}`}
      width={size}
      height={size}
      className={className}
      shapeRendering="crispEdges"
    >
      <rect x={0} y={0} width={dimension} height={dimension} fill="white" />
      {darkModules.map(({ x, y }) => (
        <rect key={`${x}-${y}`} x={x + margin} y={y + margin} width={1} height={1} fill="black" />
      ))}
    </svg>
  );
}
