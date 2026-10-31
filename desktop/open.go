// SPDX-License-Identifier: Apache-2.0

package main

import "sync"

// fileOpener passes replays opened from the OS while the app runs (Finder's
// "Open With" and double-clicks on macOS, which arrive as events rather than
// command-line arguments) to the viewer. A file can arrive before the page
// has loaded, as it does when a double-click starts the app, so it waits
// until the page says it is listening.
type fileOpener struct {
	mu      sync.Mutex
	ready   bool
	pending []string
	send    func(path string) // to the page; set before ready
}

// Open shows path in the viewer now, or once it is listening.
func (o *fileOpener) Open(path string) {
	o.mu.Lock()
	defer o.mu.Unlock()
	if !o.ready {
		o.pending = append(o.pending, path)
		return
	}
	o.send(path)
}

// Ready marks the page as listening and hands it the files that came early.
// Only the last of those is shown: the viewer has one replay open at a time.
func (o *fileOpener) Ready() {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.ready = true
	if n := len(o.pending); n > 0 {
		o.send(o.pending[n-1])
		o.pending = nil
	}
}
