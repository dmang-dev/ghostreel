// SPDX-License-Identifier: Apache-2.0

package server

import "runtime"

// browser is true in the browser build (GOOS=js, cmd/ghostreel-wasm), which
// has no game data. Routes it can't serve sit behind it, so the compiler
// drops them and the linker leaves what they use out of ghostreel.wasm.
const browser = runtime.GOOS == "js"
