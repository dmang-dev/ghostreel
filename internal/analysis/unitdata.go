// SPDX-License-Identifier: Apache-2.0

package analysis

import (
	"sort"

	pb "github.com/dmang-dev/ghostreel/gen/timeline/v1"
	"github.com/dmang-dev/ghostreel/internal/timeline"
)

// Game numbers the timeline doesn't carry: supply costs, supply provided and
// production times. They are the 5.0 (Legacy of the Void) values, the same
// for every build, like gamedata.Stub; times are on the in-game clock.

// secs converts in-game seconds to loops.
func secs(s float64) uint32 { return uint32(s*22.4 + 0.5) }

// supplyCost is the supply a unit takes, by type name (every mode it has).
var supplyCost = map[string]float32{}

// supplyProvided is the supply a unit or structure gives once finished.
var supplyProvided = map[string]float32{}

func init() {
	for cost, names := range map[float32][]string{
		0.5: {"Zergling", "ZerglingBurrowed", "Baneling", "BanelingBurrowed", "BanelingCocoon"},
		1:   {"SCV", "Probe", "Drone", "DroneBurrowed", "Marine", "Reaper", "Observer", "ObserverSiegeMode"},
		2: {"Marauder", "Ghost", "Hellion", "HellionTank", "WidowMine", "WidowMineBurrowed", "VikingFighter",
			"VikingAssault", "Medivac", "Raven", "Zealot", "Stalker", "Sentry", "Adept", "HighTemplar", "DarkTemplar",
			"WarpPrism", "WarpPrismPhasing", "Phoenix", "MothershipCore", "Queen", "QueenBurrowed", "Roach",
			"RoachBurrowed", "Hydralisk", "HydraliskBurrowed", "Infestor", "InfestorBurrowed", "Mutalisk", "Corruptor"},
		3: {"Cyclone", "SiegeTank", "SiegeTankSieged", "Liberator", "LiberatorAG", "Banshee", "Disruptor", "Oracle",
			"Ravager", "RavagerBurrowed", "RavagerCocoon", "LurkerMP", "LurkerMPBurrowed", "LurkerMPEgg",
			"SwarmHostMP", "SwarmHostBurrowedMP", "Viper"},
		4: {"Immortal", "VoidRay", "Archon", "BroodLord", "BroodLordCocoon"},
		5: {"Tempest"},
		6: {"Thor", "ThorAP", "Battlecruiser", "Colossus", "Carrier", "Ultralisk", "UltraliskBurrowed"},
		8: {"Mothership"},
	} {
		for _, n := range names {
			supplyCost[n] = cost
		}
	}
	for given, names := range map[float32][]string{
		6: {"Hatchery", "Lair", "Hive"},
		8: {"SupplyDepot", "SupplyDepotLowered", "Pylon", "Overlord", "OverlordTransport", "OverlordCocoon",
			"TransportOverlordCocoon", "Overseer", "OverseerSiegeMode"},
		15: {"CommandCenter", "CommandCenterFlying", "OrbitalCommand", "OrbitalCommandFlying", "PlanetaryFortress", "Nexus"},
	} {
		for _, n := range names {
			supplyProvided[n] = given
		}
	}
}

// trainTime is how long a unit takes to train (or hatch from its egg).
var trainTime = map[string]uint32{
	"SCV": secs(12), "Marine": secs(18), "Marauder": secs(21), "Reaper": secs(32), "Ghost": secs(29),
	"Hellion": secs(21), "HellionTank": secs(21), "WidowMine": secs(21), "Cyclone": secs(32), "SiegeTank": secs(32),
	"Thor": secs(43), "VikingFighter": secs(30), "Medivac": secs(30), "Liberator": secs(43), "Raven": secs(34),
	"Banshee": secs(43), "Battlecruiser": secs(64),
	"Probe": secs(12), "Zealot": secs(27), "Sentry": secs(26), "Stalker": secs(30), "Adept": secs(30),
	"HighTemplar": secs(39), "DarkTemplar": secs(39), "Observer": secs(21), "WarpPrism": secs(36),
	"Immortal": secs(39), "Colossus": secs(54), "Disruptor": secs(36), "Phoenix": secs(25), "Oracle": secs(37),
	"VoidRay": secs(37), "Tempest": secs(43), "Carrier": secs(64), "Mothership": secs(79), "MothershipCore": secs(21),
	"Drone": secs(12), "Overlord": secs(18), "Zergling": secs(17), "Queen": secs(36), "Roach": secs(19),
	"Hydralisk": secs(24), "Infestor": secs(36), "SwarmHostMP": secs(29), "Ultralisk": secs(39),
	"Mutalisk": secs(24), "Corruptor": secs(29), "Viper": secs(29),
}

