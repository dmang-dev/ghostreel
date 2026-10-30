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

	// About the game, when the replay says.
	StartTime   time.Time // when the game started, UTC; zero when unknown
	MapAuthor   string
	Region      string   // Battle.net region code ("EU"); empty for local games
	Ladder      bool     // a ranked or unranked ladder game (automatic matchmaking)
	Competitive bool     // ranked
	Observers   []string // names of observers and casters

	EventCounts  map[string]int // by stream and s2prot event name, e.g. "tracker/UnitBorn"
	DecodeErrors []string       // streams that failed to decode completely
}

// MapHandle names a Battle.net depot file by content hash.
type MapHandle struct {
	Ext    string // "s2ma"
	Region string // two-letter depot region, e.g. "EU"
	Digest string // hex SHA-256
	// DepotURL is the region's depot server base URL (from s2prot), or empty.
	DepotURL string
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

	// Optional details; zero when the replay doesn't record them.
	SelectedRace string // the race picked in the lobby, "Random" when random
	Handicap     int    // percent; 100 is none
	Clan         string // clan tag without brackets
	Toon         string // Battle.net profile, "region-S2-realm-id"
	MMR          int    // ladder matchmaking rating
	APM          int    // as the game counts it
	Difficulty   string // computer players: "Elite", "Cheater 2 (Money)", …
	AIBuild      string // computer players: "Macro", "Rush", …
}

// Replay is an opened, fully decoded replay.
type Replay struct {
	Info   Info
	Events []Event
}

// Options select what to decode. The zero value decodes everything.
type Options struct {
	// SkipGameEvents leaves out game events (commands, selections, camera).
	// They are by far the largest stream (over a million events in long bot
	// games) and the tracker tier only uses them for the command feed.
	SkipGameEvents bool
	// InfoOnly decodes only the header, details and lobby (no event streams).
	// Info.HasTracker and EventCounts are then unknown (false / empty).
	InfoOnly bool
}

// Open decodes the replay at path.
func Open(path string) (*Replay, error) { return OpenWith(path, Options{}) }

// OpenWith is Open with options.
func OpenWith(path string, opts Options) (*Replay, error) {
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
	r, err := DecodeWith(f, opts)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	r.Info.Path = path
	r.Info.Hash = hash
	return r, nil
}

// Decode decodes a replay from any seekable source (Path and Hash stay empty).
func Decode(input io.ReadSeeker) (*Replay, error) { return DecodeWith(input, Options{}) }

