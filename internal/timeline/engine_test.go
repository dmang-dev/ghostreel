// SPDX-License-Identifier: Apache-2.0

package timeline

import (
	"testing"

	enginev1 "github.com/dmang-dev/ghostreel/gen/engine/v1"
	pb "github.com/dmang-dev/ghostreel/gen/timeline/v1"
	"github.com/dmang-dev/ghostreel/internal/replay"
)

func TestApplyEngine(t *testing.T) {
	tl := buildFixture(t)
	marine := uint64(replay.MakeTag(5, 1))
	s := &enginev1.Samples{ReplayHash: "fixture", Frames: []*enginev1.Frame{
		{Loop: 160, Units: []*enginev1.UnitSample{{Tag: 1<<40 | marine, X: 25, Y: 18.25, Facing: 1}}},
		{Loop: 320, Units: []*enginev1.UnitSample{{Tag: 1<<40 | marine, X: 30.1, Y: 20}}},
		{Loop: 900, Units: []*enginev1.UnitSample{{Tag: 1<<40 | marine, X: 99, Y: 99}}}, // after its death: ignored
	}}
	if err := ApplyEngine(tl, s); err != nil {
		t.Fatal(err)
	}
	if tl.Meta.Fidelity != pb.Source_SOURCE_ENGINE {
		t.Error("fidelity not raised")
	}
	var keys []*pb.Keyframe
	for _, tr := range tl.Tracks {
		if tl.Units[tr.UnitId].Tag == uint32(marine) {
			keys = tr.Keyframes
		}
	}
	if len(keys) != 3 || keys[0].X != 25 || keys[0].Source != pb.Source_SOURCE_ENGINE || keys[2].State != pb.UnitState_UNIT_STATE_DEAD {
		t.Fatalf("marine keyframes = %v", keys)
	}
	s.ReplayHash = "other"
	if err := ApplyEngine(tl, s); err == nil {
		t.Error("samples from another replay accepted")
	}
}
