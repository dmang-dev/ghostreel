// SPDX-License-Identifier: Apache-2.0

package replay

import (
	"testing"
	"time"
)

func TestTagRoundTrip(t *testing.T) {
	cases := []struct {
		index, recycle uint32
		packed         Tag
	}{
		{0, 1, 1},
		{191, 1, 50069505}, // observed in a real replay: tracker index 191 = game-event tag 50069505
		{192, 1, 50331649},
		{193, 2, 50593794},
		{1<<14 - 1, 1<<18 - 1, Tag((1<<14-1)<<18 | (1<<18 - 1))},
	}
	for _, c := range cases {
		got := MakeTag(c.index, c.recycle)
		if got != c.packed {
			t.Errorf("MakeTag(%d,%d) = %d, want %d", c.index, c.recycle, got, c.packed)
		}
		if got.Index() != c.index || got.Recycle() != c.recycle {
			t.Errorf("tag %d decoded to %d/%d, want %d/%d", got, got.Index(), got.Recycle(), c.index, c.recycle)
		}
	}
}

func TestTagRecycleDoesNotBleedIntoIndex(t *testing.T) {
	if got := MakeTag(5, 1<<18); got.Index() != 5 {
		t.Fatalf("oversized recycle changed index to %d", got.Index())
	}
}

func TestClock(t *testing.T) {
	cases := []struct {
		loops int64
		want  string
	}{
		{0, "0:00"},
		{22, "0:00"}, // 0.98 s: the clock truncates
		{23, "0:01"},
		{1344, "1:00"},
		{5174, "3:50"},
		{80640, "1:00:00"},
	}
	for _, c := range cases {
		if got := FormatClock(c.loops); got != c.want {
			t.Errorf("FormatClock(%d) = %q, want %q", c.loops, got, c.want)
		}
	}
	if got := GameSeconds(160); got != 10 {
		t.Errorf("GameSeconds(160) = %v, want 10", got)
	}
	if got := ClockDuration(224); got != 10*time.Second {
		t.Errorf("ClockDuration(224) = %v, want 10s", got)
	}
	if got := LoopAtClock(60); got != 1344 {
		t.Errorf("LoopAtClock(60) = %d, want 1344", got)
	}
}
