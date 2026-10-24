// SPDX-License-Identifier: Apache-2.0

package command

import (
	"math"
	"strings"

	pb "github.com/dmang-dev/ghostreel/gen/timeline/v1"
	"github.com/dmang-dev/ghostreel/internal/timeline"
)

const (
	// stimLoops is how long Stimpack lasts: 11 s on the in-game clock.
	stimLoops = 246
	// stimFactor is Stimpack's speed bonus.
	stimFactor = 1.5
)

// creepFactor is how much faster a Zerg ground unit moves on creep.
func creepFactor(name string) float64 {
	if name == "Queen" {
		return 2.67 // 0.94 off creep, 2.5 on it
	}
	return 1.3
}

// legSpeed is the agent's speed (cells per loop) at t, without creep.
func (a *agent) legSpeed(t float64) float64 {
	if t < a.stimUntil {
		return a.speed * stimFactor
	}
	return a.speed
}

// appendLeg adds a leg, extending the previous one instead when it continues
// in the same direction at the same speed (keeps timelines small).
func (a *agent) appendLeg(l leg) {
	if l.t1 <= l.t0 && l.a == l.b {
		return
	}
	if n := len(a.legs); n > 0 {
		p := &a.legs[n-1]
		if p.t1 == l.t0 && p.b == l.a {
			pd, ld := p.t1-p.t0, l.t1-l.t0
			pvx, pvy := (p.b.X-p.a.X)/pd, (p.b.Y-p.a.Y)/pd
			lvx, lvy := (l.b.X-l.a.X)/ld, (l.b.Y-l.a.Y)/ld
			if pd > 0 && ld > 0 && math.Abs(pvx-lvx) < 1e-6 && math.Abs(pvy-lvy) < 1e-6 {
				p.t1, p.b = l.t1, l.b
				return
			}
		}
	}
	a.legs = append(a.legs, l)
}

// walk appends legs that follow path from `from` starting at loop start, at
// the agent's speed (stim, creep), and returns where and when it ends. Legs
// are split where a stim runs out.
func (b *builder) walk(a *agent, from pt, start float64, path []pt) (pt, float64) {
	cur, tt := from, start
	for _, p := range path {
		for {
			d := math.Hypot(p.X-cur.X, p.Y-cur.Y)
			if d < 1e-6 {
				break
			}
			v := a.legSpeed(tt) * b.creepAverage(a, cur, p, tt)
			reach := tt + d/v
			if tt < a.stimUntil && reach > a.stimUntil {
				f := (a.stimUntil - tt) * v / d
				mid := pt{X: cur.X + (p.X-cur.X)*f, Y: cur.Y + (p.Y-cur.Y)*f}
				a.appendLeg(leg{t0: tt, t1: a.stimUntil, a: cur, b: mid})
				cur, tt = mid, a.stimUntil
				continue
			}
			a.appendLeg(leg{t0: tt, t1: reach, a: cur, b: p})
			cur, tt = p, reach
			break
		}
	}
	return cur, tt
}

// walkFor moves along path for at most budget loops, consuming waypoints it
// reaches, and returns the new position, time and remaining path.
func (b *builder) walkFor(a *agent, from pt, start, budget float64, path []pt) (pt, float64, []pt) {
	cur, tt := from, start
	end := start + budget
	for len(path) > 0 && tt < end {
		p := path[0]
		d := math.Hypot(p.X-cur.X, p.Y-cur.Y)
		if d < 1e-6 {
			path = path[1:]
			continue
		}
		v := a.legSpeed(tt) * b.creepAverage(a, cur, p, tt)
		limit := end
		if tt < a.stimUntil && a.stimUntil < limit {
			limit = a.stimUntil
		}
		if reach := tt + d/v; reach <= limit {
			a.appendLeg(leg{t0: tt, t1: reach, a: cur, b: p})
			cur, tt = p, reach
			path = path[1:]
			continue
		}
		f := (limit - tt) * v / d
		next := pt{X: cur.X + (p.X-cur.X)*f, Y: cur.Y + (p.Y-cur.Y)*f}
		a.appendLeg(leg{t0: tt, t1: limit, a: cur, b: next})
		cur, tt = next, limit
	}
	return cur, tt, path
}

// ---------------------------------------------------------------- creep

type creepSource struct{ x, y, r float64 }

// creepAverage is the speed factor creep gives a Zerg ground unit on the
// segment a→b (1 for everyone else): the time-weighted mean over the
// segment's share on creep.
func (b *builder) creepAverage(a *agent, p, q pt, t float64) float64 {
	if a.typ.Air || !b.zerg[a.owner] {
		return 1
	}
	d := math.Hypot(q.X-p.X, q.Y-p.Y)
	n := int(d) + 1
	on := 0
	for i := 0; i <= n; i++ {
		f := float64(i) / float64(n)
		if b.onCreep(p.X+(q.X-p.X)*f, p.Y+(q.Y-p.Y)*f, t) {
			on++
		}
	}
	share := float64(on) / float64(n+1)
	c := creepFactor(a.typ.Name)
	return 1 / ((1 - share) + share/c)
}

// onCreep approximates creep as discs around Zerg bases and creep tumors
// alive at t (creep spreads and recedes gradually; this ignores both).
func (b *builder) onCreep(x, y, t float64) bool {
	bucket := int(t) / 160
	src, ok := b.creep[bucket]
	if !ok {
		loop := uint32(bucket * 160)
		for _, u := range b.tl.Units {
			if !b.zerg[int(u.Owner)] || !timeline.Alive(u, loop) {
				continue
			}
			name := b.unitType(u, int64(loop)).Name
			r := 0.0
			switch {
			case name == "Hatchery" || name == "Lair" || name == "Hive":
				r = 12
			case strings.HasPrefix(name, "CreepTumor"):
				r = 10
			default:
				continue
			}
			tr := b.tracks[u.Id]
			if tr == nil || len(tr.Keyframes) == 0 {
				continue
			}
			k := tr.Keyframes[0]
			src = append(src, creepSource{float64(k.X), float64(k.Y), r})
		}
		b.creep[bucket] = src
	}
	for _, s := range src {
		if (x-s.x)*(x-s.x)+(y-s.y)*(y-s.y) <= s.r*s.r {
			return true
		}
	}
	return false
}

// big reports whether a unit needs two cells of room to pass (see pathing).
func big(t *pb.UnitType) bool { return t.Radius > 0.5 }
