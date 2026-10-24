// SPDX-License-Identifier: Apache-2.0

package analysis

import (
	"fmt"
	"math"
	"sort"
	"strings"

	pb "github.com/dmang-dev/ghostreel/gen/timeline/v1"
)

func init() { Register(Hotspots{}) }

// Hotspots finds the moments worth watching, each with a place, a span, a
// score and a tier (major or minor). The viewer's director camera pans to
// them, and video export cuts highlight reels from them.
//
// The detectors follow Adjutant's (the SC2 highlight generator): battles,
// army wipes, worker raids, base kills, high-value kills, transport kills,
// siege breaks, multi-prong attacks, first blood and the end of the game.
// Fights are weighed by the resources each side lost (the replay's player
// stats), not just by how many units died.
type Hotspots struct{}

func (Hotspots) ID() string { return "hotspots" }

// Weights per category, as in Adjutant's defaults.
var hotspotWeight = map[string]float64{
	"battle": 1.0, "army_wipe": 1.0, "base_kill": 0.9, "game_end": 1.0, "multi_prong": 0.75,
	"worker_raid": 0.7, "siege_break": 0.7, "transport_kill": 0.65, "high_value_kill": 0.6, "first_blood": 0.6,
}

var hotspotName = map[string]string{
	"battle": "Battle", "army_wipe": "Army wipe", "base_kill": "Base destroyed", "game_end": "End of the game",
	"multi_prong": "Multi-prong attack", "worker_raid": "Worker raid", "siege_break": "Siege broken",
	"transport_kill": "Transport down", "high_value_kill": "High-value kill", "first_blood": "First blood",
}

var highValue = map[string]bool{
	"Battlecruiser": true, "Thor": true, "ThorAP": true, "SiegeTank": true, "SiegeTankSieged": true,
	"Liberator": true, "LiberatorAG": true, "Raven": true, "Banshee": true, "Ghost": true,
	"Mothership": true, "Carrier": true, "Tempest": true, "Colossus": true, "Disruptor": true,
	"Archon": true, "HighTemplar": true, "DarkTemplar": true, "Immortal": true, "VoidRay": true,
	"BroodLord": true, "Ultralisk": true, "Viper": true, "Infestor": true, "InfestorBurrowed": true,
	"SwarmHostMP": true, "LurkerMP": true, "LurkerMPBurrowed": true, "Ravager": true,
}

var transports = map[string]bool{"Medivac": true, "WarpPrism": true, "WarpPrismPhasing": true, "OverlordTransport": true}

var siegeUnits = map[string]bool{
	"SiegeTankSieged": true, "LiberatorAG": true, "Bunker": true, "PhotonCannon": true,
	"ShieldBattery": true, "SpineCrawler": true, "SporeCrawler": true, "PlanetaryFortress": true,
}

var bases = map[string]bool{
	"CommandCenter": true, "CommandCenterFlying": true, "OrbitalCommand": true, "OrbitalCommandFlying": true,
	"PlanetaryFortress": true, "Nexus": true, "Hatchery": true, "Lair": true, "Hive": true,
}

// MajorLoss is the resources lost (both sides) that make a fight major.
const MajorLoss = 1500

type hotspot struct {
	start, end uint32
	x, y, r    float64
	category   string
	label      string
	raw        float64 // weight times strength, before normalizing
	major      bool
}

// lossTracker answers how many resources a player had lost by a loop.
type lossTracker struct {
	loops map[uint32][]uint32
	lost  map[uint32][]float64
}

func newLossTracker(tl *pb.Timeline) *lossTracker {
	lt := &lossTracker{loops: map[uint32][]uint32{}, lost: map[uint32][]float64{}}
	for _, e := range tl.Events {
		if ps := e.GetPlayerStats(); ps != nil {
			lt.loops[ps.Player] = append(lt.loops[ps.Player], e.Loop)
			lt.lost[ps.Player] = append(lt.lost[ps.Player], float64(ps.LostMinerals+ps.LostVespene))
		}
	}
	return lt
}

// at is the latest cumulative loss reported at or before loop.
func (lt *lossTracker) at(player, loop uint32) float64 {
	ls := lt.loops[player]
	i := sort.Search(len(ls), func(i int) bool { return ls[i] > loop })
	if i == 0 {
		return 0
	}
	return lt.lost[player][i-1]
}

// between is what a player lost from just before start to the first stats
// after end (stats come every 160 loops).
func (lt *lossTracker) between(player, start, end uint32) float64 {
	from := lt.at(player, start)
	to := lt.at(player, end+160)
	return math.Max(0, to-from)
}

