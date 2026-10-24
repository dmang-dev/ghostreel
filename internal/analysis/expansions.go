// SPDX-License-Identifier: Apache-2.0

package analysis

import (
	"fmt"

	pb "github.com/dmang-dev/ghostreel/gen/timeline/v1"
	"github.com/dmang-dev/ghostreel/internal/timeline"
)

func init() { Register(Expansions{}) }

// Expansions lists every town hall placed after the start: when it was
// started and finished (or destroyed first), and where.
type Expansions struct{}

func (Expansions) ID() string { return "expansions" }

var townHalls = map[string]bool{"Nexus": true, "Hatchery": true, "CommandCenter": true}

func (Expansions) Analyze(tl *pb.Timeline) (*Report, error) {
	tracks := make(map[uint32]*pb.Track, len(tl.Tracks))
	for _, t := range tl.Tracks {
		tracks[t.UnitId] = t
	}
	rep := &Report{Title: "Expansions"}
	rows := map[uint32][]Row{}
	count := map[uint32]int{}
	for _, u := range tl.Units {
		if u.Owner == 0 || u.BornLoop == 0 || int(u.Type) >= len(tl.UnitTypes) || !townHalls[tl.UnitTypes[u.Type].Name] {
			continue
		}
		var x, y float32
		if t := tracks[u.Id]; t != nil && len(t.Keyframes) > 0 {
			x, y, _ = timeline.PositionAt(t, 0, u.BornLoop)
		}
		count[u.Owner]++
		done := "—"
		switch {
		case u.DoneLoop != nil:
			done = clock(*u.DoneLoop)
		case u.DiedLoop != nil:
			done = "destroyed " + clock(*u.DiedLoop)
		case !u.Constructed:
			done = clock(u.BornLoop)
		}
		name := tl.UnitTypes[u.Type].Name
		rows[u.Owner] = append(rows[u.Owner], Row{Loop: u.BornLoop, Cells: []string{
			clock(u.BornLoop), done, fmt.Sprintf("%d", count[u.Owner]+1), name,
		}})
		rep.Markers = append(rep.Markers, Marker{Loop: u.BornLoop, Player: u.Owner, X: x, Y: y, R: 3, Kind: "expansion",
			Label: fmt.Sprintf("%s takes base %d", playerName(tl, u.Owner), count[u.Owner]+1)})
	}
	for _, p := range tl.GetMeta().GetPlayers() {
		rep.Tables = append(rep.Tables, Table{Title: playerName(tl, p.Id), Player: p.Id,
			Columns: []string{"Started", "Finished", "Base", "Type"}, Rows: rows[p.Id]})
	}
	return rep, nil
}
