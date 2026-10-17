import { defineConfig } from "vite";
import react from "@vitejs/plugin-react";

// In development the Go API runs on :8080 (make compose-up, or doiq-api with
// DEPLOYIQ_MONGO_URI=memory://). Proxying /api keeps the dashboard same-origin,
// exactly as in production where one host serves both (Phase 2.3).
export default defineConfig({
  plugins: [react()],
  server: {
    port: 5173,
    proxy: {
      "/api": "http://localhost:8080",
    },
  },
  build: {
    outDir: "dist",
    sourcemap: false,
  },
  test: {
    environment: "jsdom",
    globals: true,
    setupFiles: "./src/setupTests.js",
    css: false,
  },
});
