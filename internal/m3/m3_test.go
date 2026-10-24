// SPDX-License-Identifier: Apache-2.0

package m3

import (
	"math"
	"os"
	"strings"
	"sync"
	"testing"

	"github.com/OWNER/ghostreel/internal/casc"
)

const cascDir = "../../testdata/casc"

var (
	storageOnce sync.Once
	storage     *casc.Storage
	storageErr  error
)

// openStorage opens the local game data, skipping when it is absent (it is
// never committed).
func openStorage(t *testing.T) *casc.Storage {
	t.Helper()
	if _, err := os.Stat(cascDir + "/.build.info"); err != nil {
		t.Skip("no game data in testdata/casc")
	}
	storageOnce.Do(func() { storage, storageErr = casc.Open(cascDir) })
	if storageErr != nil {
		t.Fatal(storageErr)
	}
	return storage
}

const unitsDir = "mods/liberty.sc2mod/base.sc2assets/assets/units/"

func TestMarine(t *testing.T) {
	s := openStorage(t)
	b, err := s.ReadFile(unitsDir + "terran/marine/marine.m3")
	if err != nil {
		t.Fatal(err)
	}
	m, err := Parse(b)
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("v%d %q: %d bones, %d vertices, %d meshes, %d materials, %d sequences",
		m.Version, m.Name, len(m.Bones), len(m.Vertices), len(m.Meshes), len(m.Materials), len(m.Sequences))
	if len(m.Vertices) == 0 || len(m.Meshes) == 0 || len(m.Bones) == 0 {
		t.Fatal("empty model")
	}
	for _, mt := range m.Materials {
		t.Logf("material %q diffuse %q blend %d", mt.Name, mt.Diffuse, mt.BlendMode)
	}
	names := map[string]bool{}
	for _, sq := range m.Sequences {
		names[sq.Name] = true
		t.Logf("sequence %-28q %5d..%5d ms, %3d tracks, looping %v", sq.Name, sq.StartMs, sq.EndMs, len(sq.Tracks), sq.Looping)
	}
	for _, want := range []string{"Stand", "Walk", "Attack"} {
		if !names[want] {
			t.Errorf("no %q sequence", want)
		}
	}
	// Every skinned vertex's weights sum to one and its bones exist.
	for i, v := range m.Vertices {
		sum := v.Weights[0] + v.Weights[1] + v.Weights[2] + v.Weights[3]
		if sum != 0 && math.Abs(float64(sum)-1) > 0.02 {
			t.Fatalf("vertex %d weights sum to %v", i, sum)
		}
		for _, j := range v.Joints {
			if int(j) >= len(m.Bones) {
				t.Fatalf("vertex %d uses bone %d of %d", i, j, len(m.Bones))
			}
		}
	}
	// The marine is roughly a meter tall and stands at the origin.
	var lo, hi [3]float32
	for i, v := range m.Vertices {
		for k := 0; k < 3; k++ {
			if i == 0 || v.Pos[k] < lo[k] {
				lo[k] = v.Pos[k]
			}
			if i == 0 || v.Pos[k] > hi[k] {
				hi[k] = v.Pos[k]
			}
		}
	}
	t.Logf("bounds %v .. %v", lo, hi)
	if h := hi[2] - lo[2]; h < 0.5 || h > 3 {
		t.Errorf("height %v, want about 1", h)
	}
}

// TestAllUnits parses every unit and building model in the game data.
func TestAllUnits(t *testing.T) {
	s := openStorage(t)
	if testing.Short() {
		t.Skip("short")
	}
	if err := s.LoadNames(); err != nil {
		t.Fatal(err)
	}
	var n, fails int
	for _, name := range s.Names() {
		if !strings.HasSuffix(name, ".m3") || !(strings.Contains(name, "/assets/units/") || strings.Contains(name, "/assets/buildings/")) {
			continue
		}
		b, err := s.ReadFile(name)
		if err != nil {
			continue // stripped from this data
		}
		n++
		if _, err := Parse(b); err != nil {
			fails++
			if fails <= 20 {
				t.Errorf("%s: %v", name, err)
			}
		}
	}
	t.Logf("%d models, %d failed", n, fails)
	if n == 0 {
		t.Skip("no unit models")
	}
}

func TestMarineGLB(t *testing.T) {
	s := openStorage(t)
	b, err := s.ReadFile(unitsDir + "terran/marine/marine.m3")
	if err != nil {
		t.Fatal(err)
	}
	m, err := Parse(b)
	if err != nil {
		t.Fatal(err)
	}
	glb, err := m.GLB(GLBOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if string(glb[:4]) != "glTF" || len(glb)%4 != 0 {
		t.Fatalf("bad GLB header %q, length %d", glb[:4], len(glb))
	}
	t.Logf("GLB %d bytes", len(glb))
	if out := os.Getenv("GHOSTREEL_GLB_OUT"); out != "" {
		os.WriteFile(out, glb, 0o644)
	}
}
