// SPDX-License-Identifier: Apache-2.0

package assets

import (
	"fmt"
	"reflect"
	"regexp"
	"slices"
	"strconv"
	"strings"
)

// What a unit's actor adds to its model file, as the game draws it:
//
//   - Scale: the CModel's ScaleMin/ScaleMax times the actor's own Scale.
//     Most ladder models are drawn smaller than they were modelled (the
//     Thor at 0.5, the Mothership at 0.6, the Zergling at 0.77).
//   - Animation groups: AnimGroupApply messages on creation pick a variant
//     of every animation. The Planetary Fortress and Orbital Command are the
//     Command Center's model with group A or B ("Stand A"), the Warp Gate is
//     the Gateway's with Superior, burrowed units add Burrow.
//   - The idle animation: AnimPlay ... PlayForever and AnimBracketStart
//     messages that replace Stand while nothing else plays. The Thor stands
//     in "Cover Stand"; a burrowed Hydralisk holds the last frame of
//     "Burrow".
//   - Attachments: other actors' models the actor creates with it and
//     places at one of its model's attachment points ("Ref_Weapon"): the
//     Tempest's charge sphere, the Widow Mine's armed light.
//   - Upgrades: the same messages, sent once the owner has an upgrade, and
//     ModelSwap, which replaces the model (Zerglings with Metabolic Boost
//     grow wings).
//
// Only what holds for a unit type on its own counts: messages on
// ActorCreation or on UnitBirth of that type, and on an upgrade's arrival,
// with no condition but that upgrade (none on options, clan decals or
// cargo). The state machines that change these later (attacks, morphs in
// progress) aren't followed.

// keepActor says which actor classes the library reads.
func keepActor(class string) bool {
	switch class {
	case "CActorUnit", "CActorModel", "CActorSiteOpAttach", "CActorSiteOpLocalOffset":
		return true
	}
	return false
}

// keepReq says which validator and requirement classes the library reads:
// those that can say whether a player has an upgrade.
func keepReq(class string) bool {
	switch class {
	case "CValidatorPlayerRequirement", "CValidatorCombine", "CRequirement", "CRequirementCountUpgrade", "CRequirementNot":
		return true
	}
	return false
}

// look is how a unit type is drawn beyond its model file. It travels in the
// unit's glTF extras.
type look struct {
	Scale    [3]float64
	Groups   []string // animation groups ("A", "Superior", "Burrow")
	Attach   []attachment
	Idle     *idle    // nil: Stand
	Model    string   // a CModel that replaces the unit's own; "" keeps it
	Upgrades []string // upgrades that change the look, as the game data spells them
}

// idle is the animation a unit plays when it isn't doing anything else.
type idle struct {
	Anim []string `json:"anim"`           // the clip's words: "Cover", "Stand"
	Hold bool     `json:"hold,omitempty"` // play once and stay on the last frame
}

// attachment is a model drawn on another: Model is a CModel id (served by
// ModelGLB), Point the host model's attachment point ("" for its origin),
// Offset a local offset from it, and Scale the attached model's own.
type attachment struct {
	Model  string     `json:"model"`
	Point  string     `json:"point,omitempty"`
	Offset [3]float64 `json:"offset"`
	Scale  [3]float64 `json:"scale"`
}

// extras is the look as glTF extras, leaving out what changes nothing.
func (lk look) extras() map[string]any {
	out := map[string]any{}
	if lk.Scale != [3]float64{1, 1, 1} {
		out["scale"] = lk.Scale
	}
	if len(lk.Groups) > 0 {
		out["groups"] = lk.Groups
	}
	if len(lk.Attach) > 0 {
		out["attach"] = lk.Attach
	}
	if lk.Idle != nil {
		out["idle"] = lk.Idle
	}
	if len(lk.Upgrades) > 0 {
		out["upgrades"] = lk.Upgrades
	}
	return out
}

// drawn is the part of a look that actor messages set.
func (lk look) drawn() look {
	return look{Groups: lk.Groups, Attach: lk.Attach, Idle: lk.Idle, Model: lk.Model}
}

// When an actor event fires, for unitLook.
const (
	onCreation = iota // the actor is created
	onBirth           // a unit of the type is born (or morphs into it)
	onUpgrade         // its owner finishes an upgrade
)

