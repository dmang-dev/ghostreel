// SPDX-License-Identifier: Apache-2.0

package replay

import "fmt"

// Tag identifies a unit for its whole lifetime. The game packs it as
// index<<18 | recycle; tracker events carry the two halves separately while
// game events (selections, target units) carry the packed form. Both streams
// must go through this type so that orders can be matched to units.
type Tag uint32

const tagIndexShift = 18

// MakeTag packs a unit index and recycle counter into a Tag.
func MakeTag(index, recycle uint32) Tag {
	return Tag(index<<tagIndexShift | recycle&(1<<tagIndexShift-1))
}

// Index is the unit slot index. Slots are reused after a unit dies.
func (t Tag) Index() uint32 { return uint32(t) >> tagIndexShift }

// Recycle counts how many times the slot has been reused.
func (t Tag) Recycle() uint32 { return uint32(t) & (1<<tagIndexShift - 1) }

func (t Tag) String() string { return fmt.Sprintf("%d/%d", t.Index(), t.Recycle()) }
