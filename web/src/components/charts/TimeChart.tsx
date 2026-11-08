import { useEffect, useId, useRef, useState } from "react";

export interface ChartPoint {
  /** Milliseconds since the epoch. */
  t: number;
  v: number;
}

/** Rounds a maximum up to 1, 2, 2.5 or 5 times a power of ten, so the axis ends on a clean number. */
function niceMax(max: number): number {
  if (max <= 0) return 1;
  const pow = 10 ** Math.floor(Math.log10(max));
  for (const m of [1, 2, 2.5, 5, 10]) if (m * pow >= max) return m * pow;
  return 10 * pow;
}

const dateLabel = (t: number) => new Date(t).toLocaleDateString("en", { month: "short", day: "numeric" });
const M = { l: 56, r: 18, t: 14, b: 28 };

/** A bar from `y` down to the baseline with a 4px rounded top and a square foot. */
function barPath(x: number, w: number, y: number, base: number): string {
  const h = base - y;
  const r = Math.min(4, w / 2, h);
  if (h <= 0) return "";
  return `M${x},${base}V${y + r}Q${x},${y} ${x + r},${y}H${x + w - r}Q${x + w},${y} ${x + w},${y + r}V${base}Z`;
}

/**
 * One series over time: a line with a faint area under it, or bars. Marks
 * follow the design's specs (2px line, bars at most 24px with a rounded cap,
 * hairline grid, text in muted ink, the accent for the data). Hover or
 * arrow keys move a crosshair to the nearest day and show its value; the
 * same numbers are in the table beside the chart.
 */
export function TimeChart({
  points,
  kind,
  label,
  format,
  height = 220,
}: {
  points: readonly ChartPoint[];
  kind: "line" | "bars";
  /** What is plotted, for the accessible name. */
  label: string;
  format: (v: number) => string;
  height?: number;
}) {
  const ref = useRef<HTMLDivElement>(null);
  const [width, setWidth] = useState(720);
  const [hover, setHover] = useState<number | null>(null);
  const id = useId();

  useEffect(() => {
    const el = ref.current;
    if (!el || typeof ResizeObserver === "undefined") return;
    const ro = new ResizeObserver(([entry]) => {
      const w = entry?.contentRect.width;
      if (w && w > 120) setWidth(Math.round(w));
    });
    ro.observe(el);
    return () => ro.disconnect();
  }, []);

  if (points.length === 0) return null;
  const top = niceMax(Math.max(...points.map((p) => p.v)));
  const plotW = width - M.l - M.r;
  const plotH = height - M.t - M.b;
  const base = M.t + plotH;
  const band = plotW / points.length;
  const xAt = (i: number) => (kind === "bars" ? M.l + band * (i + 0.5) : M.l + (points.length === 1 ? plotW / 2 : (plotW * i) / (points.length - 1)));
  const yAt = (v: number) => M.t + plotH - (v / top) * plotH;
  const ticks = [0, 1, 2, 3, 4].map((k) => (top * k) / 4);
  const xTicks = [0, 1, 2, 3, 4].map((k) => Math.round(((points.length - 1) * k) / 4));

  const line = points.map((p, i) => `${i === 0 ? "M" : "L"}${xAt(i).toFixed(1)},${yAt(p.v).toFixed(1)}`).join("");
  const area = `${line}L${xAt(points.length - 1).toFixed(1)},${base}L${xAt(0).toFixed(1)},${base}Z`;
  const barW = Math.min(24, band - 2);
  const last = points.length - 1;
  const at = hover ?? null;
  const p = at === null ? undefined : points[at];

  const nearest = (clientX: number) => {
    const box = ref.current?.getBoundingClientRect();
    if (!box) return 0;
    const x = ((clientX - box.left) / box.width) * width;
    let best = 0;
    for (let i = 1; i < points.length; i++) if (Math.abs(xAt(i) - x) < Math.abs(xAt(best) - x)) best = i;
    return best;
  };
  const tipLeft = at === null ? 0 : xAt(at);
  const flip = at !== null && tipLeft > width * 0.62;

  return (
    // The crosshair follows the pointer or the arrow keys; the values are also in the table next to the chart.
    // biome-ignore lint/a11y/useSemanticElements: a chart is an image with a keyboard-moved readout
    <div
      ref={ref}
      className="chart"
      role="img"
      aria-label={`${label}, ${points.length} days, latest ${format(points[last]?.v ?? 0)}`}
      tabIndex={0}
      onPointerMove={(e) => setHover(nearest(e.clientX))}
      onPointerLeave={() => setHover(null)}
      onFocus={() => setHover((h) => h ?? last)}
      onBlur={() => setHover(null)}
      onKeyDown={(e) => {
        if (e.key === "ArrowLeft") setHover((h) => Math.max(0, (h ?? last) - 1));
        else if (e.key === "ArrowRight") setHover((h) => Math.min(last, (h ?? last) + 1));
        else if (e.key === "Escape") setHover(null);
        else return;
        e.preventDefault();
      }}
    >
      <svg width={width} height={height} viewBox={`0 0 ${width} ${height}`} aria-hidden="true">
        {ticks.map((v) => (
          <g key={v}>
            <line className="grid" x1={M.l} x2={width - M.r} y1={yAt(v)} y2={yAt(v)} />
            <text x={M.l - 10} y={yAt(v) + 4} textAnchor="end">
              {format(v)}
            </text>
          </g>
        ))}
        {xTicks.map((i, k) => (
          <text key={`${id}-${k}`} x={xAt(i)} y={height - 8} textAnchor={k === 0 ? "start" : k === xTicks.length - 1 ? "end" : "middle"}>
            {dateLabel(points[i]?.t ?? 0)}
          </text>
        ))}
        {kind === "line" ? (
          <>
            <path className="area" d={area} />
            <path className="line" d={line} />
            <circle className="dot" cx={xAt(last)} cy={yAt(points[last]?.v ?? 0)} r={5} />
          </>
        ) : (
          points.map((pt, i) => <path key={pt.t} className={`bar${at === i ? " hov" : ""}`} d={barPath(xAt(i) - barW / 2, barW, yAt(pt.v), base)} />)
        )}
        {at !== null && (
          <>
            <line className="cross" x1={xAt(at)} x2={xAt(at)} y1={M.t} y2={base} />
            {kind === "line" && p && <circle className="dot" cx={xAt(at)} cy={yAt(p.v)} r={5} />}
          </>
        )}
      </svg>
      {p && (
        <div className="tip chart-tip" style={{ left: flip ? undefined : tipLeft + 12, right: flip ? width - tipLeft + 12 : undefined, top: M.t }}>
          <span className="mut2">{dateLabel(p.t)}</span>
          <br />
          <strong className="ink-h">{format(p.v)}</strong>
        </div>
      )}
    </div>
  );
}
