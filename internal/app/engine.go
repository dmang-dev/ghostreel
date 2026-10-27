// SPDX-License-Identifier: Apache-2.0

//go:build !js

package app

import (
	pb "github.com/dmang-dev/ghostreel/gen/timeline/v1"
	"github.com/dmang-dev/ghostreel/internal/engine"
	"github.com/dmang-dev/ghostreel/internal/timeline"
)

// applyEngine replaces tl's motion with the engine recording at path.
func applyEngine(tl *pb.Timeline, path string) error {
	smp, err := engine.ReadSamples(path)
	if err != nil {
		return err
	}
	return timeline.ApplyEngine(tl, smp)
}
