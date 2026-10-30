// SPDX-License-Identifier: Apache-2.0

package replay

import (
	"os"
	"testing"
)

// openTest opens a replay from testdata, skipping the test when it is absent.
func openTest(t *testing.T, name string) *Replay {
	t.Helper()
	path := "../../testdata/replays/" + name
	if _, err := os.Stat(path); err != nil {
		t.Skipf("%s not present", path)
	}
	r, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	return r
}

// TestAboutLadderGame checks the details read from a tournament game on
// Battle.net: players' profiles and APM, observers, kill credit, how units
// were made, and both players' cameras.
func TestAboutLadderGame(t *testing.T) {
	r := openTest(t, "20220224 - GAME 1 - Dream vs Spirit - T vs T - Curious Minds LE.SC2Replay")
	in := r.Info
	if in.StartTime.IsZero() || in.StartTime.Year() != 2022 {
		t.Errorf("start time %v, want a time in 2022", in.StartTime)
	}
	if in.Region != "EU" {
		t.Errorf("region %q, want EU", in.Region)
	}
	if len(in.Observers) == 0 {
		t.Error("no observers; the game had casters")
	}
	for _, p := range in.Players {
		if p.Toon == "" || p.APM == 0 || p.SelectedRace != "Terran" || p.Handicap != 100 {
			t.Errorf("player %+v: want a profile, APM, Terran picked and no handicap", p)
		}
	}
	var killers, abilities, cameras, deaths int
	cameraPlayers := map[int]bool{}
	for _, e := range r.Events {
		switch e.Kind {
		case KindUnitDied:
			deaths++
			if e.Killer != 0 {
				killers++
			}
		case KindUnitBorn:
			if e.CreatorAbility != "" {
				abilities++
			}
		case KindCamera:
			cameras++
			cameraPlayers[e.Player] = true
		}
	}
	if killers == 0 || killers > deaths {
		t.Errorf("%d of %d deaths name a killer", killers, deaths)
	}
	if abilities == 0 {
		t.Error("no unit names the ability that made it")
	}
	if !cameraPlayers[1] || !cameraPlayers[2] || cameraPlayers[0] {
		t.Errorf("camera moves for players %v, want 1 and 2 only (observers dropped)", cameraPlayers)
	}
}

// TestAboutComputerGame checks a game against the built-in AI: its
// difficulty and build, and a random race pick.
func TestAboutComputerGame(t *testing.T) {
	r := openTest(t, "engine/GhostProtocol_2026-10-24_04-08-37_g10of10_Defeat.SC2Replay")
	bot, ai := r.Info.Players[0], r.Info.Players[1]
	if ai.Human || ai.Difficulty != "Elite" || ai.AIBuild == "" {
		t.Errorf("computer player %+v: want difficulty Elite (API VeryHard) and a build", ai)
	}
	if bot.Difficulty != "" {
		t.Errorf("the API player %+v got a computer difficulty", bot)
	}
	if ai.SelectedRace != "Random" {
		t.Errorf("computer picked %q, want Random", ai.SelectedRace)
	}
	if r.Info.Region != "" || r.Info.Ladder {
		t.Errorf("local game reported region %q, ladder %v", r.Info.Region, r.Info.Ladder)
	}
}
