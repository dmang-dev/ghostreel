// SPDX-License-Identifier: Apache-2.0

// Command releasetool makes the signed update manifest for a release (see
// internal/update), and the key that signs it.
//
//	releasetool keygen -out FILE     write a new private key to FILE, print the public key
//	releasetool manifest -tag v1.2.3 -dir artifacts -url https://github.com/OWNER/REPO/releases/download/v1.2.3
//	                                 write latest.json and latest.json.sig into the directory,
//	                                 signed with the key in $GHOSTREEL_UPDATE_KEY
//	releasetool notices -out THIRD_PARTY_NOTICES.txt -go . -go desktop -npm viewer -third-party third_party
//	                                 collect the licenses of everything the builds include
package main

import (
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/dmang-dev/ghostreel/internal/update"
)

func main() {
	if len(os.Args) < 2 {
		fmt.Fprintln(os.Stderr, "usage: releasetool keygen|manifest|notices [flags]")
		os.Exit(2)
	}
	var err error
	switch os.Args[1] {
	case "keygen":
		err = keygen(os.Args[2:])
	case "manifest":
		err = manifest(os.Args[2:])
	case "notices":
		err = notices(os.Args[2:])
	default:
		err = fmt.Errorf("unknown command %q", os.Args[1])
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "releasetool:", err)
		os.Exit(1)
	}
}

func keygen(args []string) error {
	fs := flag.NewFlagSet("keygen", flag.ExitOnError)
	out := fs.String("out", "", "file for the private key (keep it secret; it becomes the GHOSTREEL_UPDATE_KEY secret)")
	fs.Parse(args)
	if *out == "" {
		return errors.New("-out is required")
	}
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(*out), 0o700); err != nil {
		return err
	}
	if err := os.WriteFile(*out, []byte(base64.StdEncoding.EncodeToString(priv.Seed())+"\n"), 0o600); err != nil {
		return err
	}
	fmt.Println(base64.StdEncoding.EncodeToString(pub))
	return nil
}

// roles names the files the app may update from, by their release names.
var roles = []struct {
	role string
	re   *regexp.Regexp
}{
	{"windows-amd64-installer", regexp.MustCompile(`^ghostreel-desktop_.*_windows_amd64_setup\.exe$`)},
	{"windows-amd64-portable", regexp.MustCompile(`^ghostreel-desktop_.*_windows_amd64_portable\.zip$`)},
	{"darwin-universal", regexp.MustCompile(`^ghostreel-desktop_.*_macos_universal\.zip$`)},
	{"linux-amd64", regexp.MustCompile(`^ghostreel-desktop_.*_linux_amd64\.tar\.gz$`)},
	{"web", regexp.MustCompile(`^ghostreel_.*_web\.zip$`)},
}

func manifest(args []string) error {
	fs := flag.NewFlagSet("manifest", flag.ExitOnError)
	tag := fs.String("tag", "", "the release tag, e.g. v1.2.3")
	dir := fs.String("dir", "", "folder with the release files")
	url := fs.String("url", "", "download URL of the release's files (…/releases/download/TAG)")
	notes := fs.String("notes", "", "the release page")
	fs.Parse(args)
	if *tag == "" || *dir == "" || *url == "" {
		return errors.New("-tag, -dir and -url are required")
	}
	seed, err := base64.StdEncoding.DecodeString(strings.TrimSpace(os.Getenv("GHOSTREEL_UPDATE_KEY")))
	if err != nil || len(seed) != ed25519.SeedSize {
		return errors.New("GHOSTREEL_UPDATE_KEY is missing or not a key from `releasetool keygen`")
	}
	key := ed25519.NewKeyFromSeed(seed)
	if got := base64.StdEncoding.EncodeToString(key.Public().(ed25519.PublicKey)); got != update.PublicKey {
		return fmt.Errorf("GHOSTREEL_UPDATE_KEY doesn't match the public key built into the app (%s)", update.PublicKey)
	}

	m := update.Manifest{
		Version:   *tag,
		Published: time.Now().UTC().Format(time.RFC3339),
		NotesURL:  *notes,
		Files:     map[string]update.File{},
	}
	entries, err := os.ReadDir(*dir)
	if err != nil {
		return err
	}
	for _, e := range entries {
		for _, r := range roles {
			if e.IsDir() || !r.re.MatchString(e.Name()) {
				continue
			}
			f, err := describe(filepath.Join(*dir, e.Name()))
			if err != nil {
				return err
			}
			f.URL = strings.TrimSuffix(*url, "/") + "/" + e.Name()
			m.Files[r.role] = f
		}
	}
	if len(m.Files) == 0 {
		return fmt.Errorf("no release files found in %s", *dir)
	}
	body, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(*dir, "latest.json"), body, 0o644); err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(*dir, "latest.json.sig"), update.Sign(body, key), 0o644); err != nil {
		return err
	}
	for role, f := range m.Files {
		fmt.Printf("%s: %s (%d bytes)\n", role, f.Name, f.Size)
	}
	return nil
}

func describe(path string) (update.File, error) {
	f, err := os.Open(path)
	if err != nil {
		return update.File{}, err
	}
	defer f.Close()
	h := sha256.New()
	n, err := io.Copy(h, f)
	if err != nil {
		return update.File{}, err
	}
	return update.File{Name: filepath.Base(path), Size: n, SHA256: hex.EncodeToString(h.Sum(nil))}, nil
}
