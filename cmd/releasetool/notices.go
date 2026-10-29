// SPDX-License-Identifier: Apache-2.0

package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strings"
)

// licenseFile matches the files that carry a package's license terms.
var licenseFile = regexp.MustCompile(`(?i)^(licen[cs]e|copying|notice|unlicense)([-._].*)?$`)

type component struct {
	name    string // module path or npm package name
	version string
	dir     string
	files   []string // license files, relative to dir
	// declared is the license a package states without shipping its text
	// (npm's package.json), and source is where its source is.
	declared, source string
}

type multiFlag []string

func (m *multiFlag) String() string     { return strings.Join(*m, ",") }
func (m *multiFlag) Set(v string) error { *m = append(*m, v); return nil }

// notices writes the license of everything ghostreel's builds include from
// others: Go itself, the Go modules linked into each module's packages (for
// every release OS), the npm packages the viewer bundles, and third_party/.
// MIT, BSD and Apache code want their notices to travel with binaries.
func notices(args []string) error {
	fs := flag.NewFlagSet("notices", flag.ExitOnError)
	out := fs.String("out", "THIRD_PARTY_NOTICES.txt", "file to write")
	var goDirs, npmDirs, extra multiFlag
	fs.Var(&goDirs, "go", "a Go module's folder; its packages' dependencies are listed (repeatable)")
	fs.Var(&npmDirs, "npm", "a folder with package-lock.json and node_modules; its non-dev packages are listed (repeatable)")
	fs.Var(&extra, "third-party", "a folder of vendored code, one subfolder per project with its LICENSE (repeatable)")
	fs.Parse(args)

	var parts []component
	goroot, err := exec.Command("go", "env", "GOROOT").Output()
	if err != nil {
		return fmt.Errorf("go env GOROOT: %v", err)
	}
	parts = append(parts, withFiles(component{name: "Go (standard library and runtime)", version: runtime.Version(), dir: strings.TrimSpace(string(goroot))}))
	for _, d := range goDirs {
		mods, err := goModules(d)
		if err != nil {
			return err
		}
		parts = append(parts, mods...)
	}
	for _, d := range npmDirs {
		pkgs, err := npmPackages(d)
		if err != nil {
			return err
		}
		parts = append(parts, pkgs...)
	}
	for _, d := range extra {
		entries, err := os.ReadDir(d)
		if err != nil {
			return err
		}
		for _, e := range entries {
			if e.IsDir() {
				parts = append(parts, withFiles(component{name: e.Name() + " (vendored or ported, " + filepath.ToSlash(filepath.Join(d, e.Name())) + ")", dir: filepath.Join(d, e.Name())}))
			}
		}
	}

	parts = dedupe(parts)
	var b bytes.Buffer
	b.WriteString("Third-party software in ghostreel\n\n")
	b.WriteString("ghostreel is Apache-2.0 licensed (see LICENSE). Its builds include the\n")
	b.WriteString("software below, under the licenses that follow each entry.\n")
	var missing []string
	for _, c := range parts {
		b.WriteString("\n" + strings.Repeat("=", 78) + "\n")
		b.WriteString(c.name)
		if c.version != "" {
			b.WriteString(" " + c.version)
		}
		b.WriteString("\n")
		if len(c.files) == 0 {
			if c.declared == "" {
				missing = append(missing, c.name)
				b.WriteString("\n(No license file found in the package; see its source.)\n")
				continue
			}
			fmt.Fprintf(&b, "\nLicense: %s (the package states it but ships no license text)\n", c.declared)
			if c.source != "" {
				fmt.Fprintf(&b, "Source and full terms: %s\n", c.source)
			}
			continue
		}
		for _, f := range c.files {
			text, err := os.ReadFile(filepath.Join(c.dir, f))
			if err != nil {
				return err
			}
			fmt.Fprintf(&b, "\n--- %s\n\n%s\n", f, strings.TrimRight(strings.ReplaceAll(string(text), "\r\n", "\n"), "\n"))
		}
	}
	if err := os.WriteFile(*out, b.Bytes(), 0o644); err != nil {
		return err
	}
	fmt.Printf("%s: %d components\n", *out, len(parts))
	if len(missing) > 0 {
		fmt.Fprintf(os.Stderr, "releasetool: no license file found for: %s\n", strings.Join(missing, ", "))
	}
	return nil
}

