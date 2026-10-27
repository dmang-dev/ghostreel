// SPDX-License-Identifier: Apache-2.0

package main

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/dmang-dev/ghostreel/internal/server"
	"github.com/dmang-dev/ghostreel/internal/update"
)

// version is set at release builds (-ldflags "-X main.version=v1.2.3").
var version = "dev"

// latestURL is the newest published release's manifest. GitHub serves
// /releases/latest/download/ from the latest release that isn't a draft or
// pre-release. GHOSTREEL_UPDATE_URL points elsewhere (for testing).
const latestURL = "https://github.com/dmang-dev/ghostreel/releases/latest/download/latest.json"

// updater checks GitHub releases (internal/update) and installs a newer
// build: the installer for an installed copy, a swapped exe for the
// portable one (install_windows.go). Elsewhere it only links to the release.
type updater struct {
	url     string
	key     string // the public key manifests must be signed with
	client  *http.Client
	quit    func()                  // closes the app once the new build is ready to start
	install func(path string) error // starts the new build (install_*.go)

	mu     sync.Mutex
	st     server.UpdateStatus
	latest *update.Manifest
}

func newUpdater(quit func()) *updater {
	u := &updater{url: latestURL, key: update.PublicKey, client: &http.Client{Timeout: 10 * time.Minute}, quit: quit, install: install}
	if v := os.Getenv("GHOSTREEL_UPDATE_URL"); v != "" {
		u.url = v
	}
	u.st = server.UpdateStatus{Current: version, State: "idle"}
	cleanUpOldExe()
	return u
}

func (u *updater) Status() server.UpdateStatus {
	u.mu.Lock()
	defer u.mu.Unlock()
	return u.st
}

func (u *updater) set(f func(*server.UpdateStatus)) {
	u.mu.Lock()
	f(&u.st)
	u.mu.Unlock()
}

func (u *updater) Check(ctx context.Context) server.UpdateStatus {
	u.mu.Lock()
	if u.st.State == "downloading" || u.st.State == "installing" {
		defer u.mu.Unlock()
		return u.st
	}
	u.st.State, u.st.Error = "checking", ""
	u.mu.Unlock()

	m, err := update.Check(ctx, u.client, u.url, u.key)
	u.mu.Lock()
	defer u.mu.Unlock()
	u.st.Checked = time.Now().UTC().Format(time.RFC3339)
	if err != nil {
		u.st.State, u.st.Error = "error", err.Error()
		log.Printf("update check: %v", err)
		return u.st
	}
	u.latest = m
	u.st.State = "idle"
	u.st.Latest, u.st.NotesURL = m.Version, m.NotesURL
	u.st.Available = update.Newer(m.Version, version)
	_, ok := m.Files[installRole()]
	u.st.CanApply = u.st.Available && ok && canInstall()
	return u.st
}

func (u *updater) Apply() error {
	u.mu.Lock()
	m := u.latest
	switch {
	case m == nil || !u.st.Available:
		u.mu.Unlock()
		return errors.New("no update to install; check first")
	case !u.st.CanApply:
		u.mu.Unlock()
		return errors.New("this copy can't update itself; download the new version from the release page")
	case u.st.State == "downloading" || u.st.State == "installing":
		u.mu.Unlock()
		return nil
	}
	f := m.Files[installRole()]
	u.st.State, u.st.Error, u.st.Progress = "downloading", "", 0
	u.mu.Unlock()

	go func() {
		dir := filepath.Join(os.TempDir(), "ghostreel-update", m.Version)
		path, err := update.Download(context.Background(), u.client, f, dir, func(done, total int64) {
			u.set(func(s *server.UpdateStatus) { s.Progress = float64(done) / float64(max(total, 1)) })
		})
		if err == nil {
			u.set(func(s *server.UpdateStatus) { s.State = "installing" })
			err = u.install(path)
		}
		if err != nil {
			log.Printf("update: %v", err)
			u.set(func(s *server.UpdateStatus) { s.State, s.Error = "error", err.Error() })
			return
		}
		// The installer or the new exe takes over once this one has gone.
		time.Sleep(500 * time.Millisecond)
		u.quit()
	}()
	return nil
}

// canInstall: a release build that knows how to replace itself here.
func canInstall() bool {
	if version == "dev" {
		return false // a build from source: don't overwrite it
	}
	return installRole() != ""
}

func installErr(format string, a ...any) error {
	return fmt.Errorf("update: "+format, a...)
}
