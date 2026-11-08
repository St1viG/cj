import { writeFileSync } from "node:fs";
import { fileURLToPath } from "node:url";
import react from "@vitejs/plugin-react";
import { defineConfig, type Plugin } from "vitest/config";

// The generated client lives outside web/ (buf.gen.yaml writes gen/ts), so it
// is reached through an alias. @bufbuild/protobuf resolves to the single copy
// both packages share through the pnpm workspace.
const gen = fileURLToPath(new URL("../gen/ts", import.meta.url));

// `pnpm dev` proxies the API to a locally running `kondukter serve`. The Go
// server answers under /api itself, so the path is forwarded unchanged.
const apiTarget = process.env.KONDUKTER_API_URL ?? "http://localhost:8080";

const outDir = fileURLToPath(new URL("dist", import.meta.url));

// web/dist is embedded into the Go binary (web/embed.go) and is ignored by git
// except for an empty .gitkeep, which lets `go build` work on a checkout that
// never ran Node. Vite empties the directory on every build, so put it back.
const keepPlaceholder: Plugin = {
  name: "kondukter:keep-dist-placeholder",
  apply: "build",
  closeBundle() {
    writeFileSync(`${outDir}/.gitkeep`, "");
  },
};

export default defineConfig({
  plugins: [react(), keepPlaceholder],
  resolve: { alias: { "@gen": gen } },
  server: {
    proxy: { "/api": { target: apiTarget } },
  },
  build: {
    outDir,
    emptyOutDir: true,
  },
  test: {
    environment: "jsdom",
    include: ["src/**/*.test.{ts,tsx}"],
    setupFiles: ["src/test/setup.ts"],
    // Above the 8 s that findBy/waitFor may wait (src/test/setup.ts).
    testTimeout: 20_000,
  },
});