// unitLook works out a unit type's look once its owner has the upgrades in
// have (or its death model's look, which keeps only the scale).
func (l *Library) unitLook(unit string, death bool, have []string) look {
	lk := look{Scale: [3]float64{1, 1, 1}}
	id, ok := l.modelID(unit, death)
	if !ok {
		return lk
	}
	actor, hasActor := l.actorOf[unit]
	if hasActor && !death {
		l.actorMessages(&lk, unit, actor, have)
		// Upgrades whose messages change nothing drawn here (the Marine's
		// Combat Shield creates no lasting model) aren't listed.
		var base look
		l.actorMessages(&base, unit, actor, nil)
		lk.Upgrades = slices.DeleteFunc(lk.Upgrades, func(u string) bool {
			var with look
			l.actorMessages(&with, unit, actor, []string{u})
			return reflect.DeepEqual(with.drawn(), base.drawn())
		})
	}
	if lk.Model != "" {
		id = lk.Model
	}
	lk.Scale = l.modelScale(id)
	if hasActor {
		if v, ok := l.actors.field("CActorUnit", actor, "Scale"); ok {
			s := parseVec(v, 1)
			for i := range lk.Scale {
				lk.Scale[i] *= s[i]
			}
		}
	}
	return lk
}

// actorMessages applies to lk the messages a unit's actor sends itself when
// the unit appears, and when its owner has the upgrades in have. The actor
// is created first, then the unit's birth fires, then upgrades arrive;
// within each, events run in the order actorEvents gives.
func (l *Library) actorMessages(lk *look, unit, actor string, have []string) {
	type layer struct {
		name string
		idle idle
	}
	var layers []layer // playing animations that replace Stand, oldest first
	stop := func(name string) {
		layers = slices.DeleteFunc(layers, func(x layer) bool { return x.name == name })
	}
	play := func(name, anim string, hold bool) {
		stop(name)
		layers = append(layers, layer{name, idle{strings.Split(anim, ","), hold}})
	}
	evs := l.actorEvents(actor)
	for phase := onCreation; phase <= onUpgrade; phase++ {
		for _, ev := range evs {
			p, up, ok := l.trigger(ev.terms, unit)
			if !ok || ev.target != "" {
				continue
			}
			if up != "" && !slices.Contains(lk.Upgrades, up) {
				lk.Upgrades = append(lk.Upgrades, up)
			}
			if p != phase || (up != "" && !slices.Contains(have, up)) {
				continue
			}
			f := strings.Fields(ev.send)
			if len(f) < 2 {
				continue
			}
			arg := func(i int) string {
				if i < len(f) {
					return f[i]
				}
				return ""
			}
			switch f[0] {
			case "AnimGroupApply":
				if !slices.Contains(lk.Groups, f[1]) {
					lk.Groups = append(lk.Groups, f[1])
				}
			case "AnimGroupRemove":
				lk.Groups = slices.DeleteFunc(lk.Groups, func(g string) bool { return g == f[1] })
			case "Create":
				if a, ok := l.attachment(f[1]); ok && !slices.Contains(lk.Attach, a) {
					lk.Attach = append(lk.Attach, a)
				}
			case "ModelSwap":
				// Only for upgrades: on birth, a swap is a passing birth
				// model (the Changeling's), swapped back by later events.
				if up != "" && safeName.MatchString(f[1]) && l.models.has("CModel", f[1]) {
					lk.Model = f[1]
				}
			case "AnimPlay": // name props flags ...
				flags := strings.Split(arg(3), ",")
				if animProps(arg(2)) && slices.Contains(flags, "PlayForever") {
					play(f[1], arg(2), slices.Contains(flags, "NonLooping"))
				} else {
					stop(f[1]) // it plays once and is gone
				}
			case "AnimBracketStart": // name opening loop closing flags
				flags := strings.Split(arg(5), ",")
				switch {
				case animProps(arg(3)):
					play(f[1], arg(3), false)
				case animProps(arg(2)) && slices.Contains(flags, "OpeningPlayForever"):
					play(f[1], arg(2), true)
				default:
					stop(f[1])
				}
			case "AnimClear", "AnimBracketStop":
				stop(f[1])
			}
		}
	}
	if n := len(layers); n > 0 {
		lk.Idle = &layers[n-1].idle
	}
	slices.Sort(lk.Upgrades)
}

// animProps says whether a message's animation field names an animation
// rather than none ("IGNORE", "{}").
func animProps(s string) bool {
	return s != "IGNORE" && safeAnim.MatchString(s)
}

var safeAnim = regexp.MustCompile(`^[A-Za-z0-9_]+(,[A-Za-z0-9_]+)*$`)

