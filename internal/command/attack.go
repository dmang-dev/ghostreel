// SPDX-License-Identifier: Apache-2.0

package command

import (
	"math"
	"sort"

	pb "github.com/dmang-dev/ghostreel/gen/timeline/v1"
	"github.com/dmang-dev/ghostreel/internal/timeline"
)

// An attack-move walks toward its goal but stops to fight whatever enemy
// comes within reach, then carries on once the area is quiet. The tier can't
// see the fight itself, so "enemies" are the other players' units where the
// tracker tier puts them, which is most accurate exactly during fights (the
// replay samples units that deal or take damage).
type attackJob struct {
	goal    pt
	path    []pt
	pos     pt
	time    float64
	engaged bool
	quiet   float64 // loops without an enemy in reach while engaged
}

const (
	attackStep = 16 // loops between checks for enemies
)

// startAttackMove replaces the agent's plan with an attack-move to goal.
func (b *builder) startAttackMove(a *agent, t float64, goal pt) {
	from := b.current(a, t)
	a.legs, a.mining, a.goal = nil, nil, nil
	j := &attackJob{goal: goal, pos: from, time: t}
	j.path = b.route(a, from, goal)
	a.attack = j
}

// route is the path from a to b for this agent (nil when unreachable).
func (b *builder) route(a *agent, from, to pt) []pt {
	if a.typ.Air || b.opts.Grid == nil {
		return []pt{to}
	}
	if !b.opts.Grid.ReachableFor(from, to, sizeOf(a.typ)) {
		return nil
	}
	return b.opts.Grid.PathFor(from, to, sizeOf(a.typ))
}

// extendAttack steps an attack-move until its plan reaches past t.
func (b *builder) extendAttack(a *agent, t float64) {
	j := a.attack
	if j == nil {
		return
	}
	for j.time <= t {
		if j.engaged {
			hold := j.time + attackStep
			a.appendLeg(leg{t0: j.time, t1: hold, a: j.pos, b: j.pos})
			j.time = hold
			if b.enemyNear(a, j.pos, j.time) {
				j.quiet = 0
				continue
			}
			j.quiet += attackStep
			if j.quiet >= b.resumeAfter() {
				j.engaged = false
				j.path = b.route(a, j.pos, j.goal)
			}
			continue
		}
		if len(j.path) == 0 {
			// Arrived (or the goal is unreachable): the unit idles there.
			a.attack = nil
			return
		}
		j.pos, j.time, j.path = b.walkFor(a, j.pos, j.time, attackStep, j.path)
		if b.enemyNear(a, j.pos, j.time) {
			j.engaged, j.quiet = true, 0
		}
	}
}

// enemy is another player's unit at some loop.
type enemy struct {
	owner uint32
	x, y  float64
	r     float64
}

// enemiesAt returns every non-neutral unit's tracker-tier position at the
// start of t's 16-loop bucket (computed once per bucket).
func (b *builder) enemiesAt(t float64) []enemy {
	bucket := int(t) / attackStep
	if es, ok := b.enemies[bucket]; ok {
		return es
	}
	loop := uint32(bucket * attackStep)
	var es []enemy
	for _, u := range b.tl.Units {
		if u.Owner == 0 || !timeline.Alive(u, loop) {
			continue
		}
		typ := b.unitType(u, int64(loop))
		if typ.Class == pb.UnitClass_UNIT_CLASS_HIDDEN {
			continue
		}
		tr := b.tracks[u.Id]
		if tr == nil {
			continue
		}
		structure := typ.FootprintW > 0 && !typ.Air && typ.Speed == 0
		if !structure && !observedNear(tr, loop, b.enemyWindow()) {
			continue // where the tracker tier puts it is a guess
		}
		x, y, _ := timeline.PositionAt(tr, typ.Speed, loop)
		r := float64(typ.Radius)
		if typ.FootprintW > 0 && !typ.Air {
			r = float64(typ.FootprintW) / 2
		}
		es = append(es, enemy{owner: u.Owner, x: float64(x), y: float64(y), r: r})
	}
	b.enemies[bucket] = es
	return es
}

// enemyNear reports whether an enemy is within the agent's reach at p, t.
func (b *builder) enemyNear(a *agent, p pt, t float64) bool {
	if b.opts.NoEngage {
		return false
	}
	reach, ok := engageRange(a.typ, b.engageSlack())
	if !ok {
		return false
	}
	for _, e := range b.enemiesAt(t) {
		if int(e.owner) == a.owner {
			continue
		}
		d := reach + e.r
		if (e.x-p.X)*(e.x-p.X)+(e.y-p.Y)*(e.y-p.Y) <= d*d {
			return true
		}
	}
	return false
}

// engageRange is how close (beyond the target's radius) an enemy must be for
// the unit to stop and fight: weapon range plus a little acquisition slack.
// Units without a weapon never stop.
func engageRange(t *pb.UnitType, slack float64) (float64, bool) {
	switch t.Name {
	case "Medivac", "Raven", "Observer", "Overlord", "OverlordTransport", "Overseer", "WarpPrism", "MULE":
		return 0, false
	}
	r := math.Max(attackRange(t.Name), 0.5)
	return r + float64(t.Radius) + slack, true
}

func (b *builder) enemyWindow() float64 {
	if b.opts.EnemyWindow > 0 {
		return b.opts.EnemyWindow
	}
	return 40
}

// observedNear reports whether a tracker track has an observation (birth,
// sample, death) within w loops of loop.
func observedNear(tr *pb.Track, loop uint32, w float64) bool {
	ks := tr.Keyframes
	i := sort.Search(len(ks), func(i int) bool { return ks[i].Loop >= loop })
	for _, j := range []int{i - 1, i} {
		if j >= 0 && j < len(ks) && math.Abs(float64(ks[j].Loop)-float64(loop)) <= w {
			return true
		}
	}
	return false
}

func (b *builder) resumeAfter() float64 {
	if b.opts.ResumeAfter > 0 {
		return b.opts.ResumeAfter
	}
	return 16
}

func (b *builder) engageSlack() float64 {
	if b.opts.EngageSlack != 0 {
		return b.opts.EngageSlack
	}
	return 1
}
