import { MutationCache, QueryCache, QueryClient } from "@tanstack/react-query";
import { isUnauthenticated } from "./errors";
import { keys } from "./keys";

/**
 * The app's query client. Live pages use server streams where WEB_PAGES.md
 * says so; everything else polls every 5 s.
 *
 * Any call that comes back Unauthenticated means the session ended (expired,
 * logged out in another tab), so the cached principal is cleared. The route
 * guards then send the person to /login.
 */
export function createQueryClient(): QueryClient {
  const client: QueryClient = new QueryClient({
    queryCache: new QueryCache({ onError: (e) => dropSession(client, e) }),
    mutationCache: new MutationCache({ onError: (e) => dropSession(client, e) }),
    defaultOptions: {
      queries: {
        refetchInterval: 5_000,
        retry: (count, e) => count < 2 && !isUnauthenticated(e),
        refetchOnWindowFocus: true,
      },
    },
  });
  return client;
}

function dropSession(client: QueryClient, e: unknown): void {
  if (isUnauthenticated(e)) client.setQueryData(keys.me, null);
}
