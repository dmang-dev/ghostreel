// SPDX-License-Identifier: Apache-2.0

// Package gamedata answers per-unit-type questions (size, speed, whether it is
// a building) for a given base build. The Stub provider is a small checked-in
// table of common ladder units; a provider generated from the game's own data
// files by the future CASC extractor can replace it behind the Provider
// interface. Unknown types always get usable defaults, never an error.
package gamedata

import (
	"regexp"
	"sort"
	"strconv"
	"strings"
)

// Class groups unit types for rendering and stats.
type Class uint8

const (
	ClassArmy Class = iota
	ClassWorker
	ClassBuilding
	ClassResource
	ClassNeutral // destructible rocks, towers, critters
	ClassHidden  // UI-only units the game never draws (beacons, dummies)
)

var classNames = [...]string{"army", "worker", "building", "resource", "neutral", "hidden"}

func (c Class) String() string { return classNames[c] }

// Unit describes one unit type.
type Unit struct {
	Name   string
	Class  Class
	Radius float64 // collision radius in cells
	// FootprintW/H is the placement footprint in cells (buildings, resources).
	FootprintW, FootprintH int
	// Speed is the movement speed in cells per game second (Normal speed).
	// 0 for things that never move.
	Speed float64
	// Movable marks buildings that can relocate (lift off, uproot), so the
	// timeline must not pin them to their placement cell.
	Movable bool
	Air     bool
	// Sight is the vision radius in cells (for fog of war).
	Sight float64
	// Known is false when the type wasn't in the table and defaults were used.
	Known bool
}

// IsStructure reports whether the type occupies a fixed footprint.
func (u Unit) IsStructure() bool {
	return u.Class == ClassBuilding || u.Class == ClassResource || (u.Class == ClassNeutral && u.FootprintW > 0)
}

// Provider looks up unit types for a replay's base build.
type Provider interface {
	Unit(baseBuild int, name string) Unit
}

// Stub is the built-in table. Values are current-patch approximations and the
// same for every build; the extractor will make them exact per build.
type Stub struct{}

var _ Provider = Stub{}

// DefaultSpeed is used for unknown mobile units (cells per game second).
const DefaultSpeed = 2.5

var footprintRe = regexp.MustCompile(`(\d+)x(\d+)`)

// Unit implements Provider.
func (Stub) Unit(_ int, name string) Unit {
	if u, ok := table[name]; ok {
		u.Name, u.Known = name, true
		if u.Class == ClassBuilding && u.Radius == 0 {
			u.Radius = float64(u.FootprintW) / 2
		}
		u.Sight = sightFor(u)
		return u
	}
	u := guess(name)
	u.Sight = sightFor(u)
	return u
}

// guess classifies unknown names by the conventions Blizzard's data follows.
func guess(name string) Unit {
	u := Unit{Name: name}
	switch {
	case strings.Contains(name, "MineralField"):
		u.Class, u.FootprintW, u.FootprintH, u.Radius = ClassResource, 2, 1, 1
	case strings.Contains(name, "Geyser"):
		u.Class, u.FootprintW, u.FootprintH, u.Radius = ClassResource, 3, 3, 1.5
	case strings.HasPrefix(name, "Beacon"), strings.Contains(name, "Dummy"), strings.HasSuffix(name, "Escort"):
		u.Class = ClassHidden
	case strings.Contains(name, "Rock"), strings.Contains(name, "Destructible"), strings.Contains(name, "Debris"),
		strings.Contains(name, "Inhibitor"), strings.Contains(name, "XelNaga"):
		u.Class = ClassNeutral
		u.FootprintW, u.FootprintH = 2, 2
		if m := footprintRe.FindStringSubmatch(name); m != nil {
			u.FootprintW, _ = strconv.Atoi(m[1])
			u.FootprintH, _ = strconv.Atoi(m[2])
		}
		u.Radius = float64(max(u.FootprintW, u.FootprintH)) / 2
	default:
		u.Class, u.Radius, u.Speed = ClassArmy, 0.5, DefaultSpeed
	}
	return u
}

// Shorthands for the table below.
func army(radius, speed float64) Unit { return Unit{Class: ClassArmy, Radius: radius, Speed: speed} }
func air(radius, speed float64) Unit {
	return Unit{Class: ClassArmy, Radius: radius, Speed: speed, Air: true}
}
func worker(radius, speed float64) Unit {
	return Unit{Class: ClassWorker, Radius: radius, Speed: speed}
}
func bld(w, h int) Unit   { return Unit{Class: ClassBuilding, FootprintW: w, FootprintH: h} }
func movable(u Unit) Unit { u.Movable = true; return u }
func flying(w int, speed float64) Unit {
	return Unit{Class: ClassBuilding, FootprintW: w, FootprintH: w, Radius: float64(w) / 2, Speed: speed, Movable: true, Air: true}
}
func uprooted(speed float64) Unit {
	return Unit{Class: ClassBuilding, FootprintW: 2, FootprintH: 2, Radius: 1, Speed: speed, Movable: true}
}
func static(radius float64) Unit { return Unit{Class: ClassArmy, Radius: radius} }
func hidden() Unit               { return Unit{Class: ClassHidden} }