// selfModule is ghostreel's own module, which the desktop module requires.
const selfModule = "github.com/dmang-dev/ghostreel"

// goModules lists the modules linked into dir's packages on each release OS.
func goModules(dir string) ([]component, error) {
	var out []component
	for _, goos := range []string{"linux", "windows", "darwin"} {
		cmd := exec.Command("go", "list", "-e", "-deps", "-f",
			`{{with .Module}}{{if not .Main}}{{.Path}}|{{.Version}}|{{.Dir}}{{end}}{{end}}`, "./...")
		cmd.Dir = dir
		cmd.Env = append(os.Environ(), "GOOS="+goos, "CGO_ENABLED=1")
		b, err := cmd.Output()
		if err != nil {
			var ee *exec.ExitError
			if errors.As(err, &ee) {
				return nil, fmt.Errorf("go list in %s (GOOS=%s): %v\n%s", dir, goos, err, ee.Stderr)
			}
			return nil, err
		}
		for _, line := range strings.Split(string(b), "\n") {
			f := strings.Split(strings.TrimSpace(line), "|")
			if len(f) != 3 || f[2] == "" || f[0] == selfModule {
				continue
			}
			out = append(out, withFiles(component{name: f[0], version: f[1], dir: f[2]}))
		}
	}
	return out, nil
}

// npmPackages lists the packages package-lock.json installs for production.
func npmPackages(dir string) ([]component, error) {
	b, err := os.ReadFile(filepath.Join(dir, "package-lock.json"))
	if err != nil {
		return nil, err
	}
	var lock struct {
		Packages map[string]struct {
			Version     string `json:"version"`
			Dev         bool   `json:"dev"`
			DevOptional bool   `json:"devOptional"`
		} `json:"packages"`
	}
	if err := json.Unmarshal(b, &lock); err != nil {
		return nil, fmt.Errorf("%s/package-lock.json: %v", dir, err)
	}
	var out []component
	for key, p := range lock.Packages {
		if key == "" || p.Dev || p.DevOptional {
			continue
		}
		name := key[strings.LastIndex(key, "node_modules/")+len("node_modules/"):]
		pkgDir := filepath.Join(dir, filepath.FromSlash(key))
		if _, err := os.Stat(pkgDir); err != nil {
			return nil, fmt.Errorf("%s isn't installed (run npm ci in %s first)", name, dir)
		}
		c := withFiles(component{name: name, version: p.Version, dir: pkgDir})
		if len(c.files) == 0 {
			c.declared, c.source = declaredLicense(pkgDir)
		}
		out = append(out, c)
	}
	return out, nil
}

// declaredLicense reads the license and repository a package.json states.
func declaredLicense(dir string) (license, source string) {
	b, err := os.ReadFile(filepath.Join(dir, "package.json"))
	if err != nil {
		return "", ""
	}
	var p struct {
		License    string          `json:"license"`
		Repository json.RawMessage `json:"repository"`
	}
	json.Unmarshal(b, &p)
	var repo struct {
		URL string `json:"url"`
	}
	if json.Unmarshal(p.Repository, &repo) != nil {
		json.Unmarshal(p.Repository, &repo.URL) // "repository": "url"
	}
	return p.License, strings.TrimSuffix(strings.TrimPrefix(repo.URL, "git+"), ".git")
}

func withFiles(c component) component {
	entries, _ := os.ReadDir(c.dir)
	for _, e := range entries {
		if !e.IsDir() && licenseFile.MatchString(e.Name()) {
			c.files = append(c.files, e.Name())
		}
	}
	return c
}

// dedupe keeps one entry per name and version, sorted by name, with Go
// first.
func dedupe(in []component) []component {
	seen := map[string]bool{}
	var out []component
	for _, c := range in {
		k := c.name + "@" + c.version
		if !seen[k] {
			seen[k] = true
			out = append(out, c)
		}
	}
	sort.SliceStable(out[1:], func(i, j int) bool { return strings.ToLower(out[i+1].name) < strings.ToLower(out[j+1].name) })
	return out
}
