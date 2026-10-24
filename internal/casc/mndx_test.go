// SPDX-License-Identifier: Apache-2.0

package casc

import (
	"bytes"
	"os"
	"strings"
	"testing"
	"time"
)

// openTestStorage opens the local test install, skipping the test if absent.
func openTestStorage(t *testing.T) *Storage {
	t.Helper()
	const dir = "../../testdata/casc"
	if _, err := os.Stat(dir); err != nil {
		t.Skip("no test storage at", dir)
	}
	s, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

func TestParseMNDX(t *testing.T) {
	s := openTestStorage(t)
	rootKey, err := s.RootKey()
	if err != nil {
		t.Fatal(err)
	}
	root, err := s.ReadCKey(rootKey)
	if err != nil {
		t.Fatal(err)
	}
	start := time.Now()
	entries, err := ParseMNDX(root)
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("ParseMNDX: %d entries from a %d-byte root in %v", len(entries), len(root), time.Since(start))
	if len(entries) < 100000 {
		t.Fatalf("only %d entries", len(entries))
	}
	for _, e := range entries {
		if e.Name == "" || strings.HasPrefix(e.Name, "/") || strings.Contains(e.Name, `\`) {
			t.Fatalf("bad name %q", e.Name)
		}
	}
	if _, err := ParseMNDX(root[:len(root)/2]); err == nil {
		t.Error("truncated root parsed without error")
	}
}

func TestStorageNames(t *testing.T) {
	s := openTestStorage(t)
	if err := s.LoadNames(); err != nil {
		t.Fatal(err)
	}
	names := s.Names()
	t.Logf("%d names", len(names))
	if len(names) < 100000 {
		t.Fatalf("only %d names", len(names))
	}
	for i := 0; i < len(names); i += len(names) / 10 {
		t.Logf("sample: %s", names[i])
	}

	// readFirst reads the first name matching keep whose data decodes,
	// trying a few in case some are not in the local storage.
	readFirst := func(what string, keep func(string) bool) (string, []byte) {
		t.Helper()
		var lastErr error
		tries := 0
		for _, n := range names {
			if !keep(n) {
				continue
			}
			data, err := s.ReadFile(n)
			if err == nil {
				return n, data
			}
			lastErr = err
			if tries++; tries == 20 {
				break
			}
		}
		t.Fatalf("no readable %s (last error: %v)", what, lastErr)
		return "", nil
	}

	var marine string
	for _, n := range names {
		if strings.HasSuffix(n, "/units/terran/marine/marine.m3") {
			marine = n
			t.Logf("Marine model: %s", n)
		}
	}
	isM3 := func(n string) bool { return strings.HasSuffix(n, ".m3") }
	if marine != "" {
		isM3 = func(n string) bool { return n == marine }
	}
	name, data := readFirst(".m3 model", isM3)
	if !bytes.HasPrefix(data, []byte("43DM")) {
		t.Errorf("%s starts with %q, want 43DM", name, data[:min(4, len(data))])
	}
	t.Logf("%s: %d bytes", name, len(data))

	name, data = readFirst(".dds texture", func(n string) bool { return strings.HasSuffix(n, ".dds") })
	if !bytes.HasPrefix(data, []byte("DDS ")) {
		t.Errorf("%s starts with %q, want DDS", name, data[:min(4, len(data))])
	}
	t.Logf("%s: %d bytes", name, len(data))

	name, data = readFirst("UnitData.xml", func(n string) bool { return strings.HasSuffix(n, "/unitdata.xml") })
	if !bytes.HasPrefix(bytes.TrimPrefix(data, []byte("\xEF\xBB\xBF")), []byte("<?xml")) {
		t.Errorf("%s starts with %q, want <?xml", name, data[:min(8, len(data))])
	}
	t.Logf("%s: %d bytes", name, len(data))

	// The lookup ignores case and takes backslashes.
	alt := strings.ToUpper(strings.ReplaceAll(name, "/", `\`))
	data2, err := s.ReadFile(alt)
	if err != nil || !bytes.Equal(data, data2) {
		t.Errorf("ReadFile(%q): %v", alt, err)
	}
	if _, err := s.ReadFile("no/such/file.txt"); err == nil {
		t.Error("ReadFile of a missing name succeeded")
	}
}
