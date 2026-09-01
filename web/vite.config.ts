import { defineConfig } from "vitest/config";
import react from "@vitejs/plugin-react";

export default defineConfig({
  plugins: [react()],
  build: {
    outDir: "../internal/webui/dist",
    emptyOutDir: true,
  },
  server: {
    proxy: { "/api": "http://127.0.0.1:8787" },
  },
  test: {
    environment: "jsdom",
    setupFiles: "./src/test-setup.ts",
    coverage: {
      provider: "v8",
      reporter: ["text", "lcov"],
      include: [
        "src/api.ts",
        "src/hooks.ts",
        "src/BusyOverlay.tsx",
        "src/ApiKeyHelpDialog.tsx",
      ],
      thresholds: { statements: 95, branches: 95, functions: 95, lines: 95 },
    },
  },
});
