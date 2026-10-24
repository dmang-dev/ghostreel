// SPDX-License-Identifier: Apache-2.0

// Package mapcache finds the map file a replay was played on.
//
// Ladder and custom games name their map by content hash (a cache handle):
// it is looked up in the local Battle.net cache, then in ghostreel's own
// download cache, then (only if allowed) downloaded from Blizzard's regional
// depot and verified against the hash. Games started from a local map file
// (API, bot and editor games) only record the file name, which is searched
// for in map folders. A path given by the user always wins.
package mapcache

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/dmang-dev/ghostreel/internal/replay"
)

// ErrNotFound means no source had the map. The error text lists what was tried.
var ErrNotFound = errors.New("map not found")

// How says where a map came from.
type How string

const (
	HowManual     How = "manual"     // --map / file picker
	HowBnetCache  How = "bnet-cache" // local Battle.net cache
	HowOwnCache   How = "own-cache"  // ghostreel's download cache
	HowDownloaded How = "downloaded" // fetched from the depot just now
	HowMapFolder  How = "map-folder" // found by file name in a map folder
)

// Resolver holds the places to look. The zero value finds nothing; use
// Default for platform defaults.
type Resolver struct {
	// BnetCacheDirs are Battle.net cache roots (holding ab/cd/<hash>.s2ma).
	BnetCacheDirs []string
	// MapDirs are searched recursively for maps referenced by file name.
	MapDirs []string
	// DownloadDir caches depot downloads as <hash>.s2ma. Empty disables both
	// the cache and downloading.
	DownloadDir string
	// AllowNetwork permits depot downloads. Off unless the user opts in.
	AllowNetwork bool
	Client       *http.Client
	// Log receives one line per notable step (lookups that hit, every
	// network request). Nil discards.
	Log func(format string, args ...any)
}

// Default returns a resolver with this platform's usual locations. Extra
// cache roots and map folders can be added with the GHOSTREEL_BNET_CACHE and
// GHOSTREEL_MAPS environment variables (os.PathListSeparator-separated).
func Default() *Resolver {
	r := &Resolver{Client: &http.Client{Timeout: 60 * time.Second}}
	home, _ := os.UserHomeDir()
	switch runtime.GOOS {
	case "windows":
		pd := os.Getenv("ProgramData")
		if pd == "" {
			pd = `C:\ProgramData`
		}
		r.BnetCacheDirs = append(r.BnetCacheDirs, filepath.Join(pd, "Blizzard Entertainment", "Battle.net", "Cache"))
		for _, pf := range []string{os.Getenv("ProgramFiles(x86)"), os.Getenv("ProgramFiles")} {
			if pf != "" {
				r.MapDirs = append(r.MapDirs, filepath.Join(pf, "StarCraft II", "Maps"))
			}
		}
	case "darwin":
		r.BnetCacheDirs = append(r.BnetCacheDirs, "/Users/Shared/Blizzard/Battle.net/Cache")
		r.MapDirs = append(r.MapDirs, "/Applications/StarCraft II/Maps")
	default: // Linux: Blizzard's headless package, or the Windows client under Wine.
		if home != "" {
			r.MapDirs = append(r.MapDirs, filepath.Join(home, "StarCraftII", "Maps"))
			r.BnetCacheDirs = append(r.BnetCacheDirs,
				filepath.Join(home, "StarCraftII", "Battle.net", "Cache"),
				filepath.Join(home, ".wine", "drive_c", "ProgramData", "Blizzard Entertainment", "Battle.net", "Cache"))
		}
	}
	r.BnetCacheDirs = append(r.BnetCacheDirs, filepath.SplitList(os.Getenv("GHOSTREEL_BNET_CACHE"))...)
	r.MapDirs = append(r.MapDirs, filepath.SplitList(os.Getenv("GHOSTREEL_MAPS"))...)
	if dir, err := os.UserCacheDir(); err == nil {
		r.DownloadDir = filepath.Join(dir, "ghostreel", "maps")
	}
	return r
}