// actorEvents is an actor's events with tokens filled in, in the order the
// game runs them: its templates' first, then its own, each in file order
// (later mods after earlier ones), without those a later <Remove/> takes
// out.
func (l *Library) actorEvents(actor string) []event {
	ch := l.actors.chain("CActorUnit", actor)
	var out []event
	for i := len(ch) - 1; i >= 0; i-- {
		for _, ev := range ch[i].events {
			ev.terms = strings.TrimSpace(subst(ch, actor, ev.terms))
			ev.send = strings.TrimSpace(subst(ch, actor, ev.send))
			if ev.remove {
				out = slices.DeleteFunc(out, func(x event) bool { return x.terms == ev.terms && x.send == ev.send })
				continue
			}
			out = append(out, ev)
		}
	}
	return out
}

// trigger says when an event fires for every unit of a type, and which
// upgrade it waits for ("" for none): on the actor's creation, on the
// type's birth, or when an upgrade arrives; with no condition but that
// upgrade. ok is false for events that fire at other times or under other
// conditions.
func (l *Library) trigger(terms, unit string) (phase int, upgrade string, ok bool) {
	parts := strings.Split(terms, ";")
	first := strings.TrimSpace(parts[0])
	if birth, ok := holdsFor(first, unit); ok {
		phase = onCreation
		if birth {
			phase = onBirth
		}
	} else if rest, ok := strings.CutPrefix(first, "Upgrade."); ok && strings.HasSuffix(rest, ".Add") && safeName.MatchString(strings.TrimSuffix(rest, ".Add")) {
		phase, upgrade = onUpgrade, strings.TrimSuffix(rest, ".Add")
	} else {
		return 0, "", false
	}
	for _, c := range parts[1:] {
		f := strings.Fields(c)
		switch {
		case len(f) == 1 && f[0] == "!ModelIsLowQuality": // the full models are drawn
		case len(f) == 2 && f[0] == "ValidateUnit" && phase == onUpgrade && strings.HasPrefix(f[1], "NotDead"):
			// Units already built: the ones an upgrade changes.
		case len(f) == 2 && (f[0] == "ValidateUnit" || f[0] == "ValidatePlayer"):
			u, ok := l.upgradeOf(f[1])
			if !ok || (upgrade != "" && u != upgrade) {
				return 0, "", false
			}
			upgrade = u
		default:
			return 0, "", false
		}
	}
	return phase, upgrade, true
}

// upgradeOf names the upgrade a validator checks its player has, when that
// is all it checks: a player requirement that counts one upgrade, possibly
// through negations ("NeosteelFramePurchased" is "not
// NeosteelFrameNotPurchased", which passes when the player does not count
// NeosteelFrame).
func (l *Library) upgradeOf(validator string) (string, bool) {
	up, neg, ok := l.validatorUpgrade(validator, 0)
	return up, ok && !neg
}

// validatorUpgrade is the upgrade a validator counts, and whether it passes
// when the player doesn't have it (neg).
func (l *Library) validatorUpgrade(v string, depth int) (up string, neg, ok bool) {
	if depth > 8 || !safeName.MatchString(v) {
		return "", false, false
	}
	switch {
	case l.reqs.has("CValidatorPlayerRequirement", v):
		req, _ := l.reqs.field("CValidatorPlayerRequirement", v, "Value")
		node, _ := l.reqs.field("CRequirement", req, "NodeArray[Use].Link")
		up, neg, ok = l.nodeUpgrade(node, depth+1)
		// Find 1: the validator passes when the requirement is met; without
		// it, when it isn't.
		find, _ := l.reqs.field("CValidatorPlayerRequirement", v, "Find")
		return up, neg != (find != "1"), ok
	case l.reqs.has("CValidatorCombine", v):
		ops := l.reqs.list("CValidatorCombine", v, "CombineArray")
		if len(ops) != 1 {
			return "", false, false
		}
		up, neg, ok = l.validatorUpgrade(ops[0], depth+1)
		negate, _ := l.reqs.field("CValidatorCombine", v, "Negate")
		return up, neg != (negate == "1"), ok
	}
	return "", false, false
}

// nodeUpgrade is validatorUpgrade for a requirement node.
func (l *Library) nodeUpgrade(n string, depth int) (up string, neg, ok bool) {
	if depth > 8 || !safeName.MatchString(n) {
		return "", false, false
	}
	switch {
	case l.reqs.has("CRequirementCountUpgrade", n):
		up, _ := l.reqs.field("CRequirementCountUpgrade", n, "Count.Link")
		return up, false, safeName.MatchString(up)
	case l.reqs.has("CRequirementNot", n):
		op, _ := l.reqs.field("CRequirementNot", n, "OperandArray[0]")
		up, neg, ok = l.nodeUpgrade(op, depth+1)
		return up, !neg, ok
	}
	return "", false, false
}

