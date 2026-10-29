// SPDX-License-Identifier: Apache-2.0

package gamedata

import (
	"regexp"
	"strconv"
	"testing"
)

// footprint must match exactly what the regexp it replaced matched.
func TestFootprintMatchesRegexp(t *testing.T) {
	re := regexp.MustCompile(`(\d+)x(\d+)`)
	for _, name := range []string{
		"Rocks2x2NonConjoined", "UnbuildableRocksDestructible", "Debris2x2", "DestructibleRock6x6",
		"x2", "2x", "12x34x56", "a1b22x333c", "4xx5", "Rock4x 4", "0x0", "",
	} {
		w, h, ok := footprint(name)
		m := re.FindStringSubmatch(name)
		if ok != (m != nil) {
			t.Errorf("%q: ok %v, regexp matched %v", name, ok, m != nil)
			continue
		}
		if m != nil {
			ww, _ := strconv.Atoi(m[1])
			hh, _ := strconv.Atoi(m[2])
			if w != ww || h != hh {
				t.Errorf("%q: %dx%d, regexp %dx%d", name, w, h, ww, hh)
			}
		}
	}
}
