import type { ReactNode } from "react";

/** A telemetry tile: a small label over a big serif numeral. */
/** `from` makes the numeral count to `value` when the tile scrolls into view (landing page only). */
export function Stat({ label, value, unit, from }: { label: string; value: ReactNode; unit?: string; from?: number }) {
  return (
    <div className="stat reg">
      <span className="lbl cw" data-w={label} />
      <span className="v" data-count-from={from}>
        {value}
        {unit && <small>{unit}</small>}
      </span>
    </div>
  );
}
