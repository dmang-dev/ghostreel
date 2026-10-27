// SPDX-License-Identifier: Apache-2.0

package server

import (
	"bytes"
	"compress/gzip"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/dmang-dev/ghostreel/internal/app"
	"github.com/dmang-dev/ghostreel/internal/mapcache"
	"github.com/dmang-dev/ghostreel/internal/timeline"
)

var testdata = filepath.Join("..", "..", "testdata")

func newTestServer(t *testing.T, mapDirs ...string) (*httptest.Server, *app.Service) {
	t.Helper()
	svc := &app.Service{CacheDir: t.TempDir(), Resolver: &mapcache.Resolver{MapDirs: mapDirs}}
	ts := httptest.NewServer((&Server{App: svc, Library: []string{filepath.Join(testdata, "replays")}}).Handler())
	t.Cleanup(ts.Close)
	return ts, svc
}

func post(t *testing.T, url, name string, body []byte) (*http.Response, Summary) {
	t.Helper()
	req, _ := http.NewRequest(http.MethodPost, url, bytes.NewReader(body))
	req.Header.Set("X-Filename", name)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var s Summary
	json.NewDecoder(resp.Body).Decode(&s)
	return resp, s
}

func get(t *testing.T, url string) (int, []byte) {
	t.Helper()
	resp, err := http.Get(url)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var buf bytes.Buffer
	buf.ReadFrom(resp.Body)
	return resp.StatusCode, buf.Bytes()
}

func TestOpenReplayAndFetch(t *testing.T) {
	replayPath := filepath.Join(testdata, "replays", "GhostProtocol_2026-03-11_18-03-30_Defeat.SC2Replay")
	data, err := os.ReadFile(replayPath)
	if err != nil {
		t.Skip("test replay not present")
	}

	// Without map folders: the replay loads, the map is reported missing.
	ts, _ := newTestServer(t)
	resp, s := post(t, ts.URL+"/api/open", "x.SC2Replay", data)
	if resp.StatusCode != 200 || s.ID == "" || s.HasMap || !strings.Contains(s.MapError, "AbyssalReefAIE.SC2Map") {
		t.Fatalf("open: %d %+v", resp.StatusCode, s)
	}
	code, tlBytes := get(t, ts.URL+"/api/r/"+s.ID+"/timeline")
	if code != 200 {
		t.Fatalf("timeline: %d", code)
	}
	tl, err := timeline.Unmarshal(tlBytes)
	if err != nil || len(tl.Units) == 0 {
		t.Fatalf("timeline decode: %v", err)
	}
	if code, _ := get(t, ts.URL+"/api/r/"+s.ID+"/map"); code != 404 {
		t.Errorf("map without a map: %d", code)
	}
	// Replay data isn't cached by the browser: the map can change under the
	// same id.
	if r, err := http.Get(ts.URL + "/api/r/" + s.ID + "/timeline"); err == nil {
		if cc := r.Header.Get("Cache-Control"); cc != "no-cache" {
			t.Errorf("timeline Cache-Control %q", cc)
		}
		r.Body.Close()
	}
	code, body := get(t, ts.URL+"/api/r/"+s.ID+"/analysis")
	if code != 200 || !strings.Contains(string(body), `"plugin":"build-order"`) || !strings.Contains(string(body), `"plugin":"battles"`) {
		t.Errorf("analysis: %d %.200s", code, body)
	}

	// Picking the map file fixes it.
	mapData, err := os.ReadFile(filepath.Join(testdata, "maps", "AbyssalReefAIE.SC2Map"))
	if err != nil {
		t.Skip("test map not present")
	}
	resp, s2 := post(t, ts.URL+"/api/r/"+s.ID+"/map", "AbyssalReefAIE.SC2Map", mapData)
	if resp.StatusCode != 200 || !s2.HasMap || s2.MapHow != "manual" {
		t.Fatalf("set map: %d %+v", resp.StatusCode, s2)
	}
	code, mj := get(t, ts.URL+"/api/r/"+s.ID+"/map")
	var m struct {
		Width, Height int
		TerrainMasks  *struct{ Width, Height int }
	}
	if code != 200 || json.Unmarshal(mj, &m) != nil || m.Width != 200 || m.Height != 176 {
		t.Fatalf("map: %d %+v", code, m)
	}
	// The texture masks: two RGBA planes, gzipped.
	code, gz := get(t, ts.URL+"/api/r/"+s.ID+"/masks")
	if m.TerrainMasks == nil {
		if code != 404 {
			t.Errorf("masks without masks: %d", code)
		}
	} else {
		zr, err := gzip.NewReader(bytes.NewReader(gz))
		if code != 200 || err != nil {
			t.Fatalf("masks: %d %v", code, err)
		}
		planes, err := io.ReadAll(zr)
		if want := m.TerrainMasks.Width * m.TerrainMasks.Height * 8; err != nil || len(planes) != want {
			t.Errorf("masks: %d bytes (%v), want %d", len(planes), err, want)
		}
		t.Logf("masks: %d KiB gzipped", len(gz)/1024)
	}
	if code, png := get(t, ts.URL+"/api/r/"+s.ID+"/minimap"); code != 200 || !bytes.HasPrefix(png, []byte("\x89PNG")) {
		t.Errorf("minimap: %d", code)
	}

	// A wrong map is rejected.
	other, err := os.ReadFile(filepath.Join(testdata, "maps", "DefeatMarines.SC2Map"))
	if err == nil {
		if resp, _ := post(t, ts.URL+"/api/r/"+s.ID+"/map", "DefeatMarines.SC2Map", other); resp.StatusCode != 422 {
			t.Errorf("wrong-size map accepted: %d", resp.StatusCode)
		}
	}
}

