// SPDX-License-Identifier: Apache-2.0

package timeline

import (
	"math"

	pb "github.com/dmang-dev/ghostreel/gen/timeline/v1"
)

// SimplifyTolerance is how far (cells) a simplified track may stray from the
// keyframes it dropped.
const SimplifyTolerance = 0.25

// maxSpan bounds how many keyframes one kept span may replace, keeping
// simplification linear-time on long idle stretches.
const maxSpan = 48

// Simplify drops redundant command- and engine-sourced keyframes from every
// track: those that PositionAt, evaluated on the remaining keyframes, already
// reproduces within tol (checked at each dropped keyframe and halfway to the
// next). Observations (SOURCE_TRACKER) and death keyframes always stay, so
// "last observed" information survives.
func Simplify(tl *pb.Timeline, tol float64) {
	for _, t := range tl.Tracks {
		u := tl.Units[t.UnitId]
		speed := func(loop uint32) float32 { return tl.UnitTypes[TypeAt(u, loop)].Speed }
		t.Keyframes = simplifyKeys(t.Keyframes, speed, tol)
	}
}

func droppable(k *pb.Keyframe) bool {
	return k.Source != pb.Source_SOURCE_TRACKER && k.State != pb.UnitState_UNIT_STATE_DEAD
}

func simplifyKeys(ks []*pb.Keyframe, speed func(uint32) float32, tol float64) []*pb.Keyframe {
	if len(ks) < 3 {
		return ks
	}
	out := []*pb.Keyframe{ks[0]}
	anchor := 0
	for anchor < len(ks)-1 {
		// Extend the span anchor..j while every key strictly inside it can go.
		best := anchor + 1
		for j := anchor + 2; j < len(ks) && j-anchor <= maxSpan; j++ {
			if !droppable(ks[j-1]) || !spanFits(ks, anchor, j, speed, tol) {
				break
			}
			best = j
		}
		out = append(out, ks[best])
		anchor = best
	}
	return out
}

// spanFits reports whether the two-key track ks[i], ks[j] reproduces every
// key between them (and the midpoints between consecutive ones) within tol.
func spanFits(ks []*pb.Keyframe, i, j int, speed func(uint32) float32, tol float64) bool {
	span := &pb.Track{Keyframes: []*pb.Keyframe{ks[i], ks[j]}}
	full := &pb.Track{Keyframes: ks[i : j+1]}
	for m := i; m < j; m++ {
		for _, loop := range []uint32{ks[m].Loop, (ks[m].Loop + ks[m+1].Loop) / 2} {
			s := speed(loop)
			ax, ay, _ := PositionAt(span, s, loop)
			bx, by, _ := PositionAt(full, s, loop)
			if math.Hypot(float64(ax-bx), float64(ay-by)) > tol {
				return false
			}
		}
	}
	return true
}
