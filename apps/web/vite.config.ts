// Vite config for apps/web — ADR-0047 (Vite 6 + React 18 + Tailwind v4 SPA).
//
// Dev proxy preserves the ADR-0047 §10 same-origin posture in dev (the browser
// sees everything as :5173, so cookies + CSRF round-trip without CORS):
//   - /v1/sessions/{id}/channel  → ws://localhost:8082 (services/session-broker)
//   - everything else /v1/*      → http://localhost:8080 (services/api)
// The broker channel rule is a regex key listed first so it wins over the
// general /v1 rule. Prod puts all three behind Caddy/Traefik (deep-dive 09),
// where same-origin holds natively.
//
// Build output: dist/ static assets only. No Node runtime ships in prod
// (ADR-0047 §Decision item "Production serving").

import { defineConfig } from "vite";
import react from "@vitejs/plugin-react";
import tailwindcss from "@tailwindcss/vite";

const API_TARGET = process.env.AIREOS_API_PROXY ?? "http://localhost:8080";
const BROKER_TARGET = process.env.AIREOS_BROKER_PROXY ?? "http://localhost:8082";

export default defineConfig({
  plugins: [react(), tailwindcss()],
  server: {
    port: 5173,
    strictPort: true,
    proxy: {
      // Broker WebSocket channel — regex key, matched before the general /v1
      // rule. ws:true upgrades the connection.
      "^/v1/sessions/[^/]+/channel$": {
        target: BROKER_TARGET,
        changeOrigin: false,
        ws: true,
      },
      "/v1": {
        target: API_TARGET,
        changeOrigin: false,
        // Don't rewrite the path — services/api routes are /v1/*.
      },
    },
  },
  build: {
    outDir: "dist",
    sourcemap: true,
    // Cache-busting via hashed filenames is on by default.
  },
});