// warpCooldown is how long a warp gate waits after warping a unit in.
var warpCooldown = map[string]uint32{
	"Zealot": secs(20), "Adept": secs(20), "Stalker": secs(23), "Sentry": secs(23),
	"HighTemplar": secs(32), "DarkTemplar": secs(32),
}

// upgradeTime is how long a production building is busy morphing.
var upgradeTime = map[string]uint32{"OrbitalCommand": secs(25), "PlanetaryFortress": secs(36)}

// producers are the structures that train units, by type name, and whether
// they make workers. Lifted buildings and warp gates are not listed: a
// lifted building can't train, and warp gates are counted together.
var producers = map[string]bool{
	"Barracks": false, "Factory": false, "Starport": false, "Gateway": false, "RoboticsFacility": false,
	"Stargate": false, "CommandCenter": true, "OrbitalCommand": true, "PlanetaryFortress": true, "Nexus": true,
}

// displayName is the name players use for a unit type: modes folded into
// the unit (SiegeTankSieged is a Siege Tank) and cocoons into what they
// become.
func displayName(name string) string {
	if n, ok := displayNames[name]; ok {
		return n
	}
	return name
}

var displayNames = map[string]string{
	"SiegeTankSieged": "SiegeTank", "VikingFighter": "Viking", "VikingAssault": "Viking",
	"WidowMineBurrowed": "WidowMine", "HellionTank": "Hellbat", "LiberatorAG": "Liberator", "ThorAP": "Thor",
	"WarpPrismPhasing": "WarpPrism", "ObserverSiegeMode": "Observer", "ZerglingBurrowed": "Zergling",
	"BanelingBurrowed": "Baneling", "BanelingCocoon": "Baneling", "RoachBurrowed": "Roach",
	"RavagerBurrowed": "Ravager", "RavagerCocoon": "Ravager", "HydraliskBurrowed": "Hydralisk",
	"LurkerMP": "Lurker", "LurkerMPBurrowed": "Lurker", "LurkerMPEgg": "Lurker", "InfestorBurrowed": "Infestor",
	"SwarmHostMP": "SwarmHost", "SwarmHostBurrowedMP": "SwarmHost", "UltraliskBurrowed": "Ultralisk",
	"QueenBurrowed": "Queen", "BroodLordCocoon": "BroodLord", "DroneBurrowed": "Drone",
}

// typeName is the name of a unit type index.
func typeName(tl *pb.Timeline, t uint32) string {
	if int(t) < len(tl.UnitTypes) {
		return tl.UnitTypes[t].Name
	}
	return ""
}

// nameAt is a unit's type name at loop.
func nameAt(tl *pb.Timeline, u *pb.Unit, loop uint32) string {
	return typeName(tl, timeline.TypeAt(u, loop))
}

// endLoop is the loop a unit stops existing: its death or the game's end.
func endLoop(tl *pb.Timeline, u *pb.Unit) uint32 {
	if u.DiedLoop != nil {
		return *u.DiedLoop
	}
	return tl.GetMeta().GetLoops()
}

// chronoProducers are the structures Chrono Boost speeds up (Protoss).
var chronoProducers = map[string]bool{"Nexus": true, "Gateway": true, "RoboticsFacility": true, "Stargate": true}

