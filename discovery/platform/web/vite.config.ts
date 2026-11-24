/// <reference types="vitest/config" />
import { defineConfig } from "vite";
import react from "@vitejs/plugin-react";

// Node's process global, declared locally to avoid a @types/node dependency.
declare const process: { env: Record<string, string | undefined> };

// The dev server proxies API calls to the Go server on :8080, so the browser
// talks to one origin in development exactly as in production.
const api = process.env.OBS_API_URL ?? "http://localhost:8080";

export default defineConfig({
  plugins: [react()],
  server: {
    port: 5173,
    proxy: { "/api": api, "/healthz": api, "/readyz": api },
  },
  build: { outDir: "dist", sourcemap: true },
  test: {
    environment: "jsdom",
    globals: true,
    setupFiles: ["./src/test/setup.ts"],
  },
});
