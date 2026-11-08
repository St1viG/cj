import type { Transport } from "@connectrpc/connect";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { type ReactNode, useState } from "react";
import { ApiProvider } from "./api/ApiProvider";
import { createQueryClient } from "./api/queryClient";
import { AuthProvider } from "./auth/AuthProvider";

/**
 * Everything the routes need above them: the API clients, the query cache and
 * the session. Tests pass an in-memory transport; the app passes the browser's.
 */
export function Providers({
  transport,
  queryClient,
  children,
}: {
  transport: Transport;
  queryClient?: QueryClient;
  children: ReactNode;
}) {
  const [client] = useState(() => queryClient ?? createQueryClient());
  return (
    <ApiProvider transport={transport}>
      <QueryClientProvider client={client}>
        <AuthProvider>{children}</AuthProvider>
      </QueryClientProvider>
    </ApiProvider>
  );
}
