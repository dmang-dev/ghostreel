// SPDX-License-Identifier: Apache-2.0

// Package replay opens .SC2Replay files through s2prot and turns them into
// plain Go values: an Info summary and a loop-ordered list of normalized
// Events. Nothing outside this package imports s2prot.
package replay

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"sort"
	"time"

	"github.com/icza/mpq"
	"github.com/icza/s2prot"
	"github.com/icza/s2prot/rep"
)

// MinTrackerBaseBuild is the first base build with tracker events (patch 2.0.8).
// Timelines can only be built from replays at or after it.
const MinTrackerBaseBuild = 25604

// ErrNoTrackerEvents is returned by callers that need tracker events when a
// replay predates them.
var ErrNoTrackerEvents = errors.New("replay predates tracker events (patch 2.0.8); playback is not supported")

// Info summarizes a replay.
type Info struct {
	Path string
	Hash string // hex SHA-256 of the file, used as the cache key

	Version   string // major.minor.revision.build
	BaseBuild int
	// ProtocolApproxFrom is non-zero when s2prot had no protocol for BaseBuild
	// and the protocol of this older build was used instead.
	ProtocolApproxFrom int

	Loops    int64
	Duration time.Duration // in-game clock
	Speed    string

	MapTitle    string
	MapFileName string     // set for maps loaded from a local file (API and custom games)
	MapHandle   *MapHandle // set for maps downloaded through Battle.net
	MapWidth    int
	MapHeight   int

	Players    []Player
	HasTracker bool

	EventCounts  map[string]int // by stream and s2prot event name, e.g. "tracker/UnitBorn"
	DecodeErrors []string       // streams that failed to decode completely
}

// MapHandle names a Battle.net depot file by content hash.
type MapHandle struct {
	Ext    string // "s2ma"
	Region string // two-letter depot region, e.g. "EU"
	Digest string // hex SHA-256
}

// FileName is the cache file name, e.g. "f0091d….s2ma".
func (h MapHandle) FileName() string { return h.Digest + "." + h.Ext }

// Player is one participant (observers excluded).
type Player struct {
	ID     int // 1-based, as used in tracker events
	UserID int // -1 when unknown (computer players)
	Name   string
	Race   string
	Result string
	Color  [3]byte
	Human  bool
	StartX int // start location in cells, 0 when unknown
	StartY int
}

// Replay is an opened, fully decoded replay.
type Replay struct {
	Info   Info
	Events []Event
}

// Open decodes the replay at path.
func Open(path string) (*Replay, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return nil, err
	}
	hash := hex.EncodeToString(h.Sum(nil))
	if _, err := f.Seek(0, io.SeekStart); err != nil {
		return nil, err
	}
	r, err := Decode(f)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	r.Info.Path = path
	r.Info.Hash = hash
	return r, nil
}

// Decode decodes a replay from any seekable source (Path and Hash stay empty).
func Decode(input io.ReadSeeker) (*Replay, error) {
	baseBuild, err := peekBaseBuild(input)
	if err != nil {
		return nil, err
	}
	approx := ensureProtocol(baseBuild)
	if _, err := input.Seek(0, io.SeekStart); err != nil {
		return nil, err
	}
	r, err := rep.New(input)
	if err != nil {
		if errors.Is(err, rep.ErrUnsupportedRepVersion) {
			return nil, fmt.Errorf("base build %d: %w", baseBuild, err)
		}
		return nil, err
	}
	defer r.Close()
	return convert(r, approx), nil
}

func peekBaseBuild(input io.ReadSeeker) (int, error) {
	m, err := mpq.New(input)
	if err != nil {
		return 0, fmt.Errorf("not an MPQ archive: %w", err)
	}
	h := s2prot.DecodeHeader(m.UserData())
	if h == nil {
		return 0, rep.ErrInvalidRepFile
	}
	return int(h.Int("version", "baseBuild")), nil
}

