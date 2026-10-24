// SPDX-License-Identifier: Apache-2.0

package gamedata

import "testing"

func TestStubKnownAndGuessed(t *testing.T) {
	s := Stub{}
	cases := []struct {
		name    string
		class   Class
		w, h    int
		movable bool
		known   bool
	}{
		{"Marine", ClassArmy, 0, 0, false, true},
		{"SCV", ClassWorker, 0, 0, false, true},
		{"Barracks", ClassBuilding, 3, 3, true, true},
		{"Nexus", ClassBuilding, 5, 5, false, true},
		{"SpineCrawlerUprooted", ClassBuilding, 2, 2, true, true},
		{"LabMineralField750", ClassResource, 2, 1, false, false},
		{"PurifierVespeneGeyser", ClassResource, 3, 3, false, false},
		{"DestructibleRock6x6", ClassNeutral, 6, 6, false, false},
		{"UnbuildableRocksDestructible", ClassNeutral, 2, 2, false, false},
		{"BeaconArmy", ClassHidden, 0, 0, false, false},
		{"SomeBrandNewUnit", ClassArmy, 0, 0, false, false},
	}
	for _, c := range cases {
		u := s.Unit(0, c.name)
		if u.Class != c.class || u.FootprintW != c.w || u.FootprintH != c.h || u.Movable != c.movable || u.Known != c.known {
			t.Errorf("%s: got class=%v %dx%d movable=%v known=%v, want class=%v %dx%d movable=%v known=%v",
				c.name, u.Class, u.FootprintW, u.FootprintH, u.Movable, u.Known, c.class, c.w, c.h, c.movable, c.known)
		}
		if u.Class != ClassHidden && u.Radius <= 0 {
			t.Errorf("%s: radius %v, want > 0", c.name, u.Radius)
		}
	}
	if u := s.Unit(0, "Totally unknown"); u.Speed != DefaultSpeed {
		t.Errorf("unknown unit speed %v, want default %v", u.Speed, DefaultSpeed)
	}
}
