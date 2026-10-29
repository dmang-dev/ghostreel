// SPDX-License-Identifier: Apache-2.0

// Package app is the layer frontends talk to: it loads a replay, resolves
// its map, builds (or reuses a cached) timeline at the best fidelity
// available, and keeps the results for serving. Both `ghostreel serve` and
// the desktop app use it through the HTTP API in package server.
package app

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"image/png"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"

	"github.com/dmang-dev/ghostreel/internal/analysis"
	"github.com/dmang-dev/ghostreel/internal/command"
	"github.com/dmang-dev/ghostreel/internal/mapcache"
	"github.com/dmang-dev/ghostreel/internal/mapfile"
	"github.com/dmang-dev/ghostreel/internal/replay"
	"github.com/dmang-dev/ghostreel/internal/timeline"
)

// CacheVersion is part of every cached timeline's file name. Bump it whenever
// the builders' output changes so stale timelines are rebuilt.
const CacheVersion = "tl8"

// browser is true in the browser build (GOOS=js, cmd/ghostreel-wasm). It
// has no file system to look maps up in and can't reach Blizzard's depot,
// so map lookup is compiled out of it: the HTTP client and TLS stack it
// would pull in are about a quarter of ghostreel.wasm.
const browser = runtime.GOOS == "js"

// Tiers a Service can build.
const (
	TierTracker = "tracker"
	TierCommand = "command"
)

// Service loads replays. It is safe for concurrent use.
type Service struct {
	// CacheDir holds timelines/<hash>-<version>-<tier>.pb and user-picked
	// maps. Empty disables caching.
	CacheDir string
	Resolver *mapcache.Resolver
	// Tier is the reconstruction to use when no engine recording exists:
	// TierCommand (follows players' orders; needs game events, so loading
	// is slower) or TierTracker. Empty means TierTracker.
	Tier string
	// Commands keeps player commands in the event feed.
	Commands bool
	Log      func(format string, args ...any)

	mu     sync.Mutex
	loaded map[string]*Loaded
}

// Loaded is one replay ready for viewing.
type Loaded struct {
	ID        string // replay content hash
	Name      string // file name the replay came from
	Info      replay.Info
	Timeline  []byte // binary timeline
	Fidelity  string // "tracker", "command" or "engine"
	FromCache bool

	Map     []byte // mapfile.Export JSON; nil when no map was found
	Doodads []byte // JSON list of the map's doodads (mapfile.Object); nil without a map
	Minimap []byte // PNG; nil when unavailable
	MapHow  mapcache.How
	MapErr  string // why the map is missing, for the UI

	data       []byte       // the replay itself, to rebuild after a map change
	mapFile    *mapfile.Map // the decoded map, for the command tier's pathing
	noLookup   bool         // map lookup is disabled (the browser build)
	analysis   []byte       // JSON of the plugins' reports, computed on first request
	enginePath string       // ground-truth recording next to the replay, if any
}

// New returns a service with the default cache directory and map resolver,
// building command-tier timelines.
func New() *Service {
	s := &Service{Resolver: mapcache.Default(), Tier: TierCommand}
	if dir, err := os.UserCacheDir(); err == nil {
		s.CacheDir = filepath.Join(dir, "ghostreel")
	}
	return s
}

func (s *Service) logf(format string, args ...any) {
	if s.Log != nil {
		s.Log(format, args...)
	}
}

// Get returns a previously loaded replay.
func (s *Service) Get(id string) *Loaded {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.loaded[id]
}

func (s *Service) put(l *Loaded) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.loaded == nil {
		s.loaded = map[string]*Loaded{}
	}
	s.loaded[l.ID] = l
}

// LoadFile loads a replay from disk. An engine recording saved next to it
// (<replay>.engine.pb.gz, from ghostreel-engine) makes the timeline exact.
func (s *Service) LoadFile(ctx context.Context, path string) (*Loaded, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	eng := strings.TrimSuffix(path, filepath.Ext(path)) + ".engine.pb.gz"
	if _, err := os.Stat(eng); err != nil {
		eng = ""
	}
	return s.load(ctx, filepath.Base(path), data, eng, path)
}

// LoadBytes loads a replay from its contents (an upload or a dropped file).
func (s *Service) LoadBytes(ctx context.Context, name string, data []byte) (*Loaded, error) {
	return s.load(ctx, name, data, "", "")
}

// load decodes and caches a replay; path is where it was read from ("" for
// uploads), so maps kept beside it can be found.
func (s *Service) load(ctx context.Context, name string, data []byte, enginePath, path string) (*Loaded, error) {
	sum := sha256.Sum256(data)
	id := hex.EncodeToString(sum[:])
	if l := s.Get(id); l != nil {
		// Look for a missing map again: it may be beside the replay (opened
		// by path this time) or in a map folder added since.
		if l.Map == nil && !l.noLookup && s.Resolver != nil {
			cp := *l
			if path != "" {
				cp.Info.Path = path
			}
			if nl, err := s.replaceMap(ctx, &cp, "", s.Resolver); err == nil {
				return nl, nil
			}
		}
		return l, nil
	}
	r, err := replay.DecodeWith(bytes.NewReader(data), replay.Options{InfoOnly: true})
	if err != nil {
		return nil, err
	}
	l := &Loaded{ID: id, Name: name, Info: r.Info, data: data, enginePath: enginePath}
	l.Info.Hash = id
	l.Info.Path = path
	s.attachMap(ctx, l, "", s.Resolver)
	if err := s.buildTimeline(l); err != nil {
		return nil, err
	}
	s.put(l)
	return l, nil
}

