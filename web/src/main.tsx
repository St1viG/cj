import { StrictMode } from "react";
import { createRoot } from "react-dom/client";
import { App } from "./App";
import "./reset.css";
import "./theme.css";
import { applyTheme, storedTheme } from "./lib/theme";

applyTheme(storedTheme());

const root = document.getElementById("root");
if (!root) throw new Error("index.html has no #root");

// `VITE_MOCK_API=1 pnpm dev` runs the pages against an in-memory backend, so
// the app can be seen without `kondukter serve`. Dead code in a build.
const transport =
  import.meta.env.DEV && import.meta.env.VITE_MOCK_API === "1"
    ? (await import("./devMock")).createDevMockTransport()
    : undefined;

createRoot(root).render(
  <StrictMode>
    <App transport={transport} />
  </StrictMode>,
);
