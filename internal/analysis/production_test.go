// SPDX-License-Identifier: Apache-2.0

package analysis

import (
	"strings"
	"testing"

	pb "github.com/dmang-dev/ghostreel/gen/timeline/v1"
)

func rows(tb Table) []string {
	var out []string
	for _, r := range tb.Rows {
		out = append(out, strings.Join(r.Cells, " | "))
	}
	return out
}

func TestProductionTerran(t *testing.T) {
	tl := terranStart()
	tl.Meta.Loops = 2344 // the Barracks stands 2240 loops: 1:40
	// A Barracks done at 104, which trains two Marines (403 loops each) and
	// builds a Reactor (placed 2.5 cells right, 0.5 down) from 1000 to 1500.
	rax := addUnit(tl, 1, typeIndex(tl, "Barracks", building), 0, 20, 20, nil)
	rax.Constructed, rax.DoneLoop = true, u32(104)
	for _, born := range []uint32{600, 1003} {
		m := unit(tl, 1, "Marine", army, born, nil)
		m.Creator = u32(rax.Id)
	}
	reactor := addUnit(tl, 1, typeIndex(tl, "BarracksReactor", building), 1000, 22.5, 19.5, nil)
	reactor.Constructed, reactor.DoneLoop = true, u32(1500)
	// It lifts off for 448 loops (20 s): not idle, just not there.
	rax.Morphs = []*pb.Morph{{Loop: 1800, Type: typeIndex(tl, "BarracksFlying", building)}, {Loop: 2248, Type: rax.Type}}
	// The CC turns into an Orbital at 1000 (busy for 560 loops before).
	tl.Units[0].Morphs = []*pb.Morph{{Loop: 1000, Type: typeIndex(tl, "OrbitalCommand", building)}}

	rep, err := Production{}.Analyze(tl)
	if err != nil {
		t.Fatal(err)
	}
	if len(rep.Tables) != 1 {
		t.Fatalf("tables = %+v", rep.Tables)
	}
	// Barracks: on the ground 2240 - 448 = 1792 loops; busy 197-1003
	// (marines) and 1000-1500 (reactor), so idle 104-197, 1500-1800 and
	// 2248-2344: 489 loops = 0:21, 27%.
	// CC: 2344 loops, busy 440-1000: idle 1784 = 1:19, 76%.
	want := []string{
		" | All army production | 0:21 | 27%",
		" | All town halls | 1:19 | 76%",
		"0:00 | CommandCenter | 1:19 | 76%",
		"0:04 | Barracks | 0:21 | 27%",
	}
	if got := rows(rep.Tables[0]); strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Errorf("rows\n got %q\nwant %q", got, want)
	}
	if len(rep.Series) != 1 || rep.Series[0].Title != "Idle production" {
		t.Fatalf("series = %+v", rep.Series)
	}
	// At 112 the Barracks is idle (the CC too); at 336 only the CC is.
	if p := rep.Series[0].Points; p[1].Value != 2 || p[3].Value != 1 {
		t.Errorf("idle at 112, 336 = %v, %v", p[1].Value, p[3].Value)
	}
}

func TestProductionWarpGatesAndLarvae(t *testing.T) {
	tl := game()
	tl.Meta.Loops = 3000
	// Two gates become warp gates at 1000; one Stalker warps in at 1200
	// (515-loop cooldown): 2 × 2000 gate-loops, 515 of them busy.
	for i := 0; i < 2; i++ {
		g := unit(tl, 1, "Gateway", building, 0, nil)
		g.Morphs = []*pb.Morph{{Loop: 1000, Type: typeIndex(tl, "WarpGate", building)}}
	}
	st := unit(tl, 1, "Stalker", army, 1200, nil)
	st.Constructed, st.DoneLoop = true, u32(1312)
	// Player 2's hatchery holds three larvae from 0 to 500, then two.
	tl.Meta.Players[1].Race = "Zerg"
	h := unit(tl, 2, "Hatchery", building, 0, nil)
	for _, died := range []uint32{500, 2000, 2000} {
		l := unit(tl, 2, "Larva", army, 0, u32(died))
		l.Creator = u32(h.Id)
	}
	rep, err := Production{}.Analyze(tl)
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]bool{}
	for _, tb := range rep.Tables {
		for _, r := range rows(tb) {
			got[r] = true
		}
	}
	// Gateways: idle 0-1000 each (2000 loops, 1:29, 100% of their time).
	// Warp gates: 4000 - 515 = 3485 loops idle = 2:35, 87%.
	for _, w := range []string{"0:00 | Gateway | 0:44 | 100%", "0:44 | Warp gates | 2:35 | 87%",
		" | All army production | 4:04 | 91%", "0:00 | Hatchery | 0:22 | 17%"} {
		if !got[w] {
			t.Errorf("missing row %q in %v", w, got)
		}
	}
}
