import { defineConfig } from "vitest/config";
import react from "@vitejs/plugin-react";
export default defineConfig({
  plugins: [react()],
  build: { outDir: process.env.OPS_WEB_BUILD_DIR || "dist" },
  test: {
    maxWorkers: 2,
    environment: "jsdom",
    include: ["tests/**/*.test.{ts,tsx}"],
    setupFiles: ["tests/setup.ts"],
  },
  server: {
    proxy: {
      "/api": {
        target: process.env.OPS_API_PROXY || "http://127.0.0.1:18080",
        changeOrigin: false,
      },
    },
  },
});