func convert(r *rep.Rep, approx int) *Replay {
	info := Info{
		Version:            r.Header.VersionString(),
		BaseBuild:          int(r.Header.BaseBuild()),
		ProtocolApproxFrom: approx,
		Loops:              r.Header.Loops(),
		Speed:              r.Details.GameSpeed().String(),
		MapTitle:           r.Details.Title(),
		MapFileName:        r.Details.MapFileName(),
		MapHandle:          mapHandle(r),
		MapWidth:           int(r.InitData.GameDescription.MapSizeX()),
		MapHeight:          int(r.InitData.GameDescription.MapSizeY()),
		EventCounts:        map[string]int{},
	}
	if info.MapFileName == "" {
		info.MapFileName = r.InitData.GameDescription.MapFileName()
	}
	info.Duration = ClockDuration(info.Loops)
	if r.GameEvtsErr {
		info.DecodeErrors = append(info.DecodeErrors, "game events")
	}
	if r.MessageEvtsErr {
		info.DecodeErrors = append(info.DecodeErrors, "message events")
	}
	if r.TrackerEvtsErr {
		info.DecodeErrors = append(info.DecodeErrors, "tracker events")
	}

	var tracker []s2prot.Event
	if r.TrackerEvts != nil {
		tracker = r.TrackerEvts.Evts
	}
	info.HasTracker = len(tracker) > 0
	info.Players = players(r, tracker)

	for _, e := range tracker {
		info.EventCounts["tracker/"+e.Name]++
	}
	for _, e := range r.GameEvts {
		info.EventCounts["game/"+e.Name]++
	}
	for _, e := range r.MessageEvts {
		info.EventCounts["message/"+e.Name]++
	}

	userToPlayer := map[int64]int{}
	for _, p := range info.Players {
		if p.UserID >= 0 {
			userToPlayer[int64(p.UserID)] = p.ID
		}
	}

	evts := make([]Event, 0, len(tracker)+len(r.GameEvts)/4)
	for i := range tracker {
		if e, ok := convertTracker(&tracker[i]); ok {
			evts = append(evts, e)
		}
	}
	for i := range r.GameEvts {
		if e, ok := convertGame(&r.GameEvts[i], userToPlayer); ok {
			evts = append(evts, e)
		}
	}
	for i := range r.MessageEvts {
		me := &r.MessageEvts[i]
		if me.Name != "Chat" {
			continue
		}
		evts = append(evts, Event{
			Loop:   me.Loop(),
			Kind:   KindChat,
			Player: userToPlayer[me.UserID()],
			Text:   me.Stringv("string"),
		})
	}
	// Stable: events at the same loop keep stream order (tracker first), which
	// the builder relies on (a unit dies before its slot is reborn).
	sort.SliceStable(evts, func(i, j int) bool { return evts[i].Loop < evts[j].Loop })
	return &Replay{Info: info, Events: evts}
}

// mapHandle picks the map out of the dependency list: the last s2ma handle
// that isn't standard data (Core.SC2Mod, Liberty.SC2Mod, …). Games started
// from a local map file (API and custom games) have no handles at all.
func mapHandle(r *rep.Rep) *MapHandle {
	hs := r.Details.CacheHandles()
	if len(hs) == 0 {
		hs = r.InitData.GameDescription.CacheHandles()
	}
	for i := len(hs) - 1; i >= 0; i-- {
		h := hs[i]
		if h.Type != "s2ma" || h.StandardData() != "" {
			continue
		}
		region := ""
		if h.Region != nil {
			region = h.Region.Code
		}
		return &MapHandle{Ext: h.Type, Region: region, Digest: h.Digest}
	}
	return nil
}

// players lists participants. Tracker player IDs are the 1-based positions
// in the details player list. User IDs (which game and message events carry)
// come from PlayerSetup tracker events when present (base build 27950+),
// otherwise from matching lobby slots on their working-set slot ID.
func players(r *rep.Rep, tracker []s2prot.Event) []Player {
	ds := r.Details.Players()
	out := make([]Player, len(ds))
	for i := range ds {
		d := &ds[i]
		out[i] = Player{
			ID:     i + 1,
			UserID: -1,
			Name:   d.Name,
			Race:   d.Race().String(),
			Result: d.Result().String(),
			Color:  [3]byte{d.Color[1], d.Color[2], d.Color[3]},
			Human:  d.Control() == rep.ControlHuman,
		}
	}
	found := false
	for i := range tracker {
		e := &tracker[i]
		if e.Name != "PlayerSetup" {
			continue
		}
		pid := int(e.Int("playerId"))
		if pid >= 1 && pid <= len(out) && e.Value("userId") != nil {
			out[pid-1].UserID = int(e.Int("userId"))
			found = true
		}
	}
	if !found {
		for _, s := range r.InitData.LobbyState.Slots {
			if s.Value("userId") == nil {
				continue
			}
			for i := range ds {
				if ds[i].WorkingSetSlotID() == s.WorkingSetSlotID() {
					out[i].UserID = int(s.UserID())
				}
			}
		}
	}
	if r.TrackerEvts != nil {
		for pid, d := range r.TrackerEvts.PIDPlayerDescMap {
			if pid >= 1 && int(pid) <= len(out) {
				out[pid-1].StartX, out[pid-1].StartY = int(d.StartLocX), int(d.StartLocY)
			}
		}
	}
	return out
}

