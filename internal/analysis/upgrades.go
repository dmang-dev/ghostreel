// SPDX-License-Identifier: Apache-2.0

package analysis

import (
	"strings"
	"unicode"

	pb "github.com/dmang-dev/ghostreel/gen/timeline/v1"
)

func init() { Register(Upgrades{}) }

// Upgrades lists when each player's research finished, over the whole game.
// Replays record research only when it is done.
type Upgrades struct{}

func (Upgrades) ID() string { return "upgrades" }

func (Upgrades) Analyze(tl *pb.Timeline) (*Report, error) {
	rows := map[uint32][]Row{}
	for _, e := range tl.Events {
		up := e.GetUpgrade()
		if up == nil || up.Player == 0 || up.Count <= 0 || e.Loop == 0 || !listedUpgrade(up.Name) {
			continue
		}
		rows[up.Player] = append(rows[up.Player], Row{Loop: e.Loop, Cells: []string{clock(e.Loop), upgradeName(up.Name)}})
	}
	rep := &Report{Title: "Upgrades"}
	for _, p := range tl.GetMeta().GetPlayers() {
		rep.Tables = append(rep.Tables, Table{Title: playerName(tl, p.Id), Player: p.Id,
			Columns: []string{"Done", "Upgrade"}, Rows: rows[p.Id]})
	}
	return rep, nil
}

// upgradeNames are the in-game names of research whose data names differ.
var upgradeNames = map[string]string{
	"ShieldWall": "Combat Shield", "PunisherGrenades": "Concussive Shells", "HiSecAutoTracking": "Hi-Sec Auto Tracking",
	"TerranBuildingArmor": "Neosteel Armor", "HighCapacityBarrels": "Infernal Pre-Igniter", "DrillClaws": "Drilling Claws",
	"CycloneLockOnDamageUpgrade": "Mag-Field Accelerator", "BansheeCloak": "Cloaking Field",
	"BansheeSpeed": "Hyperflight Rotors", "PersonalCloaking": "Personal Cloaking", "RavenCorvidReactor": "Corvid Reactor",
	"LiberatorAGRangeUpgrade": "Advanced Ballistics", "BattlecruiserEnableSpecializations": "Weapon Refit",
	"MedivacCaduceusReactor": "Caduceus Reactor", "WarpGateResearch": "Warp Gate", "BlinkTech": "Blink",
	"AdeptPiercingAttack": "Resonating Glaives", "PsiStormTech": "Psionic Storm", "DarkTemplarBlinkUpgrade": "Shadow Stride",
	"ObserverGraviticBooster": "Gravitic Boosters", "GraviticDrive": "Gravitic Drive",
	"PhoenixRangeUpgrade": "Anion Pulse-Crystals", "VoidRaySpeedUpgrade": "Flux Vanes",
	"TempestGroundAttackUpgrade": "Tectonic Destabilizers", "zerglingmovementspeed": "Metabolic Boost",
	"zerglingattackspeed": "Adrenal Glands", "overlordspeed": "Pneumatized Carapace", "CentrificalHooks": "Centrifugal Hooks",
	"EvolveGroovedSpines": "Grooved Spines", "EvolveMuscularAugments": "Muscular Augments",
	"InfestorEnergyUpgrade": "Pathogen Glands", "DiggingClaws": "Adaptive Talons", "LurkerRange": "Seismic Spines",
}

// upgradeName is research's in-game name: from the table, or the data name
// split into words ("TerranInfantryWeaponsLevel1" is "Terran Infantry
// Weapons 1").
func upgradeName(name string) string {
	if n, ok := upgradeNames[name]; ok {
		return n
	}
	var b strings.Builder
	rs := []rune(name)
	for i, r := range rs {
		if i > 0 && (unicode.IsUpper(r) && !unicode.IsUpper(rs[i-1]) || unicode.IsDigit(r) && !unicode.IsDigit(rs[i-1])) {
			b.WriteByte(' ')
		}
		b.WriteRune(r)
	}
	return strings.Replace(b.String(), "Level ", "", 1)
}
