import type { ReactNode } from "react";

/** A telemetry tile: a small label over a big serif numeral. */
export function Stat({ label, value, unit }: { label: string; value: ReactNode; unit?: string }) {
  return (
    <div className="stat reg">
      <span className="lbl cw" data-w={label} />
      <span className="v">
        {value}
        {unit && <small>{unit}</small>}
      </span>
    </div>
  );
}
