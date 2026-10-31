// SPDX-License-Identifier: Apache-2.0

package assets

import (
	"fmt"
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
//   - Attachments: other actors' models the actor creates with it and
//     places at one of its model's attachment points ("Ref_Weapon"): the
//     Tempest's charge sphere, the Widow Mine's armed light.
//
// Only what holds for a unit type on its own counts: messages on
// ActorCreation or on UnitBirth of that type, with no other conditions
// (upgrades, options, clan decals). The state machines that change these
// later (attacks, morphs in progress) aren't followed.

// keepActor says which actor classes the library reads.
func keepActor(class string) bool {
	switch class {
	case "CActorUnit", "CActorModel", "CActorSiteOpAttach", "CActorSiteOpLocalOffset":
		return true
	}
	return false
}

// look is how a unit type is drawn beyond its model file. It travels in the
// unit's glTF extras.
type look struct {
	Scale  [3]float64
	Groups []string // animation groups ("A", "Superior", "Burrow")
	Attach []attachment
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
	return out
}

// unitLook works out a unit type's look (or its death model's, which keeps
// only the scale).
func (l *Library) unitLook(unit string, death bool) look {
	lk := look{Scale: [3]float64{1, 1, 1}}
	id, ok := l.modelID(unit, death)
	if !ok {
		return lk
	}
	lk.Scale = l.modelScale(id)
	actor, ok := l.actorOf[unit]
	if !ok {
		return lk
	}
	if v, ok := l.actors.field("CActorUnit", actor, "Scale"); ok {
		s := parseVec(v, 1)
		for i := range lk.Scale {
			lk.Scale[i] *= s[i]
		}
	}
	if death {
		return lk
	}
	// The actor is created first, then the unit's birth fires; within each,
	// templates' events come first, then the actor's own, in file order.
	ch := l.actors.chain("CActorUnit", actor)
	for _, birth := range []bool{false, true} {
		for i := len(ch) - 1; i >= 0; i-- {
			for _, ev := range ch[i].events {
				if b, ok := holdsFor(subst(ch, actor, ev.terms), unit); !ok || b != birth {
					continue
				}
				f := strings.Fields(subst(ch, actor, ev.send))
				if len(f) < 2 {
					continue
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
				}
			}
		}
	}
	return lk
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
