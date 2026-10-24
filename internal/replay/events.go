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
	KindSelection                       // Player, Selection: a change to the active selection
	KindControlGroup                    // Player, ControlGroup: set / add / recall / clear a group
)

var kindNames = [...]string{"", "unit_born", "unit_init", "unit_done", "unit_died", "unit_type_change",
	"unit_owner_change", "unit_positions", "player_stats", "upgrade", "chat", "command", "selection", "control_group"}

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

	// Creator is the unit that produced a UnitBorn unit (0 when unknown):
	// the building or larva it came out of.
	Creator Tag

	Positions []PositionSample
	Stats     *PlayerStats

	Name  string // upgrade name
	Count int64  // upgrade count (negative counts remove the upgrade)
	Text  string // chat text

	AbilityLink     int64
	AbilityCmdIndex int64
	HasTarget       bool // X, Y hold the target point
	TargetTag       Tag  // non-zero when the command targets a unit
	// TargetUpdate marks a command recorded as a target update (CmdUpdateTarget*):
	// the replay stores a repeated command as just its new target, reusing the
	// ability of the user's previous command (copied into AbilityLink here).
	TargetUpdate bool
	// Queued is set for shift-queued commands.
	Queued bool

	Selection    *SelectionChange
	ControlGroup *ControlGroupChange
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
	SupplyUsed, SupplyMade    float64
	Minerals, Vespene         int64
	MineralRate, VespeneRate  int64 // collection rate per in-game minute
	Workers                   int64
	ArmyMinerals, ArmyGas     int64 // value of the active army
	LostMinerals, LostGas     int64 // total resources lost (army + economy + tech)
	KilledMinerals, KilledGas int64
}

// MaskKind says how a selection mask names the units to remove.
type MaskKind uint8

const (
	MaskNone        MaskKind = iota // remove nothing
	MaskBits                        // Bits[i] set: remove the unit at index i
	MaskOneIndices                  // remove the units at Indices
	MaskZeroIndices                 // keep only the units at Indices
)

// Mask selects units of an ordered selection by index.
type Mask struct {
	Kind    MaskKind
	Bits    []bool
	Indices []int
}

// Removes reports whether the unit at index i (of n) is removed.
func (m Mask) Removes(i int) bool {
	switch m.Kind {
	case MaskBits:
		return i < len(m.Bits) && m.Bits[i]
	case MaskOneIndices:
		for _, j := range m.Indices {
			if j == i {
				return true
			}
		}
	case MaskZeroIndices:
		for _, j := range m.Indices {
			if j == i {
				return false
			}
		}
		return true
	}
	return false
}

// Subgroup describes a run of added units of one type, in the order the
// game sorts selections (higher priority first).
type Subgroup struct {
	UnitLink           int64
	Priority, IntraPri int64
	Count              int
}

// SelectionChange is a SelectionDelta game event: remove the units the mask
// names from the selection, then add AddTags.
type SelectionChange struct {
	Group        int // 10 is the active selection
	Remove       Mask
	AddTags      []Tag
	AddSubgroups []Subgroup
}

// Control group update types.
const (
	GroupSet            = 0
	GroupAppend         = 1
	GroupRecall         = 2
	GroupClear          = 3
	GroupSetAndSteal    = 4
	GroupAppendAndSteal = 5
)

// ControlGroupChange is a ControlGroupUpdate game event.
type ControlGroupChange struct {
	Index  int
	Update int
	Mask   Mask
}