// holdsFor says whether an event's terms fire for every unit of a type when
// it appears, alone: on the actor's creation, or (birth) on the type's
// birth.
func holdsFor(terms, unit string) (birth, ok bool) {
	terms = strings.TrimSpace(terms)
	if terms == "ActorCreation" {
		return false, true
	}
	if terms == "UnitBirth."+unit {
		return true, true
	}
	// "UnitBirth.Mothership.Normal": a birth of a kind (not a warp-in).
	rest, ok := strings.CutPrefix(terms, "UnitBirth."+unit+".")
	return true, ok && safeName.MatchString(rest)
}

// attachment resolves a model actor that a unit's actor creates, if it is a
// lasting model attached to the unit. One-shot models (birth flashes,
// teleports) are effects, not parts of the unit.
func (l *Library) attachment(id string) (attachment, bool) {
	if !safeName.MatchString(id) || !l.actors.has("CActorModel", id) {
		return attachment{}, false
	}
	for _, e := range l.actors.chain("CActorModel", id) {
		if strings.Contains(e.id, "OneShot") {
			return attachment{}, false
		}
	}
	model, _ := l.actors.field("CActorModel", id, "Model")
	// Hit-test models only make a unit easier to click (the Oracle's
	// Stasis Ward); the game doesn't show them.
	if model == "" || strings.Contains(model, "##") || strings.Contains(model, "HitTest") {
		return attachment{}, false
	}
	if _, err := l.modelFile(model, 0); err != nil {
		return attachment{}, false
	}
	a := attachment{Model: model, Scale: l.modelScale(model)}
	if v, ok := l.actors.field("CActorModel", id, "Scale"); ok {
		s := parseVec(v, 1)
		for i := range a.Scale {
			a.Scale[i] *= s[i]
		}
	}
	ops, _ := l.actors.field("CActorModel", id, "HostSiteOps.Ops")
	for _, op := range strings.Fields(ops) {
		switch {
		case l.actors.has("CActorSiteOpAttach", op):
			m, _ := l.actors.field("CActorSiteOpAttach", op, "AttachQuery.Methods")
			if a.Point = attachPoint(m); a.Point == "" {
				return attachment{}, false // an attach method, not a plain point
			}
		case l.actors.has("CActorSiteOpLocalOffset", op):
			v, _ := l.actors.field("CActorSiteOpLocalOffset", op, "LocalOffset")
			o := parseVec(v, 0)
			for i := range a.Offset {
				a.Offset[i] += o[i]
			}
		}
	}
	return a, true
}

// attachPoint names the attachment point an attach query's keywords pick:
// "Weapon" is "Ref_Weapon", "Weapon 1" is "Ref_Weapon 01". Queries through
// attach methods (AMBestMatchWeapon) give "".
func attachPoint(methods string) string {
	words := strings.Fields(methods)
	if len(words) == 0 || !safeName.MatchString(words[0]) || strings.HasPrefix(words[0], "AM") {
		return ""
	}
	for i, w := range words[1:] {
		if n, err := strconv.Atoi(w); err == nil {
			words[i+1] = fmt.Sprintf("%02d", n)
		}
	}
	return "Ref_" + strings.Join(words, " ")
}

// modelScale is a CModel's scale. The game picks one between ScaleMin and
// ScaleMax for each unit; here it is their midpoint (the two are equal for
// every ladder unit).
func (l *Library) modelScale(id string) [3]float64 {
	lo, hi := [3]float64{1, 1, 1}, [3]float64{1, 1, 1}
	if v, ok := l.models.field("CModel", id, "ScaleMin"); ok {
		lo = parseVec(v, 1)
	}
	if v, ok := l.models.field("CModel", id, "ScaleMax"); ok {
		hi = parseVec(v, 1)
	}
	var s [3]float64
	for i := range s {
		s[i] = (lo[i] + hi[i]) / 2
	}
	return s
}

// parseVec reads "x,y,z" or a single value for all three; missing or bad
// parts are def.
func parseVec(v string, def float64) [3]float64 {
	out := [3]float64{def, def, def}
	parts := strings.Split(v, ",")
	for i := 0; i < 3 && i < len(parts); i++ {
		if f, err := strconv.ParseFloat(strings.TrimSpace(parts[i]), 64); err == nil {
			out[i] = f
		}
	}
	if len(parts) == 1 {
		out[1], out[2] = out[0], out[0]
	}
	return out
}
