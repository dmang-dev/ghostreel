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
		actors: newCatalog(), models: newCatalog(), reqs: newCatalog(),
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
		lk := l.unitLook(c.unit, false, nil)
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

// An actor with idle animations and upgrades, and the validators and
// requirements upgrades go through, in the game data's shapes.
const testIdleActors = `<?xml version="1.0" encoding="us-ascii"?>
<Catalog>
    <CActorUnit id="Walker" unitName="Walker">
        <On Terms="ActorCreation" Send="AnimPlay CoverStand Cover,Stand PlayForever 0.000000"/>
        <On Terms="UnitBirth.WalkerBurrowed" Send="AnimBracketStart Burrow Burrow IGNORE Unburrow ClosingFull,OpeningPlayForever,Instant"/>
        <On Terms="UnitBirth.WalkerSieged" Send="AnimBracketStart Morph IGNORE Morph,Stand Morph,End"/>
        <On Terms="UnitBirth.WalkerSieged" Send="AnimPlay Flash Spell"/>
        <On Terms="UnitBirth.WalkerPlain" Send="AnimClear CoverStand"/>
        <On Terms="ActorCreation" Target="Other" Send="AnimPlay Wrong Wrong PlayForever"/>
        <On Terms="ActorCreation; ValidatePlayer HasWings" Send="ModelSwap WalkerWings"/>
        <On Terms="Upgrade.Spikes.Add; ValidateUnit NotDeadOrConstruction" Send="AnimGroupApply Spiked"/>
        <On Terms="ActorCreation; ValidateUnit HasSpikes" Send="AnimGroupApply Spiked"/>
        <On Terms="ActorCreation; ValidateUnit HasCargo" Send="AnimGroupApply Loaded"/>
        <On Terms="ActorCreation; ValidatePlayer HasNoShield" Send="AnimGroupApply Bare"/>
        <On Terms="Upgrade.Shield.Add" Send="StatusSet Shield 1"/>
        <On Terms="ActorCreation" Send="AnimGroupApply Removed"/>
        <Remove Terms="ActorCreation" Send="AnimGroupApply Removed"/>
    </CActorUnit>
</Catalog>`

const testIdleModels = `<?xml version="1.0" encoding="us-ascii"?>
<Catalog>
    <CModel id="Walker"><Model value="Assets\Walker.m3"/></CModel>
    <CModel id="WalkerWings"><Model value="Assets\WalkerWings.m3"/><ScaleMax value="2"/></CModel>
</Catalog>`

const testReqs = `<?xml version="1.0" encoding="us-ascii"?>
<Catalog>
    <CValidatorPlayerRequirement id="HasWings"><Find value="1"/><Value value="HaveWings"/></CValidatorPlayerRequirement>
    <CRequirement id="HaveWings"><NodeArray index="Use" Link="CountUpgradeWings"/></CRequirement>
    <CRequirementCountUpgrade id="CountUpgradeWings"><Count Link="Wings" State="CompleteOnly"/></CRequirementCountUpgrade>
    <CValidatorCombine id="HasSpikes"><CombineArray value="NoSpikes"/><Negate value="1"/></CValidatorCombine>
    <CValidatorPlayerRequirement id="NoSpikes"><Find value="1"/><Value value="SpikesNotResearched"/></CValidatorPlayerRequirement>
    <CRequirement id="SpikesNotResearched"><NodeArray index="Use" Link="NotCountUpgradeSpikes"/></CRequirement>
    <CRequirementNot id="NotCountUpgradeSpikes"><OperandArray index="0" value="CountUpgradeSpikes"/></CRequirementNot>
    <CRequirementCountUpgrade id="CountUpgradeSpikes"><Count Link="Spikes"/></CRequirementCountUpgrade>
    <CValidatorPlayerRequirement id="HasNoShield"><Value value="HaveShield"/></CValidatorPlayerRequirement>
    <CRequirement id="HaveShield"><NodeArray index="Use" Link="CountUpgradeShield"/></CRequirement>
    <CRequirementCountUpgrade id="CountUpgradeShield"><Count Link="Shield"/></CRequirementCountUpgrade>
</Catalog>`

