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

	"github.com/OWNER/ghostreel/internal/app"
	"github.com/OWNER/ghostreel/internal/server"
)

func cmdServe(args []string) error {
	fs := flag.NewFlagSet("serve", flag.ExitOnError)
	addr := fs.String("addr", "127.0.0.1:8080", "listen address (keep it on localhost: the API reads local files)")
	replays := fs.String("replays", "", "folders of replays to list in the viewer, "+string(os.PathListSeparator)+"-separated")
	static := fs.String("static", filepath.Join("viewer", "dist"), "built viewer to serve (npm run build in viewer/)")
	commands := fs.Bool("commands", false, "keep player commands in timelines (slow on long games)")
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
	if fi, err := os.Stat(*static); err == nil && fi.IsDir() {
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

func onOff(b bool) string {
	if b {
		return "on"
	}
	return "off (use --network to allow)"
}
