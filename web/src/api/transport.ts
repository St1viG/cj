import type { Transport } from "@connectrpc/connect";
import { createConnectTransport } from "@connectrpc/connect-web";

/**
 * Where the Connect API is mounted. `kondukter serve` answers under /api, and
 * the Vite dev server proxies the same prefix (vite.config.ts).
 */
export const API_BASE = "/api";

/**
 * The browser transport. The session is an HttpOnly cookie (ADR-0021), so
 * every request carries credentials. JSON on the wire keeps the network tab
 * readable.
 */
export function createApiTransport(baseUrl: string = API_BASE): Transport {
  return createConnectTransport({
    baseUrl,
    fetch: (input, init) => globalThis.fetch(input, { ...init, credentials: "include" }),
  });
}
