// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

/**
 * The Dex mark: a durable track, most of it travelled, and one live head.
 *
 * The dark stroke is what has already happened and cannot un-happen; the bright
 * cap is the only thing moving. Two flat colours, no gradient — a gradient
 * cannot be a token.
 *
 * Inline SVG rather than an <img>, because the mark it replaces was a 1254x1254
 * 8-bit PNG with no alpha: it could not be tinted, could not sit on a dark
 * ground, and needed a wrapper that cropped it with negative offsets. These two
 * fills are CSS custom properties, so the mark follows the theme for free and
 * the head is the same token as the primary button.
 *
 * The gap is load-bearing. A closed ring reads "finished"; the 26 degrees of
 * bare track between the head and the tail is what says the work is still going.
 */
export function DexMark({ size = 28 }: { size?: number }) {
  // Geometry is a fraction of the box so it scales without re-tuning. Centre
  // 0.5, radius 0.336, stroke 0.15 — the head sits at -62 degrees with 26
  // degrees of clearance, and the track runs 300 degrees back from there.
  const c = size / 2;
  const r = size * 0.336;
  const w = size * 0.15;
  const at = (deg: number) => {
    const a = (deg * Math.PI) / 180;
    return [c + r * Math.cos(a), c + r * Math.sin(a)] as const;
  };
  const [hx, hy] = at(-62);
  const [x0, y0] = at(-62 - 26);
  const [x1, y1] = at(-62 - 300);
  return (
    <svg
      aria-hidden="true"
      focusable="false"
      height={size}
      viewBox={`0 0 ${size} ${size}`}
      width={size}
      xmlns="http://www.w3.org/2000/svg"
    >
      <path
        d={`M ${x1.toFixed(2)} ${y1.toFixed(2)} A ${r.toFixed(2)} ${r.toFixed(2)} 0 1 1 ${x0.toFixed(2)} ${y0.toFixed(2)}`}
        fill="none"
        stroke="var(--brand-track)"
        strokeLinecap="round"
        strokeWidth={w.toFixed(2)}
      />
      <circle cx={hx.toFixed(2)} cy={hy.toFixed(2)} fill="var(--brand-head)" r={(w * 0.8).toFixed(2)} />
    </svg>
  );
}