func TestTimelineCache(t *testing.T) {
	data, err := os.ReadFile(filepath.Join(testdata, "replays", "GhostProtocol_2026-03-11_18-03-30_Defeat.SC2Replay"))
	if err != nil {
		t.Skip("test replay not present")
	}
	_, svc := newTestServer(t, filepath.Join(testdata, "maps"))
	first, err := svc.LoadBytes(t.Context(), "a", data)
	if err != nil || first.FromCache || first.Map == nil {
		t.Fatalf("first load: %v fromCache=%v map=%v (%s)", err, first.FromCache, first.Map != nil, first.MapErr)
	}
	// A fresh service with the same cache dir reuses the timeline.
	svc2 := &app.Service{CacheDir: svc.CacheDir, Resolver: svc.Resolver}
	second, err := svc2.LoadBytes(t.Context(), "a", data)
	if err != nil || !second.FromCache || !bytes.Equal(first.Timeline, second.Timeline) || second.Info.MapFileName == "" {
		t.Fatalf("second load: %v fromCache=%v", err, second.FromCache)
	}
}

func TestLibrary(t *testing.T) {
	ts, _ := newTestServer(t)
	code, body := get(t, ts.URL+"/api/library")
	if code != 200 {
		t.Fatal(code)
	}
	var list []struct{ Name string }
	json.Unmarshal(body, &list)
	if len(list) == 0 {
		t.Skip("no replays in testdata")
	}
	for _, bad := range []string{"../go.mod", `..\go.mod`, "", "nope.SC2Replay"} {
		b, _ := json.Marshal(map[string]string{"name": bad})
		resp, err := http.Post(ts.URL+"/api/library/open", "application/json", bytes.NewReader(b))
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		if resp.StatusCode == 200 {
			t.Errorf("library open %q succeeded", bad)
		}
	}
	b, _ := json.Marshal(map[string]string{"name": list[0].Name})
	resp, err := http.Post(ts.URL+"/api/library/open", "application/json", bytes.NewReader(b))
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != 200 && resp.StatusCode != 422 { // 422 for the pre-2.0.8 test replay
		t.Errorf("library open %q: %d", list[0].Name, resp.StatusCode)
	}
}

