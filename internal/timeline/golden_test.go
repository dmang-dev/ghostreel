// SPDX-License-Identifier: Apache-2.0

package timeline

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

// The viewer re-implements seeking and interpolation in TypeScript. These
// golden files pin both implementations to the same answers: the synthetic
// fixture's timeline, and the expected state at many loops. The TypeScript
// test (viewer/src/model.test.ts) replays them.
//
// Regenerate with: GHOSTREEL_UPDATE_GOLDEN=1 go test ./internal/timeline -run Golden
var goldenDir = filepath.Join("..", "..", "viewer", "src", "testdata")

type goldenUnit struct {
	ID           uint32  `json:"id"`
	Type         uint32  `json:"type"`
	Owner        uint32  `json:"owner"`
	X            float32 `json:"x"`
	Y            float32 `json:"y"`
	Facing       float32 `json:"facing"`
	Constructing bool    `json:"constructing"`
}

type goldenState struct {
	Loop    uint32            `json:"loop"`
	Units   []goldenUnit      `json:"units"`
	Workers map[uint32]uint32 `json:"workers"` // player → workers from the latest stats
}

func TestGoldenForViewer(t *testing.T) {
	tl := buildFixture(t)
	tl.Meta.Generator = "golden"
	pbBytes, err := Marshal(tl)
	if err != nil {
		t.Fatal(err)
	}
	ix := NewIndex(tl)
	var states []goldenState
	for loop := uint32(0); loop <= tl.Meta.Loops+20; loop += 3 {
		st := ix.Seek(loop)
		gs := goldenState{Loop: loop, Units: []goldenUnit{}, Workers: map[uint32]uint32{}}
		for _, u := range st.Units {
			gs.Units = append(gs.Units, goldenUnit{u.ID, u.Type, u.Owner, u.X, u.Y, u.Facing, u.Constructing})
		}
		for p, s := range st.Stats {
			gs.Workers[p] = s.Workers
		}
		states = append(states, gs)
	}
	jsonBytes, err := json.MarshalIndent(states, "", " ")
	if err != nil {
		t.Fatal(err)
	}
	pbPath := filepath.Join(goldenDir, "fixture.pb")
	jsonPath := filepath.Join(goldenDir, "fixture.states.json")
	if os.Getenv("GHOSTREEL_UPDATE_GOLDEN") != "" {
		os.MkdirAll(goldenDir, 0o755)
		if err := os.WriteFile(pbPath, pbBytes, 0o644); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(jsonPath, jsonBytes, 0o644); err != nil {
			t.Fatal(err)
		}
		return
	}
	for path, want := range map[string][]byte{pbPath: pbBytes, jsonPath: jsonBytes} {
		got, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("%v (regenerate with GHOSTREEL_UPDATE_GOLDEN=1)", err)
		}
		if !bytes.Equal(bytes.ReplaceAll(got, []byte("\r\n"), []byte("\n")), want) {
			t.Errorf("%s is stale; regenerate with GHOSTREEL_UPDATE_GOLDEN=1", path)
		}
	}
}
