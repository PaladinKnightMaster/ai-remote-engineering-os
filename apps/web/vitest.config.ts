// Vitest config for apps/web — jsdom env for React route tests.
//
// `environmentOptions.jsdom.url` is set so jsdom's window.location has a
// real origin. The api client uses baseUrl: "/" which the Request constructor
// resolves against the current document's URL; without this, jsdom's default
// `about:blank` makes `/v1/auth/login` an invalid URL and openapi-fetch
// throws before reaching the stubbed `fetch`. Matches the Vite dev server
// origin so tests rehearse against the same host pattern.

import { defineConfig } from "vitest/config";
import react from "@vitejs/plugin-react";

export default defineConfig({
  plugins: [react()],
  test: {
    environment: "jsdom",
    environmentOptions: {
      jsdom: { url: "http://localhost:5173" },
    },
    globals: false,
    include: ["src/**/*.test.{ts,tsx}"],
  },
});