func TestOpenPathFindsMapBeside(t *testing.T) {
	src := filepath.Join(testdata, "replays", "GhostProtocol_2026-03-11_18-03-30_Defeat.SC2Replay")
	data, err := os.ReadFile(src)
	mapData, err2 := os.ReadFile(filepath.Join(testdata, "maps", "AbyssalReefAIE.SC2Map"))
	if err != nil || err2 != nil {
		t.Skip("test replay or map not present")
	}
	dir := t.TempDir()
	replayPath := filepath.Join(dir, "game.SC2Replay")
	os.WriteFile(replayPath, data, 0o644)
	os.Mkdir(filepath.Join(dir, "maps"), 0o755)
	os.WriteFile(filepath.Join(dir, "maps", "AbyssalReefAIE.SC2Map"), mapData, 0o644)
	body, _ := json.Marshal(map[string]string{"path": replayPath})

	// serve doesn't open paths.
	ts, _ := newTestServer(t)
	if resp, _ := post(t, ts.URL+"/api/open-path", "", body); resp.StatusCode != 404 {
		t.Errorf("open-path without OpenPaths: %d", resp.StatusCode)
	}

	svc := &app.Service{CacheDir: t.TempDir(), Resolver: &mapcache.Resolver{}}
	desk := httptest.NewServer((&Server{App: svc, OpenPaths: true}).Handler())
	defer desk.Close()
	resp, s := post(t, desk.URL+"/api/open-path", "", body)
	if resp.StatusCode != 200 || !s.HasMap || s.MapHow != "beside" {
		t.Fatalf("open-path: %d %+v", resp.StatusCode, s)
	}
	bad, _ := json.Marshal(map[string]string{"path": "relative.SC2Replay"})
	if resp, _ := post(t, desk.URL+"/api/open-path", "", bad); resp.StatusCode != 400 {
		t.Errorf("relative path: %d", resp.StatusCode)
	}
	if code, _ := get(t, desk.URL+"/api/pick"); code != 405 {
		t.Errorf("GET pick: %d", code)
	}
}

type memSettings struct{ s Settings }

func (m *memSettings) Settings() Settings     { return m.s }
func (m *memSettings) GameDataStatus() string { return "status" }
func (m *memSettings) Apply(s Settings) error {
	if s.GameData == "bad" {
		return errors.New("not an install")
	}
	m.s = s
	return nil
}

func TestSettings(t *testing.T) {
	ts, _ := newTestServer(t)
	if code, _ := get(t, ts.URL+"/api/settings"); code != 404 {
		t.Errorf("settings without a store: %d", code)
	}
	svc := &app.Service{CacheDir: t.TempDir()}
	desk := httptest.NewServer((&Server{App: svc, Settings: &memSettings{}}).Handler())
	defer desk.Close()
	put := func(body string) (int, map[string]any) {
		req, _ := http.NewRequest(http.MethodPut, desk.URL+"/api/settings", strings.NewReader(body))
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		var out map[string]any
		json.NewDecoder(resp.Body).Decode(&out)
		return resp.StatusCode, out
	}
	if code, out := put(`{"gameData":"I:/SC2","mapFolders":["P:/maps"]}`); code != 200 || out["gameData"] != "I:/SC2" || out["gameDataStatus"] != "status" {
		t.Errorf("put: %d %v", code, out)
	}
	if code, out := put(`{"gameData":"bad"}`); code != 400 || out["error"] != "not an install" {
		t.Errorf("bad put: %d %v", code, out)
	}
	code, body := get(t, desk.URL+"/api/settings")
	if code != 200 || !strings.Contains(string(body), `"mapFolders":["P:/maps"]`) {
		t.Errorf("get: %d %s", code, body)
	}
}
