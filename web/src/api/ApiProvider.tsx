import type { Transport } from "@connectrpc/connect";
import { createContext, type ReactNode, useContext, useMemo } from "react";
import { type Clients, createClients } from "./clients";

const ApiContext = createContext<Clients | null>(null);

/** Gives the tree typed service clients over one transport. */
export function ApiProvider({ transport, children }: { transport: Transport; children: ReactNode }) {
  const clients = useMemo(() => createClients(transport), [transport]);
  return <ApiContext.Provider value={clients}>{children}</ApiContext.Provider>;
}

/** The typed clients: `useApi().instances.listInstances({})`. */
export function useApi(): Clients {
  const clients = useContext(ApiContext);
  if (!clients) throw new Error("useApi needs an <ApiProvider>");
  return clients;
}