func (r *Resolver) logf(format string, args ...any) {
	if r.Log != nil {
		r.Log(format, args...)
	}
}

// Resolve returns the path of the replay's map. manual, if non-empty, is a
// path the user picked and is used as is.
func (r *Resolver) Resolve(ctx context.Context, info *replay.Info, manual string) (string, How, error) {
	if manual != "" {
		if _, err := os.Stat(manual); err != nil {
			return "", "", fmt.Errorf("map file: %w", err)
		}
		return manual, HowManual, nil
	}
	var tried []string
	if h := info.MapHandle; h != nil {
		name := h.FileName()
		for _, root := range r.BnetCacheDirs {
			p := filepath.Join(root, h.Digest[0:2], h.Digest[2:4], name)
			if fileExists(p) {
				r.logf("map: found %s in Battle.net cache", name)
				return p, HowBnetCache, nil
			}
			tried = append(tried, p)
		}
		if r.DownloadDir != "" {
			p := filepath.Join(r.DownloadDir, name)
			if fileExists(p) {
				return p, HowOwnCache, nil
			}
			tried = append(tried, p)
			if r.AllowNetwork && h.DepotURL != "" {
				if err := r.download(ctx, h, p); err != nil {
					tried = append(tried, fmt.Sprintf("depot download failed: %v", err))
				} else {
					return p, HowDownloaded, nil
				}
			} else if h.DepotURL != "" {
				tried = append(tried, "depot download (network access not enabled)")
			}
		}
	}
	if info.MapFileName != "" {
		if p := r.findByName(info.MapFileName); p != "" {
			r.logf("map: found %s in %s", info.MapFileName, filepath.Dir(p))
			return p, HowMapFolder, nil
		}
		tried = append(tried, fmt.Sprintf("%s in map folders %v", info.MapFileName, r.MapDirs))
	}
	if len(tried) == 0 {
		tried = append(tried, "the replay names no map file")
	}
	return "", "", fmt.Errorf("%w for %q; tried:\n  %s", ErrNotFound, info.MapTitle, strings.Join(tried, "\n  "))
}

// findByName searches the map folders for a file, case-insensitively, and
// also accepts the name without its directory part.
func (r *Resolver) findByName(name string) string {
	want := strings.ToLower(filepath.Base(strings.ReplaceAll(name, `\`, "/")))
	for _, dir := range r.MapDirs {
		found := ""
		filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
			if err != nil {
				return nil // unreadable entries are skipped, not fatal
			}
			if !d.IsDir() && strings.ToLower(d.Name()) == want {
				found = p
				return fs.SkipAll
			}
			return nil
		})
		if found != "" {
			return found
		}
	}
	return ""
}

// download fetches a depot file to dest, accepting it only if its SHA-256
// matches the handle's digest.
func (r *Resolver) download(ctx context.Context, h *replay.MapHandle, dest string) error {
	url := strings.TrimSuffix(h.DepotURL, "/") + "/" + h.FileName()
	r.logf("map: downloading %s", url)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return err
	}
	client := r.Client
	if client == nil {
		client = http.DefaultClient
	}
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("%s: HTTP %s", url, resp.Status)
	}
	if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(dest), ".download-*")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	sum := sha256.New()
	n, err := io.Copy(io.MultiWriter(tmp, sum), io.LimitReader(resp.Body, 256<<20))
	if cerr := tmp.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		return err
	}
	if got := hex.EncodeToString(sum.Sum(nil)); got != h.Digest {
		return fmt.Errorf("%s: content hash %s does not match %s", url, got, h.Digest)
	}
	r.logf("map: downloaded %d KiB, hash verified", n/1024)
	return os.Rename(tmp.Name(), dest)
}

func fileExists(p string) bool {
	fi, err := os.Stat(p)
	return err == nil && !fi.IsDir()
}
