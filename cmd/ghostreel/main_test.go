// SPDX-License-Identifier: Apache-2.0

package main

import (
	"flag"
	"reflect"
	"testing"
)

func TestReorder(t *testing.T) {
	fs := flag.NewFlagSet("t", flag.ContinueOnError)
	fs.String("o", "", "")
	fs.String("replays", "", "")
	fs.Bool("json", false, "")
	cases := []struct{ in, want []string }{
		{[]string{"a.SC2Replay", "-o", "x.pb"}, []string{"-o", "x.pb", "a.SC2Replay"}},
		{[]string{"--replays", "dir", "--json", "file"}, []string{"--replays", "dir", "--json", "file"}},
		{[]string{"file", "--json", "--replays=dir"}, []string{"--json", "--replays=dir", "file"}},
		{[]string{"-o", "x", "--", "-weird-name"}, []string{"-o", "x", "-weird-name"}},
	}
	for _, c := range cases {
		if got := reorder(fs, c.in); !reflect.DeepEqual(got, c.want) {
			t.Errorf("reorder(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}
