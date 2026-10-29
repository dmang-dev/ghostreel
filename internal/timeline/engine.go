// SPDX-License-Identifier: Apache-2.0

package timeline

import (
	"fmt"
	"math"

	enginev1 "github.com/dmang-dev/ghostreel/gen/engine/v1"
	pb "github.com/dmang-dev/ghostreel/gen/timeline/v1"
)

// ApplyEngine replaces the tracks of every unit the engine recorded with its
// exact positions (SOURCE_ENGINE keyframes, one per engine frame), keeping
// the tracker's death keyframes, and adds their health, shields and energy
// when the recording has them. Units the engine never saw (inside
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
	vitals := map[uint32][]*pb.Vitals{}
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
			if v := vitalsOf(f.Loop, us); v != nil && changed(vitals[u.Id], v) {
				vitals[u.Id] = append(vitals[u.Id], v)
			}
		}
	}
	for _, t := range tl.Tracks {
		t.Vitals = vitals[t.UnitId]
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
	Simplify(tl, SimplifyTolerance)
	return nil
}

// vitalsOf is a sample's health, shields and energy, or nil when the
// recording has none (made before they were kept, or a unit without any).
func vitalsOf(loop uint32, us *enginev1.UnitSample) *pb.Vitals {
	if us.HealthMax == 0 && us.ShieldMax == 0 && us.EnergyMax == 0 {
		return nil
	}
	return &pb.Vitals{
		Loop: loop, Health: us.Health, HealthMax: us.HealthMax,
		Shield: us.Shield, ShieldMax: us.ShieldMax, Energy: us.Energy, EnergyMax: us.EnergyMax,
	}
}

// changed reports whether v is worth a key after the last of keys: a value
// moved by more than 1% of its maximum, or a maximum changed.
func changed(keys []*pb.Vitals, v *pb.Vitals) bool {
	if len(keys) == 0 {
		return true
	}
	p := keys[len(keys)-1]
	if p.HealthMax != v.HealthMax || p.ShieldMax != v.ShieldMax || p.EnergyMax != v.EnergyMax {
		return true
	}
	moved := func(a, b, max float32) bool { return max > 0 && math.Abs(float64(a-b)) > 0.01*float64(max) }
	return moved(p.Health, v.Health, v.HealthMax) || moved(p.Shield, v.Shield, v.ShieldMax) || moved(p.Energy, v.Energy, v.EnergyMax)
}
