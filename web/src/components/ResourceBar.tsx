/**
 * "6 of 8" over a thin bar showing how much is in use. The text is the
 * readout; the bar turns blue past 70 % and red past 90 %.
 */
export function ResourceBar({ text, used, total }: { text: string; used: number; total: number }) {
  const pct = total > 0 ? Math.min(100, Math.max(0, Math.round((used / total) * 100))) : 0;
  const tone = pct >= 90 ? " r" : pct >= 70 ? " w" : "";
  return (
    <span className="rb">
      <span className="m">{text}</span>
      <span className={`bar${tone}`} style={{ ["--v" as string]: pct }} aria-hidden="true">
        <i />
      </span>
    </span>
  );
}
