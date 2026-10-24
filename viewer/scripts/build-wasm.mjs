// SPDX-License-Identifier: Apache-2.0
// Builds the Go core for the browser-only viewer: cmd/ghostreel-wasm →
// public-web/ghostreel.wasm, plus Go's wasm_exec.js loader next to it.
import { execFileSync } from "node:child_process";
import { copyFileSync, existsSync, mkdirSync } from "node:fs";
import { join } from "node:path";

const out = new URL("../public-web/", import.meta.url).pathname.replace(/^\/([A-Za-z]:)/, "$1");
mkdirSync(out, { recursive: true });
const env = { ...process.env, GOOS: "js", GOARCH: "wasm", CGO_ENABLED: "0" };
execFileSync("go", ["build", "-trimpath", "-ldflags=-s -w", "-o", join(out, "ghostreel.wasm"), "./cmd/ghostreel-wasm"], {
  cwd: new URL("../../", import.meta.url),
  env,
  stdio: "inherit",
});
const goroot = execFileSync("go", ["env", "GOROOT"], { encoding: "utf8" }).trim();
// Go 1.24 moved the loader from misc/wasm to lib/wasm.
const loader = [join(goroot, "lib", "wasm", "wasm_exec.js"), join(goroot, "misc", "wasm", "wasm_exec.js")].find(existsSync);
if (!loader) throw new Error(`wasm_exec.js not found under ${goroot}`);
copyFileSync(loader, join(out, "wasm_exec.js"));
console.log(`built ${join(out, "ghostreel.wasm")}`);
