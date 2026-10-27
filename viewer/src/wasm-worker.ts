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

// The files sit next to the page (public-web/, written by scripts/build-wasm.mjs).
const base = new URL("../", import.meta.url);

// The core comes compressed as well as raw: ghostreel.wasm is about 14 MB,
// 3.4 MB gzipped and 2.4 MB with Brotli. Decoding it here with
// DecompressionStream means any static host serves the small file, with no
// Content-Encoding set up; the raw file is the fallback (for a browser that
// can't decode either, or a build without them, like npm run dev:web).
const encodings: { suffix: string; format?: string }[] = [
  { suffix: ".br", format: "brotli" },
  { suffix: ".gz", format: "gzip" },
  { suffix: "" },
];

function canDecode(format: string): boolean {
  try {
    new DecompressionStream(format as CompressionFormat);
    return true;
  } catch {
    return false;
  }
}

type Bytes = Uint8Array<ArrayBuffer>;

/** Splits off a stream's first chunk, to look at, and returns the stream whole again. */
async function peek(body: ReadableStream<Bytes>): Promise<[Bytes, ReadableStream<Bytes>]> {
  const reader = body.getReader();
  const first = await reader.read();
  const head: Bytes = first.value ?? new Uint8Array();
  const whole = new ReadableStream<Bytes>({
    start(c) {
      if (head.length) c.enqueue(head);
      if (first.done) c.close();
    },
    async pull(c) {
      const { done, value } = await reader.read();
      if (done) c.close();
      else c.enqueue(value);
    },
    cancel: (why) => reader.cancel(why),
  });
  return [head, whole];
}

const isWasm = (b: Uint8Array) => b.length >= 4 && b[0] === 0 && b[1] === 0x61 && b[2] === 0x73 && b[3] === 0x6d; // "\0asm"

/** Fetches and compiles the core, trying each encoding the browser can decode in turn. */
async function instantiate(imports: WebAssembly.Imports): Promise<WebAssembly.Instance> {
  let lastErr: unknown;
  for (const { suffix, format } of encodings) {
    if (format && !canDecode(format)) continue;
    try {
      const res = await fetch(new URL("ghostreel.wasm" + suffix, base));
      // A missing file may come back as the page (a dev server's fallback).
      if (!res.ok || !res.body || res.headers.get("Content-Type")?.startsWith("text/html")) {
        throw new Error(`ghostreel.wasm${suffix}: HTTP ${res.status} ${res.headers.get("Content-Type") ?? ""}`);
      }
      let body: ReadableStream<Bytes> = res.body;
      if (format) {
        // A host that sends the compressed file with Content-Encoding has
        // had the browser decode it already.
        const [head, whole] = await peek(body);
        body = isWasm(head) ? whole : whole.pipeThrough(new DecompressionStream(format as CompressionFormat));
      }
      const wasm = new Response(body, { headers: { "Content-Type": "application/wasm" } });
      return (await WebAssembly.instantiateStreaming(wasm, imports)).instance;
    } catch (err) {
      lastErr = err;
    }
  }
  throw lastErr;
}

const ready = (async () => {
  // wasm_exec.js is Go's loader (it defines globalThis.Go); it isn't a
  // module, so load it by URL rather than through the bundler.
  const loader = new URL("wasm_exec.js", base).href;
  await import(/* @vite-ignore */ loader);
  const go = new scope.Go();
  const instance = await instantiate(go.importObject);
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
