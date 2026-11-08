import { useEffect, useState } from "react";

/**
 * `Date.now()`, re-rendering every `intervalMs`, for ages such as "last seen
 * 12 s ago" that change between data updates.
 */
export function useNow(intervalMs = 1000): number {
  const [now, setNow] = useState(() => Date.now());
  useEffect(() => {
    const t = setInterval(() => setNow(Date.now()), intervalMs);
    return () => clearInterval(t);
  }, [intervalMs]);
  return now;
}
