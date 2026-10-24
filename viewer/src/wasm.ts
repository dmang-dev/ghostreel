// SPDX-License-Identifier: Apache-2.0
//
// The browser-only build's backend: API requests go to the Go core running
// as WebAssembly in a worker (wasm-worker.ts) instead of to a server. The
// answers are ordinary Responses, so the rest of the viewer can't tell the
// difference.

type Reply = { id: number; status?: number; contentType?: string; body?: Uint8Array; error?: string };

let worker: Worker | undefined;
let nextId = 1;
const waiting = new Map<number, (r: Reply) => void>();

function start(): Worker {
  if (worker) return worker;
  worker = new Worker(new URL("./wasm-worker.ts", import.meta.url), { type: "module" });
  worker.onmessage = (e: MessageEvent<Reply>) => {
    const done = waiting.get(e.data.id);
    waiting.delete(e.data.id);
    done?.(e.data);
  };
  worker.onerror = (e) => {
    for (const done of waiting.values()) done({ id: 0, error: e.message || "the WebAssembly core failed to start" });
    waiting.clear();
  };
  return worker;
}

/** fetch() for /api/... paths, answered by the WebAssembly core. */
export async function wasmFetch(path: string, init?: RequestInit): Promise<Response> {
  const headers: Record<string, string> = {};
  new Headers(init?.headers).forEach((v, k) => (headers[k] = v));
  const body = init?.body == null ? undefined : new Uint8Array(await new Response(init.body).arrayBuffer());
  const w = start();
  const id = nextId++;
  const reply = await new Promise<Reply>((resolve) => {
    waiting.set(id, resolve);
    w.postMessage({ id, method: init?.method ?? "GET", path, headers, body }, body ? [body.buffer] : []);
  });
  if (reply.error !== undefined) throw new Error(reply.error);
  return new Response(reply.body as BodyInit, { status: reply.status, headers: { "Content-Type": reply.contentType ?? "" } });
}
