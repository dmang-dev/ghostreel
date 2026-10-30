// SPDX-License-Identifier: Apache-2.0

package replay

import (
	"regexp"
	"strings"

	"github.com/icza/s2prot/rep"
)

// about fills in what the replay says about the game beyond its events: when
// and where it was played, the lobby (observers, computer difficulty), and
// per-player details from the lobby and the replay's metadata file.
func about(r *rep.Rep, info *Info) {
	if t := r.Details.TimeUTC(); t.Year() >= 2000 {
		info.StartTime = t.UTC()
	}
	gd := &r.InitData.GameDescription
	// Blizzard's maps give a profile handle ("2-S2-1-10077063") rather than a
	// name; only a name is worth showing.
	if a := gd.MapAuthorName(); !toonHandle.MatchString(a) {
		info.MapAuthor = a
	}
	opts := &gd.GameOptions
	if opts.BattleNet() {
		if rg := gd.Region(); rg != nil {
			info.Region = rg.Code
		}
	}
	info.Ladder = opts.Amm()
	info.Competitive = opts.CompetitiveOrRanked()

	users := r.InitData.UserInitDatas
	userName := func(id int64) string {
		if id >= 0 && int(id) < len(users) {
			return users[id].Name()
		}
		return ""
	}
	// Computer players' lobby slots, in order. Local games can give every
	// slot the same working-set slot ID, so computers are matched in order.
	var computers []*rep.Slot
	for i := range r.InitData.LobbyState.Slots {
		s := &r.InitData.LobbyState.Slots[i]
		if s.Control() == rep.ControlComputer {
			computers = append(computers, s)
		}
		if s.Control() == rep.ControlHuman && (s.Observe() == rep.ObserveSpectator || s.Observe() == rep.ObserveReferee) && s.Value("userId") != nil {
			if n := userName(s.UserID()); n != "" {
				info.Observers = append(info.Observers, n)
			}
		}
	}

	ds := r.Details.Players()
	for i := range ds {
		if i >= len(info.Players) {
			break
		}
		d, p := &ds[i], &info.Players[i]
		p.Handicap = int(d.Handicap())
		if d.Toon.ID() != 0 {
			p.Toon = d.Toon.String()
		}
		if d.Control() == rep.ControlComputer && len(computers) > 0 {
			s := computers[0]
			computers = computers[1:]
			p.Difficulty = difficultyName(s.Difficulty())
			p.AIBuild = aiBuildName(s.AIBuild())
		}
		if u := p.UserID; u >= 0 && u < len(users) {
			p.Clan = users[u].ClanTag()
			if m := users[u].MMR(); m > 0 {
				p.MMR = int(m)
			}
		}
	}

	for _, m := range r.Metadata.Players() {
		id := int(m.PlayerID())
		if id < 1 || id > len(info.Players) {
			continue
		}
		p := &info.Players[id-1]
		p.APM = int(m.APM() + 0.5)
		if mmr := m.MMR(); mmr > 0 {
			p.MMR = int(mmr + 0.5)
		}
		p.SelectedRace = raceName(m.SelectedRace())
	}
}

var toonHandle = regexp.MustCompile(`^\d+-S2-\d+-\d+$`)

// raceName expands the metadata file's race abbreviations.
func raceName(s string) string {
	switch strings.ToLower(s) {
	case "terr":
		return "Terran"
	case "prot":
		return "Protoss"
	case "zerg":
		return "Zerg"
	case "rand":
		return "Random"
	}
	return s
}

// difficultyName names a computer's difficulty as the game's lobby does. The
// numbers are the s2client API's (where 7 is "VeryHard"); the lobby calls
// that level Elite.
func difficultyName(d int64) string {
	names := []string{"", "Very Easy", "Easy", "Medium", "Hard", "Harder", "Very Hard", "Elite",
		"Cheater 1 (Vision)", "Cheater 2 (Money)", "Cheater 3 (Insane)"}
	if d > 0 && int(d) < len(names) {
		return names[d]
	}
	return ""
}

// aiBuildName names a computer's build (s2client API numbering).
func aiBuildName(b int64) string {
	names := []string{"", "Random build", "Rush", "Timing", "Power", "Macro", "Air"}
	if b > 0 && int(b) < len(names) {
		return names[b]
	}
	return ""
}
