// SPDX-License-Identifier: Apache-2.0

package engine

import (
	"context"
	"errors"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"time"
)

// Game is a running SC2 client started by this process. It is launched as a
// direct child of the current process (hosts may police SC2 processes by
// their parent) and killed by Stop.
type Game struct {
	cmd  *exec.Cmd
	Addr string
}

// LaunchOptions say which client to start.
type LaunchOptions struct {
	// Dir is the SC2 install root (holding Versions/ and Maps/).
	Dir string
	// BaseBuild picks Versions/Base<BaseBuild>. 0 uses the newest installed.
	BaseBuild int
	// Port to listen on; 0 picks a free one in PortMin..PortMax.
	Port, PortMin, PortMax int
	TempDir                string
	Log                    func(format string, args ...any)
}

// ErrNoMatchingBuild means the install lacks the replay's game version.
var ErrNoMatchingBuild = errors.New("no matching SC2 build installed")

// Launch starts SC2 and waits until its API accepts connections.
func Launch(ctx context.Context, o LaunchOptions) (*Game, *Client, error) {
	exe, err := executable(o.Dir, o.BaseBuild)
	if err != nil {
		return nil, nil, err
	}
	port := o.Port
	if port == 0 {
		if port, err = freePort(o.PortMin, o.PortMax); err != nil {
			return nil, nil, err
		}
	}
	addr := "127.0.0.1:" + strconv.Itoa(port)
	tmp := o.TempDir
	if tmp == "" {
		tmp = os.TempDir()
	}
	args := []string{"-listen", "127.0.0.1", "-port", strconv.Itoa(port), "-dataDir", o.Dir, "-tempDir", tmp}
	if runtime.GOOS == "windows" {
		args = append(args, "-displayMode", "0")
	}
	cmd := exec.Command(exe, args...)
	// SC2 must run from its support directory on Windows and macOS.
	for _, sup := range []string{"Support64", "Support"} {
		if fi, err := os.Stat(filepath.Join(o.Dir, sup)); err == nil && fi.IsDir() {
			cmd.Dir = filepath.Join(o.Dir, sup)
			break
		}
	}
	if o.Log != nil {
		o.Log("engine: starting %s on %s", exe, addr)
	}
	if err := cmd.Start(); err != nil {
		return nil, nil, err
	}
	g := &Game{cmd: cmd, Addr: addr}
	dctx, cancel := context.WithTimeout(ctx, 3*time.Minute)
	defer cancel()
	c, err := Dial(dctx, addr)
	if err != nil {
		g.Stop()
		return nil, nil, err
	}
	return g, c, nil
}

// Stop kills the client and waits for it to exit.
func (g *Game) Stop() {
	if g.cmd.Process != nil {
		g.cmd.Process.Kill()
		g.cmd.Wait()
	}
}

// executable finds Versions/Base<build>/SC2_x64(.exe).
func executable(dir string, build int) (string, error) {
	name := "SC2_x64"
	if runtime.GOOS == "windows" {
		name += ".exe"
	}
	if build != 0 {
		p := filepath.Join(dir, "Versions", fmt.Sprintf("Base%d", build), name)
		if _, err := os.Stat(p); err != nil {
			installed, _ := filepath.Glob(filepath.Join(dir, "Versions", "Base*"))
			return "", fmt.Errorf("%w: need Base%d, have %v", ErrNoMatchingBuild, build, baseNames(installed))
		}
		return p, nil
	}
	found, _ := filepath.Glob(filepath.Join(dir, "Versions", "Base*", name))
	if len(found) == 0 {
		return "", fmt.Errorf("no SC2 client under %s", filepath.Join(dir, "Versions"))
	}
	best, bestN := "", -1
	for _, f := range found {
		n, _ := strconv.Atoi(filepath.Base(filepath.Dir(f))[len("Base"):])
		if n > bestN {
			best, bestN = f, n
		}
	}
	return best, nil
}

func baseNames(paths []string) []string {
	var out []string
	for _, p := range paths {
		out = append(out, filepath.Base(p))
	}
	return out
}

// freePort returns a port in [lo, hi] that is free right now.
func freePort(lo, hi int) (int, error) {
	if lo == 0 {
		lo, hi = 5800, 5899
	}
	for p := lo; p <= hi; p++ {
		ln, err := net.Listen("tcp", "127.0.0.1:"+strconv.Itoa(p))
		if err == nil {
			ln.Close()
			return p, nil
		}
	}
	return 0, fmt.Errorf("no free port in %d-%d", lo, hi)
}
