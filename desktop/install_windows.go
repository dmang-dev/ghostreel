// SPDX-License-Identifier: Apache-2.0

package main

import (
	"archive/zip"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
)

// installed: the installer put this exe here (its uninstaller is beside it).
func installed() bool {
	exe, err := os.Executable()
	if err != nil {
		return false
	}
	_, err = os.Stat(filepath.Join(filepath.Dir(exe), "uninstall.exe"))
	return err == nil
}

// installRole is the release file this copy updates from.
func installRole() string {
	if installed() {
		return "windows-amd64-installer"
	}
	return "windows-amd64-portable"
}

// install starts the new build; the caller then quits.
//
// Installed: the new installer runs silently once this app has exited (it
// asks for administrator rights, as the first install did) and then starts
// the new ghostreel. Portable: the running exe is renamed aside (Windows
// allows that), the new one takes its place and starts; the old one is
// deleted on the next start.
func install(path string) error {
	exe, err := os.Executable()
	if err != nil {
		return err
	}
	if strings.EqualFold(filepath.Ext(path), ".exe") {
		// cmd's "start" goes through the shell, so the installer's request
		// for elevation shows the usual prompt. The ping is a short wait for
		// this process to exit and free its files. /D= keeps the install
		// where it is (NSIS wants it last and unquoted).
		// No space before the "&": it would end up in the folder name.
		line := `cmd /C ping -n 3 127.0.0.1 >nul & start "" /wait "` + path + `" /S /D=` + filepath.Dir(exe) +
			`& start "" "` + exe + `"`
		cmd := exec.Command("cmd")
		cmd.SysProcAttr = &syscall.SysProcAttr{CmdLine: line, HideWindow: true, CreationFlags: 0x08000000} // CREATE_NO_WINDOW
		return cmd.Start()
	}
	newExe, err := extractExe(path, filepath.Dir(path))
	if err != nil {
		return err
	}
	old := exe + ".old"
	os.Remove(old)
	if err := os.Rename(exe, old); err != nil {
		return installErr("can't replace %s: %v", exe, err)
	}
	if err := copyFile(newExe, exe); err != nil {
		os.Rename(old, exe) // put it back
		return installErr("can't write the new version: %v", err)
	}
	return exec.Command(exe).Start()
}

// cleanUpOldExe removes the exe a portable update set aside.
func cleanUpOldExe() {
	if exe, err := os.Executable(); err == nil {
		os.Remove(exe + ".old")
	}
}

// extractExe takes ghostreel-desktop.exe out of the portable zip.
func extractExe(zipPath, dir string) (string, error) {
	z, err := zip.OpenReader(zipPath)
	if err != nil {
		return "", installErr("bad download: %v", err)
	}
	defer z.Close()
	for _, f := range z.File {
		if !strings.EqualFold(filepath.Base(f.Name), "ghostreel-desktop.exe") {
			continue
		}
		r, err := f.Open()
		if err != nil {
			return "", err
		}
		defer r.Close()
		out := filepath.Join(dir, "ghostreel-desktop.new.exe")
		w, err := os.Create(out)
		if err != nil {
			return "", err
		}
		if _, err := io.Copy(w, r); err != nil {
			w.Close()
			return "", err
		}
		return out, w.Close()
	}
	return "", installErr("the download has no ghostreel-desktop.exe")
}

func copyFile(src, dst string) error {
	r, err := os.Open(src)
	if err != nil {
		return err
	}
	defer r.Close()
	w, err := os.OpenFile(dst, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o755)
	if err != nil {
		return err
	}
	if _, err := io.Copy(w, r); err != nil {
		w.Close()
		return err
	}
	return w.Close()
}