// zergBuildings are the structures a Drone turns into.
var zergBuildings = map[string]bool{
	"Hatchery": true, "Extractor": true, "ExtractorRich": true, "SpawningPool": true, "EvolutionChamber": true,
	"RoachWarren": true, "BanelingNest": true, "SpineCrawler": true, "SporeCrawler": true, "HydraliskDen": true,
	"LurkerDenMP": true, "InfestationPit": true, "Spire": true, "NydusNetwork": true, "UltraliskCavern": true,
}

// lifetimes says when each unit takes supply: from the start of its
// production until it is gone.
type lifetimes struct {
	start, end []uint32 // by unit id
}

// unitLifetimes estimates, for every unit, when its production started
// (when the game takes its supply) and when it stopped counting:
//
//   - Zerg units start at their egg's morph, which the timeline records.
//   - Warp-ins start when placed, which is their birth.
//   - Units from a building start their train time before they appear. A
//     Protoss building can't start one before its previous unit is out, which
//     corrects most of Chrono Boost (it makes production faster, so the train
//     time would put the start too early).
//   - A Drone stops counting when it starts a building. The replay says it
//     died only when the building finished, so it is matched to that.
func unitLifetimes(tl *pb.Timeline) lifetimes {
	lt := lifetimes{make([]uint32, len(tl.Units)), make([]uint32, len(tl.Units))}
	lastOut := map[uint32]uint32{} // creator → latest child birth so far
	var drones []*pb.Unit          // dead drones, by death loop
	for _, u := range tl.Units {
		lt.end[u.Id] = endLoop(tl, u)
		lt.start[u.Id] = u.BornLoop
		if u.BornLoop == 0 || u.Constructed {
			continue
		}
		if u.Creator != nil && int(*u.Creator) < len(tl.Units) {
			c := tl.Units[*u.Creator]
			if s, ok := eggStart(tl, c, u.BornLoop); ok {
				lt.start[u.Id] = s
				continue
			}
			s := u.BornLoop - min(u.BornLoop, trainTime[typeName(tl, u.Type)])
			if chronoProducers[nameAt(tl, c, u.BornLoop)] {
				s = max(s, lastOut[c.Id])
			}
			lastOut[c.Id] = u.BornLoop
			lt.start[u.Id] = s
			continue
		}
		lt.start[u.Id] = u.BornLoop - min(u.BornLoop, trainTime[typeName(tl, u.Type)])
	}
	for _, u := range tl.Units {
		if u.DiedLoop != nil && typeName(tl, u.Type) == "Drone" {
			drones = append(drones, u)
		}
	}
	sort.Slice(drones, func(i, j int) bool { return *drones[i].DiedLoop < *drones[j].DiedLoop })
	used := map[uint32]bool{}
	for _, b := range tl.Units {
		if !b.Constructed || b.DoneLoop == nil || !zergBuildings[typeName(tl, b.Type)] {
			continue
		}
		// The drone that died closest after the building finished.
		done := *b.DoneLoop
		i := sort.Search(len(drones), func(i int) bool { return *drones[i].DiedLoop >= done })
		for ; i < len(drones) && *drones[i].DiedLoop <= done+droneLag; i++ {
			d := drones[i]
			if d.Owner == b.Owner && !used[d.Id] && d.BornLoop <= b.BornLoop {
				used[d.Id] = true
				lt.end[d.Id] = b.BornLoop
				break
			}
		}
	}
	return lt
}

// droneLag is how long after its building finishes a Drone is reported dead.
const droneLag = 32

// eggStart is when the egg a unit hatched from at loop born was started.
func eggStart(tl *pb.Timeline, egg *pb.Unit, born uint32) (uint32, bool) {
	start, found := uint32(0), false
	for _, m := range egg.Morphs {
		if m.Loop > born {
			break
		}
		if typeName(tl, m.Type) == "Egg" {
			start, found = m.Loop, true
		}
	}
	return start, found
}
