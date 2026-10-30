// SPDX-License-Identifier: Apache-2.0

package assets

import (
	"path/filepath"
	"testing"

	"github.com/dmang-dev/ghostreel/internal/mapfile"
)

// TestInfo checks unit stats and names read from the game data against
// values that have held since Wings of Liberty.
func TestInfo(t *testing.T) {
	l := openLib(t)
	in := l.Info([]string{"Marine", "Thor", "MineralField", "MineralField750", "NotAUnit"},
		[]string{"Stimpack", "TerranInfantryWeaponsLevel1"}, []string{"CalldownMULE"})

	m := in.Units["Marine"]
	if m == nil || m.Name != "Marine" || m.Life != 45 || m.Minerals != 50 || m.Supply != 1 || m.BuildTime == 0 {
		t.Fatalf("Marine: %+v", m)
	}
	if len(m.Weapons) != 1 || m.Weapons[0].Range != 5 || m.Weapons[0].Damage != 6 || !m.Weapons[0].Air || !m.Weapons[0].Ground {
		t.Errorf("Marine weapons: %+v", m.Weapons)
	}
	bio := false
	for _, a := range m.Attributes {
		bio = bio || a == "Biological"
	}
	if !bio {
		t.Errorf("Marine attributes %v, want Biological", m.Attributes)
	}

	th := in.Units["Thor"]
	if th == nil || len(th.Weapons) != 2 {
		t.Fatalf("Thor: %+v", th)
	}
	if w := th.Weapons[0]; !w.Air || w.Ground || w.Attacks != 4 || w.Bonus["Light"] == 0 {
		t.Errorf("Thor's anti-air weapon: %+v, want 4 missiles, bonus against Light", w)
	}

	full, small := in.Units["MineralField"], in.Units["MineralField750"]
	if full == nil || small == nil || full.Resources == 0 || small.Resources >= full.Resources {
		t.Errorf("mineral fields hold %+v and %+v; the small one should hold less", full, small)
	}
	if _, ok := in.Units["NotAUnit"]; ok {
		t.Error("an unknown unit type got stats")
	}
	if in.Upgrades["TerranInfantryWeaponsLevel1"] == "" || in.Abilities["CalldownMULE"] == "" {
		t.Errorf("names: %v %v", in.Upgrades, in.Abilities)
	}
}

// TestLighting resolves a ladder map's lighting with its own overrides.
func TestLighting(t *testing.T) {
	l := openLib(t)
	path := filepath.Join("..", "..", "testdata", "maps", "LeyLinesAIE_v3.SC2Map")
	m, err := mapfile.Open(path)
	if err != nil {
		t.Skip(err)
	}
	lt := l.Lighting(m.Tileset, m.TerrainData, m.LightData)
	if lt == nil {
		t.Fatalf("no lighting %q", m.Tileset)
	}
	// The map overrides the ambient color and the fill multiplier.
	if lt.Ambient != [3]float64{0.564706, 0.549020, 0.450980} || lt.Fill.Multiplier != 3 {
		t.Errorf("map overrides not applied: %+v", lt)
	}
	if lt.Key.Direction == [3]float64{} || lt.Key.Direction[2] >= 0 {
		t.Errorf("key light direction %v, want one pointing down", lt.Key.Direction)
	}
	if l.Lighting("NoSuchTerrain", nil, nil) != nil {
		t.Error("an unknown light resolved")
	}
	// Ultralove's terrain, RedstoneEx1, names a lighting of another name
	// (Avernus in 4.10, which the map's LightData.xml adjusts; Redstone in
	// later versions).
	if u, err := mapfile.Open(filepath.Join("..", "..", "testdata", "maps", "UltraloveAIE_v2.SC2Map")); err == nil {
		if lt := l.Lighting(u.Tileset, u.TerrainData, u.LightData); lt == nil || lt.ID == u.Tileset {
			t.Errorf("Ultralove (terrain %s) lighting %+v, want the terrain's own lighting", u.Tileset, lt)
		}
	}
}
