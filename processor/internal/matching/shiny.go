package matching

import (
	"crypto/sha1" //nolint:gosec // the game derives the shiny roll from SHA-1; not used for security
	"encoding/binary"
	"strconv"

	"github.com/pokemon/poracleng/processor/internal/db"
)

const (
	javaMult = 0x5DEECE66D
	javaMask = (uint64(1) << 48) - 1
	// BaseShinyRate is the normal wild shiny rate (1/512).
	BaseShinyRate = 512
)

// shinyBucket returns floor(java.util.Random(seed).nextFloat() * n); 0 means
// shiny at a 1/n chance. For n = 512 this equals nextInt(512), for other rates
// (Community Day 1/25) it does not: the game compares nextFloat() < 1/n
// (checked against scanner encounters on the 2026-10-10 Zorua CD).
func shinyBucket(seed uint64, n int) int {
	state := (((seed^javaMult)&javaMask)*javaMult + 0xB) & javaMask
	return int(((state >> 24) * uint64(n)) >> 24)
}

// ShinyRoll reproduces the game's per-player shiny die for a wild spawn:
// floor(new java.util.Random(le64(sha1(le64(encounterID) || playerID))).nextFloat() * rate).
// A result of 0 means the spawn is shiny for that player at a 1/rate chance
// (512 normally, e.g. 25 on Community Day).
func ShinyRoll(encounterID, playerID string, rate int) (int, bool) {
	enc, err := strconv.ParseUint(encounterID, 10, 64)
	if err != nil || rate <= 0 {
		return 0, false
	}
	buf := make([]byte, 8, 8+len(playerID))
	binary.LittleEndian.PutUint64(buf, enc)
	buf = append(buf, playerID...)
	sum := sha1.Sum(buf) //nolint:gosec // see import
	return shinyBucket(binary.LittleEndian.Uint64(sum[:8]), rate), true
}

// filterPersonalShiny drops shiny-predict rules whose owner has no linked
// player id, for whom this spawn is not shiny, or whose species has no shiny
// release. Other rules pass unchanged.
func filterPersonalShiny(encounterID string, pokemonID int, lat, lon float64, rules []*db.MonsterTracking, humans map[string]*db.Human) []*db.MonsterTracking {
	released := shinyReleasedFor(pokemonID)
	rate := shinyRateFor(pokemonID, lat, lon)
	out := rules[:0]
	for _, r := range rules {
		if r.ShinyPredict != 0 {
			if !released {
				continue
			}
			h := humans[r.ID]
			if h == nil || h.ShinyPlayerID == "" {
				continue
			}
			if roll, ok := ShinyRoll(encounterID, h.ShinyPlayerID, rate); !ok || roll != 0 {
				continue
			}
		}
		out = append(out, r)
	}
	return out
}
