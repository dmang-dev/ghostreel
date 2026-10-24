// SPDX-License-Identifier: Apache-2.0

package replay

import (
	"fmt"
	"math"
	"time"
)

const (
	// LoopsPerGameSecond is the simulation rate in game seconds ("Normal" speed).
	// Use it for game-data math such as unit speeds, which the data files
	// express per game second.
	LoopsPerGameSecond = 16

	// LoopsPerRealSecond is the rate at "Faster", the speed of every ladder game.
	// Since Legacy of the Void the in-game clock shows real seconds, so every
	// user-facing time must use this rate.
	LoopsPerRealSecond = 22.4
)

// GameSeconds converts loops to game seconds.
func GameSeconds(loops int64) float64 { return float64(loops) / LoopsPerGameSecond }

// ClockSeconds converts loops to in-game clock seconds (what players see).
func ClockSeconds(loops int64) float64 { return float64(loops) / LoopsPerRealSecond }

// ClockDuration converts loops to an in-game clock duration.
func ClockDuration(loops int64) time.Duration {
	return time.Duration(ClockSeconds(loops) * float64(time.Second))
}

// LoopAtClock converts in-game clock seconds back to the loop at that time.
func LoopAtClock(seconds float64) int64 { return int64(math.Floor(seconds * LoopsPerRealSecond)) }

// FormatClock renders a loop as the in-game clock shows it: m:ss, or h:mm:ss
// past an hour. The clock truncates, it never rounds up.
func FormatClock(loops int64) string {
	s := int64(ClockSeconds(loops))
	if s >= 3600 {
		return fmt.Sprintf("%d:%02d:%02d", s/3600, s/60%60, s%60)
	}
	return fmt.Sprintf("%d:%02d", s/60, s%60)
}
