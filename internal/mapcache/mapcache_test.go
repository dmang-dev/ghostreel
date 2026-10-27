// SPDX-License-Identifier: Apache-2.0

package mapcache

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/dmang-dev/ghostreel/internal/replay"
)

func write(t *testing.T, path string, data []byte) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatal(err)
	}
}

func handleFor(content []byte, depot string) *replay.MapHandle {
	sum := sha256.Sum256(content)
	return &replay.MapHandle{Ext: "s2ma", Region: "EU", Digest: hex.EncodeToString(sum[:]), DepotURL: depot}
}

func TestResolveOrder(t *testing.T) {
	ctx := context.Background()
	content := []byte("fake map")
	var hits atomic.Int32
	depot := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		hits.Add(1)
		if strings.HasSuffix(req.URL.Path, ".s2ma") {
			w.Write(content)
			return
		}
		http.NotFound(w, req)
	}))
	defer depot.Close()

	tmp := t.TempDir()
	h := handleFor(content, depot.URL+"/")
	info := &replay.Info{MapTitle: "Test LE", MapHandle: h}
	r := &Resolver{
		BnetCacheDirs: []string{filepath.Join(tmp, "bnet")},
		DownloadDir:   filepath.Join(tmp, "dl"),
		Client:        depot.Client(),
	}

	// Nothing local and network off: not found, and the error says why.
	_, _, err := r.Resolve(ctx, info, "")
	if !errors.Is(err, ErrNotFound) || !strings.Contains(err.Error(), "network access not enabled") {
		t.Fatalf("err = %v", err)
	}
	if hits.Load() != 0 {
		t.Fatal("contacted the depot without permission")
	}

	// Network on: downloaded, verified, cached.
	r.AllowNetwork = true
	p, how, err := r.Resolve(ctx, info, "")
	if err != nil || how != HowDownloaded {
		t.Fatalf("download: %v %v", how, err)
	}
	if got, _ := os.ReadFile(p); string(got) != string(content) {
		t.Fatal("downloaded content differs")
	}
	if _, how, _ = r.Resolve(ctx, info, ""); how != HowOwnCache || hits.Load() != 1 {
		t.Fatalf("second resolve: %v after %d requests", how, hits.Load())
	}

	// The Battle.net cache wins over our own cache.
	bnet := filepath.Join(tmp, "bnet", h.Digest[:2], h.Digest[2:4], h.FileName())
	write(t, bnet, content)
	if p, how, _ = r.Resolve(ctx, info, ""); how != HowBnetCache || p != bnet {
		t.Fatalf("bnet cache: %v %s", how, p)
	}

	// A manual pick wins over everything.
	manual := filepath.Join(tmp, "picked.SC2Map")
	write(t, manual, []byte("x"))
	if p, how, _ = r.Resolve(ctx, info, manual); how != HowManual || p != manual {
		t.Fatalf("manual: %v %s", how, p)
	}
	if _, _, err = r.Resolve(ctx, info, filepath.Join(tmp, "missing")); err == nil {
		t.Fatal("missing manual file accepted")
	}
}

func TestDownloadRejectsWrongHash(t *testing.T) {
	depot := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Write([]byte("tampered"))
	}))
	defer depot.Close()
	tmp := t.TempDir()
	r := &Resolver{DownloadDir: tmp, AllowNetwork: true, Client: depot.Client()}
	info := &replay.Info{MapHandle: handleFor([]byte("genuine"), depot.URL)}
	_, _, err := r.Resolve(context.Background(), info, "")
	if err == nil || !strings.Contains(err.Error(), "does not match") {
		t.Fatalf("err = %v", err)
	}
	if entries, _ := os.ReadDir(tmp); len(entries) != 0 {
		t.Fatalf("bad download left files behind: %v", entries)
	}
}

func TestFindBeside(t *testing.T) {
	tmp := t.TempDir()
	replayPath := filepath.Join(tmp, "replays", "game.SC2Replay")
	write(t, replayPath, []byte("x"))
	inMaps := filepath.Join(tmp, "replays", "Maps", "PylonAIE_v4.SC2Map")
	write(t, inMaps, []byte("x"))
	elsewhere := filepath.Join(tmp, "folder", "PylonAIE_v4.SC2Map")
	write(t, elsewhere, []byte("x"))
	r := &Resolver{MapDirs: []string{filepath.Join(tmp, "folder")}}
	ctx := context.Background()

	// A maps folder beside the replay (any case) wins over the map folders.
	p, how, err := r.Resolve(ctx, &replay.Info{Path: replayPath, MapFileName: "pylonaie_v4.sc2map"}, "")
	if err != nil || how != HowBeside || p != inMaps {
		t.Errorf("maps folder: %v %v %s", err, how, p)
	}
	// Right next to the replay comes first.
	beside := filepath.Join(tmp, "replays", "PylonAIE_v4.SC2Map")
	write(t, beside, []byte("x"))
	if p, how, err := r.Resolve(ctx, &replay.Info{Path: replayPath, MapFileName: "PylonAIE_v4.SC2Map"}, ""); err != nil || how != HowBeside || p != beside {
		t.Errorf("next to replay: %v %v %s", err, how, p)
	}
	// Without a replay path (an upload), only the map folders are searched.
	if p, how, err := r.Resolve(ctx, &replay.Info{MapFileName: "PylonAIE_v4.SC2Map"}, ""); err != nil || how != HowMapFolder || p != elsewhere {
		t.Errorf("no path: %v %v %s", err, how, p)
	}
	// Not found anywhere: the error names the folders beside the replay.
	_, _, err = r.Resolve(ctx, &replay.Info{Path: replayPath, MapFileName: "Other.SC2Map"}, "")
	if !errors.Is(err, ErrNotFound) || !strings.Contains(err.Error(), filepath.Join(tmp, "replays", "maps")) {
		t.Errorf("missing: %v", err)
	}
}

func TestFindByName(t *testing.T) {
	tmp := t.TempDir()
	want := filepath.Join(tmp, "Ladder2019", "AbyssalReefAIE.SC2Map")
	write(t, want, []byte("x"))
	r := &Resolver{MapDirs: []string{filepath.Join(tmp, "nope"), tmp}}
	for _, name := range []string{"AbyssalReefAIE.SC2Map", "abyssalreefaie.sc2map", `Ladder\AbyssalReefAIE.SC2Map`} {
		p, how, err := r.Resolve(context.Background(), &replay.Info{MapFileName: name}, "")
		if err != nil || how != HowMapFolder || p != want {
			t.Errorf("%s: %v %v %s", name, err, how, p)
		}
	}
	if _, _, err := r.Resolve(context.Background(), &replay.Info{MapFileName: "Other.SC2Map"}, ""); !errors.Is(err, ErrNotFound) {
		t.Errorf("missing name: %v", err)
	}
}
