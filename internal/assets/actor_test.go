// SPDX-License-Identifier: Apache-2.0

package assets

import (
	"errors"
	"reflect"
	"slices"
	"testing"

	"github.com/dmang-dev/ghostreel/internal/casc"
	"github.com/dmang-dev/ghostreel/internal/gamedata"
	"github.com/dmang-dev/ghostreel/internal/m3"
)

// A unit's actor and its template, in the shapes the game's data uses.
const testActors = `<?xml version="1.0" encoding="us-ascii"?>
<Catalog>
    <CActorUnit id="GenericUnit" default="1">
        <On Terms="ActorCreation" Send="AnimGroupRemove A"/>
    </CActorUnit>
    <CActorUnit id="Fortress" parent="GenericUnit" unitName="Fortress">
        <Scale value="0.500000"/>
        <On Terms="UnitBirth.FortressLifted" Send="AnimGroupRemove A"/>
        <On Terms="ActorCreation" Send="AnimGroupApply A"/>
        <On Terms="ActorCreation; ValidatePlayer HasUpgrade" Send="AnimGroupApply Upgraded"/>
        <On Terms="ActorCreation" Send="Create FortressGun"/>
        <On Terms="ActorCreation" Send="Create FortressFlash"/>
        <On Terms="ActorCreation" Send="Create FortressShadow"/>
        <On index="0" Terms="UnitBirth.FortressBurrowed" Send="AnimGroupApply Burrow Burrow Unburrow ClosingFull"/>
        <On Terms="Abil.Attack.Start" Send="Create FortressGun"/>
    </CActorUnit>
    <CActorModel id="ModelAnimationStyleOneShot" default="1"/>
    <CActorModel id="FortressGun">
        <Model value="Gun"/>
        <HostSiteOps Ops="SOpAttachWeapon01 GunOffset"/>
    </CActorModel>
    <CActorModel id="FortressFlash" parent="ModelAnimationStyleOneShot">
        <Model value="Gun"/>
    </CActorModel>
    <CActorSiteOpAttach id="SOpAttachWeapon01">
        <AttachQuery Methods="Weapon 1"/>
    </CActorSiteOpAttach>
    <CActorSiteOpLocalOffset id="GunOffset">
        <LocalOffset value="0.000000,-0.020000,0.250000"/>
    </CActorSiteOpLocalOffset>
</Catalog>`

const testModels = `<?xml version="1.0" encoding="us-ascii"?>
<Catalog>
    <CModel id="Fortress">
        <Model value="Assets\Fortress.m3"/>
        <ScaleMin value="0.800000,0.800000,0.800000"/>
        <ScaleMax value="1.000000,1.000000,1.000000"/>
    </CModel>
    <CModel id="Gun">
        <Model value="Assets\Gun.m3"/>
        <ScaleMax value="0.5"/>
    </CModel>
</Catalog>`

func TestUnitLook(t *testing.T) {
	l := &Library{
		actors: newCatalog(), models: newCatalog(),
		files:   map[string]string{"assets/fortress.m3": "f", "assets/gun.m3": "g"},
		actorOf: map[string]string{"Fortress": "Fortress", "FortressBurrowed": "Fortress", "FortressLifted": "Fortress"},
	}
	if err := l.actors.add([]byte(testActors), keepActor); err != nil {
		t.Fatal(err)
	}
	if err := l.models.add([]byte(testModels), func(c string) bool { return c == "CModel" }); err != nil {
		t.Fatal(err)
	}
	gun := attachment{Model: "Gun", Point: "Ref_Weapon 01", Offset: [3]float64{0, -0.02, 0.25}, Scale: [3]float64{0.75, 0.75, 0.75}}
	for _, c := range []struct {
		unit   string
		groups []string
	}{
		{"Fortress", []string{"A"}},                   // the template's removal comes first
		{"FortressBurrowed", []string{"A", "Burrow"}}, // the birth of a type adds to its creation
		{"FortressLifted", nil},                       // ... and takes away, though listed first
	} {
		lk := l.unitLook(c.unit, false)
		if lk.Scale != [3]float64{0.45, 0.45, 0.45} {
			t.Errorf("%s: scale %v, want the model's mean scale 0.9 times the actor's 0.5", c.unit, lk.Scale)
		}
		if !slices.Equal(lk.Groups, c.groups) {
			t.Errorf("%s: groups %v, want %v", c.unit, lk.Groups, c.groups)
		}
		// One-shot models, actors with no model and attacks don't count.
		if !reflect.DeepEqual(lk.Attach, []attachment{gun}) {
			t.Errorf("%s: attachments %+v, want %+v", c.unit, lk.Attach, gun)
		}
	}
	if ex := (look{Scale: [3]float64{1, 1, 1}}).extras(); len(ex) != 0 {
		t.Errorf("plain look has extras %v", ex)
	}
}

