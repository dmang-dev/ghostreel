// SPDX-License-Identifier: Apache-2.0
import { defineConfig } from "vite";

// In development, run `ghostreel serve` (API on :8080) and `npm run dev`;
// Vite proxies /api to it. Relative asset paths let the same build be served
// by `ghostreel serve` and embedded in the desktop app.
//
// `--mode web` (npm run build:web) is the browser-only build: the Go core
// runs in the page as WebAssembly (.env.web sets VITE_BACKEND=wasm), its
// files come from public-web/, and the result goes to dist-web/ for any
// static host.
export default defineConfig(({ mode }) => ({
  base: "./",
  publicDir: mode === "web" ? "public-web" : "public",
  server: {
    proxy: { "/api": "http://127.0.0.1:8080" },
  },
  build: {
    outDir: process.env.GHOSTREEL_VIEWER_OUT ?? (mode === "web" ? "dist-web" : "dist"),
    emptyOutDir: true,
    chunkSizeWarningLimit: 1000,
  },
}));