// fidelity is the tier buildTimeline will produce for l.
func (s *Service) fidelity(l *Loaded) string {
	switch {
	case l.enginePath != "":
		return "engine"
	case s.Tier == TierCommand:
		return TierCommand
	}
	return TierTracker
}

// buildTimeline (re)builds l's timeline, using the cache when possible. The
// command tier depends on the map (for pathing), so its cache key records
// whether a map was used.
func (s *Service) buildTimeline(l *Loaded) error {
	fid := s.fidelity(l)
	key := fid
	if fid == TierCommand && l.mapFile == nil {
		key += "-nomap"
	}
	if s.Commands {
		key += "-cmds"
	}
	cachePath := ""
	if s.CacheDir != "" && fid != "engine" {
		cachePath = filepath.Join(s.CacheDir, "timelines", l.ID+"-"+CacheVersion+"-"+key+".pb")
		if tl, err := os.ReadFile(cachePath); err == nil {
			l.Timeline, l.Fidelity, l.FromCache = tl, fid, true
			s.logf("timeline for %s from cache", l.Name)
			return nil
		}
	}
	// Game events give the command tier its orders and every tier its action
	// counts (APM); only an explicit tracker tier skips them, for speed.
	needGame := s.Commands || fid != TierTracker
	r, err := replay.DecodeWith(bytes.NewReader(l.data), replay.Options{SkipGameEvents: !needGame})
	if err != nil {
		return err
	}
	r.Info.Hash = l.ID
	tl, err := timeline.Build(&r.Info, r.Events, timeline.Options{Generator: "ghostreel " + CacheVersion, SkipCommands: !s.Commands})
	if err != nil {
		return err
	}
	switch fid {
	case "engine":
		if err := applyEngine(tl, l.enginePath); err != nil {
			s.logf("%s: engine recording not used: %v", l.Name, err)
			fid = TierTracker
		}
	case TierCommand:
		opts := command.Options{}
		if l.mapFile != nil {
			opts.Grid = command.Grid(l.mapFile)
		}
		command.Apply(tl, r.Events, opts)
	}
	if l.Timeline, err = timeline.Marshal(tl); err != nil {
		return err
	}
	l.analysis = nil
	l.Fidelity, l.FromCache = fid, false
	if cachePath != "" {
		if err := writeFileAtomic(cachePath, l.Timeline); err != nil {
			s.logf("timeline cache: %v", err)
		}
	}
	return nil
}

// Analysis runs the analysis plugins (package analysis) on a loaded
// replay's timeline, once, and returns their reports as JSON.
func (s *Service) Analysis(id string) ([]byte, error) {
	l := s.Get(id)
	if l == nil {
		return nil, fmt.Errorf("replay %s is not loaded", id)
	}
	s.mu.Lock()
	done := l.analysis
	s.mu.Unlock()
	if done != nil {
		return done, nil
	}
	tl, err := timeline.Unmarshal(l.Timeline)
	if err != nil {
		return nil, err
	}
	out, err := json.Marshal(analysis.Run(tl))
	if err != nil {
		return nil, err
	}
	s.mu.Lock()
	l.analysis = out
	s.mu.Unlock()
	return out, nil
}

// DownloadMap retries the map lookup with depot downloads allowed for this
// one replay. It is what the viewer's "download from Blizzard" button calls,
// so a download only ever happens on the user's explicit request.
func (s *Service) DownloadMap(ctx context.Context, id string) (*Loaded, error) {
	l := s.Get(id)
	if l == nil {
		return nil, fmt.Errorf("replay %s is not loaded", id)
	}
	if l.Info.MapHandle == nil {
		return nil, errors.New("this replay's map isn't on Blizzard's depot (it was played on a local map file)")
	}
	if s.Resolver == nil {
		return nil, errors.New("map lookup disabled")
	}
	return s.replaceMap(ctx, l, "", s.Resolver.WithNetwork())
}

// FindMap looks for a replay's missing map again, in the places as they are
// now (map folders added in the settings, say).
func (s *Service) FindMap(ctx context.Context, id string) (*Loaded, error) {
	l := s.Get(id)
	if l == nil {
		return nil, fmt.Errorf("replay %s is not loaded", id)
	}
	if l.Map != nil {
		return l, nil
	}
	if s.Resolver == nil || l.noLookup {
		return nil, errors.New("map lookup disabled")
	}
	return s.replaceMap(ctx, l, "", s.Resolver)
}

