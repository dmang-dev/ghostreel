// SPDX-License-Identifier: Apache-2.0

package update

import (
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
	"strings"
	"testing"
)

func TestNewer(t *testing.T) {
	for _, c := range []struct {
		a, b string
		want bool
	}{
		{"v1.2.0", "v1.1.9", true},
		{"v1.10.0", "v1.9.0", true},
		{"v1.2.0", "v1.2.0", false},
		{"v1.1.0", "v1.2.0", false},
		{"v1.2.0", "v1.2.0-rc1", true},
		{"v1.2.0-rc2", "v1.2.0-rc1", true},
		{"v1.2.0-rc1", "v1.2.0", false},
		{"1.2.0", "v1.1.0", true},
		{"v1.0.0", "dev", true},
		{"dev", "v1.0.0", false},
		{"v1.0", "v0.9.0", false},
	} {
		if got := Newer(c.a, c.b); got != c.want {
			t.Errorf("Newer(%q, %q) = %v", c.a, c.b, got)
		}
	}
}

func testKey(t *testing.T) (ed25519.PrivateKey, string) {
	t.Helper()
	pub, priv, err := ed25519.GenerateKey(nil)
	if err != nil {
		t.Fatal(err)
	}
	return priv, base64.StdEncoding.EncodeToString(pub)
}

func TestSignVerify(t *testing.T) {
	priv, pub := testKey(t)
	m := []byte(`{"version":"v1.2.0"}`)
	sig := Sign(m, priv)
	if err := Verify(m, sig, pub); err != nil {
		t.Fatal(err)
	}
	if err := Verify([]byte(`{"version":"v9.9.9"}`), sig, pub); err == nil {
		t.Error("tampered manifest verified")
	}
	_, other := testKey(t)
	if err := Verify(m, sig, other); err == nil {
		t.Error("verified with the wrong key")
	}
	if err := Verify(m, []byte("not base64!"), pub); err == nil {
		t.Error("garbage signature verified")
	}
}

// A release server: latest.json, its signature, and one file.
func release(t *testing.T, priv ed25519.PrivateKey, payload []byte, tamper bool) *httptest.Server {
	t.Helper()
	sum := sha256.Sum256(payload)
	mux := http.NewServeMux()
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	m := Manifest{Version: "v1.2.0", Files: map[string]File{
		"windows-amd64-installer": {Name: "setup.exe", URL: srv.URL + "/setup.exe", Size: int64(len(payload)), SHA256: hex.EncodeToString(sum[:])},
	}}
	body, _ := json.Marshal(m)
	sig := Sign(body, priv)
	if tamper {
		body = []byte(strings.Replace(string(body), "v1.2.0", "v9.0.0", 1))
	}
	mux.HandleFunc("/latest.json", func(w http.ResponseWriter, r *http.Request) { w.Write(body) })
	mux.HandleFunc("/latest.json.sig", func(w http.ResponseWriter, r *http.Request) { w.Write(sig) })
	mux.HandleFunc("/setup.exe", func(w http.ResponseWriter, r *http.Request) { w.Write(payload) })
	return srv
}

func TestCheckAndDownload(t *testing.T) {
	priv, pub := testKey(t)
	payload := []byte("the new installer")
	srv := release(t, priv, payload, false)
	ctx := context.Background()

	m, err := Check(ctx, srv.Client(), srv.URL+"/latest.json", pub)
	if err != nil || m.Version != "v1.2.0" {
		t.Fatalf("check: %v %+v", err, m)
	}
	dir := t.TempDir()
	var last int64
	p, err := Download(ctx, srv.Client(), m.Files["windows-amd64-installer"], dir, func(done, total int64) { last = done })
	if err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(p); string(b) != string(payload) || last != int64(len(payload)) {
		t.Errorf("downloaded %q, progress %d", b, last)
	}

	// A file that doesn't match the manifest is refused and removed.
	f := m.Files["windows-amd64-installer"]
	f.SHA256 = strings.Repeat("0", 64)
	if _, err := Download(ctx, srv.Client(), f, dir, nil); err == nil || !strings.Contains(err.Error(), "checksum") {
		t.Errorf("bad hash: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "setup.exe")); err == nil {
		t.Error("a bad download was left behind")
	}
	f = m.Files["windows-amd64-installer"]
	f.Name = "../evil.exe"
	if _, err := Download(ctx, srv.Client(), f, dir, nil); err == nil {
		t.Error("a path in the file name was accepted")
	}
}

func TestCheckRejectsTampering(t *testing.T) {
	priv, pub := testKey(t)
	srv := release(t, priv, []byte("x"), true)
	if _, err := Check(context.Background(), srv.Client(), srv.URL+"/latest.json", pub); err == nil {
		t.Error("a tampered manifest was trusted")
	}
}