func (Hotspots) Analyze(tl *pb.Timeline) (*Report, error) {
	deaths := playerDeaths(tl)
	lt := newLossTracker(tl)
	players := tl.GetMeta().GetPlayers()
	name := func(id uint32) string { return playerName(tl, id) }
	var hs []hotspot
	isWorker := func(d death) bool { return d.typ.Class == pb.UnitClass_UNIT_CLASS_WORKER }
	isArmy := func(d death) bool { return d.typ.Class == pb.UnitClass_UNIT_CLASS_ARMY }

	// Fights: clusters of non-worker deaths; worker deaths alone are raids.
	var fighting, workers []death
	for _, d := range deaths {
		if isWorker(d) {
			workers = append(workers, d)
		} else {
			fighting = append(fighting, d)
		}
	}
	var fights []hotspot
	for _, b := range cluster(fighting, 14, 336) {
		if len(b.deaths) < 6 {
			continue
		}
		cx, cy := b.center()
		lost := map[uint32]float64{}
		total := 0.0
		for _, p := range players {
			lost[p.Id] = lt.between(p.Id, b.start, b.end)
			total += lost[p.Id]
		}
		var parts []string
		for _, p := range players {
			parts = append(parts, fmt.Sprintf("%s lost %d", p.Name, int(lost[p.Id])))
		}
		h := hotspot{start: b.start, end: b.end, x: cx, y: cy, r: b.radius(), category: "battle",
			label: fmt.Sprintf("%d units died; %s", len(b.deaths), strings.Join(parts, ", ")),
			raw:   math.Max(total, float64(len(b.deaths))*50), major: total >= MajorLoss || len(b.deaths) >= 25}
		fights = append(fights, h)
		// Army wipe: 6+ army units dying within 3 s inside the fight.
		for i := range b.deaths {
			n, j := 0, i
			for ; j < len(b.deaths) && b.deaths[j].loop-b.deaths[i].loop <= 67; j++ {
				if isArmy(b.deaths[j]) {
					n++
				}
			}
			if n >= 6 {
				w := &battle{start: b.deaths[i].loop}
				for _, d := range b.deaths[i:j] {
					w.end, w.sx, w.sy = d.loop, w.sx+d.x, w.sy+d.y
					w.deaths = append(w.deaths, d)
				}
				wx, wy := w.center()
				hs = append(hs, hotspot{start: w.start, end: w.end, x: wx, y: wy, r: w.radius(), category: "army_wipe",
					label: fmt.Sprintf("%d units killed in 3 s", n), raw: float64(n) * 150, major: true})
				break // one per fight
			}
		}
		// Siege broken: 2+ siege units or static defenses dying in the fight.
		sieges := 0
		for _, d := range b.deaths {
			if siegeUnits[d.typ.Name] {
				sieges++
			}
		}
		if sieges >= 2 {
			hs = append(hs, hotspot{start: b.start, end: b.end, x: cx, y: cy, r: b.radius(), category: "siege_break",
				label: fmt.Sprintf("%d siege units or defenses destroyed", sieges), raw: float64(sieges) * 200})
		}
	}
	hs = append(hs, fights...)

	// Worker raids: 4+ workers killed together.
	for _, b := range cluster(workers, 12, 180) {
		if len(b.deaths) < 4 {
			continue
		}
		cx, cy := b.center()
		owners := map[uint32]int{}
		for _, d := range b.deaths {
			owners[d.owner]++
		}
		var victim uint32
		for p, n := range owners {
			if n > owners[victim] {
				victim = p
			}
		}
		hs = append(hs, hotspot{start: b.start, end: b.end, x: cx, y: cy, r: b.radius(), category: "worker_raid",
			label: fmt.Sprintf("%d of %s's workers killed", owners[victim], name(victim)), raw: float64(len(b.deaths)) * 100,
			major: len(b.deaths) >= 10})
	}

	// Single deaths that matter: bases, high-value units, transports.
	lastHV := -1 // index in hs of the latest high-value kill
	for i, d := range deaths {
		switch {
		case bases[d.typ.Name]:
			hs = append(hs, hotspot{start: d.loop - min(d.loop, 224), end: d.loop + 112, x: d.x, y: d.y, r: 5, category: "base_kill",
				label: fmt.Sprintf("%s's %s destroyed", name(d.owner), d.typ.Name), raw: 1200, major: true})
		case highValue[d.typ.Name]:
			if lastHV >= 0 && d.loop <= hs[lastHV].end+112 && math.Hypot(d.x-hs[lastHV].x, d.y-hs[lastHV].y) < 16 {
				hs[lastHV].end = d.loop + 45
				hs[lastHV].label += ", " + d.typ.Name
				hs[lastHV].raw += 250
				continue
			}
			hs = append(hs, hotspot{start: d.loop - min(d.loop, 90), end: d.loop + 45, x: d.x, y: d.y, r: 4, category: "high_value_kill",
				label: fmt.Sprintf("%s's %s", name(d.owner), d.typ.Name), raw: 300})
			lastHV = len(hs) - 1
		case transports[d.typ.Name]:
			// Loaded if other units died right there (their cargo).
			cargo := 0
			for _, o := range deaths[max(0, i-40):min(len(deaths), i+40)] {
				if o.loop+67 >= d.loop && o.loop <= d.loop+67 && !transports[o.typ.Name] && !isWorker(o) &&
					math.Hypot(o.x-d.x, o.y-d.y) < 8 && o.owner == d.owner {
					cargo++
				}
			}
			if cargo >= 2 {
				hs = append(hs, hotspot{start: d.loop - min(d.loop, 90), end: d.loop + 67, x: d.x, y: d.y, r: 5, category: "transport_kill",
					label: fmt.Sprintf("Loaded %s shot down (+%d units)", d.typ.Name, cargo), raw: 300 + float64(cargo)*100})
			}
		}
	}

	// First blood: the first army unit (not a structure) to die.
	for _, d := range deaths {
		if isArmy(d) && !strings.Contains(d.typ.Name, "Changeling") {
			hs = append(hs, hotspot{start: d.loop - min(d.loop, 112), end: d.loop + 67, x: d.x, y: d.y, r: 5, category: "first_blood",
				label: fmt.Sprintf("%s's %s", name(d.owner), d.typ.Name), raw: 400})
			break
		}
	}

	// Multi-prong: two fights or raids under way at once, 40+ cells apart.
	var action []hotspot
	for _, h := range hs {
		if h.category == "battle" || h.category == "worker_raid" {
			action = append(action, h)
		}
	}
	sort.Slice(action, func(i, j int) bool { return action[i].start < action[j].start })
	lastMP := uint32(0)
	for i := range action {
		for j := i + 1; j < len(action) && action[j].start <= action[i].end; j++ {
			a, b := action[i], action[j]
			if d := math.Hypot(a.x-b.x, a.y-b.y); d >= 40 && (lastMP == 0 || a.start > lastMP+448) {
				hs = append(hs, hotspot{start: min(a.start, b.start), end: max(a.end, b.end), x: (a.x + b.x) / 2, y: (a.y + b.y) / 2,
					r: d / 2, category: "multi_prong", label: fmt.Sprintf("Fighting in two places %d cells apart", int(d)),
					raw: (a.raw + b.raw) / 2})
				lastMP = a.start
			}
		}
	}

	// The end: the last 20 s, where the last deaths happened.
	if loops := tl.GetMeta().GetLoops(); loops > 0 && len(deaths) > 0 {
		var xs, ys float64
		n := 0
		for i := len(deaths) - 1; i >= 0 && n < 12; i-- {
			if loops-deaths[i].loop > 1344 && n > 0 {
				break
			}
			// Without deaths in the last minute, the last one marks the place.
			xs, ys, n = xs+deaths[i].x, ys+deaths[i].y, n+1
			if loops-deaths[i].loop > 1344 {
				break
			}
		}
		if n > 0 {
			winner := ""
			for _, p := range players {
				if p.Result == "Win" || p.Result == "Victory" {
					winner = p.Name + " wins"
				}
			}
			if winner == "" {
				winner = "The game ends"
			}
			hs = append(hs, hotspot{start: loops - min(loops, 448), end: loops, x: xs / float64(n), y: ys / float64(n), r: 10,
				category: "game_end", label: winner, raw: 1500, major: true})
		}
	}

	// Score: weight times strength, normalized to the strongest; the end of
	// the game always ranks high.
	maxRaw := 1.0
	for _, h := range hs {
		maxRaw = math.Max(maxRaw, h.raw*hotspotWeight[h.category])
	}
	sort.Slice(hs, func(i, j int) bool { return hs[i].start < hs[j].start })
	rep := &Report{Title: "Hotspots"}
	table := Table{Title: "Hotspots", Columns: []string{"Time", "Tier", "What", "Details"}}
	for _, h := range hs {
		score := h.raw * hotspotWeight[h.category] / maxRaw
		if h.category == "game_end" {
			score = math.Min(1, score+0.3)
		}
		// Major: big in absolute terms, or among the game's best moments
		// (early fights lose less but are just as decisive).
		tier := "minor"
		if h.major || score >= 0.6 {
			tier = "major"
		}
		rep.Markers = append(rep.Markers, Marker{
			Loop: h.start, End: h.end, X: float32(h.x), Y: float32(h.y), R: float32(math.Max(h.r, 3)),
			Label: hotspotName[h.category] + ": " + h.label, Kind: "hotspot",
			Category: h.category, Score: float32(math.Round(score*1000) / 1000), Tier: tier,
		})
		table.Rows = append(table.Rows, Row{Loop: h.start, Cells: []string{clock(h.start), tier, hotspotName[h.category], h.label}})
	}
	rep.Tables = []Table{table}
	return rep, nil
}
