// SPDX-License-Identifier: Apache-2.0

// Package accuracy measures a reconstructed timeline against engine ground
// truth: for every unit in every engine frame, how far is the timeline's
// position from the real one? It is the yardstick for the tracker and
// command tiers.
package accuracy

import (
	"math"
	"sort"

	enginev1 "github.com/OWNER/ghostreel/gen/engine/v1"
	pb "github.com/OWNER/ghostreel/gen/timeline/v1"
	"github.com/OWNER/ghostreel/internal/timeline"
)

// Stats summarizes position errors in cells.
type Stats struct {
	N                 int
	Mean, Median, P90 float64
	Max               float64
	Within1, Within5  float64 // fraction of samples within 1 / 5 cells
	errs              []float64
}

func (s *Stats) add(e float64) { s.errs = append(s.errs, e) }

func (s *Stats) finish() {
	s.N = len(s.errs)
	if s.N == 0 {
		return
	}
	sort.Float64s(s.errs)
	sum := 0.0
	w1, w5 := 0, 0
	for _, e := range s.errs {
		sum += e
		if e <= 1 {
			w1++
		}
		if e <= 5 {
			w5++
		}
	}
	s.Mean = sum / float64(s.N)
	s.Median = s.errs[s.N/2]
	s.P90 = s.errs[s.N*9/10]
	s.Max = s.errs[s.N-1]
	s.Within1 = float64(w1) / float64(s.N)
	s.Within5 = float64(w5) / float64(s.N)
	s.errs = nil
}

// Report is the comparison result.
type Report struct {
	All    Stats             // every matched sample of a unit the timeline has alive
	Moving Stats             // samples where the unit moved since the previous frame
	ByType map[string]*Stats // by engine unit type name (moving and still)
	// ByClass groups by the timeline's unit class (army, worker, building…).
	ByClass map[string]*Stats

	Frames int
	// Unmatched counts engine units with no timeline unit of the same tag, by
	// type; Lifecycle counts samples where the unit exists in both but the
	// timeline has it not yet born or already dead.
	Unmatched map[string]int
	Lifecycle int
	Matched   int
}

// Compare evaluates tl at every engine frame.
func Compare(tl *pb.Timeline, s *enginev1.Samples) *Report {
	r := &Report{ByType: map[string]*Stats{}, ByClass: map[string]*Stats{}, Unmatched: map[string]int{}}
	ix := timeline.NewIndex(tl)
	byTag := make(map[uint32]*pb.Unit, len(tl.Units))
	for _, u := range tl.Units {
		byTag[u.Tag] = u
	}
	tracks := make([]*pb.Track, len(tl.Units))
	for _, t := range tl.Tracks {
		tracks[t.UnitId] = t
	}
	names := map[uint32]string{}
	for _, t := range s.UnitTypes {
		names[t.Id] = t.Name
	}
	type pos struct{ x, y float32 }
	prev := map[uint64]pos{}
	for _, f := range s.Frames {
		r.Frames++
		cur := make(map[uint64]pos, len(f.Units))
		for _, us := range f.Units {
			cur[us.Tag] = pos{us.X, us.Y}
			name := names[us.UnitType]
			u := byTag[EngineTag(us.Tag)]
			if u == nil {
				r.Unmatched[name]++
				continue
			}
			if !timeline.Alive(u, f.Loop) {
				r.Lifecycle++
				continue
			}
			r.Matched++
			typ := timeline.TypeAt(u, f.Loop)
			x, y, _ := timeline.PositionAt(tracks[u.Id], ix.TL.UnitTypes[typ].Speed, f.Loop)
			e := math.Hypot(float64(x-us.X), float64(y-us.Y))
			r.All.add(e)
			if p, ok := prev[us.Tag]; ok && math.Hypot(float64(p.x-us.X), float64(p.y-us.Y)) > 0.25 {
				r.Moving.add(e)
			}
			st := r.ByType[name]
			if st == nil {
				st = &Stats{}
				r.ByType[name] = st
			}
			st.add(e)
			cls := className(tl.UnitTypes[typ].Class)
			cs := r.ByClass[cls]
			if cs == nil {
				cs = &Stats{}
				r.ByClass[cls] = cs
			}
			cs.add(e)
		}
		prev = cur
	}
	r.All.finish()
	r.Moving.finish()
	for _, st := range r.ByType {
		st.finish()
	}
	for _, st := range r.ByClass {
		st.finish()
	}
	return r
}

// EngineTag converts an s2client unit tag to the replay's packed tag. The
// client's 64-bit tag carries the replay tag (index << 18 | recycle) in its
// low 32 bits.
func EngineTag(t uint64) uint32 { return uint32(t) }

func className(c pb.UnitClass) string {
	switch c {
	case pb.UnitClass_UNIT_CLASS_WORKER:
		return "worker"
	case pb.UnitClass_UNIT_CLASS_BUILDING:
		return "building"
	case pb.UnitClass_UNIT_CLASS_RESOURCE:
		return "resource"
	case pb.UnitClass_UNIT_CLASS_NEUTRAL:
		return "neutral"
	case pb.UnitClass_UNIT_CLASS_HIDDEN:
		return "hidden"
	}
	return "army"
}
