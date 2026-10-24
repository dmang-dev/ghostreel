// SPDX-License-Identifier: Apache-2.0

// Package selection reconstructs which units each player had selected, from
// a replay's SelectionDelta and ControlGroupUpdate events, so commands can be
// attributed to units. A command applies to the active selection at the
// moment it is issued.
//
// The game orders a selection by subgroup priority (unit type), and masks in
// later events refer to positions in that order, so the tracker keeps the
// same order. Dead units leave every selection when they die.
package selection

import (
	"sort"

	"github.com/OWNER/ghostreel/internal/replay"
)

// ActiveGroup is the control group ID the replay uses for the live selection.
const ActiveGroup = 10

type member struct {
	tag            replay.Tag
	priority, intr int64
	link           int64
}

type player struct {
	active []member
	groups [10][]member
}

// Tracker follows every player's selection and control groups.
type Tracker struct {
	players map[int]*player
	dead    map[replay.Tag]bool
	// Unknown counts mask indices that pointed past the end of a selection,
	// a sign the tracker drifted from the game.
	Unknown int
}

// New returns an empty tracker.
func New() *Tracker {
	return &Tracker{players: map[int]*player{}, dead: map[replay.Tag]bool{}}
}

func (t *Tracker) player(id int) *player {
	p := t.players[id]
	if p == nil {
		p = &player{}
		t.players[id] = p
	}
	return p
}

// Apply updates the tracker with one event. Unit deaths must be applied too
// (KindUnitDied), in event order.
func (t *Tracker) Apply(e *replay.Event) {
	switch e.Kind {
	case replay.KindUnitDied:
		t.dead[e.Tag] = true
		for _, p := range t.players {
			p.active = t.alive(p.active)
			for g := range p.groups {
				p.groups[g] = t.alive(p.groups[g])
			}
		}
	case replay.KindSelection:
		t.applySelection(t.player(e.Player), e.Selection)
	case replay.KindControlGroup:
		t.applyGroup(t.player(e.Player), e.ControlGroup)
	}
}

// Selected returns the tags in a player's active selection, in game order.
func (t *Tracker) Selected(playerID int) []replay.Tag {
	p := t.players[playerID]
	if p == nil {
		return nil
	}
	out := make([]replay.Tag, len(p.active))
	for i, m := range p.active {
		out[i] = m.tag
	}
	return out
}

func (t *Tracker) alive(ms []member) []member {
	out := ms[:0]
	for _, m := range ms {
		if !t.dead[m.tag] {
			out = append(out, m)
		}
	}
	return out
}

func (t *Tracker) remove(ms []member, mask replay.Mask) []member {
	if mask.Kind == replay.MaskNone {
		return ms
	}
	for _, i := range mask.Indices {
		if i >= len(ms) && mask.Kind == replay.MaskOneIndices {
			t.Unknown++
		}
	}
	out := make([]member, 0, len(ms))
	for i, m := range ms {
		if !mask.Removes(i) {
			out = append(out, m)
		}
	}
	return out
}

func (t *Tracker) applySelection(p *player, sc *replay.SelectionChange) {
	// Group 10 is the live selection; other IDs would be control groups, which
	// the replay updates through ControlGroupUpdate instead.
	if sc.Group != ActiveGroup {
		return
	}
	sel := t.remove(p.active, sc.Remove)
	// Subgroups describe the added tags in order: Count units of each type.
	added := make([]member, 0, len(sc.AddTags))
	ti := 0
	for _, sg := range sc.AddSubgroups {
		for k := 0; k < sg.Count && ti < len(sc.AddTags); k++ {
			added = append(added, member{tag: sc.AddTags[ti], priority: sg.Priority, intr: sg.IntraPri, link: sg.UnitLink})
			ti++
		}
	}
	for ; ti < len(sc.AddTags); ti++ {
		added = append(added, member{tag: sc.AddTags[ti]})
	}
	p.active = t.merge(sel, added)
}

// merge adds units to a selection, skipping duplicates and dead units, and
// restores the game's order: higher subgroup priority first, then by type,
// then by intra-subgroup priority, then by tag.
func (t *Tracker) merge(sel, add []member) []member {
	seen := make(map[replay.Tag]bool, len(sel)+len(add))
	out := make([]member, 0, len(sel)+len(add))
	for _, m := range append(sel, add...) {
		if seen[m.tag] || t.dead[m.tag] {
			continue
		}
		seen[m.tag] = true
		out = append(out, m)
	}
	sort.SliceStable(out, func(i, j int) bool {
		a, b := out[i], out[j]
		if a.priority != b.priority {
			return a.priority > b.priority
		}
		if a.link != b.link {
			return a.link < b.link
		}
		if a.intr != b.intr {
			return a.intr > b.intr
		}
		return a.tag < b.tag
	})
	return out
}

func (t *Tracker) applyGroup(p *player, cg *replay.ControlGroupChange) {
	if cg.Index < 0 || cg.Index >= len(p.groups) {
		return
	}
	g := t.remove(p.groups[cg.Index], cg.Mask)
	switch cg.Update {
	case replay.GroupSet, replay.GroupSetAndSteal:
		g = append([]member(nil), p.active...)
	case replay.GroupAppend, replay.GroupAppendAndSteal:
		g = t.merge(g, p.active)
	case replay.GroupRecall:
		p.active = append([]member(nil), g...)
	case replay.GroupClear:
		g = nil
	}
	if cg.Update == replay.GroupSetAndSteal || cg.Update == replay.GroupAppendAndSteal {
		// Stealing moves the units out of every other group.
		in := map[replay.Tag]bool{}
		for _, m := range p.active {
			in[m.tag] = true
		}
		for i := range p.groups {
			if i == cg.Index {
				continue
			}
			kept := p.groups[i][:0]
			for _, m := range p.groups[i] {
				if !in[m.tag] {
					kept = append(kept, m)
				}
			}
			p.groups[i] = kept
		}
	}
	p.groups[cg.Index] = g
}
