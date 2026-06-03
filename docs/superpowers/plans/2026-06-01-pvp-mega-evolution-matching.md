# PVP Mega-Evolution Matching Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Let `!track` (and the ReactMap API) discriminate PVP matches by mega/temporary evolution — `mega` (any mega), `mega:x` (Mega X), `mega:y` (Mega Y) — defaulting to base-only, with the server `include_mega_evolution` flag governing only the no-keyword default.

**Architecture:** The Golbat `pvp` array already carries an `evolution` tag per rank entry (`0`=base, `1`=Mega, `2`=Mega X, `3`=Mega Y — the `HoloTemporaryEvolutionId` enum). We stop *dropping* mega entries in `pvp.Calculate`, instead carry each best-rank entry's `Evolution` through `pvp.LeagueRank` into the matcher, and add a per-rule `pvp_ranking_evolution` column the matcher filters on — exactly parallel to the existing `pvp_ranking_cap` discriminator. Column value `0` means "no per-rule preference" and is interpreted by the server `include_mega_evolution` flag; values `1/2/3` are universal and proto-aligned.

**Tech Stack:** Go, MySQL (golang-migrate SQL files), sqlx, logrus, the `bot` command framework, i18n JSON locales.

**Encoding (settled in design):**

| keyword | `pvp_ranking_evolution` | matcher keeps entry where |
|---|---|---|
| *(none)* | `0` | `evo == 0` (base only) — unless server flag on, then any |
| `mega` | `1` | `evo >= 1` (any mega) |
| `mega:x` | `2` | `evo == 2` (Mega X) |
| `mega:y` | `3` | `evo == 3` (Mega Y) |

