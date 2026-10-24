// SPDX-License-Identifier: Apache-2.0

// Package app is the layer frontends talk to: it loads a replay, builds or
// reuses its cached timeline, resolves and decodes its map, and keeps the
// results for serving. Both `ghostreel serve` and the desktop app use it
// through the HTTP API in package server.
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
	"strings"
	"sync"

	"github.com/OWNER/ghostreel/internal/mapcache"
	"github.com/OWNER/ghostreel/internal/mapfile"
	"github.com/OWNER/ghostreel/internal/replay"
	"github.com/OWNER/ghostreel/internal/timeline"
)

// CacheVersion is part of every cached timeline's file name. Bump it whenever
// the builder's output changes so stale timelines are rebuilt.
const CacheVersion = "tl1"

// Service loads replays. It is safe for concurrent use.
type Service struct {
	// CacheDir holds timelines/<hash>-<CacheVersion>.pb and user-picked maps.
	// Empty disables caching.
	CacheDir string
	Resolver *mapcache.Resolver
	// Commands keeps player commands in timelines. Off by default: decoding
	// them dominates load time on long games and the viewer doesn't need them.
	Commands bool
	Log      func(format string, args ...any)

	mu     sync.Mutex
	loaded map[string]*Loaded
}

// Loaded is one replay ready for viewing.
type Loaded struct {
	ID       string // replay content hash
	Name     string // file name the replay came from
	Info     replay.Info
	Timeline []byte // binary timeline
	FromCache bool

	Map     []byte // mapfile.Export JSON; nil when no map was found
	Minimap []byte // PNG; nil when unavailable
	MapHow  mapcache.How
	MapErr  string // why the map is missing, for the UI
}

// New returns a service with the default cache directory and map resolver.
func New() *Service {
	s := &Service{Resolver: mapcache.Default()}
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

// LoadFile loads a replay from disk.
func (s *Service) LoadFile(ctx context.Context, path string) (*Loaded, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	return s.LoadBytes(ctx, filepath.Base(path), data)
}

// LoadBytes loads a replay from its contents (an upload or a dropped file).
func (s *Service) LoadBytes(ctx context.Context, name string, data []byte) (*Loaded, error) {
	sum := sha256.Sum256(data)
	id := hex.EncodeToString(sum[:])
	if l := s.Get(id); l != nil {
		return l, nil
	}
	l := &Loaded{ID: id, Name: name}

	cachePath := ""
	if s.CacheDir != "" {
		suffix := CacheVersion
		if s.Commands {
			suffix += "c"
		}
		cachePath = filepath.Join(s.CacheDir, "timelines", id+"-"+suffix+".pb")
	}
	if cachePath != "" {
		if tl, err := os.ReadFile(cachePath); err == nil {
			r, err := replay.DecodeWith(bytes.NewReader(data), replay.Options{InfoOnly: true})
			if err != nil {
				return nil, err
			}
			l.Info, l.Timeline, l.FromCache = r.Info, tl, true
			s.logf("timeline for %s from cache", name)
		}
	}
	if l.Timeline == nil {
		r, err := replay.DecodeWith(bytes.NewReader(data), replay.Options{SkipGameEvents: !s.Commands})
		if err != nil {
			return nil, err
		}
		r.Info.Hash = id
		tl, err := timeline.Build(&r.Info, r.Events, timeline.Options{Generator: "ghostreel " + CacheVersion, SkipCommands: !s.Commands})
		if err != nil {
			return nil, err
		}
		if l.Timeline, err = timeline.Marshal(tl); err != nil {
			return nil, err
		}
		l.Info = r.Info
		if cachePath != "" {
			if err := writeFileAtomic(cachePath, l.Timeline); err != nil {
				s.logf("timeline cache: %v", err)
			}
		}
	}
	l.Info.Hash = id

	s.attachMap(ctx, l, "")
	s.put(l)
	return l, nil
}

// SetMap loads a replay's map from a file the user picked.
func (s *Service) SetMap(ctx context.Context, id string, name string, data []byte) (*Loaded, error) {
	l := s.Get(id)
	if l == nil {
		return nil, fmt.Errorf("replay %s is not loaded", id)
	}
	if s.CacheDir == "" {
		return nil, errors.New("no cache directory to store the map in")
	}
	sum := sha256.Sum256(data)
	ext := strings.ToLower(filepath.Ext(name))
	if ext != ".sc2map" && ext != ".s2ma" {
		ext = ".s2ma"
	}
	p := filepath.Join(s.CacheDir, "maps", "picked", hex.EncodeToString(sum[:8])+ext)
	if err := writeFileAtomic(p, data); err != nil {
		return nil, err
	}
	cp := *l
	s.attachMap(ctx, &cp, p)
	if cp.Map == nil {
		return nil, errors.New(cp.MapErr)
	}
	s.put(&cp)
	return &cp, nil
}

// attachMap resolves and decodes the map; failures are recorded, not fatal,
// so the viewer can still play on a blank grid.
func (s *Service) attachMap(ctx context.Context, l *Loaded, manual string) {
	l.Map, l.Minimap, l.MapErr, l.MapHow = nil, nil, "", ""
	if s.Resolver == nil {
		l.MapErr = "map lookup disabled"
		return
	}
	path, how, err := s.Resolver.Resolve(ctx, &l.Info, manual)
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
	if l.Info.MapTitle != "" {
		m.Name = l.Info.MapTitle
	}
	if m.Width != l.Info.MapWidth || m.Height != l.Info.MapHeight {
		l.MapErr = fmt.Sprintf("map %s is %d×%d but the replay was played on %d×%d; pick the right map",
			filepath.Base(path), m.Width, m.Height, l.Info.MapWidth, l.Info.MapHeight)
		return
	}
	if l.Map, err = json.Marshal(m.Export(false)); err != nil {
		l.Map, l.MapErr = nil, err.Error()
		return
	}
	if m.Minimap != nil {
		var buf bytes.Buffer
		if png.Encode(&buf, m.Minimap) == nil {
			l.Minimap = buf.Bytes()
		}
	}
	l.MapHow = how
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