// DecodeWith is Decode with options.
func DecodeWith(input io.ReadSeeker, opts Options) (*Replay, error) {
	baseBuild, err := peekBaseBuild(input)
	if err != nil {
		return nil, err
	}
	approx := ensureProtocol(baseBuild)
	if _, err := input.Seek(0, io.SeekStart); err != nil {
		return nil, err
	}
	r, err := rep.NewEvts(input, !opts.SkipGameEvents && !opts.InfoOnly, !opts.InfoOnly, !opts.InfoOnly)
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
	about(r, &info)

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
	lastCmd := map[int64]*Event{} // per user, for CmdUpdateTarget* events
	for i := range r.GameEvts {
		if e, ok := convertGame(&r.GameEvts[i], userToPlayer, lastCmd); ok {
			evts = append(evts, e)
		}
	}
	for i := range r.MessageEvts {
		me := &r.MessageEvts[i]
		switch me.Name {
		case "Chat":
			evts = append(evts, Event{
				Loop:   me.Loop(),
				Kind:   KindChat,
				Player: userToPlayer[me.UserID()],
				Text:   me.Stringv("string"),
			})
		case "Ping":
			if me.Value("point") == nil {
				continue
			}
			evts = append(evts, Event{
				Loop:   me.Loop(),
				Kind:   KindPing,
				Player: userToPlayer[me.UserID()],
				X:      float64(me.Int("point", "x")) / cmdCoordScale,
				Y:      float64(me.Int("point", "y")) / cmdCoordScale,
			})
		}
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
		mh := &MapHandle{Ext: h.Type, Digest: h.Digest}
		if h.Region != nil {
			mh.Region = h.Region.Code
			if h.Region.DepotURL != nil {
				mh.DepotURL = h.Region.DepotURL.String()
			}
		}
		return mh
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
		if e.Value("killerUnitTagIndex") != nil {
			out.Killer = MakeTag(uint32(e.Int("killerUnitTagIndex")), uint32(e.Int("killerUnitTagRecycle")))
		}
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
		split := func(kind string) Split {
			v := func(part string) int64 {
				return s.Int("scoreValueMinerals"+kind+part) + s.Int("scoreValueVespene"+kind+part)
			}
			return Split{v("Army"), v("Economy"), v("Technology")}
		}
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
			Lost:   split("Lost"),
			Killed: split("Killed"),
			FriendlyFire: s.Int("scoreValueMineralsFriendlyFireArmy") + s.Int("scoreValueMineralsFriendlyFireEconomy") +
				s.Int("scoreValueMineralsFriendlyFireTechnology") + s.Int("scoreValueVespeneFriendlyFireArmy") +
				s.Int("scoreValueVespeneFriendlyFireEconomy") + s.Int("scoreValueVespeneFriendlyFireTechnology"),
			InProgress: s.Int("scoreValueMineralsUsedInProgressArmy") + s.Int("scoreValueMineralsUsedInProgressEconomy") +
				s.Int("scoreValueMineralsUsedInProgressTechnology") + s.Int("scoreValueVespeneUsedInProgressArmy") +
				s.Int("scoreValueVespeneUsedInProgressEconomy") + s.Int("scoreValueVespeneUsedInProgressTechnology"),
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
	if e.Value("creatorUnitTagIndex") != nil {
		out.Creator = MakeTag(uint32(e.Int("creatorUnitTagIndex")), uint32(e.Int("creatorUnitTagRecycle")))
	}
	if e.Value("creatorAbilityName") != nil {
		out.CreatorAbility = e.Stringv("creatorAbilityName")
	}
	out.UnitType = e.Stringv("unitTypeName")
	out.Player = int(e.Int("controlPlayerId"))
	out.X, out.Y = float64(e.Int("x")), float64(e.Int("y"))
	return out, true
}

// Command target coordinates are fixed point with 12 fractional bits.
const cmdCoordScale = 4096

// Camera targets are fixed point with 8 fractional bits.
const cameraCoordScale = 256

// cmdFlagQueued is the shift-queue bit of a command's flags.
const cmdFlagQueued = 0x2

// convertGame converts the game events the timeline uses: commands (including
// target updates, which reuse the user's previous command), the selection
// and control-group changes needed to know which units a command applies to,
// and the players' camera moves.
func convertGame(e *s2prot.Event, userToPlayer map[int64]int, lastCmd map[int64]*Event) (Event, bool) {
	user := e.UserID()
	out := Event{Loop: e.Loop(), Player: userToPlayer[user]}
	switch e.Name {
	case "Cmd":
		out.Kind = KindCommand
		if e.Value("abil") != nil {
			out.AbilityLink = e.Int("abil", "abilLink")
			out.AbilityCmdIndex = e.Int("abil", "abilCmdIndex")
		} else {
			out.AbilityLink = -1 // smart (right-click) command
		}
		out.Queued = e.Int("cmdFlags")&cmdFlagQueued != 0
		switch {
		case e.Value("data", "TargetPoint") != nil:
			setTargetPoint(&out, e, "data", "TargetPoint")
		case e.Value("data", "TargetUnit") != nil:
			setTargetUnit(&out, e, "data", "TargetUnit")
		}
		c := out
		lastCmd[user] = &c
		return out, true

	case "CmdUpdateTargetPoint", "CmdUpdateTargetUnit":
		prev := lastCmd[user]
		if prev == nil {
			return Event{}, false
		}
		out = *prev
		out.Loop = e.Loop()
		out.TargetUpdate = true
		out.TargetTag, out.HasTarget, out.X, out.Y = 0, false, 0, 0
		if e.Name == "CmdUpdateTargetPoint" {
			setTargetPoint(&out, e, "target")
		} else {
			setTargetUnit(&out, e, "target")
		}
		return out, true

	case "SelectionDelta":
		out.Kind = KindSelection
		sc := &SelectionChange{Group: int(e.Int("controlGroupId")), Remove: mask(e.Structv("delta", "removeMask"))}
		for _, t := range e.Array("delta", "addUnitTags") {
			sc.AddTags = append(sc.AddTags, Tag(asInt(t)))
		}
		for _, sg := range e.Array("delta", "addSubgroups") {
			if m, ok := sg.(s2prot.Struct); ok {
				sc.AddSubgroups = append(sc.AddSubgroups, Subgroup{
					UnitLink: m.Int("unitLink"), Priority: m.Int("subgroupPriority"),
					IntraPri: m.Int("intraSubgroupPriority"), Count: int(m.Int("count")),
				})
			}
		}
		out.Selection = sc
		return out, true

	case "ControlGroupUpdate":
		out.Kind = KindControlGroup
		out.ControlGroup = &ControlGroupChange{
			Index: int(e.Int("controlGroupIndex")), Update: int(e.Int("controlGroupUpdate")),
			Mask: mask(e.Structv("mask")),
		}
		return out, true

	case "CameraUpdate":
		// Observers move cameras too; only players' views are kept. Updates
		// that only zoom or turn the camera have no target.
		if out.Player == 0 || e.Value("target") == nil {
			return Event{}, false
		}
		out.Kind = KindCamera
		out.X = float64(e.Int("target", "x")) / cameraCoordScale
		out.Y = float64(e.Int("target", "y")) / cameraCoordScale
		return out, true
	}
	return Event{}, false
}

func setTargetPoint(out *Event, e *s2prot.Event, path ...string) {
	out.HasTarget = true
	out.X = float64(e.Int(append(path, "x")...)) / cmdCoordScale
	out.Y = float64(e.Int(append(path, "y")...)) / cmdCoordScale
}

func setTargetUnit(out *Event, e *s2prot.Event, path ...string) {
	out.HasTarget = true
	out.X = float64(e.Int(append(path, "snapshotPoint", "x")...)) / cmdCoordScale
	out.Y = float64(e.Int(append(path, "snapshotPoint", "y")...)) / cmdCoordScale
	out.TargetTag = Tag(e.Int(append(path, "tag")...))
}

// mask converts a removeMask / control-group mask choice.
func mask(s s2prot.Struct) Mask {
	switch {
	case s["Mask"] != nil:
		var bits []bool
		switch b := s["Mask"].(type) {
		case s2prot.BitArr:
			for i := 0; i < b.Count; i++ {
				bits = append(bits, b.Bit(i))
			}
		case *s2prot.BitArr:
			for i := 0; i < b.Count; i++ {
				bits = append(bits, b.Bit(i))
			}
		}
		return Mask{Kind: MaskBits, Bits: bits}
	case s["OneIndices"] != nil:
		return Mask{Kind: MaskOneIndices, Indices: indices(s["OneIndices"])}
	case s["ZeroIndices"] != nil:
		return Mask{Kind: MaskZeroIndices, Indices: indices(s["ZeroIndices"])}
	}
	if _, ok := s["ZeroIndices"]; ok { // an empty list decodes as a nil value
		return Mask{Kind: MaskZeroIndices}
	}
	if _, ok := s["OneIndices"]; ok {
		return Mask{Kind: MaskOneIndices}
	}
	return Mask{Kind: MaskNone}
}

func indices(v interface{}) []int {
	arr, _ := v.([]interface{})
	out := make([]int, 0, len(arr))
	for _, x := range arr {
		out = append(out, int(asInt(x)))
	}
	return out
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
