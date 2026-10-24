// SPDX-License-Identifier: Apache-2.0

//go:build js && wasm

// Command ghostreel-wasm is the Go core compiled to WebAssembly for the
// browser-only viewer. It runs the same HTTP API as `ghostreel serve`
// (package server) in memory: the viewer's web worker hands it requests
// through ghostreelRequest instead of the network, so replays never leave
// the user's machine.
//
// Differences from the server: no file system (timelines aren't cached and
// replay folders aren't listed), no map lookup (the browser can read
// neither the Battle.net cache nor Blizzard's depot, which serves plain HTTP
// without CORS), so maps come from the user's file picker; and no game data,
// so the 3D view draws stand-ins.
package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"syscall/js"

	"github.com/dmang-dev/ghostreel/internal/app"
	"github.com/dmang-dev/ghostreel/internal/server"
)

func main() {
	svc := app.New()
	svc.CacheDir = ""
	svc.Resolver = nil
	h := (&server.Server{App: svc}).Handler()

	// ghostreelRequest(method, path, headersJSON, body?) → Promise of
	// {status, contentType, body: Uint8Array}.
	js.Global().Set("ghostreelRequest", js.FuncOf(func(_ js.Value, args []js.Value) any {
		method, path, hdrs := args[0].String(), args[1].String(), args[2].String()
		var body []byte
		if len(args) > 3 && !args[3].IsNull() && !args[3].IsUndefined() {
			body = make([]byte, args[3].Get("length").Int())
			js.CopyBytesToGo(body, args[3])
		}
		promise := js.Global().Get("Promise")
		return promise.New(js.FuncOf(func(_ js.Value, pa []js.Value) any {
			resolve, reject := pa[0], pa[1]
			// Handlers block (decoding, building timelines), which a JS
			// callback must not: run them on their own goroutine.
			go func() {
				defer func() {
					if r := recover(); r != nil {
						reject.Invoke(js.Global().Get("Error").New(fmt.Sprint(r)))
					}
				}()
				resolve.Invoke(serve(h, method, path, hdrs, body))
			}()
			return nil
		}))
	}))
	js.Global().Call("postMessage", map[string]any{"ready": true})
	select {}
}

func serve(h http.Handler, method, path, hdrs string, body []byte) js.Value {
	req := httptest.NewRequest(method, path, bytes.NewReader(body))
	var headers map[string]string
	if json.Unmarshal([]byte(hdrs), &headers) == nil {
		for k, v := range headers {
			req.Header.Set(k, v)
		}
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	out := js.Global().Get("Uint8Array").New(rec.Body.Len())
	js.CopyBytesToJS(out, rec.Body.Bytes())
	res := js.Global().Get("Object").New()
	res.Set("status", rec.Code)
	res.Set("contentType", rec.Header().Get("Content-Type"))
	res.Set("body", out)
	return res
}
