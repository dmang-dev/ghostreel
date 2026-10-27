// SPDX-License-Identifier: Apache-2.0

package main

import (
	"encoding/json"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/dmang-dev/ghostreel/internal/app"
	"github.com/dmang-dev/ghostreel/internal/assets"
	"github.com/dmang-dev/ghostreel/internal/server"
)

// settingsStore keeps the settings page's choices in settings.json under
// the user config folder and applies them: the game data folder for 3D
// models and ground textures, and extra folders to search for local maps.
type settingsStore struct {
	path string
	srv  *server.Server
	svc  *app.Service

	mu     sync.Mutex
	cur    server.Settings
	status string
	gen    int // bumped on every game data change, so a stale load is dropped
}

func newSettingsStore(srv *server.Server, svc *app.Service) *settingsStore {
	st := &settingsStore{srv: srv, svc: svc}
	if dir, err := os.UserConfigDir(); err == nil {
		st.path = filepath.Join(dir, "ghostreel", "settings.json")
		if b, err := os.ReadFile(st.path); err == nil {
			if err := json.Unmarshal(b, &st.cur); err != nil {
				log.Printf("settings: %v", err)
			}
		}
	}
	st.setMapDirs(st.cur)
	st.openGameData()
	return st
}

// setMapDirs searches the chosen map folders and, with a chosen install,
// its Maps folder (the defaults cover only the usual install places).
func (st *settingsStore) setMapDirs(s server.Settings) {
	dirs := s.MapFolders
	if s.GameData != "" {
		dirs = append(dirs[:len(dirs):len(dirs)], filepath.Join(s.GameData, "Maps"))
	}
	st.svc.Resolver.SetExtraMapDirs(dirs)
}

func (st *settingsStore) Settings() server.Settings {
	st.mu.Lock()
	defer st.mu.Unlock()
	return st.cur
}

func (st *settingsStore) GameDataStatus() string {
	st.mu.Lock()
	defer st.mu.Unlock()
	return st.status
}

func (st *settingsStore) Apply(s server.Settings) error {
	if s.GameData = strings.TrimSpace(s.GameData); s.GameData != "" {
		dir, err := assets.InstallDir(s.GameData)
		if err != nil {
			return err
		}
		s.GameData = dir
	}
	var folders []string
	for _, f := range s.MapFolders {
		if f = strings.TrimSpace(f); f == "" {
			continue
		}
		if fi, err := os.Stat(f); err != nil || !fi.IsDir() {
			return fmt.Errorf("map folder %s doesn't exist", f)
		}
		folders = append(folders, filepath.Clean(f))
	}
	s.MapFolders = folders

	b, _ := json.MarshalIndent(s, "", "  ")
	if st.path == "" {
		return fmt.Errorf("no user config folder to save settings in")
	}
	if err := os.MkdirAll(filepath.Dir(st.path), 0o755); err != nil {
		return fmt.Errorf("saving settings: %v", err)
	}
	if err := os.WriteFile(st.path, b, 0o644); err != nil {
		return fmt.Errorf("saving settings: %v", err)
	}

	st.mu.Lock()
	changed := s.GameData != st.cur.GameData
	st.cur = s
	st.mu.Unlock()
	st.setMapDirs(s)
	if changed {
		st.openGameData()
	}
	return nil
}

// openGameData reads the chosen (or found) install in the background; the
// 2D view needs none of it, and reading takes a moment.
func (st *settingsStore) openGameData() {
	st.mu.Lock()
	st.gen++
	gen := st.gen
	dir := st.cur.GameData
	if dir == "" {
		dir = assets.FindInstall()
	}
	st.srv.SetAssets(nil)
	if dir == "" {
		st.status = "No StarCraft II install found. Choose its folder to see the game's models and ground."
		st.mu.Unlock()
		return
	}
	st.status = "Reading the game data in " + dir + "…"
	st.mu.Unlock()
	go func() {
		l, err := assets.Open(dir, st.svc.CacheDir)
		st.mu.Lock()
		defer st.mu.Unlock()
		if gen != st.gen {
			return // the folder changed meanwhile
		}
		if err != nil {
			st.status = fmt.Sprintf("Could not read the game data in %s: %v", dir, err)
			log.Print(st.status)
			return
		}
		st.srv.SetAssets(l)
		st.status = fmt.Sprintf("Using %s (game build %s).", dir, l.Build())
	}()
}