// Speeds are cells per game second at Normal speed (multiply by 1.4 for the
// Faster clock). Radii are collision radii in cells.
var table = map[string]Unit{
	// Terran
	"SCV": worker(0.375, 2.8125), "MULE": worker(0.375, 2.8125),
	"Marine": army(0.375, 2.25), "Marauder": army(0.5625, 2.25), "Reaper": army(0.375, 3.75),
	"Ghost": army(0.375, 2.8125), "Hellion": army(0.625, 4.25), "HellionTank": army(0.625, 2.25),
	"SiegeTank": army(0.875, 2.25), "SiegeTankSieged": static(0.875), "Cyclone": army(0.75, 3.375),
	"WidowMine": army(0.5, 2.8125), "WidowMineBurrowed": static(0.5), "Thor": army(1.25, 1.875),
	"ThorAP": army(1.25, 1.875), "VikingFighter": air(0.75, 2.75), "VikingAssault": army(0.75, 2.25),
	"Medivac": air(0.75, 2.5), "Liberator": air(0.75, 3.375), "LiberatorAG": static(0.75),
	"Raven": air(0.625, 2.75), "Banshee": air(0.75, 2.75), "Battlecruiser": air(1.25, 1.875),
	"AutoTurret": static(0.75), "PointDefenseDrone": static(0.5),

	"CommandCenter": movable(bld(5, 5)), "OrbitalCommand": movable(bld(5, 5)), "PlanetaryFortress": bld(5, 5),
	"CommandCenterFlying": flying(5, 1), "OrbitalCommandFlying": flying(5, 1),
	"SupplyDepot": bld(2, 2), "SupplyDepotLowered": bld(2, 2), "Refinery": bld(3, 3), "RefineryRich": bld(3, 3),
	"Barracks": movable(bld(3, 3)), "BarracksFlying": flying(3, 1), "Factory": movable(bld(3, 3)),
	"FactoryFlying": flying(3, 1), "Starport": movable(bld(3, 3)), "StarportFlying": flying(3, 1),
	"BarracksTechLab": bld(2, 2), "BarracksReactor": bld(2, 2), "FactoryTechLab": bld(2, 2),
	"FactoryReactor": bld(2, 2), "StarportTechLab": bld(2, 2), "StarportReactor": bld(2, 2),
	"TechLab": bld(2, 2), "Reactor": bld(2, 2),
	"EngineeringBay": bld(3, 3), "Bunker": bld(3, 3), "MissileTurret": bld(2, 2), "SensorTower": bld(1, 1),
	"GhostAcademy": bld(3, 3), "Armory": bld(3, 3), "FusionCore": bld(3, 3),

	// Protoss
	"Probe": worker(0.375, 2.8125), "Zealot": army(0.5, 2.25), "Stalker": army(0.625, 2.9531),
	"Sentry": army(0.5, 2.5), "Adept": army(0.5, 2.5), "AdeptPhaseShift": army(0.5, 2.5),
	"HighTemplar": army(0.375, 2.0156), "DarkTemplar": army(0.5, 2.8125), "Archon": army(1, 2.8125),
	"Immortal": army(0.75, 2.25), "Colossus": army(1, 2.25), "Disruptor": army(0.5, 2.25),
	"DisruptorPhased": army(0.5, 4.25), "Observer": air(0.5, 1.875), "ObserverSiegeMode": static(0.5),
	"WarpPrism": air(0.875, 2.9531), "WarpPrismPhasing": static(0.875), "Phoenix": air(0.75, 4.25),
	"VoidRay": air(1, 2.75), "Oracle": air(0.75, 4), "Tempest": air(1.25, 2.25), "Carrier": air(1.25, 1.875),
	"Interceptor": air(0.25, 7.5), "Mothership": air(1.375, 1.875), "OracleStasisTrap": static(0.5),

	"Nexus": bld(5, 5), "Pylon": bld(2, 2), "Assimilator": bld(3, 3), "AssimilatorRich": bld(3, 3),
	"Gateway": bld(3, 3), "WarpGate": bld(3, 3), "Forge": bld(3, 3), "CyberneticsCore": bld(3, 3),
	"PhotonCannon": bld(2, 2), "ShieldBattery": bld(2, 2), "TwilightCouncil": bld(3, 3),
	"RoboticsFacility": bld(3, 3), "RoboticsBay": bld(3, 3), "Stargate": bld(3, 3), "FleetBeacon": bld(3, 3),
	"TemplarArchive": bld(3, 3), "DarkShrine": bld(2, 2),

	// Zerg
	"Drone": worker(0.375, 2.8125), "Larva": static(0.25), "Egg": static(0.5), "Overlord": air(1, 0.643),
	"OverlordTransport": air(1, 0.643), "Overseer": air(0.75, 1.875), "OverseerSiegeMode": static(0.75),
	"Queen": army(0.875, 0.9375), "Zergling": army(0.375, 2.9531), "Baneling": army(0.375, 2.5),
	"BanelingCocoon": static(0.5), "Roach": army(0.625, 2.25), "RoachBurrowed": army(0.625, 1.4),
	"Ravager": army(0.75, 2.75), "RavagerCocoon": static(0.75), "Hydralisk": army(0.625, 2.25),
	"LurkerMP": army(0.75, 2.9531), "LurkerMPBurrowed": static(0.75), "LurkerMPEgg": static(0.75),
	"Infestor": army(0.75, 2.25), "InfestorBurrowed": army(0.75, 2), "SwarmHostMP": army(0.8125, 2.25),
	"SwarmHostBurrowedMP": static(0.8125), "LocustMP": army(0.375, 1.875), "LocustMPFlying": air(0.375, 1.875),
	"Ultralisk": army(1, 2.9531), "Mutalisk": air(0.5, 4), "Corruptor": air(0.625, 3.375),
	"BroodLord": air(1, 1.9688), "BroodLordCocoon": static(1), "Broodling": army(0.25, 3.0),
	"Viper": air(0.75, 2.9531), "Changeling": army(0.375, 2.25), "ChangelingMarine": army(0.375, 2.25),
	"ChangelingMarineShield": army(0.375, 2.25), "ChangelingZealot": army(0.5, 2.25),
	"ChangelingZergling": army(0.375, 2.25), "ChangelingZerglingWings": army(0.375, 2.25),
	"NydusCanal": bld(3, 3), "CreepTumor": bld(1, 1), "CreepTumorBurrowed": bld(1, 1),
	"CreepTumorQueen": bld(1, 1),

	"Hatchery": bld(5, 5), "Lair": bld(5, 5), "Hive": bld(5, 5), "Extractor": bld(3, 3),
	"ExtractorRich": bld(3, 3), "SpawningPool": bld(3, 3), "EvolutionChamber": bld(3, 3),
	"RoachWarren": bld(3, 3), "BanelingNest": bld(3, 3), "HydraliskDen": bld(3, 3), "LurkerDenMP": bld(3, 3),
	"InfestationPit": bld(3, 3), "Spire": bld(3, 3), "GreaterSpire": bld(3, 3), "NydusNetwork": bld(3, 3),
	"UltraliskCavern": bld(3, 3),
	"SpineCrawler":    movable(bld(2, 2)), "SporeCrawler": movable(bld(2, 2)),
	"SpineCrawlerUprooted": uprooted(1), "SporeCrawlerUprooted": uprooted(1),

	// UI-only units that show up in tracker events.
	"InvisibleTargetDummy": hidden(), "BroodlingEscort": hidden(),
}