func TestIdleAndUpgrades(t *testing.T) {
	l := &Library{
		actors: newCatalog(), models: newCatalog(), reqs: newCatalog(),
		files: map[string]string{"assets/walker.m3": "w", "assets/walkerwings.m3": "ww"},
		actorOf: map[string]string{
			"Walker": "Walker", "WalkerBurrowed": "Walker", "WalkerSieged": "Walker", "WalkerPlain": "Walker",
		},
	}
	for _, f := range []struct {
		c    *catalog
		xml  string
		keep func(string) bool
	}{
		{l.actors, testIdleActors, keepActor},
		{l.models, testIdleModels, func(c string) bool { return c == "CModel" }},
		{l.reqs, testReqs, keepReq},
	} {
		if err := f.c.add([]byte(f.xml), f.keep); err != nil {
			t.Fatal(err)
		}
	}
	for _, c := range []struct {
		unit string
		idle *idle
	}{
		{"Walker", &idle{Anim: []string{"Cover", "Stand"}}},             // played forever on creation
		{"WalkerBurrowed", &idle{Anim: []string{"Burrow"}, Hold: true}}, // a bracket's opening, held
		{"WalkerSieged", &idle{Anim: []string{"Morph", "Stand"}}},       // a bracket's loop; a one-shot doesn't replace it
		{"WalkerPlain", nil}, // cleared
	} {
		lk := l.unitLook(c.unit, false, nil)
		if !reflect.DeepEqual(lk.Idle, c.idle) {
			t.Errorf("%s: idle %+v, want %+v", c.unit, lk.Idle, c.idle)
		}
		// Targeted, removed and otherwise conditioned messages don't count;
		// neither do upgrades that change nothing drawn (Shield).
		if len(lk.Groups) != 0 || lk.Model != "" || !slices.Equal(lk.Upgrades, []string{"Spikes", "Wings"}) {
			t.Errorf("%s: groups %v, model %q, upgrades %v; want none, none, [Spikes Wings]", c.unit, lk.Groups, lk.Model, lk.Upgrades)
		}
	}
	if lk := l.unitLook("Walker", false, []string{"Wings"}); lk.Model != "WalkerWings" || lk.Scale != [3]float64{1.5, 1.5, 1.5} || len(lk.Groups) != 0 {
		t.Errorf("with Wings: model %q, scale %v, groups %v; want WalkerWings at the swapped model's 1.5, no groups", lk.Model, lk.Scale, lk.Groups)
	}
	if lk := l.unitLook("Walker", false, []string{"Spikes"}); !slices.Equal(lk.Groups, []string{"Spiked"}) || lk.Model != "" {
		t.Errorf("with Spikes: groups %v, model %q; want [Spiked] and its own model", lk.Groups, lk.Model)
	}
	if _, err := l.UpgradedUnitGLB("Walker", []string{"Shield"}); !errors.Is(err, ErrNoModel) {
		t.Errorf("an upgrade the look doesn't list: %v, want ErrNoModel", err)
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
		if s := l.unitLook(unit, false, nil).Scale; s != [3]float64{want, want, want} {
			t.Errorf("%s: scale %v, want %v", unit, s, want)
		}
	}
	for unit, want := range map[string][]string{
		"CommandCenter": nil, "PlanetaryFortress": {"A"}, "OrbitalCommand": {"B"}, "OrbitalCommandFlying": {"Fly", "B"},
		"Gateway": nil, "WarpGate": {"Superior"}, "RoachBurrowed": {"Burrow"}, "SupplyDepotLowered": {"Burrow"},
	} {
		if g := l.unitLook(unit, false, nil).Groups; !slices.Equal(g, want) {
			t.Errorf("%s: groups %v, want %v", unit, g, want)
		}
	}
	// Idle animations from AnimPlay ... PlayForever and AnimBracketStart.
	for unit, want := range map[string]*idle{
		"Thor": {Anim: []string{"Cover", "Stand"}}, "HydraliskBurrowed": {Anim: []string{"Burrow"}, Hold: true},
		"WarpPrismPhasing": {Anim: []string{"Work"}}, "LiberatorAG": {Anim: []string{"Stand", "Work"}}, "Marine": nil,
	} {
		if got := l.unitLook(unit, false, nil).Idle; !reflect.DeepEqual(got, want) {
			t.Errorf("%s: idle %+v, want %+v", unit, got, want)
		}
	}
	// Upgrades that change a look, and what they change.
	if lk := l.unitLook("Zergling", false, nil); !slices.Equal(lk.Upgrades, []string{"zerglingmovementspeed"}) {
		t.Errorf("Zergling: upgrades %v, want [zerglingmovementspeed]", lk.Upgrades)
	}
	if m := l.unitLook("Zergling", false, []string{"zerglingmovementspeed"}).Model; m != "ZerglingUpgrade" {
		t.Errorf("Zergling with Metabolic Boost: model %q, want ZerglingUpgrade", m)
	}
	if g := l.unitLook("Baneling", false, []string{"CentrificalHooks"}).Groups; !slices.Equal(g, []string{"Superior"}) {
		t.Errorf("Baneling with Centrifugal Hooks: groups %v, want [Superior]", g)
	}
	if ups := l.unitLook("Marine", false, nil).Upgrades; len(ups) != 0 {
		t.Errorf("Marine: upgrades %v, want none (Combat Shield draws nothing lasting)", ups)
	}
	// Every attachment's point exists on its host, with every upgrade the
	// unit's look lists.
	n := 0
	for _, u := range gamedata.Names() {
		lk := l.unitLook(u, false, l.unitLook(u, false, nil).Upgrades)
		if len(lk.Attach) == 0 {
			continue
		}
		name, err := l.ModelPath(u, false)
		if lk.Model != "" {
			name, err = l.modelFile(lk.Model, 0)
		}
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

// TestUpgradedGLB checks that an upgraded unit's file carries the swapped
// model and its look, and that the plain file lists the upgrade.
func TestUpgradedGLB(t *testing.T) {
	l := openLib(t)
	plain, err := l.UnitGLB("Zergling", false)
	if errors.Is(err, casc.ErrNotLocal) {
		t.Skip("model not in this game data")
	}
	if err != nil {
		t.Fatal(err)
	}
	p, err := parseGLB(plain)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(p.extras.Upgrades, []string{"zerglingmovementspeed"}) {
		t.Errorf("plain Zergling lists upgrades %v, want [zerglingmovementspeed]", p.extras.Upgrades)
	}
	up, err := l.UpgradedUnitGLB("Zergling", []string{"zerglingmovementspeed"})
	if err != nil {
		t.Fatal(err)
	}
	if string(up) == string(plain) {
		t.Error("the upgraded Zergling is the plain one")
	}
	g, err := parseGLB(up)
	if err != nil {
		t.Fatal(err)
	}
	if !g.hasAnim("Walk") || !slices.Equal(g.extras.Scale, p.extras.Scale) {
		t.Errorf("upgraded Zergling: anims %v, scale %v; want a Walk clip and the plain one's scale %v", g.anims, g.extras.Scale, p.extras.Scale)
	}
}

// TestModelScaleInGLB checks that a model served by id (as map doodads are)
// carries its CModel scale.
func TestModelScaleInGLB(t *testing.T) {
	l := openLib(t)
	const id = "ZerglingUpgrade"
	want := l.modelScale(id)
	if want == [3]float64{1, 1, 1} {
		t.Skipf("%s has no scale in this game data", id)
	}
	b, err := l.ModelGLB(id, 0)
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
	if !slices.Equal(g.extras.Scale, want[:]) {
		t.Errorf("%s: scale %v, want %v", id, g.extras.Scale, want)
	}
}
