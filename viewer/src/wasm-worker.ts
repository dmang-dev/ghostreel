// SPDX-License-Identifier: Apache-2.0
//
// Web worker for the browser-only build: runs the Go core (ghostreel.wasm,
// cmd/ghostreel-wasm) off the main thread, so decoding a replay and building
// its timeline never freeze the page. Messages are API requests; see
// wasm.ts for the other side.

/// <reference lib="webworker" />

interface GoRuntime {
  importObject: WebAssembly.Imports;
  run(instance: WebAssembly.Instance): Promise<void>;
}

type Request = { id: number; method: string; path: string; headers: Record<string, string>; body?: Uint8Array };
type GoResponse = { status: number; contentType: string; body: Uint8Array };

const scope = self as unknown as DedicatedWorkerGlobalScope & {
  Go: new () => GoRuntime;
  ghostreelRequest?: (method: string, path: string, headers: string, body?: Uint8Array) => Promise<GoResponse>;
};

// Both files sit next to the page (public-web/, copied by scripts/build-wasm.mjs).
const base = new URL("../", import.meta.url);

const ready = (async () => {
  // wasm_exec.js is Go's loader (it defines globalThis.Go); it isn't a
  // module, so load it by URL rather than through the bundler.
  const loader = new URL("wasm_exec.js", base).href;
  await import(/* @vite-ignore */ loader);
  const go = new scope.Go();
  const { instance } = await WebAssembly.instantiateStreaming(fetch(new URL("ghostreel.wasm", base)), go.importObject);
  const started = new Promise<void>((resolve) => {
    // main() posts {ready: true} once ghostreelRequest is registered.
    const orig = scope.postMessage.bind(scope);
    scope.postMessage = ((msg: unknown) => {
      if (msg && typeof msg === "object" && (msg as { ready?: boolean }).ready) {
        scope.postMessage = orig;
        resolve();
        return;
      }
      orig(msg);
    }) as typeof scope.postMessage;
  });
  void go.run(instance);
  await started;
})();

scope.onmessage = async (e: MessageEvent<Request>) => {
  const { id, method, path, headers, body } = e.data;
  try {
    await ready;
    const res = await scope.ghostreelRequest!(method, path, JSON.stringify(headers), body);
    scope.postMessage({ id, status: res.status, contentType: res.contentType, body: res.body }, [res.body.buffer]);
  } catch (err) {
    scope.postMessage({ id, error: String(err) });
  }
};
