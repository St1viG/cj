import { useState } from "react";
import { useSearchParams } from "react-router";

/**
 * One search param as a filter value, and a setter that keeps the other
 * params and replaces the history entry (a filter is not a page to go back
 * to). An empty value removes the param.
 *
 * The router applies a location change inside a transition, so the URL
 * catches up a render later; until then the value just set is returned, and
 * a select bound to it does not snap back for a frame.
 */
export function useParam(name: string): [string, (value: string) => void] {
  const [params, setParams] = useSearchParams();
  const fromUrl = params.get(name) ?? "";
  const [pending, setPending] = useState<{ url: string; value: string } | null>(null);
  // Once the URL has moved on (to the value set, or anywhere else by a link),
  // it is the truth again; a stale shadow would win when the URL came back.
  if (pending && pending.url !== fromUrl) setPending(null);
  const value = pending && pending.url === fromUrl ? pending.value : fromUrl;
  const set = (v: string) => {
    setPending({ url: fromUrl, value: v });
    const next = new URLSearchParams(params);
    if (v) next.set(name, v);
    else next.delete(name);
    setParams(next, { replace: true });
  };
  return [value, set];
}