// MapFile is the decoded map, or nil when there is none.
func (l *Loaded) MapFile() *mapfile.Map { return l.mapFile }

// CanDownload reports whether DownloadMap could find this replay's map.
func (l *Loaded) CanDownload() bool {
	return l.Map == nil && !l.noLookup && l.Info.MapHandle != nil && l.Info.MapHandle.DepotURL != ""
}

// SetMap loads a replay's map from a file the user picked.
func (s *Service) SetMap(ctx context.Context, id string, name string, data []byte) (*Loaded, error) {
	l := s.Get(id)
	if l == nil {
		return nil, fmt.Errorf("replay %s is not loaded", id)
	}
	m, err := mapfile.OpenBytes(name, data)
	if err != nil {
		return nil, fmt.Errorf("could not read map %s: %v", filepath.Base(name), err)
	}
	if s.CacheDir != "" {
		// Keep a copy with the other maps (best effort: the browser build
		// has nowhere to write, and needs none).
		sum := sha256.Sum256(data)
		ext := strings.ToLower(filepath.Ext(name))
		if ext != ".sc2map" && ext != ".s2ma" {
			ext = ".s2ma"
		}
		p := filepath.Join(s.CacheDir, "maps", "picked", hex.EncodeToString(sum[:8])+ext)
		if err := writeFileAtomic(p, data); err != nil {
			s.logf("keeping picked map: %v", err)
		}
	}
	cp := *l
	s.clearMap(&cp)
	s.useMap(&cp, m, mapcache.HowManual, filepath.Base(name))
	return s.commitMap(&cp)
}

// replaceMap attaches a newly found map and, when the timeline depends on
// it, rebuilds the timeline.
func (s *Service) replaceMap(ctx context.Context, l *Loaded, manual string, r *mapcache.Resolver) (*Loaded, error) {
	cp := *l
	s.attachMap(ctx, &cp, manual, r)
	return s.commitMap(&cp)
}

// commitMap stores a copy of a loaded replay with its new map, rebuilding
// the timeline when it depends on the map.
func (s *Service) commitMap(cp *Loaded) (*Loaded, error) {
	if cp.Map == nil {
		return nil, errors.New(cp.MapErr)
	}
	if s.fidelity(cp) == TierCommand {
		if err := s.buildTimeline(cp); err != nil {
			return nil, err
		}
	}
	s.put(cp)
	return cp, nil
}

// attachMap resolves and decodes the map; failures are recorded, not fatal,
// so the viewer can still play on a blank grid.
func (s *Service) attachMap(ctx context.Context, l *Loaded, manual string, r *mapcache.Resolver) {
	s.clearMap(l)
	l.noLookup = browser || r == nil
	if browser || r == nil {
		l.MapErr = "map lookup disabled"
		return
	}
	path, how, err := r.Resolve(ctx, &l.Info, manual)
	if err != nil {
		l.MapErr = err.Error()
		s.logf("%s: %v", l.Name, err)
		return
	}
	m, err := mapfile.Open(path)
	if err != nil {
		l.MapErr = fmt.Sprintf("could not read map %s: %v", filepath.Base(path), err)
		return
	}
	s.useMap(l, m, how, filepath.Base(path))
}

func (s *Service) clearMap(l *Loaded) {
	l.Map, l.Doodads, l.Minimap, l.MapErr, l.MapHow, l.mapFile = nil, nil, nil, "", "", nil
}

// useMap attaches a decoded map after checking it fits the replay; file
// names it in messages.
func (s *Service) useMap(l *Loaded, m *mapfile.Map, how mapcache.How, file string) {
	var err error
	if l.Info.MapTitle != "" {
		m.Name = l.Info.MapTitle
	}
	if m.Width != l.Info.MapWidth || m.Height != l.Info.MapHeight {
		l.MapErr = fmt.Sprintf("map %s is %d×%d but the replay was played on %d×%d; pick the right map",
			file, m.Width, m.Height, l.Info.MapWidth, l.Info.MapHeight)
		return
	}
	if l.Map, err = json.Marshal(m.Export(false)); err != nil {
		l.Map, l.MapErr = nil, err.Error()
		return
	}
	doodads := []mapfile.Object{}
	for _, o := range m.Objects {
		if o.Kind == mapfile.ObjectDoodad {
			doodads = append(doodads, o)
		}
	}
	if l.Doodads, err = json.Marshal(doodads); err != nil {
		l.Doodads = nil
	}
	if m.Minimap != nil {
		var buf bytes.Buffer
		if png.Encode(&buf, m.Minimap) == nil {
			l.Minimap = buf.Bytes()
		}
	}
	l.MapHow, l.mapFile = how, m
}

func writeFileAtomic(path string, data []byte) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".tmp-*")
	if err != nil {
		return err
	}
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		os.Remove(tmp.Name())
		return err
	}
	if err := tmp.Close(); err != nil {
		os.Remove(tmp.Name())
		return err
	}
	return os.Rename(tmp.Name(), path)
}
