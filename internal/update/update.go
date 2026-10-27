// SPDX-License-Identifier: Apache-2.0

// Package update checks GitHub releases for a newer ghostreel and fetches it
// safely. Each release carries latest.json, a manifest of its files with
// their SHA-256, and latest.json.sig, an Ed25519 signature of it made with
// the release key (a GitHub secret). The app trusts a manifest only if the
// signature checks out against the public key built into it, and a file
// only if its hash matches the manifest, so neither a tampered release nor
// a man in the middle can hand it a bad build. Nothing here applies an
// update; the desktop app does that per platform.
package update

import (
	"context"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// PublicKey verifies release manifests (base64 Ed25519). Its private half
// is the GHOSTREEL_UPDATE_KEY secret of the release workflow.
const PublicKey = "u06H7YKJDHdcd3XsqLZQELpmq3wYYefFvpPt0v9DHjs="

// Manifest describes one release.
type Manifest struct {
	Version   string `json:"version"`   // the tag, e.g. "v1.2.0"
	Published string `json:"published"` // RFC 3339
	NotesURL  string `json:"notesUrl"`  // the release page
	// Files by role: "windows-amd64-installer", "windows-amd64-portable", ...
	Files map[string]File `json:"files"`
}

// File is one release download.
type File struct {
	Name   string `json:"name"`
	URL    string `json:"url"`
	Size   int64  `json:"size"`
	SHA256 string `json:"sha256"`
}

// Sign signs a manifest's bytes; the result is the .sig file's content.
func Sign(manifest []byte, key ed25519.PrivateKey) []byte {
	return []byte(base64.StdEncoding.EncodeToString(ed25519.Sign(key, manifest)) + "\n")
}

// Verify checks a .sig file's content against a manifest with a base64
// public key.
func Verify(manifest, sig []byte, publicKey string) error {
	pub, err := base64.StdEncoding.DecodeString(publicKey)
	if err != nil || len(pub) != ed25519.PublicKeySize {
		return errors.New("update: bad public key")
	}
	s, err := base64.StdEncoding.DecodeString(strings.TrimSpace(string(sig)))
	if err != nil || len(s) != ed25519.SignatureSize {
		return errors.New("update: malformed signature")
	}
	if !ed25519.Verify(ed25519.PublicKey(pub), manifest, s) {
		return errors.New("update: the release's signature doesn't match; not trusting it")
	}
	return nil
}

// Newer reports whether version a is newer than b. Versions are
// "v1.2.3" with an optional "-pre" suffix, which sorts before the release.
// Anything else (a "dev" build) counts as older than every release.
func Newer(a, b string) bool {
	pa, oka := parse(a)
	pb, okb := parse(b)
	switch {
	case !oka:
		return false
	case !okb:
		return true
	}
	for i := 0; i < 3; i++ {
		if pa.n[i] != pb.n[i] {
			return pa.n[i] > pb.n[i]
		}
	}
	switch {
	case pa.pre == pb.pre:
		return false
	case pa.pre == "":
		return true // a release beats its pre-releases
	case pb.pre == "":
		return false
	}
	return pa.pre > pb.pre
}

type version struct {
	n   [3]int
	pre string
}

func parse(v string) (version, bool) {
	v = strings.TrimPrefix(strings.TrimSpace(v), "v")
	v, _, _ = strings.Cut(v, "+")
	core, pre, _ := strings.Cut(v, "-")
	parts := strings.Split(core, ".")
	if len(parts) != 3 {
		return version{}, false
	}
	var out version
	for i, p := range parts {
		n, err := strconv.Atoi(p)
		if err != nil || n < 0 {
			return version{}, false
		}
		out.n[i] = n
	}
	out.pre = pre
	return out, true
}

// Check fetches the manifest (".../latest.json") and its signature
// (".sig" beside it) and verifies them.
func Check(ctx context.Context, client *http.Client, manifestURL, publicKey string) (*Manifest, error) {
	body, err := get(ctx, client, manifestURL, 1<<20)
	if err != nil {
		return nil, err
	}
	sig, err := get(ctx, client, manifestURL+".sig", 4096)
	if err != nil {
		return nil, err
	}
	if err := Verify(body, sig, publicKey); err != nil {
		return nil, err
	}
	var m Manifest
	if err := json.Unmarshal(body, &m); err != nil {
		return nil, fmt.Errorf("update: bad manifest: %v", err)
	}
	if _, ok := parse(m.Version); !ok {
		return nil, fmt.Errorf("update: bad version %q in the manifest", m.Version)
	}
	return &m, nil
}

// Download fetches f into dir, checking its size and hash, and returns the
// file's path. A file that doesn't match is deleted.
func Download(ctx context.Context, client *http.Client, f File, dir string, progress func(done, total int64)) (string, error) {
	if f.Name == "" || f.Name != filepath.Base(f.Name) {
		return "", fmt.Errorf("update: bad file name %q", f.Name)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, f.URL, nil)
	if err != nil {
		return "", err
	}
	resp, err := client.Do(req)
	if err != nil {
		return "", fmt.Errorf("update: downloading %s: %v", f.Name, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("update: downloading %s: HTTP %s", f.Name, resp.Status)
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	path := filepath.Join(dir, f.Name)
	out, err := os.Create(path)
	if err != nil {
		return "", err
	}
	h := sha256.New()
	n, err := io.Copy(io.MultiWriter(out, h), &counter{r: io.LimitReader(resp.Body, f.Size+1), total: f.Size, progress: progress})
	if cerr := out.Close(); err == nil {
		err = cerr
	}
	if err == nil && n != f.Size {
		err = fmt.Errorf("update: %s is %d bytes, the release says %d", f.Name, n, f.Size)
	}
	if err == nil && !strings.EqualFold(hex.EncodeToString(h.Sum(nil)), f.SHA256) {
		err = fmt.Errorf("update: %s doesn't match the release's checksum", f.Name)
	}
	if err != nil {
		os.Remove(path)
		return "", err
	}
	return path, nil
}

type counter struct {
	r        io.Reader
	done     int64
	total    int64
	progress func(done, total int64)
}

func (c *counter) Read(p []byte) (int, error) {
	n, err := c.r.Read(p)
	c.done += int64(n)
	if c.progress != nil && n > 0 {
		c.progress(c.done, c.total)
	}
	return n, err
}

func get(ctx context.Context, client *http.Client, url string, limit int64) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("update: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("update: %s: HTTP %s", url, resp.Status)
	}
	return io.ReadAll(io.LimitReader(resp.Body, limit))
}
