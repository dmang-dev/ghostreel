// SPDX-License-Identifier: Apache-2.0

package engine

import (
	"path/filepath"
	"testing"

	enginev1 "github.com/dmang-dev/ghostreel/gen/engine/v1"
	"google.golang.org/protobuf/proto"
)

// TestSamplesRoundTrip writes and reads a recording with health, shields
// and energy.
func TestSamplesRoundTrip(t *testing.T) {
	s := &enginev1.Samples{
		SchemaVersion: enginev1.SchemaVersion_SCHEMA_VERSION_1, ReplayHash: "x", Step: 16,
		Frames: []*enginev1.Frame{{Loop: 16, Units: []*enginev1.UnitSample{{
			Tag: 1, X: 1, Y: 2, Health: 120, HealthMax: 160, Shield: 40, ShieldMax: 80, Energy: 50, EnergyMax: 200,
		}}}},
	}
	path := filepath.Join(t.TempDir(), "x.engine.pb.gz")
	if err := WriteSamples(path, s); err != nil {
		t.Fatal(err)
	}
	got, err := ReadSamples(path)
	if err != nil {
		t.Fatal(err)
	}
	if !proto.Equal(got, s) {
		t.Errorf("read back %v", got)
	}
}
