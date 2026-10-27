// SPDX-License-Identifier: Apache-2.0

// Package analysis is ghostreel's plugin hook: a Plugin reads a finished
// timeline and reports what it found as markers (spans of the game, shown on
// the scrubber and the map), tables (shown in the viewer's analysis panel,
// each row jumping to its loop) and series (values over time, drawn as
// charts). The viewer draws any report, so a new plugin needs no frontend
// changes.
//
// Plugins register themselves with Register, usually from an init function,
// and a binary includes a plugin by importing its package. The built-in ones
// are build orders, battles, expansions, supply blocks, the economy charts,
// APM, production idle time, army composition, upgrades and hotspots.
package analysis

import (
	"fmt"
	"sort"
	"sync"

	pb "github.com/dmang-dev/ghostreel/gen/timeline/v1"
)

// Plugin analyzes a timeline. It must not modify the timeline, and may be
// called concurrently for different timelines.
type Plugin interface {
	// ID is a short stable name ("build-order").
	ID() string
	Analyze(tl *pb.Timeline) (*Report, error)
}

// Report is one plugin's findings.
type Report struct {
	Plugin  string   `json:"plugin"`
	Title   string   `json:"title"`
	Markers []Marker `json:"markers,omitempty"`
	Tables  []Table  `json:"tables,omitempty"`
	Series  []Series `json:"series,omitempty"`
	// Error is set instead of findings when the plugin failed.
	Error string `json:"error,omitempty"`
}

// Marker is a moment or span of the game.
type Marker struct {
	Loop   uint32 `json:"loop"`
	End    uint32 `json:"end,omitempty"`    // last loop of a span; 0 for a moment
	Player uint32 `json:"player,omitempty"` // whose it is; 0 for everyone
	// Where it happened, in map cells; both 0 when it has no place.
	X     float32 `json:"x,omitempty"`
	Y     float32 `json:"y,omitempty"`
	R     float32 `json:"r,omitempty"` // radius around X, Y
	Label string  `json:"label"`
	Kind  string  `json:"kind,omitempty"` // free-form category, e.g. "battle"
	// Hotspots (see hotspots.go) also carry what happened, how much it
	// matters (0-1) and a tier: "major" or "minor".
	Category string  `json:"category,omitempty"`
	Score    float32 `json:"score,omitempty"`
	Tier     string  `json:"tier,omitempty"`
}

// Table is rows of text, each tied to a loop.
type Table struct {
	Title   string   `json:"title"`
	Player  uint32   `json:"player,omitempty"` // whose it is; 0 for everyone
	Columns []string `json:"columns"`
	Rows    []Row    `json:"rows"`
}

// Series is a value over the game, drawn as a line chart; series with the
// same Title share a chart (one line per player).
type Series struct {
	Title  string  `json:"title"`
	Player uint32  `json:"player,omitempty"`
	Unit   string  `json:"unit,omitempty"` // shown after values, e.g. "/min"
	Points []Point `json:"points"`
}

// Point is a value at a loop.
type Point struct {
	Loop  uint32  `json:"loop"`
	Value float32 `json:"value"`
}

// Row is a table row; selecting it seeks to Loop.
type Row struct {
	Loop  uint32   `json:"loop"`
	Cells []string `json:"cells"`
}

var (
	mu      sync.Mutex
	plugins = map[string]Plugin{}
)

// Register adds a plugin. Registering two plugins with one ID panics.
func Register(p Plugin) {
	mu.Lock()
	defer mu.Unlock()
	if _, dup := plugins[p.ID()]; dup {
		panic(fmt.Sprintf("analysis: plugin %q registered twice", p.ID()))
	}
	plugins[p.ID()] = p
}

// Plugins lists the registered plugins by ID.
func Plugins() []Plugin {
	mu.Lock()
	defer mu.Unlock()
	out := make([]Plugin, 0, len(plugins))
	for _, p := range plugins {
		out = append(out, p)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID() < out[j].ID() })
	return out
}

// Run runs every registered plugin. A plugin that fails or panics gives a
// report with Error set; the others are unaffected.
func Run(tl *pb.Timeline) []Report {
	var out []Report
	for _, p := range Plugins() {
		out = append(out, runOne(p, tl))
	}
	return out
}

func runOne(p Plugin, tl *pb.Timeline) (rep Report) {
	defer func() {
		if r := recover(); r != nil {
			rep = Report{Plugin: p.ID(), Title: p.ID(), Error: fmt.Sprint("panic: ", r)}
		}
	}()
	r, err := p.Analyze(tl)
	if err != nil {
		return Report{Plugin: p.ID(), Title: p.ID(), Error: err.Error()}
	}
	if r == nil {
		return Report{Plugin: p.ID(), Title: p.ID()}
	}
	r.Plugin = p.ID()
	// Empty tables and series are [] in JSON, not null, for the viewer.
	for i := range r.Tables {
		if r.Tables[i].Rows == nil {
			r.Tables[i].Rows = []Row{}
		}
	}
	for i := range r.Series {
		if r.Series[i].Points == nil {
			r.Series[i].Points = []Point{}
		}
	}
	return *r
}

// playerName is a player's name from the timeline's meta.
func playerName(tl *pb.Timeline, id uint32) string {
	for _, p := range tl.GetMeta().GetPlayers() {
		if p.Id == id {
			return p.Name
		}
	}
	return fmt.Sprintf("Player %d", id)
}

// clock formats a loop as the in-game clock shows it (22.4 loops a second).
func clock(loop uint32) string {
	s := int(float64(loop) / 22.4)
	return fmt.Sprintf("%d:%02d", s/60, s%60)
}
