// SPDX-License-Identifier: Apache-2.0

// Command ghostreel-desktop is the desktop viewer: a Wails window showing the
// web viewer, with the Go core behind it. The viewer reaches the core through
// the same HTTP API as `ghostreel serve`, served by Wails' asset server, so
// there are no Wails-specific bindings to keep in sync.
//
// It lives in its own Go module so the core module stays free of UI and cgo
// dependencies (Wails needs cgo and GTK on Linux).
package main

import (
	"context"
	"embed"
	"io/fs"
	"log"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/wailsapp/wails/v2"
	"github.com/wailsapp/wails/v2/pkg/options"
	"github.com/wailsapp/wails/v2/pkg/options/assetserver"
	wailsruntime "github.com/wailsapp/wails/v2/pkg/runtime"

	"github.com/dmang-dev/ghostreel/internal/app"
	"github.com/dmang-dev/ghostreel/internal/server"
)

// The viewer build, written here by `wails build` (see wails.json).
//
//go:embed all:frontend/dist
var viewerFS embed.FS

func main() {
	dist, err := fs.Sub(viewerFS, "frontend/dist")
	if err != nil {
		log.Fatal(err)
	}
	svc := app.New() // timelines cached under the user cache dir; depot downloads only on request
	svc.Log = func(format string, a ...any) { log.Printf(format, a...) }
	// The viewer opens replays by path here (native dialog, file drops),
	// so maps kept beside them are found.
	var appCtx context.Context
	srv := &server.Server{App: svc, Library: replayFolders(), OpenPaths: true}
	srv.PickReplay = func() (string, error) {
		return wailsruntime.OpenFileDialog(appCtx, wailsruntime.OpenDialogOptions{
			Title:   "Open replay",
			Filters: []wailsruntime.FileFilter{{DisplayName: "SC2 replays (*.SC2Replay)", Pattern: "*.SC2Replay"}},
		})
	}
	srv.PickFolder = func() (string, error) {
		return wailsruntime.OpenDirectoryDialog(appCtx, wailsruntime.OpenDialogOptions{Title: "Choose a folder"})
	}
	// The settings page: the game data folder (read in the background) and
	// extra map folders.
	srv.Settings = newSettingsStore(srv, svc)

	// "Open with ghostreel": a replay passed on the command line opens at start.
	if len(os.Args) > 1 && strings.EqualFold(filepath.Ext(os.Args[1]), ".SC2Replay") {
		if l, err := svc.LoadFile(context.Background(), os.Args[1]); err != nil {
			log.Printf("%s: %v", os.Args[1], err)
		} else {
			srv.Startup = l
		}
	}

	err = wails.Run(&options.App{
		Title:            "ghostreel",
		Width:            1440,
		Height:           900,
		MinWidth:         900,
		MinHeight:        600,
		BackgroundColour: &options.RGBA{R: 11, G: 13, B: 16, A: 255},
		AssetServer: &assetserver.Options{
			Assets:  dist,
			Handler: srv.Handler(), // /api/* (everything not in the viewer build)
		},
		OnStartup: func(ctx context.Context) { appCtx = ctx },
		// Dropped files reach the viewer with their paths (window.runtime.
		// OnFileDrop); the page still gets the drop too, for map files.
		DragAndDrop: &options.DragAndDrop{EnableFileDrop: true},
	})
	if err != nil {
		log.Fatal(err)
	}
}

// replayFolders finds the game's own replay folders so they show up in the
// viewer's library list: <base>/Accounts/<account>/<toon>/Replays/Multiplayer.
func replayFolders() []string {
	home, err := os.UserHomeDir()
	if err != nil {
		return nil
	}
	var bases []string
	switch runtime.GOOS {
	case "windows":
		bases = []string{
			filepath.Join(home, "Documents", "StarCraft II"),
			filepath.Join(home, "OneDrive", "Documents", "StarCraft II"),
		}
	case "darwin":
		bases = []string{filepath.Join(home, "Library", "Application Support", "Blizzard", "StarCraft II")}
	default:
		bases = []string{filepath.Join(home, "StarCraftII")}
	}
	var dirs []string
	for _, b := range bases {
		found, _ := filepath.Glob(filepath.Join(b, "Accounts", "*", "*", "Replays", "Multiplayer"))
		dirs = append(dirs, found...)
	}
	if extra := os.Getenv("GHOSTREEL_REPLAYS"); extra != "" {
		dirs = append(dirs, filepath.SplitList(extra)...)
	}
	return dirs
}
