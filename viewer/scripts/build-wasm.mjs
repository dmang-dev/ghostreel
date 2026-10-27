// SPDX-License-Identifier: Apache-2.0
// Builds the Go core for the browser-only viewer: cmd/ghostreel-wasm →
// public-web/ghostreel.wasm, plus Go's wasm_exec.js loader next to it.
//
// With --compress (npm run build:web) it also writes ghostreel.wasm.gz and
// ghostreel.wasm.br, which the worker fetches and decodes itself (see
// src/wasm-worker.ts), so any static host serves the small file. Without it
// (npm run dev:web) stale compressed copies are removed, so the worker
// falls back to the fresh raw file.
import { execFileSync } from "node:child_process";
import { copyFileSync, existsSync, mkdirSync, readFileSync, rmSync, writeFileSync } from "node:fs";
import { join } from "node:path";
import { brotliCompressSync, constants, gzipSync } from "node:zlib";

const out = new URL("../public-web/", import.meta.url).pathname.replace(/^\/([A-Za-z]:)/, "$1");
mkdirSync(out, { recursive: true });
const wasm = join(out, "ghostreel.wasm");
const env = { ...process.env, GOOS: "js", GOARCH: "wasm", CGO_ENABLED: "0" };
execFileSync("go", ["build", "-trimpath", "-ldflags=-s -w", "-o", wasm, "./cmd/ghostreel-wasm"], {
  cwd: new URL("../../", import.meta.url),
  env,
  stdio: "inherit",
});
const goroot = execFileSync("go", ["env", "GOROOT"], { encoding: "utf8" }).trim();
// Go 1.24 moved the loader from misc/wasm to lib/wasm.
const loader = [join(goroot, "lib", "wasm", "wasm_exec.js"), join(goroot, "misc", "wasm", "wasm_exec.js")].find(existsSync);
if (!loader) throw new Error(`wasm_exec.js not found under ${goroot}`);
copyFileSync(loader, join(out, "wasm_exec.js"));

const mb = (n) => `${(n / 1e6).toFixed(2)} MB`;
const raw = readFileSync(wasm);
let sizes = `${mb(raw.length)}`;
if (process.argv.includes("--compress")) {
  const gz = gzipSync(raw, { level: 9 });
  // Quality 11 with a 16 MB window: about 30 s, and 30% smaller than gzip.
  const br = brotliCompressSync(raw, {
    params: {
      [constants.BROTLI_PARAM_QUALITY]: 11,
      [constants.BROTLI_PARAM_LGWIN]: 24,
      [constants.BROTLI_PARAM_SIZE_HINT]: raw.length,
    },
  });
  writeFileSync(`${wasm}.gz`, gz);
  writeFileSync(`${wasm}.br`, br);
  sizes += `, ${mb(gz.length)} gzipped, ${mb(br.length)} with Brotli`;
} else {
  rmSync(`${wasm}.gz`, { force: true });
  rmSync(`${wasm}.br`, { force: true });
}
console.log(`built ${wasm} (${sizes})`);
