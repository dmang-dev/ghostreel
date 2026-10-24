// SPDX-License-Identifier: Apache-2.0

package timeline

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/dmang-dev/ghostreel/internal/replay"
)

// Real replays are never committed; these tests run on whatever is in
// testdata/replays and skip when it's empty.
func realReplays(t testing.TB) []string {
	t.Helper()
	paths, _ := filepath.Glob(filepath.Join("..", "..", "testdata", "replays", "*.SC2Replay"))
	if len(paths) == 0 {
		t.Skip("no replays in testdata/replays")
	}
	return paths
}

func TestRealReplays(t *testing.T) {
	for _, path := range realReplays(t) {
		t.Run(filepath.Base(path), func(t *testing.T) {
			r, err := replay.Open(path)
			if err != nil {
				t.Fatal(err)
			}
			if !r.Info.HasTracker {
				if _, err := Build(&r.Info, r.Events, Options{}); err != replay.ErrNoTrackerEvents {
					t.Fatalf("pre-tracker replay: err = %v", err)
				}
				return
			}
			tl, err := Build(&r.Info, r.Events, Options{})
			if err != nil {
				t.Fatal(err)
			}
			data, err := Marshal(tl)
			if err != nil {
				t.Fatal(err)
			}
			tl, err = Unmarshal(data)
			if err != nil {
				t.Fatal(err)
			}
			ix := NewIndex(tl)
			// Every loop is too slow for hour-long games; a prime stride still
			// hits every offset within a snapshot interval.
			stride := uint32(1)
			if testing.Short() || tl.Meta.Loops > 20000 {
				stride = 37
			}
			for loop := uint32(0); loop <= tl.Meta.Loops; loop += stride {
				assertSameState(t, ix, loop)
			}
			t.Logf("%d units, %d tracks, %d events, %d KiB", len(tl.Units), len(tl.Tracks), len(tl.Events), len(data)/1024)
		})
	}
}

// BenchmarkSeek seeks to random-ish loops of the longest replay available.
func BenchmarkSeek(b *testing.B) {
	var longest string
	var size int64
	for _, p := range realReplays(b) {
		if fi, err := os.Stat(p); err == nil && fi.Size() > size {
			longest, size = p, fi.Size()
		}
	}
	r, err := replay.Open(longest)
	if err != nil {
		b.Fatal(err)
	}
	tl, err := Build(&r.Info, r.Events, Options{})
	if err != nil {
		b.Skip(err)
	}
	ix := NewIndex(tl)
	b.Logf("%s: %d loops, %d units", filepath.Base(longest), tl.Meta.Loops, len(tl.Units))
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		ix.Seek(uint32(uint64(i) * 7919 % uint64(tl.Meta.Loops)))
	}
}