Backward-compat: existing rows backfill to `0`; `0` + `include_mega_evolution=false` (today's default) = base only = unchanged; `0` + `=true` = base+mega = unchanged. Migration is a behavioral no-op.

---

## File structure

| File | Responsibility | Change |
|---|---|---|
| `processor/internal/db/migrations/000005_pvp_ranking_evolution.{up,down}.sql` | schema | **create** — add/drop `monsters.pvp_ranking_evolution` |
| `processor/internal/pvp/rankcalculator.go` | rank calc | add `LeagueRank.Evolution`, per-evolution best rank, drop `filterMega`/`IncludeMegaEvolution` |
| `processor/internal/db/monsters.go` | in-memory rule + load SQL | add `PVPRankingEvolution` field + `LoadMonsters` column |
| `processor/internal/db/tracking_queries.go` | API rule struct + CRUD SQL | add field to `MonsterTrackingAPI` + 4 SQL statements |
| `processor/internal/matching/pokemon.go` | matcher | add `IncludeMegaEvolution` + evolution discriminator |
| `processor/cmd/processor/main.go` | wiring | move flag from `pvp.Config` to `PokemonMatcher` |
| `processor/internal/bot/commands/track.go` | command | `mega` parse, `pvpEntry.Evolution`, no-X/Y warn |
| `processor/internal/rowtext/monster.go` | `!tracked` text | show mega mode |
| `processor/internal/api/trackingMonster.go` | ReactMap API | request field + persistence |
| `processor/internal/i18n/locale/en.json` | strings | keyword + rowtext + warn keys |
| `config/config.example.toml` | docs | update `include_mega_evolution` comment |

---

## Task 1: Database migration

**Files:**
- Create: `processor/internal/db/migrations/000005_pvp_ranking_evolution.up.sql`
- Create: `processor/internal/db/migrations/000005_pvp_ranking_evolution.down.sql`

- [ ] **Step 1: Write the up migration**

`processor/internal/db/migrations/000005_pvp_ranking_evolution.up.sql`:
```sql
ALTER TABLE `monsters`
  ADD COLUMN `pvp_ranking_evolution` tinyint(1) NOT NULL DEFAULT 0;
```

- [ ] **Step 2: Write the down migration**

`processor/internal/db/migrations/000005_pvp_ranking_evolution.down.sql`:
```sql
ALTER TABLE `monsters` DROP COLUMN `pvp_ranking_evolution`;
```

- [ ] **Step 3: Verify migrations are embedded and build**

Run: `cd processor && go build ./...`
Expected: PASS (migration files are embedded via `internal/db/migrations/embed.go`; no code references needed).

- [ ] **Step 4: Commit**

```bash
git add processor/internal/db/migrations/000005_pvp_ranking_evolution.up.sql processor/internal/db/migrations/000005_pvp_ranking_evolution.down.sql
git commit -m "db: add monsters.pvp_ranking_evolution column (migration 000005)"
```

---

## Task 2: Carry evolution through `pvp.LeagueRank` (core)

**Files:**
- Modify: `processor/internal/pvp/rankcalculator.go`
- Test: `processor/internal/pvp/rankcalculator_test.go`

- [ ] **Step 1: Write the failing test**

Add to `processor/internal/pvp/rankcalculator_test.go`:
```go
func TestCalculate_KeepsMegaEntriesTaggedByEvolution(t *testing.T) {
	// great league: base (evo 0, rank 10) + Mega X (evo 2, rank 5) + Mega Y (evo 3, rank 3)
	pokemon := &webhook.PokemonWebhook{
		PokemonID: 6,
		PVP: map[string][]webhook.PVPRankEntry{
			"great": {
				{Pokemon: 6, Form: 178, Rank: 10, CP: 1490, Cap: 50, Capped: true, Evolution: 0},
				{Pokemon: 6, Form: 178, Rank: 5, CP: 1480, Cap: 50, Capped: true, Evolution: 2},
				{Pokemon: 6, Form: 178, Rank: 3, CP: 1470, Cap: 50, Capped: true, Evolution: 3},
			},
		},
	}
	cfg := &Config{LevelCaps: []int{50}}
	result := Calculate(pokemon, cfg)

	got := map[int]int{} // evolution -> rank
	for _, lr := range result.BestRank[1500] {
		got[lr.Evolution] = lr.Rank
	}
	if got[0] != 10 || got[2] != 5 || got[3] != 3 {
		t.Fatalf("expected base=10, megaX=5, megaY=3 tagged by evolution; got %#v", got)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd processor && go test ./internal/pvp/ -run TestCalculate_KeepsMegaEntriesTaggedByEvolution -v`
Expected: FAIL — `LeagueRank` has no `Evolution` field (compile error) and megas are dropped.

- [ ] **Step 3: Add `Evolution` to `LeagueRank` and drop the mega filter**

In `processor/internal/pvp/rankcalculator.go`, change the struct (lines 9-15):
```go
// LeagueRank represents the best rank info for a league, for one evolution slot.
type LeagueRank struct {
	Rank      int   `json:"rank"`
	CP        int   `json:"cp"`
	Caps      []int `json:"caps,omitempty"`
	Form      int   `json:"form,omitempty"`
	Evolution int   `json:"evolution,omitempty"` // 0=base, 1=Mega, 2=Mega X, 3=Mega Y
}
```

Remove `IncludeMegaEvolution` from `Config` (delete the line in the `Config` struct, lines 23-32).

In `Calculate`, replace the four `filterMega(...)` call sites (lines 64, 71, 75, 79) so entries pass through unfiltered:
```go
	// From new pvp field (Chuck / new RDM)
	if pokemon.PVP != nil {
		for leagueName, entries := range pokemon.PVP {
			var leagueCP int
			switch leagueName {
			case "little":
				leagueCP = 500
			case "great":
				leagueCP = 1500
			case "ultra":
				leagueCP = 2500
			default:
				continue
			}
			leagueMap[leagueCP] = append(leagueMap[leagueCP], entries...)
		}
	}

	// From legacy fields
	if pokemon.PVPRankingsGreatLeague != nil {
		leagueMap[1500] = append(leagueMap[1500], pokemon.PVPRankingsGreatLeague...)
	}
	if pokemon.PVPRankingsUltraLeague != nil {
		leagueMap[2500] = append(leagueMap[2500], pokemon.PVPRankingsUltraLeague...)
	}
	if pokemon.PVPRankingsLittleLeague != nil {
		leagueMap[500] = append(leagueMap[500], pokemon.PVPRankingsLittleLeague...)
	}
```

Delete the `filterMega` function entirely (lines 195-206).

- [ ] **Step 4: Group `calculateLeague` best rank by evolution**

Replace the body of `calculateLeague` (lines 97-193) with:
```go
func calculateLeague(league int, leagueData []webhook.PVPRankEntry, capsConsidered []int, pokemonID int, cfg *Config, minCP int, evoData map[int]map[int][]LeagueRank) []LeagueRank {
	type capBest struct {
		rank int
		cp   int
	}
	// best[evolution][cap] = best rank/cp for that evolution + cap
	best := make(map[int]map[int]*capBest)
	ensure := func(evo int) map[int]*capBest {
		if best[evo] == nil {
			m := make(map[int]*capBest, len(capsConsidered))
			for _, c := range capsConsidered {
				m[c] = &capBest{rank: 4096, cp: 0}
			}
			best[evo] = m
		}
		return best[evo]
	}

	for _, stats := range leagueData {
		var caps []int
		if stats.Cap == 0 && !stats.Capped {
			caps = append(caps, 50)
		} else if stats.Capped {
			for _, c := range capsConsidered {
				if c >= stats.Cap {
					caps = append(caps, c)
				}
			}
		} else {
			caps = append(caps, stats.Cap)
		}

		capMap := ensure(stats.Evolution)
		for _, cap := range caps {
			b, ok := capMap[cap]
			if !ok {
				continue
			}
			if stats.Rank > 0 && stats.Rank < b.rank {
				b.rank = stats.Rank
				b.cp = stats.CP
			} else if stats.Rank > 0 && stats.CP > 0 && stats.Rank == b.rank && stats.CP > b.cp {
				b.cp = stats.CP
			}
		}

		// Cross-species evolution direct tracking — unchanged (base entries only)
		if stats.Evolution == 0 && cfg.PVPEvolutionDirectTracking && stats.Rank > 0 && stats.CP > 0 &&
			stats.Pokemon != pokemonID && stats.Rank <= cfg.PVPFilterMaxRank && stats.CP >= minCP {
			var evoCaps []int
			if stats.Capped {
				for _, c := range capsConsidered {
					if c >= stats.Cap {
						evoCaps = append(evoCaps, c)
					}
				}
			} else if stats.Cap > 0 {
				for _, c := range capsConsidered {
					if c == stats.Cap {
						evoCaps = append(evoCaps, c)
					}
				}
			}
			evoRank := LeagueRank{Rank: stats.Rank, CP: stats.CP, Caps: evoCaps, Form: stats.Form}
			if _, ok := evoData[stats.Pokemon]; !ok {
				evoData[stats.Pokemon] = make(map[int][]LeagueRank)
			}
			evoData[stats.Pokemon][league] = append(evoData[stats.Pokemon][league], evoRank)
		}
	}

	// Consolidate best ranks, keeping each evolution slot separate.
	var bestRanks []LeagueRank
	for evo, capMap := range best {
		var evoRanks []LeagueRank
		for cap, details := range capMap {
			if details.rank >= 4096 {
				continue
			}
			merged := false
			for i := range evoRanks {
				if evoRanks[i].CP == details.cp && evoRanks[i].Rank == details.rank {
					evoRanks[i].Caps = append(evoRanks[i].Caps, cap)
					merged = true
					break
				}
			}
			if !merged {
				evoRanks = append(evoRanks, LeagueRank{Rank: details.rank, CP: details.cp, Caps: []int{cap}, Evolution: evo})
			}
		}
		bestRanks = append(bestRanks, evoRanks...)
	}

	return bestRanks
}
```

- [ ] **Step 5: Fix the existing mega test**

The old `rankcalculator_test.go` test around line 107-116 asserts the `evolution:1` entry is dropped when `IncludeMegaEvolution:false`. That config field no longer exists and megas are no longer dropped. Replace that test's body so it asserts the mega entry now appears as its own evolution-tagged `BestRank` entry:
```go
	// (in the test that had IncludeMegaEvolution:false)
	cfg := &Config{LevelCaps: []int{50}}
	result := Calculate(pokemon, cfg)
	var sawBase, sawMega bool
	for _, lr := range result.BestRank[1500] {
		if lr.Evolution == 0 && lr.Rank == 10 {
			sawBase = true
		}
		if lr.Evolution == 1 && lr.Rank == 5 {
			sawMega = true
		}
	}
	if !sawBase || !sawMega {
		t.Fatalf("expected base(evo0,rank10) and mega(evo1,rank5) entries; base=%v mega=%v", sawBase, sawMega)
	}
```
Remove any remaining references to `IncludeMegaEvolution:` in the test file.

- [ ] **Step 6: Run the pvp tests**

Run: `cd processor && go test -count=1 ./internal/pvp/ -v`
Expected: PASS (new test + adjusted test + existing EvolutionData tests).

- [ ] **Step 7: Commit**

```bash
git add processor/internal/pvp/rankcalculator.go processor/internal/pvp/rankcalculator_test.go
git commit -m "pvp: carry evolution through best-rank; stop dropping mega entries"
```

---

## Task 3: Monster tracking struct + SQL

**Files:**
- Modify: `processor/internal/db/monsters.go` (struct + `LoadMonsters`)
- Modify: `processor/internal/db/tracking_queries.go` (`MonsterTrackingAPI` + 4 SQL statements)
- Test: `processor/internal/db/monsters_test.go`

- [ ] **Step 1: Write the failing test**

Add to `processor/internal/db/monsters_test.go`:
```go
func TestBuildMonsterIndex_CarriesEvolution(t *testing.T) {
	rules := []MonsterTracking{
		{ID: "u1", PokemonID: 6, PVPRankingLeague: 1500, PVPRankingEvolution: 2},
	}
	idx := BuildMonsterIndexFromRules(rules)
	got := idx.PVPSpecific[1500]
	if len(got) != 1 || got[0].PVPRankingEvolution != 2 {
		t.Fatalf("expected one PVP rule with PVPRankingEvolution=2, got %#v", got)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd processor && go test ./internal/db/ -run TestBuildMonsterIndex_CarriesEvolution -v`
Expected: FAIL — `MonsterTracking` has no field `PVPRankingEvolution` (compile error).

- [ ] **Step 3: Add the struct field (in-memory rule)**

In `processor/internal/db/monsters.go`, in `MonsterTracking`, add after `PVPRankingCap` (line 44):
```go
	PVPRankingEvolution   int      `db:"pvp_ranking_evolution"`
```

- [ ] **Step 4: Add the column to `LoadMonsters`**

In `processor/internal/db/monsters.go`, `LoadMonsters` SELECT (lines 92-101), change the pvp line to include the new column:
```go
		        pvp_ranking_min_cp, pvp_ranking_cap, pvp_ranking_evolution,
```

- [ ] **Step 5: Add the API struct field**

In `processor/internal/db/tracking_queries.go`, in `MonsterTrackingAPI`, add after `PVPRankingCap` (line 400). No `diff` tag — like the other PVP fields, a differing evolution means a *separate* rule:
```go
	PVPRankingEvolution   int      `db:"pvp_ranking_evolution"   json:"pvp_ranking_evolution"`
```

- [ ] **Step 6: Add the column to all four monster CRUD statements**

In `processor/internal/db/tracking_queries.go`:

`SelectMonstersByIDProfile` (lines 416-417) and `SelectMonstersByID` (lines 740-741) — change the pvp line in each SELECT to:
```go
		        pvp_ranking_min_cp, pvp_ranking_cap, pvp_ranking_evolution,
```

`InsertMonster` (lines 438-439, 448, and the VALUES list line 440): add `pvp_ranking_evolution` to the column list, one more `?` to VALUES, and `m.PVPRankingEvolution` to the args. Concretely the columns become:
```go
		        pvp_ranking_league, pvp_ranking_best, pvp_ranking_worst,
		        pvp_ranking_min_cp, pvp_ranking_cap, pvp_ranking_evolution,
```
the VALUES line gains one `?` (36 total), and the args line 447-448 becomes:
```go
		m.PVPRankingLeague, m.PVPRankingBest, m.PVPRankingWorst,
		m.PVPRankingMinCP, m.PVPRankingCap, m.PVPRankingEvolution,
```

`UpdateMonsterByUID` (lines 468-469, 477-478): set clause becomes:
```go
		        pvp_ranking_league=?, pvp_ranking_best=?, pvp_ranking_worst=?,
		        pvp_ranking_min_cp=?, pvp_ranking_cap=?, pvp_ranking_evolution=?,
```
and the args line 477-478 becomes:
```go
		m.PVPRankingLeague, m.PVPRankingBest, m.PVPRankingWorst,
		m.PVPRankingMinCP, m.PVPRankingCap, m.PVPRankingEvolution,
```

- [ ] **Step 7: Run the test and build**

Run: `cd processor && go test ./internal/db/ -run TestBuildMonsterIndex_CarriesEvolution -v && go build ./...`
Expected: PASS + build OK.

- [ ] **Step 8: Commit**

```bash
git add processor/internal/db/monsters.go processor/internal/db/tracking_queries.go processor/internal/db/monsters_test.go
git commit -m "db: thread pvp_ranking_evolution through monster rule struct + CRUD"
```

---

## Task 4: Matcher evolution discriminator

**Files:**
- Modify: `processor/internal/matching/pokemon.go` (`PokemonMatcher` struct + `matchMonsters`)
- Modify: `processor/cmd/processor/main.go` (wiring)
- Test: `processor/internal/matching/pokemon_test.go`

- [ ] **Step 1: Write the failing tests**

Add to `processor/internal/matching/pokemon_test.go` (helpers `buildState`/`makeProcessedPokemon` may already exist in that file — reuse the file's existing test scaffolding for constructing a `*state.State` with monster rules and a `*ProcessedPokemon`; if a constructor differs, mirror the closest existing matcher test). The four behaviors:
```go
func TestMatch_Evolution_BaseRuleSkipsMega_FlagOff(t *testing.T) {
	m := &PokemonMatcher{PVPQueryMaxRank: 100, IncludeMegaEvolution: false}
	// rule: charizard great, base-only (PVPRankingEvolution: 0)
	// pokemon PVPBestRank[1500] = [{Rank:5, Evolution:2}]  (only a Mega X qualifies)
	// expect: NO match (base-only rule, flag off, only mega available)
	got := runEvolutionMatch(t, m, 0 /*ruleEvo*/, []pvp.LeagueRank{{Rank: 5, CP: 1480, Evolution: 2, Caps: []int{50}}})
	if len(got) != 0 {
		t.Fatalf("base-only rule should not match a mega entry with flag off; got %d", len(got))
	}
}

func TestMatch_Evolution_BaseRuleMatchesMega_FlagOn(t *testing.T) {
	m := &PokemonMatcher{PVPQueryMaxRank: 100, IncludeMegaEvolution: true}
	got := runEvolutionMatch(t, m, 0, []pvp.LeagueRank{{Rank: 5, CP: 1480, Evolution: 2, Caps: []int{50}}})
	if len(got) != 1 {
		t.Fatalf("base-only rule should match mega when server flag on; got %d", len(got))
	}
}

func TestMatch_Evolution_MegaRuleMatchesAnyMegaNotBase(t *testing.T) {
	m := &PokemonMatcher{PVPQueryMaxRank: 100}
	// rule PVPRankingEvolution: 1 (any mega)
	base := runEvolutionMatch(t, m, 1, []pvp.LeagueRank{{Rank: 5, CP: 1480, Evolution: 0, Caps: []int{50}}})
	if len(base) != 0 {
		t.Fatalf("mega rule must not match base entry; got %d", len(base))
	}
	mega := runEvolutionMatch(t, m, 1, []pvp.LeagueRank{{Rank: 5, CP: 1480, Evolution: 3, Caps: []int{50}}})
	if len(mega) != 1 {
		t.Fatalf("mega rule must match a Mega Y entry; got %d", len(mega))
	}
}

func TestMatch_Evolution_MegaXRuleMatchesOnlyX(t *testing.T) {
	m := &PokemonMatcher{PVPQueryMaxRank: 100}
	x := runEvolutionMatch(t, m, 2, []pvp.LeagueRank{{Rank: 5, CP: 1480, Evolution: 2, Caps: []int{50}}})
	if len(x) != 1 {
		t.Fatalf("mega:x rule must match Mega X; got %d", len(x))
	}
	y := runEvolutionMatch(t, m, 2, []pvp.LeagueRank{{Rank: 5, CP: 1480, Evolution: 3, Caps: []int{50}}})
	if len(y) != 0 {
		t.Fatalf("mega:x rule must not match Mega Y; got %d", len(y))
	}
}
```
Add the shared helper at the bottom of the test file (construct one `charizard great` rule with the given evolution, one enabled human, and a processed pokemon whose `PVPBestRank[1500]` is the supplied slice):
```go
func runEvolutionMatch(t *testing.T, m *PokemonMatcher, ruleEvo int, ranks []pvp.LeagueRank) []webhook.MatchedUser {
	t.Helper()
	rules := []db.MonsterTracking{{
		ID: "u1", PokemonID: 6, PVPRankingLeague: 1500,
		PVPRankingBest: 1, PVPRankingWorst: 100, PVPRankingMinCP: 0, PVPRankingCap: 0,
		PVPRankingEvolution: ruleEvo, MinIV: -1, MaxIV: 100, MaxLevel: 100, MaxRarity: 6, MaxSize: 6, MaxCP: 9000,
	}}
	st := newMatcherState(t, rules) // existing helper in this test file; builds state.State with humans+geofence
	pk := &ProcessedPokemon{
		PokemonID: 6, Form: 178, IV: 100, Gender: 0,
		PVPBestRank: map[int][]pvp.LeagueRank{1500: ranks},
	}
	users, _ := m.Match(pk, st)
	return users
}
```
> If `newMatcherState`/`ProcessedPokemon` field names differ in the existing test file, adapt to the file's existing helpers — do not invent a new state constructor. The point is: one base-rule charizard PVP rule + one matchable human + a processed pokemon carrying `ranks`.

- [ ] **Step 2: Run tests to verify they fail**

Run: `cd processor && go test ./internal/matching/ -run TestMatch_Evolution -v`
Expected: FAIL — `PokemonMatcher` has no `IncludeMegaEvolution` field and `matchMonsters` ignores evolution.

- [ ] **Step 3: Add the matcher config field**

In `processor/internal/matching/pokemon.go`, `PokemonMatcher` struct (lines 96-101), add:
```go
	// IncludeMegaEvolution is the server default for rules with
	// PVPRankingEvolution == 0: when true, those rules also match mega entries.
	IncludeMegaEvolution bool
```

- [ ] **Step 4: Add the discriminator to `matchMonsters`**

In `processor/internal/matching/pokemon.go`, inside the `if league != 0 {` block (lines 194-207), add the evolution gate as the FIRST check, before the rank/cp/cap checks:
```go
		// PVP league filters
		if league != 0 {
			// Mega/temporary-evolution discriminator (parallel to the cap filter).
			switch {
			case monster.PVPRankingEvolution == 0:
				// "without mega": base only, unless the server default includes megas.
				if !m.IncludeMegaEvolution && leagueData.Evolution != 0 {
					continue
				}
			case monster.PVPRankingEvolution == 1:
				// any mega
				if leagueData.Evolution == 0 {
					continue
				}
			default:
				// specific mega (2 = Mega X, 3 = Mega Y)
				if leagueData.Evolution != monster.PVPRankingEvolution {
					continue
				}
			}
			if leagueData.Rank > monster.PVPRankingWorst {
				continue
			}
			if leagueData.Rank < monster.PVPRankingBest {
				continue
			}
			if leagueData.CP < monster.PVPRankingMinCP {
				continue
			}
			if monster.PVPRankingCap != 0 && len(leagueData.Caps) > 0 && !pvp.CapsContain(leagueData.Caps, monster.PVPRankingCap) {
				continue
			}
		}
```

- [ ] **Step 5: Wire the flag (move it from pvp.Config to the matcher)**

In `processor/cmd/processor/main.go`:
- In the `pvpCfg := &pvp.Config{...}` literal (around line 1222), **remove** the `IncludeMegaEvolution: cfg.PVP.IncludeMegaEvolution,` line (the field no longer exists on `pvp.Config`).
- In the `pokemonMatcher: &matching.PokemonMatcher{...}` literal (around line 1522), **add**:
```go
			IncludeMegaEvolution: cfg.PVP.IncludeMegaEvolution,
```
(`cfg.PVP.IncludeMegaEvolution` stays in `internal/config/config.go` — only its consumer moved.)

- [ ] **Step 6: Run matcher tests + build**

Run: `cd processor && go test ./internal/matching/ -run TestMatch_Evolution -v && go build ./...`
Expected: PASS + build OK.

- [ ] **Step 7: Commit**

```bash
git add processor/internal/matching/pokemon.go processor/cmd/processor/main.go processor/internal/matching/pokemon_test.go
git commit -m "matching: filter PVP matches by mega evolution; flag governs base-rule default"
```

---

## Task 5: `!track` mega keyword

**Files:**
- Modify: `processor/internal/bot/commands/track.go` (`pvpEntry`, param defs, `parsePVP`, insert)
- Modify: `processor/internal/i18n/locale/en.json` (keyword strings)
- Test: `processor/internal/bot/commands/track_test.go`

- [ ] **Step 1: Add keyword strings to en.json**

In `processor/internal/i18n/locale/en.json`, add (next to `"arg.prefix.cap"`):
```json
  "arg.mega": "mega",
  "arg.prefix.mega": "mega",
```

- [ ] **Step 2: Write the failing test**

Add to `processor/internal/bot/commands/track_test.go` (mirror an existing `parsePVP`/Execute test in that file for setup; the assertion target is the resulting `pvpEntry`/insert):
```go
func TestParsePVP_MegaKeywords(t *testing.T) {
	cases := []struct {
		args string
		want int // expected PVPRankingEvolution
	}{
		{"great5", 0},
		{"great5 mega", 1},
		{"great5 mega:x", 2},
		{"great5 mega:y", 3},
	}
	for _, tc := range cases {
		parsed := parseTrackArgs(t, tc.args) // existing helper: runs the track param matcher over args
		entries := (&TrackCommand{}).parsePVP(testTrackCtx(t), parsed)
		if len(entries) != 1 {
			t.Fatalf("%q: expected 1 pvp entry, got %d", tc.args, len(entries))
		}
		if entries[0].Evolution != tc.want {
			t.Errorf("%q: Evolution = %d, want %d", tc.args, entries[0].Evolution, tc.want)
		}
	}
}
```
> Use the test file's existing helpers for building `*bot.ParsedArgs` and a `*bot.CommandContext`. If they have different names, adapt — the behavior under test is `parsePVP` returning the right `Evolution`.

- [ ] **Step 3: Run test to verify it fails**

Run: `cd processor && go test ./internal/bot/commands/ -run TestParsePVP_MegaKeywords -v`
Expected: FAIL — `pvpEntry` has no `Evolution`, and `mega` isn't parsed.

- [ ] **Step 4: Register the param definitions**

In `processor/internal/bot/commands/track.go`, in the `parameterDefinitions` slice next to the PVP league entries (around lines 277-286), add:
```go
		{Type: bot.ParamPrefixString, Key: "arg.prefix.mega"},
		{Type: bot.ParamKeyword, Key: "arg.mega"},
```

- [ ] **Step 5: Add `Evolution` to `pvpEntry` and resolve it in `parsePVP`**

In `processor/internal/bot/commands/track.go`, extend `pvpEntry` (lines 477-483):
```go
type pvpEntry struct {
	League    int // CP cap: 500, 1500, 2500
	Best      int
	Worst     int
	MinCP     int
	Cap       int
	Evolution int // 0 base, 1 any mega, 2 Mega X, 3 Mega Y
}
```
In `parsePVP`, after the `cap` resolution block (after line 501), resolve the mega mode once (applies to every league entry):
```go
	megaEvo := 0
	if v, ok := parsed.Strings["mega"]; ok {
		switch strings.ToLower(strings.TrimSpace(v)) {
		case "", "1":
			megaEvo = 1
		case "x":
			megaEvo = 2
		case "y":
			megaEvo = 3
		}
	}
	if megaEvo == 0 && parsed.HasKeyword("arg.mega") {
		megaEvo = 1
	}
```
Then set it on each entry — change the `entries = append(entries, pvpEntry{...})` literal to include:
```go
		entries = append(entries, pvpEntry{
			League:    l.cp,
			Best:      best,
			Worst:     worst,
			MinCP:     minCP,
			Cap:       cap,
			Evolution: megaEvo,
		})
```

- [ ] **Step 6: Persist it on the insert struct**

In `processor/internal/bot/commands/track.go`, in the insert-build loop (lines 185-189), add:
```go
				PVPRankingEvolution:   pe.Evolution,
```

- [ ] **Step 7: Run the test + build**

Run: `cd processor && go test ./internal/bot/commands/ -run TestParsePVP_MegaKeywords -v && go build ./...`
Expected: PASS + build OK.

- [ ] **Step 8: Commit**

```bash
git add processor/internal/bot/commands/track.go processor/internal/i18n/locale/en.json processor/internal/bot/commands/track_test.go
git commit -m "track: parse mega / mega:x / mega:y PVP discriminator"
```

---

## Task 6: Warn when `mega:x`/`mega:y` targets a species without that mega

**Files:**
- Modify: `processor/internal/bot/commands/track.go` (warning in `Execute`)
- Modify: `processor/internal/i18n/locale/en.json` (warn string)
- Test: `processor/internal/bot/commands/track_test.go`

- [ ] **Step 1: Add the warn string**

In `processor/internal/i18n/locale/en.json`, next to `"msg.track.invalid_cap"`:
```json
  "msg.track.no_mega_form": "⚠️ {0} has no Mega {1} form — this PVP rule will never match.",
```

- [ ] **Step 2: Write the failing test**

Add to `processor/internal/bot/commands/track_test.go`:
```go
func TestExecute_WarnsNoMegaForm(t *testing.T) {
	// venusaur (id 3) has only a single Mega (evo 1), no Mega X/Y.
	replies := runTrack(t, "venusaur great5 mega:x") // existing end-to-end track helper
	joined := repliesText(replies)
	if !strings.Contains(joined, "no Mega X") {
		t.Fatalf("expected a no-Mega-X warning, got: %q", joined)
	}
}
```
> Use the file's existing end-to-end track helper (`runTrack`/equivalent) and a text-joining helper. Charizard (`great5 mega:x`) must NOT warn; venusaur must.

- [ ] **Step 3: Run test to verify it fails**

Run: `cd processor && go test ./internal/bot/commands/ -run TestExecute_WarnsNoMegaForm -v`
Expected: FAIL — no warning emitted.

- [ ] **Step 4: Implement the warning**

In `processor/internal/bot/commands/track.go` `Execute`, after `monsterList` is resolved and `pvpEntries` computed, add a check (only for specific mega modes 2/3, using `ctx.GameData`):
```go
	// Warn if a specific mega form (mega:x / mega:y) was requested for a
	// species that has no such temporary evolution — the rule can't match.
	if specificEvo := specificMegaEvo(pvpEntries); specificEvo != 0 && ctx.GameData != nil {
		for _, mon := range monsterList {
			if !speciesHasTempEvo(ctx.GameData, mon.ID, specificEvo) {
				formLabel := "X"
				if specificEvo == 3 {
					formLabel = "Y"
				}
				name := ctx.GameData.MonsterName(mon.ID) // English name; use the resolver already used elsewhere in this file
				warnings = append(warnings, tr.Tf("msg.track.no_mega_form", name, formLabel))
			}
		}
	}
```
where `warnings` is the slice already appended to the confirmation (follow the existing `common.TemplateWarn`/warning pattern in this file — append to the same message tail). Add these helpers at the bottom of `track.go`:
```go
func specificMegaEvo(entries []pvpEntry) int {
	for _, e := range entries {
		if e.Evolution == 2 || e.Evolution == 3 {
			return e.Evolution
		}
	}
	return 0
}

func speciesHasTempEvo(gd *gamedata.GameData, pokemonID, tempEvoID int) bool {
	mon := gd.GetMonster(pokemonID, 0)
	if mon == nil {
		return false
	}
	for _, te := range mon.TempEvolutions {
		if te.TempEvoID == tempEvoID {
			return true
		}
	}
	return false
}
```
> Confirm the exact name resolver used in `track.go` for the English species name (the file already resolves names for confirmations) and reuse it instead of `MonsterName` if the signature differs. Import `gamedata` if not already imported.

- [ ] **Step 5: Run the test + build + vet**

Run: `cd processor && go test ./internal/bot/commands/ -run TestExecute_WarnsNoMegaForm -v && go build ./... && go vet ./...`
Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add processor/internal/bot/commands/track.go processor/internal/i18n/locale/en.json processor/internal/bot/commands/track_test.go
git commit -m "track: warn when mega:x/mega:y targets a species without that mega"
```

---

## Task 7: `!tracked` rowtext shows the mega mode

**Files:**
- Modify: `processor/internal/rowtext/monster.go`
- Modify: `processor/internal/i18n/locale/en.json`
- Test: `processor/internal/rowtext/rowtext_test.go`

- [ ] **Step 1: Add rowtext strings**

In `processor/internal/i18n/locale/en.json`, next to `"tracking.pvp_ranking"`:
```json
  "tracking.pvp_mega_any": "mega",
  "tracking.pvp_mega_x": "mega x",
  "tracking.pvp_mega_y": "mega y",
```

- [ ] **Step 2: Write the failing test**

Add to `processor/internal/rowtext/rowtext_test.go`:
```go
func TestMonsterRowText_MegaMode(t *testing.T) {
	g := newTestGenerator(t) // existing test helper in this package
	tr := newTestTranslator(t)
	m := &db.MonsterTracking{
		PokemonID: 6, PVPRankingLeague: 1500, PVPRankingBest: 1, PVPRankingWorst: 5,
		PVPRankingEvolution: 2,
	}
	got := g.MonsterRowText(tr, m)
	if !strings.Contains(got, "mega x") {
		t.Fatalf("expected rowtext to mention 'mega x', got: %q", got)
	}
}
```
> Use this package's existing test helpers for `Generator`/`Translator`. If none exist, mirror the closest existing test in `rowtext_test.go`.

- [ ] **Step 3: Run test to verify it fails**

Run: `cd processor && go test ./internal/rowtext/ -run TestMonsterRowText_MegaMode -v`
Expected: FAIL — rowtext doesn't render the mega mode.

- [ ] **Step 4: Render the mega mode**

In `processor/internal/rowtext/monster.go`, inside the `if monster.PVPRankingLeague != 0 {` block, after the `pvpString = fmt.Sprintf(...)` assignment (around line 60), append the mega suffix:
```go
		switch monster.PVPRankingEvolution {
		case 1:
			pvpString += " " + tr.T("tracking.pvp_mega_any")
		case 2:
			pvpString += " " + tr.T("tracking.pvp_mega_x")
		case 3:
			pvpString += " " + tr.T("tracking.pvp_mega_y")
		}
```

- [ ] **Step 5: Run the test**

Run: `cd processor && go test ./internal/rowtext/ -run TestMonsterRowText_MegaMode -v`
Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add processor/internal/rowtext/monster.go processor/internal/i18n/locale/en.json processor/internal/rowtext/rowtext_test.go
git commit -m "rowtext: show mega mode on PVP monster rules"
```

---

## Task 8: ReactMap / API field

**Files:**
- Modify: `processor/internal/api/trackingMonster.go` (request struct + persistence)
- Test: `processor/internal/api/tracking_test.go`

- [ ] **Step 1: Write the failing test**

Add to `processor/internal/api/tracking_test.go` (mirror an existing monster-POST test for harness setup):
```go
func TestCreateMonster_PersistsPVPRankingEvolution(t *testing.T) {
	body := `{"pokemon_id":6,"pvp_ranking_league":1500,"pvp_ranking_best":1,"pvp_ranking_worst":5,"pvp_ranking_evolution":2}`
	rec := postMonster(t, "u1", body) // existing helper that drives HandleCreateMonster with a mock/real store
	last := lastInsertedMonster(t)     // existing helper returning the db.MonsterTrackingAPI that was inserted
	if last.PVPRankingEvolution != 2 {
		t.Fatalf("PVPRankingEvolution = %d, want 2; resp=%s", last.PVPRankingEvolution, rec.Body.String())
	}
}
```
> Use the API test file's existing monster-POST scaffolding. If the file asserts via a fake store capturing inserts, assert on the captured struct's `PVPRankingEvolution`.

- [ ] **Step 2: Run test to verify it fails**

Run: `cd processor && go test ./internal/api/ -run TestCreateMonster_PersistsPVPRankingEvolution -v`
Expected: FAIL — request struct ignores `pvp_ranking_evolution`.

- [ ] **Step 3: Add the request field**

In `processor/internal/api/trackingMonster.go`, in `monsterInsertRequest` (after the other PVP fields), add:
```go
	PVPRankingEvolution   flexInt  `json:"pvp_ranking_evolution"`
```

- [ ] **Step 4: Persist it**

In `processor/internal/api/trackingMonster.go`, where the request is mapped into `db.MonsterTrackingAPI` (the `insert = append(insert, db.MonsterTrackingAPI{...})` block — find it by the existing `PVPRankingCap: ...` assignment), add:
```go
				PVPRankingEvolution:   req.PVPRankingEvolution.intValue(0),
```

- [ ] **Step 5: Run the test + build**

Run: `cd processor && go test ./internal/api/ -run TestCreateMonster_PersistsPVPRankingEvolution -v && go build ./...`
Expected: PASS + build OK.

- [ ] **Step 6: Commit**

```bash
git add processor/internal/api/trackingMonster.go processor/internal/api/tracking_test.go
git commit -m "api: accept + persist pvp_ranking_evolution for ReactMap parity"
```

> The GET responses already serialise `MonsterTrackingAPI` (which now has `json:"pvp_ranking_evolution"` from Task 3), so ReactMap reads the value back with no extra work.

---

## Task 9: Config docs + full pre-commit gate

**Files:**
- Modify: `config/config.example.toml`

- [ ] **Step 1: Update the config comment**

In `config/config.example.toml`, find `include_mega_evolution` in the `[pvp]` section and replace its comment with:
```toml
# include_mega_evolution - server default for PVP rules that do NOT specify a
# mega keyword (i.e. pvp_ranking_evolution = 0). false: those rules match base
# forms only; true: they also match mega/temporary-evolution ranks. Per-rule
# `mega` / `mega:x` / `mega:y` keywords always override this regardless of the
# flag.
include_mega_evolution = false
```
(If the key isn't present in the example file, add it under `[pvp]` with the comment above.)

- [ ] **Step 2: Run the full pre-commit gate**

Run:
```bash
cd processor && go build ./... && go vet ./... && go test -count=1 ./... && golangci-lint run ./...
```
Expected: all PASS, `golangci-lint` reports `0 issues`.

- [ ] **Step 3: Manual smoke (optional, needs DB + scanner)**

`!track charizard great5 mega:x` → confirm `!tracked` shows "greatpvp top5 (@0+) mega x"; a Charizard webhook whose Mega X is GL rank ≤5 fires; a base-only `!track charizard great5` does NOT fire on the same Mega-X-only encounter (with `include_mega_evolution=false`).

- [ ] **Step 4: Commit**

```bash
git add config/config.example.toml
git commit -m "config: document include_mega_evolution as the no-keyword PVP default"
```

---

## Self-review notes (verified against the spec)

- **Spec coverage:** evolution semantics (Task 2/4), per-rule column 0/1/2/3 (Task 1/3), `mega`/`mega:x`/`mega:y` keywords applying to all league filters — `megaEvo` is resolved once in `parsePVP` and written to every entry (Task 5); no-X/Y warning (Task 6); display `fullName`/expansion already mega-aware (no task needed — `enrichPvpRankings` unchanged); `!tracked` rowtext (Task 7); ReactMap API parity (Task 8); `include_mega_evolution` retained as default-governor, removed only from `pvp.Config`/`filterMega` (Task 2/4), documented (Task 9).
- **Type consistency:** `PVPRankingEvolution int` (db `MonsterTracking` + `MonsterTrackingAPI`), `LeagueRank.Evolution int`, `pvpEntry.Evolution int`, matcher reads `monster.PVPRankingEvolution` + `leagueData.Evolution` + `m.IncludeMegaEvolution`, API `req.PVPRankingEvolution.intValue(0)` — all consistent.
- **No "base + mega in one rule":** intentional (use two rules); not a gap.
- **Adapt-to-existing-helpers caveats** are flagged where the test scaffolding name may differ; the *behavior* under test is fully specified in each case.
