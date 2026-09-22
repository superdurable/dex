import React, {type ReactNode} from 'react';

/**
 * The Dex mark: a durable track, most of it travelled, and one live head.
 *
 * The same geometry as the app's mark (web/app/components/DexMark.tsx), kept as a
 * second small component rather than shared through a package because the two
 * sites have no build relationship and one 40-line SVG is cheaper than a
 * dependency. If the geometry changes, both change — the numbers are the contract.
 *
 * Inline rather than an <img>, so the two flats come from the docs theme's own
 * custom properties and the mark follows light and dark for free. The asset it
 * replaces was a 1254x1254 PNG with no alpha that had to be cropped inside a
 * circle with negative offsets.
 */
export default function DexMark({size = 30}: {size?: number}): ReactNode {
  const c = size / 2;
  const r = size * 0.336;
  const w = size * 0.15;
  const at = (deg: number): readonly [number, number] => {
    const a = (deg * Math.PI) / 180;
    return [c + r * Math.cos(a), c + r * Math.sin(a)] as const;
  };
  const [hx, hy] = at(-62);
  const [x0, y0] = at(-88);
  const [x1, y1] = at(-362);
  return (
    <svg
      aria-hidden="true"
      focusable="false"
      height={size}
      viewBox={`0 0 ${size} ${size}`}
      width={size}
      xmlns="http://www.w3.org/2000/svg">
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
