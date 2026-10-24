// SPDX-License-Identifier: Apache-2.0

package timeline

import (
	"fmt"

	enginev1 "github.com/OWNER/ghostreel/gen/engine/v1"
	pb "github.com/OWNER/ghostreel/gen/timeline/v1"
)

// ApplyEngine replaces the tracks of every unit the engine recorded with its
// exact positions (SOURCE_ENGINE keyframes, one per engine frame), keeping
// the tracker's death keyframes. Units the engine never saw (inside
// transports, for example) keep their reconstructed tracks. The samples must
// come from the same replay.
func ApplyEngine(tl *pb.Timeline, s *enginev1.Samples) error {
	if s.ReplayHash != "" && tl.Meta.ReplayHash != "" && s.ReplayHash != tl.Meta.ReplayHash {
		return fmt.Errorf("engine samples are for replay %.12s…, timeline is %.12s…", s.ReplayHash, tl.Meta.ReplayHash)
	}
	byTag := make(map[uint32]*pb.Unit, len(tl.Units))
	for _, u := range tl.Units {
		byTag[u.Tag] = u
	}
	keys := map[uint32][]*pb.Keyframe{}
	for _, f := range s.Frames {
		for _, us := range f.Units {
			u := byTag[uint32(us.Tag)] // the replay tag is the low 32 bits
			if u == nil || !Alive(u, f.Loop) {
				continue
			}
			state := pb.UnitState_UNIT_STATE_IDLE
			if len(us.Orders) > 0 {
				state = pb.UnitState_UNIT_STATE_MOVING
			}
			keys[u.Id] = append(keys[u.Id], &pb.Keyframe{
				Loop: f.Loop, X: us.X, Y: us.Y, Facing: us.Facing, State: state, Source: pb.Source_SOURCE_ENGINE,
			})
		}
	}
	for _, t := range tl.Tracks {
		ek := keys[t.UnitId]
		if len(ek) == 0 {
			continue
		}
		// Keep the death keyframe (exact from the tracker) after the last frame.
		if n := len(t.Keyframes); n > 0 && t.Keyframes[n-1].State == pb.UnitState_UNIT_STATE_DEAD &&
			t.Keyframes[n-1].Loop > ek[len(ek)-1].Loop {
			ek = append(ek, t.Keyframes[n-1])
		}
		t.Keyframes = ek
	}
	tl.Meta.Fidelity = pb.Source_SOURCE_ENGINE
	return nil
}
