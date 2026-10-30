// SPDX-License-Identifier: Apache-2.0

package assets

import (
	"bufio"
	"bytes"
	"sort"
	"strconv"
	"strings"
	"sync"
)

// UnitInfo is what the game's data says about a unit type. Times are in-game
// clock seconds (the data's Normal-speed seconds divided by 1.4, as the game
// shows them at Faster). Zero values are left out of the JSON.
type UnitInfo struct {
	Name        string       `json:"name,omitempty"`
	Life        float64      `json:"life,omitempty"`
	LifeArmor   float64      `json:"lifeArmor,omitempty"`
	Shields     float64      `json:"shields,omitempty"`
	ShieldArmor float64      `json:"shieldArmor,omitempty"`
	Energy      float64      `json:"energy,omitempty"`
	Minerals    int          `json:"minerals,omitempty"`
	Vespene     int          `json:"vespene,omitempty"`
	Supply      float64      `json:"supply,omitempty"` // supply used (negative: provided)
	Attributes  []string     `json:"attributes,omitempty"`
	Sight       float64      `json:"sight,omitempty"`
	BuildTime   float64      `json:"buildTime,omitempty"`
	Resources   int          `json:"resources,omitempty"` // starting minerals or gas of a resource
	Weapons     []WeaponInfo `json:"weapons,omitempty"`
}

// WeaponInfo describes one of a unit's weapons.
type WeaponInfo struct {
	Name     string             `json:"name,omitempty"`
	Range    float64            `json:"range,omitempty"`
	Damage   float64            `json:"damage,omitempty"`  // per hit
	Attacks  int                `json:"attacks,omitempty"` // hits per attack when more than 1
	Bonus    map[string]float64 `json:"bonus,omitempty"`   // extra damage against an attribute
	Cooldown float64            `json:"cooldown,omitempty"`
	Air      bool               `json:"air,omitempty"`
	Ground   bool               `json:"ground,omitempty"`
}

// Info is the game data for a set of unit types, upgrades and abilities.
type Info struct {
	Units     map[string]*UnitInfo `json:"units"`
	Upgrades  map[string]string    `json:"upgrades"`  // display names
	Abilities map[string]string    `json:"abilities"` // display names
}

// gameSpeed converts the data's Normal-speed seconds to Faster seconds.
const gameSpeed = 1.4

// data holds the GameData catalogs that only Info needs, loaded on first
// use: they are large and the 3D view doesn't need them.
type data struct {
	once      sync.Once
	units     *catalog
	weapons   *catalog
	effects   *catalog
	abils     *catalog
	behaviors *catalog           // CBehaviorResource: what a mineral field or geyser holds
	terrains  *catalog           // CTerrain: which lighting a terrain type uses
	weaponOf  map[string]string  // weapon id → class
	effectOf  map[string]string  // effect id → class
	buildTime map[string]float64 // unit → Normal-speed seconds to train or build
	strings   map[string]string  // GameStrings: "Unit/Name/Marine" → "Marine"
	lights    lightSet
}

func (l *Library) loadData() *data {
	d := &l.data
	d.once.Do(func() {
		d.units, d.weapons, d.effects, d.abils, d.behaviors, d.terrains = newCatalog(), newCatalog(), newCatalog(), newCatalog(), newCatalog(), newCatalog()
		d.strings = map[string]string{}
		d.lights = lightSet{}
		for _, m := range mods {
			dir := "mods/" + m + ".sc2mod/base.sc2data/gamedata/"
			for _, f := range []struct {
				file string
				c    *catalog
				keep func(string) bool
			}{
				{"unitdata.xml", d.units, func(c string) bool { return c == "CUnit" }},
				{"weapondata.xml", d.weapons, func(c string) bool { return strings.HasPrefix(c, "CWeapon") }},
				{"effectdata.xml", d.effects, func(c string) bool { return strings.HasPrefix(c, "CEffect") }},
				{"abildata.xml", d.abils, func(c string) bool {
					return c == "CAbilTrain" || c == "CAbilBuild" || c == "CAbilWarpTrain"
				}},
				{"behaviordata.xml", d.behaviors, func(c string) bool { return c == "CBehaviorResource" }},
				{"terraindata.xml", d.terrains, func(c string) bool { return c == "CTerrain" }},
			} {
				if b, err := l.st.ReadFile(dir + f.file); err == nil {
					_ = f.c.add(b, f.keep) // a broken file only loses its entries
				}
			}
			if b, err := l.st.ReadFile(dir + "lightdata.xml"); err == nil {
				d.lights.add(b)
			}
			if b, err := l.st.ReadFile("mods/" + m + ".sc2mod/enus.sc2data/localizeddata/gamestrings.txt"); err == nil {
				readStrings(b, d.strings)
			}
		}
		d.weaponOf, d.effectOf = d.weapons.classes(), d.effects.classes()
		d.buildTime = map[string]float64{}
		// Training and building times, preferring a Train or Build ability over
		// warping in (whose time is the warp, not the unit's).
		ids := make([]string, 0, len(d.abils.entries))
		for k := range d.abils.entries {
			ids = append(ids, k)
		}
		sort.Strings(ids)
		for _, k := range ids {
			e := d.abils.entries[k]
			if e.id == "" || e.isDefault {
				continue
			}
			for f, unit := range e.fields {
				slot, ok := strings.CutSuffix(f, ".Unit")
				if !ok || !strings.HasPrefix(slot, "InfoArray[") {
					continue
				}
				t, _ := strconv.ParseFloat(e.fields[slot+".Time"], 64)
				if t <= 0 {
					continue
				}
				if _, set := d.buildTime[unit]; !set || e.class != "CAbilWarpTrain" {
					d.buildTime[unit] = t
				}
			}
		}
	})
	return d
}

