// SPDX-License-Identifier: Apache-2.0
import { defineConfig } from "vite";

// In development, run `ghostreel serve` (API on :8080) and `npm run dev`;
// Vite proxies /api to it. Relative asset paths let the same build be served
// by `ghostreel serve` and embedded in the desktop app.
export default defineConfig({
  base: "./",
  server: {
    proxy: { "/api": "http://127.0.0.1:8080" },
  },
  build: {
    outDir: process.env.GHOSTREEL_VIEWER_OUT ?? "dist",
    emptyOutDir: true,
    chunkSizeWarningLimit: 1000,
  },
});
