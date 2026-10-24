// SPDX-License-Identifier: Apache-2.0

package analysis

import (
	"fmt"
	"math"
	"sort"
	"strings"

	pb "github.com/dmang-dev/ghostreel/gen/timeline/v1"
	"github.com/dmang-dev/ghostreel/internal/timeline"
)

func init() { Register(Battles{}) }

// Battles finds fights: deaths of players' units close together in space
// and time. A death joins a battle when it falls within Radius cells of the
// battle's center and within Gap loops of its last death; battles with fewer
// than MinDeaths deaths are dropped.
type Battles struct {
	Radius    float64 // default 14 cells
	Gap       uint32  // default 336 loops (15 s on the clock)
	MinDeaths int     // default 6
}

func (Battles) ID() string { return "battles" }

// spawned units die in droves without a fight (or are not units at all).
var spawned = map[string]bool{
	"Larva": true, "Egg": true, "Broodling": true, "BroodlingEscort": true, "Interceptor": true,
	"LocustMP": true, "LocustMPFlying": true, "AutoTurret": true, "PointDefenseDrone": true,
	"AdeptPhaseShift": true, "CreepTumor": true, "CreepTumorBurrowed": true, "CreepTumorQueen": true,
	"MULE": true, "KD8Charge": true, "DisruptorPhased": true, "ParasiticBombDummy": true,
}

type death struct {
	loop   uint32
	x, y   float64
	owner  uint32
	killer uint32
	typ    *pb.UnitType
}

type battle struct {
	start, end uint32
	sx, sy     float64 // sums, for the center
	deaths     []death
}

func (b *battle) center() (float64, float64) {
	n := float64(len(b.deaths))
	return b.sx / n, b.sy / n
}

// radius is the farthest death from the center (at least 3 cells).
func (b *battle) radius() float64 {
	cx, cy := b.center()
	far := 3.0
	for _, d := range b.deaths {
		far = math.Max(far, math.Hypot(d.x-cx, d.y-cy))
	}
	return far
}

// playerDeaths lists the deaths of players' units, in order, leaving out
// spawned and UI-only units.
func playerDeaths(tl *pb.Timeline) []death {
	tracks := make(map[uint32]*pb.Track, len(tl.Tracks))
	for _, t := range tl.Tracks {
		tracks[t.UnitId] = t
	}
	var deaths []death
	for _, u := range tl.Units {
		if u.DiedLoop == nil {
			continue
		}
		loop := *u.DiedLoop
		owner := timeline.OwnerAt(u, loop)
		t := tl.UnitTypes[timeline.TypeAt(u, loop)]
		if owner == 0 || spawned[t.Name] || t.Class == pb.UnitClass_UNIT_CLASS_HIDDEN {
			continue
		}
		tr := tracks[u.Id]
		if tr == nil || len(tr.Keyframes) == 0 {
			continue
		}
		x, y, _ := timeline.PositionAt(tr, t.Speed, loop)
		deaths = append(deaths, death{loop, float64(x), float64(y), owner, u.KillerPlayer, t})
	}
	sort.Slice(deaths, func(i, j int) bool { return deaths[i].loop < deaths[j].loop })
	return deaths
}

// cluster groups deaths (in loop order) into fights: a death joins the
// running fight whose center is nearest within radius cells and whose last
// death is at most gap loops before it.
func cluster(deaths []death, radius float64, gap uint32) []*battle {
	var open, done []*battle
	for _, d := range deaths {
		var best *battle
		bestDist := radius
		for _, b := range open {
			if d.loop-b.end > gap {
				continue
			}
			cx, cy := b.center()
			if dist := math.Hypot(d.x-cx, d.y-cy); dist <= bestDist {
				best, bestDist = b, dist
			}
		}
		if best == nil {
			best = &battle{start: d.loop}
			open = append(open, best)
		}
		best.end = d.loop
		best.sx += d.x
		best.sy += d.y
		best.deaths = append(best.deaths, d)
		// Close fights that can no longer grow.
		keep := open[:0]
		for _, b := range open {
			if d.loop-b.end > gap {
				done = append(done, b)
			} else {
				keep = append(keep, b)
			}
		}
		open = keep
	}
	done = append(done, open...)
	sort.Slice(done, func(i, j int) bool { return done[i].start < done[j].start })
	return done
}

func (bt Battles) Analyze(tl *pb.Timeline) (*Report, error) {
	radius, gap, minDeaths := bt.Radius, bt.Gap, bt.MinDeaths
	if radius == 0 {
		radius = 14
	}
	if gap == 0 {
		gap = 336
	}
	if minDeaths == 0 {
		minDeaths = 6
	}
	done := cluster(playerDeaths(tl), radius, gap)

	players := tl.GetMeta().GetPlayers()
	rep := &Report{Title: "Battles"}
	cols := []string{"Time", "Length"}
	for _, p := range players {
		cols = append(cols, p.Name+" lost")
	}
	table := Table{Title: "Battles", Columns: cols}
	for _, b := range done {
		if len(b.deaths) < minDeaths {
			continue
		}
		lost := map[uint32]int{}
		cx, cy := b.center()
		for _, d := range b.deaths {
			lost[d.owner]++
		}
		var parts []string
		row := Row{Loop: b.start, Cells: []string{clock(b.start), clock(b.end - b.start)}}
		for _, p := range players {
			row.Cells = append(row.Cells, fmt.Sprint(lost[p.Id]))
			parts = append(parts, fmt.Sprintf("%s lost %d", p.Name, lost[p.Id]))
		}
		table.Rows = append(table.Rows, row)
		rep.Markers = append(rep.Markers, Marker{
			Loop: b.start, End: b.end, X: float32(cx), Y: float32(cy), R: float32(b.radius()),
			Label: "Battle: " + strings.Join(parts, ", "), Kind: "battle",
		})
	}
	rep.Tables = []Table{table}
	return rep, nil
}
