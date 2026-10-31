// SPDX-License-Identifier: Apache-2.0

package main

import (
	"slices"
	"testing"
)

func TestFileOpener(t *testing.T) {
	var sent []string
	o := &fileOpener{send: func(p string) { sent = append(sent, p) }}
	// Before the page listens, files wait; only the last is shown.
	o.Open("a.SC2Replay")
	o.Open("b.SC2Replay")
	if len(sent) != 0 {
		t.Fatalf("sent before ready: %v", sent)
	}
	o.Ready()
	if !slices.Equal(sent, []string{"b.SC2Replay"}) {
		t.Fatalf("after ready: %v", sent)
	}
	// Afterwards each file goes straight through.
	o.Open("c.SC2Replay")
	if !slices.Equal(sent, []string{"b.SC2Replay", "c.SC2Replay"}) {
		t.Fatalf("after open: %v", sent)
	}
	// A page reload says ready again with nothing waiting.
	o.Ready()
	if len(sent) != 2 {
		t.Fatalf("second ready: %v", sent)
	}
}