func convertTracker(e *s2prot.Event) (Event, bool) {
	out := Event{Loop: e.Loop()}
	tag := func() Tag { return MakeTag(uint32(e.Int("unitTagIndex")), uint32(e.Int("unitTagRecycle"))) }
	switch e.Name {
	case "UnitBorn":
		out.Kind = KindUnitBorn
	case "UnitInit":
		out.Kind = KindUnitInit
	case "UnitDone":
		out.Kind, out.Tag = KindUnitDone, tag()
		return out, true
	case "UnitDied":
		out.Kind, out.Tag = KindUnitDied, tag()
		out.X, out.Y = float64(e.Int("x")), float64(e.Int("y"))
		out.KillerPlayer = int(e.Int("killerPlayerId"))
		return out, true
	case "UnitTypeChange":
		out.Kind, out.Tag, out.UnitType = KindUnitTypeChange, tag(), e.Stringv("unitTypeName")
		return out, true
	case "UnitOwnerChange":
		out.Kind, out.Tag, out.Player = KindUnitOwnerChange, tag(), int(e.Int("controlPlayerId"))
		return out, true
	case "UnitPositions":
		out.Kind = KindUnitPositions
		items := e.Array("items")
		idx := uint32(e.Int("firstUnitIndex"))
		for i := 0; i+2 < len(items); i += 3 {
			idx += uint32(asInt(items[i]))
			out.Positions = append(out.Positions, PositionSample{
				Index: idx, X: float64(asInt(items[i+1])), Y: float64(asInt(items[i+2])),
			})
		}
		return out, true
	case "PlayerStats":
		out.Kind, out.Player = KindPlayerStats, int(e.Int("playerId"))
		s := e.Structv("stats")
		out.Stats = &PlayerStats{
			SupplyUsed:   float64(s.Int("scoreValueFoodUsed")) / 4096,
			SupplyMade:   float64(s.Int("scoreValueFoodMade")) / 4096,
			Minerals:     s.Int("scoreValueMineralsCurrent"),
			Vespene:      s.Int("scoreValueVespeneCurrent"),
			MineralRate:  s.Int("scoreValueMineralsCollectionRate"),
			VespeneRate:  s.Int("scoreValueVespeneCollectionRate"),
			Workers:      s.Int("scoreValueWorkersActiveCount"),
			ArmyMinerals: s.Int("scoreValueMineralsUsedActiveForces"),
			ArmyGas:      s.Int("scoreValueVespeneUsedActiveForces"),
			LostMinerals: s.Int("scoreValueMineralsLostArmy") + s.Int("scoreValueMineralsLostEconomy") +
				s.Int("scoreValueMineralsLostTechnology"),
			LostGas: s.Int("scoreValueVespeneLostArmy") + s.Int("scoreValueVespeneLostEconomy") +
				s.Int("scoreValueVespeneLostTechnology"),
			KilledMinerals: s.Int("scoreValueMineralsKilledArmy") + s.Int("scoreValueMineralsKilledEconomy") +
				s.Int("scoreValueMineralsKilledTechnology"),
			KilledGas: s.Int("scoreValueVespeneKilledArmy") + s.Int("scoreValueVespeneKilledEconomy") +
				s.Int("scoreValueVespeneKilledTechnology"),
		}
		return out, true
	case "Upgrade":
		out.Kind, out.Player = KindUpgrade, int(e.Int("playerId"))
		out.Name, out.Count = e.Stringv("upgradeTypeName"), e.Int("count")
		return out, true
	default:
		return out, false
	}
	// UnitBorn / UnitInit share their fields.
	out.Tag = tag()
	out.UnitType = e.Stringv("unitTypeName")
	out.Player = int(e.Int("controlPlayerId"))
	out.X, out.Y = float64(e.Int("x")), float64(e.Int("y"))
	return out, true
}

// Command target coordinates are fixed point with 12 fractional bits.
const cmdCoordScale = 4096

func convertGame(e *s2prot.Event, userToPlayer map[int64]int) (Event, bool) {
	if e.Name != "Cmd" {
		return Event{}, false
	}
	out := Event{Loop: e.Loop(), Kind: KindCommand, Player: userToPlayer[e.UserID()]}
	if e.Value("abil") != nil {
		out.AbilityLink = e.Int("abil", "abilLink")
		out.AbilityCmdIndex = e.Int("abil", "abilCmdIndex")
	} else {
		out.AbilityLink = -1 // smart (right-click) command
	}
	switch {
	case e.Value("data", "TargetPoint") != nil:
		out.HasTarget = true
		out.X = float64(e.Int("data", "TargetPoint", "x")) / cmdCoordScale
		out.Y = float64(e.Int("data", "TargetPoint", "y")) / cmdCoordScale
	case e.Value("data", "TargetUnit") != nil:
		out.HasTarget = true
		out.X = float64(e.Int("data", "TargetUnit", "snapshotPoint", "x")) / cmdCoordScale
		out.Y = float64(e.Int("data", "TargetUnit", "snapshotPoint", "y")) / cmdCoordScale
		out.TargetTag = Tag(e.Int("data", "TargetUnit", "tag"))
	}
	return out, true
}

func asInt(v interface{}) int64 {
	switch n := v.(type) {
	case int64:
		return n
	case int:
		return int64(n)
	case float64:
		return int64(n)
	}
	return 0
}
