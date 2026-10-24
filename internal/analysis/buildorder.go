// SPDX-License-Identifier: Apache-2.0

package analysis

import (
	"fmt"
	"strings"

	pb "github.com/dmang-dev/ghostreel/gen/timeline/v1"
	"github.com/dmang-dev/ghostreel/internal/timeline"
)

func init() { Register(BuildOrder{Until: 22.4 * 60 * 10}) }

// BuildOrder lists what each player built, trained, morphed and researched,
// with the supply at the time, the way build orders are written down.
// Structures are listed when placed; units and research when finished,
// since replays don't record when those were queued.
// Workers are left out (the supply count carries them), as are units that
// appear without being ordered (larvae, broodlings, interceptors...).
type BuildOrder struct {
	Until uint32 // last loop to list; 0 lists the whole game
}

func (BuildOrder) ID() string { return "build-order" }

// mergeWindow is how close (loops) repeats must be to share a line.
const mergeWindow = 45

// notOrdered are army units that spawn rather than being built.
var notOrdered = map[string]bool{
	"Larva": true, "Egg": true, "Broodling": true, "BroodlingEscort": true, "Interceptor": true,
	"LocustMP": true, "LocustMPFlying": true, "LocustMPPrecursor": true, "AutoTurret": true,
	"PointDefenseDrone": true, "AdeptPhaseShift": true, "Changeling": true, "ChangelingMarine": true,
	"ChangelingMarineShield": true, "ChangelingZealot": true, "ChangelingZergling": true,
	"ChangelingZerglingWings": true, "CreepTumor": true, "CreepTumorBurrowed": true, "CreepTumorQueen": true,
	"MULE": true, "KD8Charge": true, "DisruptorPhased": true, "ParasiticBombDummy": true,
	"InfestedTerransEgg": true, "InfestorTerran": true,
}

// modeSuffixes mark morphs that switch a unit's mode rather than make
// something new (lifting off, sieging, burrowing...).
var modeSuffixes = []string{"Flying", "Lowered", "Burrowed", "Sieged", "Uprooted", "Phasing", "SiegeMode",
	"Cocoon", "Egg", "AG", "AP", "Assault", "Fighter", "BurrowedMP", "Burrowed"}

var modeMorphs = map[string]bool{
	"WarpGate": true, "Gateway": true, "HellionTank": true, "Hellion": true, "WidowMine": true,
	"SiegeTank": true, "Liberator": true, "Thor": true, "Observer": true, "Overseer": false,
	"SpineCrawler": true, "SporeCrawler": true, "SupplyDepot": true, "Barracks": true, "Factory": true,
	"Starport": true, "CommandCenter": true, "OrbitalCommand": false, "VikingFighter": true,
	"VikingAssault": true, "WarpPrism": true, "Disruptor": true, "Roach": true, "Infestor": true,
	"SwarmHostMP": true, "LurkerMP": false, "Zergling": true, "Baneling": false, "Ravager": false,
	"Hydralisk": true, "Ultralisk": true, "Queen": true, "Drone": true, "Lair": false, "Hive": false,
}

func isModeSwitch(name string) bool {
	if v, ok := modeMorphs[name]; ok {
		return v
	}
	for _, s := range modeSuffixes {
		if strings.HasSuffix(name, s) {
			return true
		}
	}
	return false
}

// listedUpgrade skips cosmetic "upgrades" (sprays, rewards, skins).
func listedUpgrade(name string) bool {
	for _, s := range []string{"Spray", "Reward", "Skin", "GameHeart", "Portrait", "Emote"} {
		if strings.Contains(name, s) {
			return false
		}
	}
	return true
}

func (b BuildOrder) Analyze(tl *pb.Timeline) (*Report, error) {
	type item struct {
		loop   uint32
		supply string
		text   string
		count  int
	}
	items := map[uint32][]item{}
	// Latest stats per player, for supply.
	supply := map[uint32]*pb.PlayerStats{}
	last := b.Until
	if last == 0 {
		last = ^uint32(0)
	}
	typeName := func(t uint32) (string, *pb.UnitType) {
		if int(t) >= len(tl.UnitTypes) {
			return "", nil
		}
		ut := tl.UnitTypes[t]
		return ut.Name, ut
	}
	listable := func(ut *pb.UnitType) bool {
		if ut == nil || notOrdered[ut.Name] {
			return false
		}
		return ut.Class == pb.UnitClass_UNIT_CLASS_ARMY || ut.Class == pb.UnitClass_UNIT_CLASS_BUILDING
	}
	add := func(player, loop uint32, text string) {
		s := ""
		if st := supply[player]; st != nil {
			s = fmt.Sprintf("%d", int(st.SupplyUsed))
		}
		list := items[player]
		// The same thing again within a couple of seconds is one line,
		// "Zergling ×8", like written build orders.
		if n := len(list); n > 0 && list[n-1].text == text && loop-list[n-1].loop <= mergeWindow {
			list[n-1].count++
			return
		}
		items[player] = append(list, item{loop, s, text, 1})
	}
	for _, e := range tl.Events {
		if e.Loop > last {
			break
		}
		switch k := e.Kind.(type) {
		case *pb.Event_PlayerStats:
			supply[k.PlayerStats.Player] = k.PlayerStats
		case *pb.Event_UnitBorn:
			u := tl.Units[k.UnitBorn.UnitId]
			name, ut := typeName(u.Type)
			if u.Owner == 0 || u.BornLoop == 0 || !listable(ut) {
				continue
			}
			add(u.Owner, e.Loop, name) // structures when placed, units when done
		case *pb.Event_Morph:
			u := tl.Units[k.Morph.UnitId]
			name, ut := typeName(k.Morph.Type)
			if !listable(ut) || isModeSwitch(name) {
				continue
			}
			owner := timeline.OwnerAt(u, e.Loop)
			if owner == 0 {
				continue
			}
			add(owner, e.Loop, name)
		case *pb.Event_Upgrade:
			up := k.Upgrade
			if up.Player == 0 || up.Count <= 0 || e.Loop == 0 || !listedUpgrade(up.Name) {
				continue
			}
			add(up.Player, e.Loop, up.Name+" (done)")
		}
	}
	rep := &Report{Title: "Build order"}
	if b.Until > 0 {
		rep.Title = fmt.Sprintf("Build order (first %s)", clock(b.Until))
	}
	for _, p := range tl.GetMeta().GetPlayers() {
		t := Table{Title: playerName(tl, p.Id), Player: p.Id, Columns: []string{"Time", "Supply", "Item"}}
		for _, it := range items[p.Id] {
			text := it.text
			if it.count > 1 {
				text = fmt.Sprintf("%s ×%d", text, it.count)
			}
			t.Rows = append(t.Rows, Row{Loop: it.loop, Cells: []string{clock(it.loop), it.supply, text}})
		}
		rep.Tables = append(rep.Tables, t)
	}
	return rep, nil
}
