import { defineConfig } from "vite";
import react from "@vitejs/plugin-react";
import { fileURLToPath } from "node:url";
export default defineConfig({
  plugins: [
    {
      name: "assistant-fixture-auth",
      enforce: "pre",
      resolveId(id) {
        if (id.endsWith("/auth/AuthProvider"))
          return fileURLToPath(
            new URL("./tests/fixtures/assistant-auth.ts", import.meta.url),
          );
      },
    },
    react(),
  ],
  build: {
    outDir:
      process.env.OPS_ASSISTANT_UI_BUILD_DIR || "/tmp/ops-assistant-ui-six",
    rollupOptions: {
      input: fileURLToPath(
        new URL("./tests/fixtures/assistant-harness.html", import.meta.url),
      ),
    },
  },
});
