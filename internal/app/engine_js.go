// SPDX-License-Identifier: Apache-2.0

//go:build js

package app

import (
	"errors"

	pb "github.com/dmang-dev/ghostreel/gen/timeline/v1"
)

// applyEngine: the browser build opens replays from bytes, so it never has
// an engine recording beside one. Leaving package engine (and the SC2 API's
// protobuf types) out of it keeps ghostreel.wasm about 1 MB smaller.
func applyEngine(*pb.Timeline, string) error {
	return errors.New("engine recordings are not supported in the browser build")
}
