// SPDX-License-Identifier: Apache-2.0

package main

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/dmang-dev/ghostreel/internal/app"
	"github.com/dmang-dev/ghostreel/internal/mapcache"
	"github.com/dmang-dev/ghostreel/internal/replay"
	"github.com/dmang-dev/ghostreel/internal/server"
)

func TestSettingsStore(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("APPDATA", filepath.Join(tmp, "config"))         // os.UserConfigDir on Windows
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(tmp, "config")) // and on Linux
	t.Setenv("HOME", tmp)
	t.Setenv("GHOSTREEL_GAMEDATA", "")
	svc := &app.Service{CacheDir: filepath.Join(tmp, "cache"), Resolver: &mapcache.Resolver{}}
	srv := &server.Server{App: svc}
	st := newSettingsStore(srv, svc)

	// A folder that isn't an install is refused, with a message for the user.
	if err := st.Apply(server.Settings{GameData: tmp}); err == nil || !strings.Contains(err.Error(), ".build.info") {
		t.Errorf("not an install: %v", err)
	}
	if err := st.Apply(server.Settings{MapFolders: []string{filepath.Join(tmp, "nope")}}); err == nil {
		t.Error("missing map folder accepted")
	}

	// An install (here a parent of one) is accepted and saved; its Maps
	// folder and the chosen map folders are searched.
	install := filepath.Join(tmp, "Blizzard", "StarCraft II")
	os.MkdirAll(filepath.Join(install, "Maps"), 0o755)
	os.WriteFile(filepath.Join(install, ".build.info"), []byte("x"), 0o644)
	maps := filepath.Join(tmp, "bot maps")
	os.MkdirAll(maps, 0o755)
	os.WriteFile(filepath.Join(maps, "PylonAIE_v4.SC2Map"), []byte("x"), 0o644)
	os.WriteFile(filepath.Join(install, "Maps", "TorchesAIE_v4.SC2Map"), []byte("x"), 0o644)
	if err := st.Apply(server.Settings{GameData: filepath.Dir(install), MapFolders: []string{" " + maps + " ", ""}}); err != nil {
		t.Fatal(err)
	}
	if got := st.Settings(); got.GameData != install || len(got.MapFolders) != 1 || got.MapFolders[0] != maps {
		t.Errorf("settings: %+v", got)
	}
	for _, name := range []string{"PylonAIE_v4.SC2Map", "TorchesAIE_v4.SC2Map"} {
		if _, _, err := svc.Resolver.Resolve(context.Background(), &replay.Info{MapFileName: name}, ""); err != nil {
			t.Errorf("%s: %v", name, err)
		}
	}

	// A new store reads them back.
	again := newSettingsStore(&server.Server{App: svc}, svc)
	if got := again.Settings(); got.GameData != install || len(got.MapFolders) != 1 {
		t.Errorf("reloaded: %+v", got)
	}
}
