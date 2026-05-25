// Vitest config for @aireos/ui — jsdom env required for @testing-library/react.
// packages/shared-types stays on the node default; per-package configs avoid
// pulling jsdom into tests that don't need it.

import { defineConfig } from "vitest/config";

export default defineConfig({
  test: {
    environment: "jsdom",
    globals: false,
    include: ["src/**/*.test.{ts,tsx}"],
  },
});
