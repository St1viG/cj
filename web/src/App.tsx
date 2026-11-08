import { useState } from "react";
import { BrowserRouter } from "react-router";
import { AppRoutes } from "./AppRoutes";
import { createApiTransport } from "./api/transport";
import { Providers } from "./Providers";

/** The browser app: the real transport and the History API router. */
export function App() {
  const [transport] = useState(() => createApiTransport());
  return (
    <Providers transport={transport}>
      <BrowserRouter>
        <AppRoutes />
      </BrowserRouter>
    </Providers>
  );
}