// readStrings adds GameStrings.txt lines ("key=value") to m.
func readStrings(b []byte, m map[string]string) {
	b = bytes.TrimPrefix(b, []byte("\xef\xbb\xbf"))
	sc := bufio.NewScanner(bytes.NewReader(b))
	sc.Buffer(make([]byte, 64*1024), 1024*1024)
	for sc.Scan() {
		if k, v, ok := strings.Cut(sc.Text(), "="); ok {
			m[k] = v
		}
	}
}

// classes maps each entry id to its class, for catalogs that mix classes
// (weapons, effects).
func (c *catalog) classes() map[string]string {
	out := map[string]string{}
	for _, e := range c.entries {
		if e.id != "" {
			out[e.id] = e.class
		}
	}
	return out
}

// indexed lists the indexes of an indexed field that are set to value in an
// entry's chain, nearest entry first ("Attributes" → Armored, Biological).
func (c *catalog) indexed(class, id, name, value string) []string {
	seen := map[string]bool{}
	var out []string
	for _, e := range c.chain(class, id) {
		var keys []string
		for f := range e.fields {
			if strings.HasPrefix(f, name+"[") && strings.HasSuffix(f, "]") {
				keys = append(keys, f)
			}
		}
		sort.Strings(keys)
		for _, f := range keys {
			idx := f[len(name)+1 : len(f)-1]
			if seen[idx] {
				continue
			}
			seen[idx] = true
			if e.fields[f] == value {
				out = append(out, idx)
			}
		}
	}
	return out
}

func (c *catalog) num(class, id, name string) float64 {
	v, _ := c.field(class, id, name)
	f, _ := strconv.ParseFloat(v, 64)
	return f
}

// str looks a catalog string field up in GameStrings: Name fields hold keys
// like "Unit/Name/##id##".
func (d *data) str(c *catalog, class, id, field string) string {
	if key, ok := c.field(class, id, field); ok {
		return d.strings[key]
	}
	return ""
}

// Info returns names and stats for the given unit types, upgrades and
// abilities. Names missing from the game data are left out.
func (l *Library) Info(units, upgrades, abilities []string) Info {
	d := l.loadData()
	out := Info{Units: map[string]*UnitInfo{}, Upgrades: map[string]string{}, Abilities: map[string]string{}}
	for _, u := range units {
		if i := d.unit(u); i != nil {
			out.Units[u] = i
		}
	}
	for _, u := range upgrades {
		if n := d.strings["Upgrade/Name/"+u]; n != "" {
			out.Upgrades[u] = n
		}
	}
	for _, a := range abilities {
		if n := d.strings["Abil/Name/"+a]; n != "" {
			out.Abilities[a] = n
		}
	}
	return out
}

func (d *data) unit(id string) *UnitInfo {
	if !d.units.has("CUnit", id) {
		return nil
	}
	c := d.units
	u := &UnitInfo{
		Name:        d.str(c, "CUnit", id, "Name"),
		Life:        c.num("CUnit", id, "LifeMax"),
		LifeArmor:   c.num("CUnit", id, "LifeArmor"),
		Shields:     c.num("CUnit", id, "ShieldsMax"),
		ShieldArmor: c.num("CUnit", id, "ShieldArmor"),
		Energy:      c.num("CUnit", id, "EnergyMax"),
		Minerals:    int(c.num("CUnit", id, "CostResource[Minerals]")),
		Vespene:     int(c.num("CUnit", id, "CostResource[Vespene]")),
		Supply:      -c.num("CUnit", id, "Food"),
		Attributes:  c.indexed("CUnit", id, "Attributes", "1"),
		Sight:       c.num("CUnit", id, "Sight"),
		BuildTime:   round(d.buildTime[id]/gameSpeed, 1),
	}
	behaviors := c.list("CUnit", id, "BehaviorArray.Link")
	for _, i := range []string{"0", "1"} {
		if b, ok := c.field("CUnit", id, "BehaviorArray["+i+"].Link"); ok {
			behaviors = append([]string{b}, behaviors...)
		}
	}
	for _, b := range behaviors {
		if r := d.behaviors.num("CBehaviorResource", b, "Contents"); r > 0 {
			u.Resources = int(r)
			break
		}
	}
	links := c.list("CUnit", id, "WeaponArray.Link")
	if len(links) == 0 {
		for _, i := range []string{"0", "1", "2"} {
			if l, ok := c.field("CUnit", id, "WeaponArray["+i+"].Link"); ok && l != "" {
				links = append(links, l)
			}
		}
	}
	for _, w := range links {
		if wi := d.weapon(w); wi != nil {
			u.Weapons = append(u.Weapons, *wi)
		}
	}
	return u
}

