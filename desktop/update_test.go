// SPDX-License-Identifier: Apache-2.0

package main

import (
	"archive/zip"
	"context"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"github.com/dmang-dev/ghostreel/internal/update"
)

func TestUpdater(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("installing updates is Windows-only")
	}
	pub, priv, _ := ed25519.GenerateKey(nil)
	payload := []byte("new portable build")
	sum := sha256.Sum256(payload)
	mux := http.NewServeMux()
	srv := httptest.NewServer(mux)
	defer srv.Close()
	file := update.File{Name: "ghostreel-desktop_v1.2.0_windows_amd64_portable.zip", URL: srv.URL + "/f", Size: int64(len(payload)), SHA256: hex.EncodeToString(sum[:])}
	body, _ := json.Marshal(update.Manifest{Version: "v1.2.0", NotesURL: "https://example.invalid/notes",
		Files: map[string]update.File{"windows-amd64-portable": file, "windows-amd64-installer": file}})
	mux.HandleFunc("/latest.json", func(w http.ResponseWriter, r *http.Request) { w.Write(body) })
	mux.HandleFunc("/latest.json.sig", func(w http.ResponseWriter, r *http.Request) { w.Write(update.Sign(body, priv)) })
	mux.HandleFunc("/f", func(w http.ResponseWriter, r *http.Request) { w.Write(payload) })

	old := version
	defer func() { version = old }()
	installedPath := make(chan string, 1)
	quit := make(chan bool, 1)
	u := newUpdater(func() { quit <- true })
	u.url, u.key, u.client = srv.URL+"/latest.json", base64.StdEncoding.EncodeToString(pub), srv.Client()
	u.install = func(p string) error { installedPath <- p; return nil }

	// A build from source sees the release but won't replace itself.
	version = "dev"
	if st := u.Check(context.Background()); !st.Available || st.CanApply || st.Latest != "v1.2.0" {
		t.Fatalf("dev: %+v", st)
	}
	if err := u.Apply(); err == nil {
		t.Error("a dev build applied an update")
	}
	// The same version: nothing to do.
	version = "v1.2.0"
	if st := u.Check(context.Background()); st.Available {
		t.Errorf("same version: %+v", st)
	}
	// An older release build downloads, verifies, installs and quits.
	version = "v1.1.0"
	if st := u.Check(context.Background()); !st.Available || !st.CanApply {
		t.Fatalf("older: %+v", st)
	}
	if err := u.Apply(); err != nil {
		t.Fatal(err)
	}
	select {
	case p := <-installedPath:
		if b, _ := os.ReadFile(p); string(b) != string(payload) {
			t.Errorf("installed %s: %q", p, b)
		}
	case <-time.After(10 * time.Second):
		t.Fatalf("not installed: %+v", u.Status())
	}
	select {
	case <-quit:
	case <-time.After(5 * time.Second):
		t.Error("the app didn't quit for the update")
	}
	if st := u.Status(); st.State != "installing" || st.Progress != 1 {
		t.Errorf("status: %+v", st)
	}

	// A manifest signed with another key is refused.
	u2 := newUpdater(func() {})
	u2.url, u2.client = srv.URL+"/latest.json", srv.Client() // the real key: the test key's signature fails
	if st := u2.Check(context.Background()); st.State != "error" || st.Available {
		t.Errorf("wrong key: %+v", st)
	}
}

func TestExtractExe(t *testing.T) {
	dir := t.TempDir()
	zp := filepath.Join(dir, "p.zip")
	f, _ := os.Create(zp)
	z := zip.NewWriter(f)
	w, _ := z.Create("ghostreel-desktop.exe")
	w.Write([]byte("MZ new"))
	z.Close()
	f.Close()
	p, err := extractExe(zp, dir)
	if err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(p); string(b) != "MZ new" {
		t.Errorf("extracted %q", b)
	}
}
