// SPDX-License-Identifier: Apache-2.0

package server

import (
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/dmang-dev/ghostreel/internal/app"
	"github.com/dmang-dev/ghostreel/internal/mapcache"
)

// TestDownloadMap serves a real depot map from a fake depot and checks that
// the download button's endpoint fetches it, while the plain open does not.
func TestDownloadMap(t *testing.T) {
	replayData, err := os.ReadFile(filepath.Join(testdata, "replays", "20220227 - GAME 2 - Reynor vs Serral - Z vs Z - Berlingrad LE.SC2Replay"))
	if err != nil {
		t.Skip("test replay not present")
	}
	const digest = "f0091d83828f7a50f1cb72d558f053a55d3232e8f8afc60616ebf26535d46da7"
	mapData, err := os.ReadFile(filepath.Join(testdata, "depot", "maps", digest+".s2ma"))
	if err != nil {
		t.Skip("depot map not present")
	}
	if sum := sha256.Sum256(mapData); hex.EncodeToString(sum[:]) != digest {
		t.Fatal("test map doesn't match its digest")
	}
	requests := 0
	depot := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		w.Write(mapData)
	}))
	defer depot.Close()

	svc := &app.Service{CacheDir: t.TempDir(), Resolver: &mapcache.Resolver{Client: depot.Client()}}
	svc.Resolver.DownloadDir = filepath.Join(svc.CacheDir, "maps")
	ts := httptest.NewServer((&Server{App: svc}).Handler())
	defer ts.Close()

	resp, s := post(t, ts.URL+"/api/open", "berlingrad.SC2Replay", replayData)
	if resp.StatusCode != 200 || s.HasMap || !s.CanDownload || requests != 0 {
		t.Fatalf("open: %d hasMap=%v canDownload=%v requests=%d", resp.StatusCode, s.HasMap, s.CanDownload, requests)
	}
	// Point the replay's handle at the fake depot.
	l := svc.Get(s.ID)
	l.Info.MapHandle.DepotURL = depot.URL

	resp, s = post(t, ts.URL+"/api/r/"+s.ID+"/download-map", "", nil)
	if resp.StatusCode != 200 || !s.HasMap || s.MapHow != "downloaded" || requests != 1 {
		t.Fatalf("download: %d %+v requests=%d", resp.StatusCode, s, requests)
	}
	if code, _ := get(t, ts.URL+"/api/r/"+s.ID+"/map"); code != 200 {
		t.Fatalf("map after download: %d", code)
	}
}
