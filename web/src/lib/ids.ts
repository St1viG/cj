import { useCallback, useRef } from "react";

/**
 * A random UUID v4. `crypto.randomUUID` only exists in secure contexts (HTTPS
 * or localhost), and the demo serves the app over plain HTTP on the tailnet,
 * so fall back to building one from `getRandomValues`, which is always there.
 */
export function newId(): string {
  const c = globalThis.crypto;
  if (typeof c.randomUUID === "function") return c.randomUUID();
  const b = c.getRandomValues(new Uint8Array(16));
  b[6] = ((b[6] ?? 0) & 0x0f) | 0x40; // version 4
  b[8] = ((b[8] ?? 0) & 0x3f) | 0x80; // variant 10
  const h = Array.from(b, (x) => x.toString(16).padStart(2, "0"));
  return [h.slice(0, 4), h.slice(4, 6), h.slice(6, 8), h.slice(8, 10), h.slice(10)].map((p) => p.join("")).join("-");
}

/**
 * An idempotency key for one form. The server replays the first result for a
 * repeated key, so a retry of the same submit (the network dropped after the
 * server created the instance) must reuse the key, while a different request
 * needs a new one. A new visit to the form starts with a new key too, so
 * "create the same thing again" really creates it again.
 *
 * `keyFor(fingerprint)` returns the same key for the same fingerprint.
 */
export function useIdempotencyKey(): (fingerprint: string) => string {
  const last = useRef<{ fingerprint: string; key: string } | null>(null);
  return useCallback((fingerprint: string) => {
    if (last.current?.fingerprint !== fingerprint) last.current = { fingerprint, key: newId() };
    return last.current.key;
  }, []);
}
