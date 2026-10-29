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

func TestApplyEngineVitals(t *testing.T) {
	tl := buildFixture(t)
	marine := uint64(replay.MakeTag(5, 1))
	at := func(loop uint32, health, max float32) *enginev1.Frame {
		return &enginev1.Frame{Loop: loop, Units: []*enginev1.UnitSample{{Tag: marine, X: 25, Y: 18, Health: health, HealthMax: max}}}
	}
	s := &enginev1.Samples{ReplayHash: "fixture", Frames: []*enginev1.Frame{
		at(160, 45, 45),
		at(176, 45, 45),   // no change
		at(192, 44.8, 45), // under 1% of the maximum
		at(208, 39, 45),   // shot
		at(224, 39, 55),   // Combat Shield: the maximum changes
	}}
	if err := ApplyEngine(tl, s); err != nil {
		t.Fatal(err)
	}
	var got []*pb.Vitals
	for _, tr := range tl.Tracks {
		if tl.Units[tr.UnitId].Tag == uint32(marine) {
			got = tr.Vitals
		}
	}
	var loops []uint32
	for _, v := range got {
		loops = append(loops, v.Loop)
	}
	if len(got) != 3 || loops[0] != 160 || loops[1] != 208 || loops[2] != 224 || got[1].Health != 39 || got[2].HealthMax != 55 {
		t.Fatalf("vitals at %v: %v", loops, got)
	}

	// Recordings without vitals give none.
	tl = buildFixture(t)
	s.Frames = []*enginev1.Frame{{Loop: 160, Units: []*enginev1.UnitSample{{Tag: marine, X: 25, Y: 18}}}}
	if err := ApplyEngine(tl, s); err != nil {
		t.Fatal(err)
	}
	for _, tr := range tl.Tracks {
		if len(tr.Vitals) > 0 {
			t.Errorf("unit %d has vitals from a recording without any", tr.UnitId)
		}
	}
}
