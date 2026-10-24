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

	"github.com/OWNER/ghostreel/internal/app"
	"github.com/OWNER/ghostreel/internal/assets"
	"github.com/OWNER/ghostreel/internal/server"
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
	srv := &server.Server{App: svc, Library: replayFolders()}
	if dir := assets.FindInstall(); dir != "" {
		go func() { // reading the game data takes a moment; the 2D view needs none of it
			if l, err := assets.Open(dir, svc.CacheDir); err != nil {
				log.Printf("3D models unavailable: %v", err)
			} else {
				srv.SetAssets(l)
			}
		}()
	}

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
		// Let the page handle dropped files like a browser does (the viewer
		// uploads them to the API).
		DragAndDrop: &options.DragAndDrop{DisableWebViewDrop: false},
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
