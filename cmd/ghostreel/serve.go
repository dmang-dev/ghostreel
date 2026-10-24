// SPDX-License-Identifier: Apache-2.0

package main

import (
	"errors"
	"flag"
	"log"
	"net"
	"net/http"
	"os"
	"path/filepath"

	"github.com/dmang-dev/ghostreel/internal/app"
	"github.com/dmang-dev/ghostreel/internal/assets"
	"github.com/dmang-dev/ghostreel/internal/server"
)

func cmdServe(args []string) error {
	fs := flag.NewFlagSet("serve", flag.ExitOnError)
	addr := fs.String("addr", "127.0.0.1:8080", "listen address (keep it on localhost: the API reads local files)")
	replays := fs.String("replays", "", "folders of replays to list in the viewer, "+string(os.PathListSeparator)+"-separated")
	static := fs.String("static", filepath.Join("viewer", "dist"), "built viewer to serve (npm run build in viewer/)")
	commands := fs.Bool("commands", false, "keep player commands in timelines (slow on long games)")
	gameData := fs.String("gamedata", assets.FindInstall(), "game installation for 3D models (the folder with .build.info); empty disables them")
	rf := addResolverFlags(fs)
	fs.Parse(reorder(fs, args))

	svc := app.New()
	svc.Resolver = rf.resolver()
	svc.Commands = *commands
	if rf.cacheDir != "" {
		svc.CacheDir = rf.cacheDir
	}
	svc.Log = func(format string, a ...any) { log.Printf(format, a...) }
	srv := &server.Server{App: svc, Library: filepath.SplitList(*replays)}
	if *gameData != "" {
		go func() {
			l, err := assets.Open(*gameData, svc.CacheDir)
			if err != nil {
				log.Printf("3D models unavailable: %v", err)
				return
			}
			srv.SetAssets(l)
			log.Printf("3D models from %s (build %s)", *gameData, l.Build())
		}()
	} else {
		log.Printf("no game installation found: the 3D view uses stand-in shapes (set --gamedata)")
	}
	if !isDir(*static) && !explicit(fs, "static") {
		// A release archive keeps the viewer next to the executable.
		if exe, err := os.Executable(); err == nil {
			if dir := filepath.Join(filepath.Dir(exe), "viewer", "dist"); isDir(dir) {
				*static = dir
			}
		}
	}
	if isDir(*static) {
		srv.Static = os.DirFS(*static)
	} else {
		log.Printf("no viewer build at %s: serving the API only (or run the Vite dev server)", *static)
	}
	ln, err := net.Listen("tcp", *addr)
	if err != nil {
		return err
	}
	if host, _, _ := net.SplitHostPort(*addr); host != "127.0.0.1" && host != "localhost" && host != "::1" {
		log.Printf("warning: listening on %s; anyone who can reach it can load replays from your library folders", *addr)
	}
	log.Printf("viewer at http://%s/  (map downloads %s; timeline cache %s)", ln.Addr(), onOff(rf.network), svc.CacheDir)
	err = http.Serve(ln, srv.Handler())
	if errors.Is(err, http.ErrServerClosed) {
		return nil
	}
	return err
}

func isDir(path string) bool {
	fi, err := os.Stat(path)
	return err == nil && fi.IsDir()
}

// explicit reports whether a flag was given on the command line.
func explicit(fs *flag.FlagSet, name string) bool {
	set := false
	fs.Visit(func(f *flag.Flag) { set = set || f.Name == name })
	return set
}

func onOff(b bool) string {
	if b {
		return "on"
	}
	return "off (use --network to allow)"
}