func TestAttachPoint(t *testing.T) {
	for in, want := range map[string]string{
		"Weapon": "Ref_Weapon", "Weapon 1": "Ref_Weapon 01", "Weapon Left": "Ref_Weapon Left",
		"Target 12": "Ref_Target 12", "AMBestMatchWeapon": "", "": "",
	} {
		if got := attachPoint(in); got != want {
			t.Errorf("attachPoint(%q) = %q, want %q", in, got, want)
		}
	}
}

// TestLadderLooks checks the game's own data: the sizes known units are
// drawn at, the variants the Command Center and Gateway models are drawn
// in, and that every attachment's point exists on its host model.
func TestLadderLooks(t *testing.T) {
	l := openLib(t)
	for unit, want := range map[string]float64{
		"Marine": 1, "Zergling": 0.77, "Ultralisk": 0.85, "Archon": 0.8, "Mothership": 0.6, "Thor": 0.5, "SiegeTank": 0.8,
	} {
		if s := l.unitLook(unit, false).Scale; s != [3]float64{want, want, want} {
			t.Errorf("%s: scale %v, want %v", unit, s, want)
		}
	}
	for unit, want := range map[string][]string{
		"CommandCenter": nil, "PlanetaryFortress": {"A"}, "OrbitalCommand": {"B"}, "OrbitalCommandFlying": {"Fly", "B"},
		"Gateway": nil, "WarpGate": {"Superior"}, "RoachBurrowed": {"Burrow"}, "SupplyDepotLowered": {"Burrow"},
	} {
		if g := l.unitLook(unit, false).Groups; !slices.Equal(g, want) {
			t.Errorf("%s: groups %v, want %v", unit, g, want)
		}
	}
	n := 0
	for _, u := range gamedata.Names() {
		lk := l.unitLook(u, false)
		if len(lk.Attach) == 0 {
			continue
		}
		name, err := l.ModelPath(u, false)
		if err != nil {
			t.Errorf("%s: %v", u, err)
			continue
		}
		b, err := l.st.ReadFile(name)
		if errors.Is(err, casc.ErrNotLocal) {
			continue
		}
		if err != nil {
			t.Fatal(err)
		}
		m, err := m3.Parse(b)
		if err != nil {
			t.Fatalf("%s: %v", u, err)
		}
		for _, a := range lk.Attach {
			n++
			if a.Point != "" && !slices.ContainsFunc(m.Attachments, func(p m3.Attachment) bool { return p.Name == a.Point }) {
				t.Errorf("%s: attaches %s at %q, which its model doesn't have", u, a.Model, a.Point)
			}
			t.Logf("%-20s %s at %q", u, a.Model, a.Point)
		}
	}
	if n == 0 {
		t.Error("no ladder unit has an attachment")
	}
}

// TestLookInGLB checks that a unit's look and its model's attachment points
// reach the glTF the viewer loads.
func TestLookInGLB(t *testing.T) {
	l := openLib(t)
	b, err := l.UnitGLB("PlanetaryFortress", false)
	if errors.Is(err, casc.ErrNotLocal) {
		t.Skip("model not in this game data")
	}
	if err != nil {
		t.Fatal(err)
	}
	g, err := parseGLB(b)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(g.extras.Groups, []string{"A"}) || !slices.Equal(g.extras.Scale, []float64{0.8, 0.8, 0.8}) {
		t.Errorf("extras %+v, want groups [A] and scale 0.8", g.extras)
	}
	if g.extras.Attachments["Ref_Origin"] == "" {
		t.Errorf("no Ref_Origin among the attachment points %v", g.extras.Attachments)
	}
}