// sightOverrides lists vision radii that differ from the class default
// (current-patch values, rounded).
var sightOverrides = map[string]float64{
	"CommandCenter": 11, "OrbitalCommand": 11, "PlanetaryFortress": 11, "Nexus": 11,
	"Hatchery": 12, "Lair": 12, "Hive": 12, "MissileTurret": 11, "PhotonCannon": 11,
	"SporeCrawler": 11, "SpineCrawler": 11, "Bunker": 10, "SensorTower": 12,
	"SiegeTank": 11, "SiegeTankSieged": 11, "Ghost": 11, "Thor": 11, "Raven": 11,
	"Observer": 11, "ObserverSiegeMode": 13.75, "Overlord": 11, "Overseer": 11,
	"OverseerSiegeMode": 13.75, "Colossus": 10, "Tempest": 12, "Carrier": 12,
	"Battlecruiser": 12, "Viper": 11, "BroodLord": 12, "Mothership": 14, "Oracle": 10,
	"Larva": 5, "Egg": 5, "Zergling": 8, "Broodling": 7, "Interceptor": 7,
}

// sightFor fills in a type's vision radius.
func sightFor(u Unit) float64 {
	if s, ok := sightOverrides[u.Name]; ok {
		return s
	}
	switch {
	case u.Class == ClassWorker:
		return 8
	case u.Class == ClassResource || u.Class == ClassNeutral || u.Class == ClassHidden:
		return 0
	case u.Class == ClassBuilding:
		return 9
	case u.Air:
		return 10
	}
	return 9
}

// Names lists the unit types this package knows (the current ladder pool's
// units and structures), sorted, without UI-only types.
func Names() []string {
	var out []string
	for n, u := range table {
		if u.Class != ClassHidden {
			out = append(out, n)
		}
	}
	sort.Strings(out)
	return out
}
