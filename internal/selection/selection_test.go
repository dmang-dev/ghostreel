// SPDX-License-Identifier: Apache-2.0

package selection

import (
	"reflect"
	"testing"

	"github.com/OWNER/ghostreel/internal/replay"
)

func sel(remove replay.Mask, tags ...replay.Tag) *replay.Event {
	sc := &replay.SelectionChange{Group: ActiveGroup, Remove: remove, AddTags: tags}
	return &replay.Event{Kind: replay.KindSelection, Player: 1, Selection: sc}
}

func group(index, update int) *replay.Event {
	return &replay.Event{Kind: replay.KindControlGroup, Player: 1, ControlGroup: &replay.ControlGroupChange{Index: index, Update: update}}
}

var none = replay.Mask{}

func TestSelectionAndGroups(t *testing.T) {
	tr := New()
	check := func(want ...replay.Tag) {
		t.Helper()
		got := tr.Selected(1)
		if len(want) == 0 && len(got) == 0 {
			return
		}
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("selected %v, want %v", got, want)
		}
	}
	tr.Apply(sel(none, 3, 1, 2))
	check(1, 2, 3) // same priority: by tag

	// Remove index 1 (tag 2) by bit mask.
	tr.Apply(sel(replay.Mask{Kind: replay.MaskBits, Bits: []bool{false, true}}))
	check(1, 3)

	// Ctrl+1, then select something else, then recall 1.
	tr.Apply(group(1, replay.GroupSet))
	tr.Apply(sel(replay.Mask{Kind: replay.MaskZeroIndices}, 9)) // clear, add 9
	check(9)
	tr.Apply(group(1, replay.GroupRecall))
	check(1, 3)

	// Shift+1 adds the current selection to the group.
	tr.Apply(sel(replay.Mask{Kind: replay.MaskZeroIndices}, 5))
	tr.Apply(group(1, replay.GroupAppend))
	tr.Apply(group(1, replay.GroupRecall))
	check(1, 3, 5)

	// Keep only index 2 (ZeroIndices lists the survivors).
	tr.Apply(sel(replay.Mask{Kind: replay.MaskZeroIndices, Indices: []int{2}}))
	check(5)

	// A death removes the unit from the selection and from groups.
	tr.Apply(group(1, replay.GroupRecall))
	tr.Apply(&replay.Event{Kind: replay.KindUnitDied, Tag: 3})
	check(1, 5)
	tr.Apply(sel(replay.Mask{Kind: replay.MaskZeroIndices}))
	tr.Apply(group(1, replay.GroupRecall))
	check(1, 5)

	// Steal: binding unit 5 to group 2 removes it from group 1.
	tr.Apply(sel(replay.Mask{Kind: replay.MaskZeroIndices}, 5))
	tr.Apply(group(2, replay.GroupSetAndSteal))
	tr.Apply(group(1, replay.GroupRecall))
	check(1)
	tr.Apply(group(1, replay.GroupClear))
	tr.Apply(group(1, replay.GroupRecall))
	check()
}

func TestSubgroupOrder(t *testing.T) {
	tr := New()
	e := sel(none, 10, 11, 20)
	e.Selection.AddSubgroups = []replay.Subgroup{
		{UnitLink: 5, Priority: 10, Count: 2}, // two low-priority units
		{UnitLink: 7, Priority: 50, Count: 1}, // one high-priority unit
	}
	tr.Apply(e)
	if got := tr.Selected(1); !reflect.DeepEqual(got, []replay.Tag{20, 10, 11}) {
		t.Fatalf("selected %v, want the high-priority unit first", got)
	}
}
