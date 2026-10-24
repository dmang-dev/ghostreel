// SPDX-License-Identifier: Apache-2.0

package replay

import (
	"sync"

	"github.com/icza/s2prot"
	"github.com/icza/s2prot/build"
)

var (
	protoMu sync.Mutex
	// approximated remembers stand-ins registered by ensureProtocol, since
	// after registration the build looks exactly supported.
	approximated = map[int]int{}
)

// ensureProtocol makes s2prot able to decode baseBuild. When s2prot has no
// protocol for it, the nearest older known protocol is registered as a
// stand-in and its build number returned (0 means exact support). Protocols
// rarely change between nearby builds, and the caller verifies the guess by
// checking the decode-error flags.
//
// This must run before s2prot first looks the build up, because s2prot caches
// failed lookups.
func ensureProtocol(baseBuild int) (approxFrom int) {
	protoMu.Lock()
	defer protoMu.Unlock()

	if from, ok := approximated[baseBuild]; ok {
		return from
	}
	if _, ok := build.Builds[baseBuild]; ok {
		return 0
	}
	if _, ok := build.Duplicates[baseBuild]; ok {
		return 0
	}
	best := 0
	for b := range build.Builds {
		if b < baseBuild && b > best {
			best = b
		}
	}
	for b := range build.Duplicates {
		if b < baseBuild && b > best {
			best = b
		}
	}
	if best == 0 {
		return 0
	}
	orig := best
	if o, ok := build.Duplicates[best]; ok {
		orig = o
	}
	build.Duplicates[baseBuild] = orig
	if s2prot.GetProtocol(baseBuild) == nil {
		delete(build.Duplicates, baseBuild)
		return 0
	}
	approximated[baseBuild] = best
	return best
}
