// SPDX-License-Identifier: Apache-2.0

package main

import (
	"archive/zip"
	"os"
	"path/filepath"
	"testing"
)

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
