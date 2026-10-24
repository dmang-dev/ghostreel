// SPDX-License-Identifier: Apache-2.0

package replay

// Kind says which fields of an Event are meaningful.
type Kind uint8

const (
	KindUnitBorn        Kind = iota + 1 // unit appears complete: Tag, UnitType, Player, X, Y
	KindUnitInit                        // building placed, under construction: Tag, UnitType, Player, X, Y
	KindUnitDone                        // construction finished: Tag
	KindUnitDied                        // Tag, X, Y, KillerPlayer
	KindUnitTypeChange                  // morph: Tag, UnitType
	KindUnitOwnerChange                 // Tag, Player
	KindUnitPositions                   // Positions (by unit index; see PositionSample)
	KindPlayerStats                     // Player, Stats
	KindUpgrade                         // Player, Name, Count
	KindChat                            // Player, Text
	KindCommand                         // Player, AbilityLink, AbilityCmdIndex, HasTarget/X/Y, TargetTag
)

var kindNames = [...]string{"", "unit_born", "unit_init", "unit_done", "unit_died", "unit_type_change",
	"unit_owner_change", "unit_positions", "player_stats", "upgrade", "chat", "command"}

func (k Kind) String() string {
	if int(k) < len(kindNames) {
		return kindNames[k]
	}
	return "unknown"
}

// Event is one replay event in a form that doesn't depend on s2prot, so the
// timeline builder can be driven by hand-written fixtures in tests.
// Coordinates are in map cells; Player is the 1-based player ID used by
// tracker events (0 = neutral).
type Event struct {
	Loop int64
	Kind Kind

	Tag      Tag
	UnitType string
	Player   int
	X, Y     float64

	KillerPlayer int // 0 when unknown

	Positions []PositionSample
	Stats     *PlayerStats

	Name  string // upgrade name
	Count int64  // upgrade count (negative counts remove the upgrade)
	Text  string // chat text

	AbilityLink     int64
	AbilityCmdIndex int64
	HasTarget       bool // X, Y hold the target point
	TargetTag       Tag  // non-zero when the command targets a unit
}

// PositionSample is one entry of a UnitPositions tracker event. It names the
// unit by slot index only (no recycle counter), so it refers to whichever
// unit holds that slot at the event's loop.
type PositionSample struct {
	Index uint32
	X, Y  float64
}

// PlayerStats is the subset of the periodic score snapshot the viewer shows.
// Supply values are already converted from the game's ×4096 fixed point.
type PlayerStats struct {
	SupplyUsed, SupplyMade   float64
	Minerals, Vespene        int64
	MineralRate, VespeneRate int64 // collection rate per in-game minute
	Workers                  int64
	ArmyMinerals, ArmyGas    int64 // value of the active army
	LostMinerals, LostGas    int64 // total resources lost (army + economy + tech)
	KilledMinerals, KilledGas int64
}
