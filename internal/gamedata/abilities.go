// SPDX-License-Identifier: Apache-2.0

package gamedata

// AbilityKind is what a command does, as far as movement is concerned.
type AbilityKind uint8

const (
	AbilityUnknown AbilityKind = iota // anything else (build, train, cast…)
	AbilityMove                       // move, or right-click on the ground
	AbilityPatrol
	AbilityHold
	AbilityAttackMove // attack (or scan-move) toward a point: stops at enemies on the way
	AbilityAttack     // attack a unit
	AbilityStop
	AbilityStim        // Stimpack: faster for a while
	AbilitySpeedBoost  // Medivac afterburners
	AbilityRally       // set where new units go
	AbilityRallyWorker // set where new workers go
)

var abilityNames = [...]string{"unknown", "move", "patrol", "hold", "attack-move", "attack", "stop", "stim", "speed-boost", "rally", "rally-workers"}

func (k AbilityKind) String() string { return abilityNames[k] }

type abilityPack struct {
	from  int
	links map[int64]string
}

// Ability classifies a replay command. Ability links are build-specific
// indexes into the game's ability catalog; the table comes from sc2reader's
// LotV datapacks. link -1 is a right-click ("smart") command, whose meaning
// depends on the target: the caller decides.
func Ability(baseBuild int, link, cmdIndex int64, targetsUnit bool) AbilityKind {
	if link < 0 {
		return AbilityMove
	}
	var pack *abilityPack
	for i := range abilityPacks {
		if abilityPacks[i].from <= baseBuild {
			pack = &abilityPacks[i]
		}
	}
	if pack == nil {
		return AbilityUnknown
	}
	switch pack.links[link] {
	case "move":
		switch cmdIndex {
		case 0:
			return AbilityMove
		case 1:
			return AbilityPatrol
		case 2:
			return AbilityHold
		case 3:
			return AbilityAttackMove // ScanMove
		}
	case "attack":
		if targetsUnit {
			return AbilityAttack
		}
		return AbilityAttackMove
	case "stop":
		if cmdIndex == 0 {
			return AbilityStop
		}
	case "stim":
		return AbilityStim
	case "speedboost":
		return AbilitySpeedBoost
	case "rally":
		return AbilityRally
	case "rally-workers":
		return AbilityRallyWorker
	case "rally-hatchery":
		if cmdIndex == 1 {
			return AbilityRallyWorker
		}
		return AbilityRally
	}
	return AbilityUnknown
}