func (d *data) weapon(id string) *WeaponInfo {
	class, ok := d.weaponOf[id]
	if !ok {
		return nil
	}
	c := d.weapons
	// Weapons inherit from their class's default, then from CWeapon's.
	field := func(name string) string {
		if v, ok := c.field(class, id, name); ok {
			return v
		}
		if def := c.entries["CWeapon/"]; def != nil {
			return strings.ReplaceAll(def.fields[name], "##id##", id)
		}
		return ""
	}
	num := func(name string) float64 {
		f, _ := strconv.ParseFloat(field(name), 64)
		return f
	}
	if field("Options[Hidden]") == "1" {
		return nil // helper weapons the game doesn't show
	}
	w := &WeaponInfo{
		Name:     d.strings[field("Name")],
		Range:    num("Range"),
		Cooldown: round(num("Period")/gameSpeed, 3),
		Attacks:  int(num("DisplayAttackCount")),
	}
	filters := field("TargetFilters")
	required, _, _ := strings.Cut(filters, ";")
	w.Air = strings.Contains(required, "Air")
	w.Ground = strings.Contains(required, "Ground")
	if !w.Air && !w.Ground {
		w.Air, w.Ground = true, true
	}
	// DisplayEffect is the damage the game's tooltip shows; Effect is what
	// the weapon fires, which may repeat it.
	attacks := 0
	if e := field("Effect"); e != "" {
		w.Damage, attacks, w.Bonus = d.damage(e, 0)
	}
	if e := field("DisplayEffect"); d.effectOf[e] != "" {
		if a, _, b := d.damage(e, 0); a > 0 {
			w.Damage, w.Bonus = a, b
		}
	}
	if w.Attacks == 0 {
		w.Attacks = attacks
	}
	if w.Attacks == 1 {
		w.Attacks = 0
	}
	if w.Damage == 0 {
		return nil
	}
	if w.Name == "" {
		w.Name = d.strings["Weapon/Name/"+id]
	}
	return w
}

// damage follows an effect to the damage it deals: through missiles, sets
// and persistent (repeating) effects to the first CEffectDamage.
func (d *data) damage(id string, depth int) (amount float64, attacks int, bonus map[string]float64) {
	class, ok := d.effectOf[id]
	if !ok || depth > 6 {
		return 0, 0, nil
	}
	c := d.effects
	switch class {
	case "CEffectDamage":
		amount = c.num(class, id, "Amount")
		for _, e := range c.chain(class, id) {
			for f, v := range e.fields {
				if attr, ok := strings.CutPrefix(f, "AttributeBonus["); ok && strings.HasSuffix(attr, "]") {
					attr = strings.TrimSuffix(attr, "]")
					if bonus == nil {
						bonus = map[string]float64{}
					}
					if _, set := bonus[attr]; !set {
						bonus[attr], _ = strconv.ParseFloat(v, 64)
					}
				}
			}
		}
		for k, v := range bonus {
			if v == 0 {
				delete(bonus, k)
			}
		}
		return amount, 0, bonus
	case "CEffectLaunchMissile":
		next, _ := c.field(class, id, "ImpactEffect")
		return d.damage(next, depth+1)
	case "CEffectCreatePersistent", "CEffectPersistent":
		n := int(c.num(class, id, "PeriodCount"))
		for _, next := range c.list(class, id, "PeriodicEffectArray") {
			if a, _, b := d.damage(next, depth+1); a > 0 {
				if n > 1 {
					return a, n, b
				}
				return a, 0, b
			}
		}
	case "CEffectSet":
		for _, next := range c.list(class, id, "EffectArray") {
			if a, n, b := d.damage(next, depth+1); a > 0 {
				return a, n, b
			}
		}
	}
	return 0, 0, nil
}

func round(v float64, places int) float64 {
	p := 1.0
	for i := 0; i < places; i++ {
		p *= 10
	}
	return float64(int64(v*p+0.5)) / p
}
