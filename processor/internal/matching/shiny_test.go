package matching

import (
	"testing"

	"github.com/pokemon/poracleng/processor/internal/db"
)

const testPID = "a25c79a30a4430d0e33ee918"

// Reference values computed with the original Node.js shinyDie and an
// independent Python port of java.util.Random.nextFloat.
func TestShinyRoll(t *testing.T) {
	if roll, ok := ShinyRoll("15624440781314219107", testPID, BaseShinyRate); !ok || roll != 0 {
		t.Fatalf("expected shiny roll 0, got %d (ok=%v)", roll, ok)
	}
	if roll, ok := ShinyRoll("12068743784565473006", testPID, BaseShinyRate); !ok || roll == 0 {
		t.Fatalf("expected non-shiny roll, got %d (ok=%v)", roll, ok)
	}
	if _, ok := ShinyRoll("not-a-number", testPID, BaseShinyRate); ok {
		t.Fatal("expected parse failure")
	}
}

// floor(nextFloat() * n) for several rates; 0 = shiny at 1/n.
func TestShinyRollRates(t *testing.T) {
	cases := []struct {
		enc  string
		want map[int]int
	}{
		{"15624440781314219107", map[int]int{512: 0, 128: 0, 64: 0, 25: 0, 20: 0}},
		{"12068743784565473006", map[int]int{512: 143, 128: 35, 64: 17, 25: 7, 20: 5}},
		{"5981960629525864751", map[int]int{512: 66, 128: 16, 64: 8, 25: 3, 20: 2}},
		{"1728372192399379054", map[int]int{512: 251, 128: 62, 64: 31, 25: 12, 20: 9}},
		{"2039119943687723724", map[int]int{512: 16, 128: 4, 64: 2, 25: 0, 20: 0}},
	}
	for _, c := range cases {
		for rate, want := range c.want {
			if got, ok := ShinyRoll(c.enc, testPID, rate); !ok || got != want {
				t.Errorf("enc %s rate %d: got %d, want %d", c.enc, rate, got, want)
			}
		}
	}
}

// Real scanner encounters from the 2026-10-10 Zorua CD (1/25) right at the
// boundary: nextInt(512) is 19/20 for all of them, only nextFloat() < 0.04 decides.
func TestShinyRollCommunityDayScanner(t *testing.T) {
	cases := []struct {
		pid, enc string
		shiny    bool
	}{
		{"552cf204e679cc4e", "7157440938125153327", true},   // 0.03911
		{"552cf204e679cc4e", "18415831828035568928", true},  // 0.03986
		{"552cf204e679cc4e", "361654323170777401", false},   // 0.04021
		{"6111e367f0632176", "13142585876352865694", false}, // 0.04024
		{"8d3c5ef6e7149166", "102664885078587792", true},    // 0.03871
		{"8d3c5ef6e7149166", "10089117709929588751", false}, // 0.04147
		{"6111e367f0632176", "6098666380074141916", true},   // 0.00204, CD only
	}
	for _, c := range cases {
		got, ok := ShinyRoll(c.enc, c.pid, 25)
		if !ok || (got == 0) != c.shiny {
			t.Errorf("enc %s pid %s: roll %d, want shiny=%v", c.enc, c.pid, got, c.shiny)
		}
	}
}

func TestFilterPersonalShiny(t *testing.T) {
	defer shinyReleased.Store(nil)
	SetShinyReleased([]int{25})
	humans := map[string]*db.Human{
		"linked":   {ID: "linked", ShinyPlayerID: testPID},
		"unlinked": {ID: "unlinked"},
	}
	rules := []*db.MonsterTracking{
		{ID: "linked", ShinyPredict: 1},
		{ID: "unlinked", ShinyPredict: 1},
		{ID: "unlinked", ShinyPredict: 0},
	}
	got := filterPersonalShiny("15624440781314219107", 25, 0, 0, append([]*db.MonsterTracking(nil), rules...), humans)
	if len(got) != 2 || got[0].ID != "linked" || got[1].ShinyPredict != 0 {
		t.Fatalf("shiny spawn: unexpected result %+v", got)
	}
	got = filterPersonalShiny("12068743784565473006", 25, 0, 0, append([]*db.MonsterTracking(nil), rules...), humans)
	if len(got) != 1 || got[0].ShinyPredict != 0 {
		t.Fatalf("non-shiny spawn: unexpected result %+v", got)
	}
}

// During a boost only the boosted die counts: a 1/512 hit is not shiny at 1/25.
func TestFilterPersonalShinyBoosted(t *testing.T) {
	defer shinyBoosted.Store(nil)
	defer shinyReleased.Store(nil)
	SetShinyReleased([]int{570, 16})
	humans := map[string]*db.Human{"linked": {ID: "linked", ShinyPlayerID: testPID}}
	rules := func() []*db.MonsterTracking { return []*db.MonsterTracking{{ID: "linked", ShinyPredict: 1}} }
	m := map[int]shinyBoost{570: {Rate: 25}}
	shinyBoosted.Store(&m)
	if got := filterPersonalShiny("2039119943687723724", 570, 0, 0, rules(), humans); len(got) != 1 {
		t.Fatalf("nextFloat()<1/25 should be shiny on CD, got %+v", got)
	}
	if got := filterPersonalShiny("12068743784565473006", 570, 0, 0, rules(), humans); len(got) != 0 {
		t.Fatalf("roll above 1/25 must not be shiny on CD, got %+v", got)
	}
	if got := filterPersonalShiny("3886801910080020890", 16, 0, 0, rules(), humans); len(got) != 1 {
		t.Fatalf("unboosted species keeps 1/512, got %+v", got)
	}

	// Event nur in Europa: in Lima gilt weiter 1/512
	m = map[int]shinyBoost{570: {Rate: 25, Regions: [][4]float64{{34, -25, 72, 45}}}}
	shinyBoosted.Store(&m)
	if got := filterPersonalShiny("2039119943687723724", 570, 48.8, 9.5, rules(), humans); len(got) != 1 {
		t.Fatalf("event region: nextFloat()<1/25 should be shiny, got %+v", got)
	}
	if got := filterPersonalShiny("2039119943687723724", 570, -12.05, -77.04, rules(), humans); len(got) != 0 {
		t.Fatalf("outside event region the CD roll must not count, got %+v", got)
	}
	if got := filterPersonalShiny("3886801910080020890", 570, -12.05, -77.04, rules(), humans); len(got) != 1 {
		t.Fatalf("outside event region 1/512 applies, got %+v", got)
	}
}

func TestFilterPersonalShinyReleased(t *testing.T) {
	defer shinyReleased.Store(nil)
	humans := map[string]*db.Human{"linked": {ID: "linked", ShinyPlayerID: testPID}}
	rules := func() []*db.MonsterTracking {
		return []*db.MonsterTracking{{ID: "linked", ShinyPredict: 1}, {ID: "linked", ShinyPredict: 0}}
	}
	SetShinyReleased([]int{25})
	if got := filterPersonalShiny("15624440781314219107", 25, 0, 0, rules(), humans); len(got) != 2 {
		t.Fatalf("released species: expected both rules, got %+v", got)
	}
	got := filterPersonalShiny("15624440781314219107", 931, 0, 0, rules(), humans)
	if len(got) != 1 || got[0].ShinyPredict != 0 {
		t.Fatalf("unreleased species: expected only the normal rule, got %+v", got)
	}
	// Liste unbekannt (Controller nicht erreichbar): keine persönlichen Shinys
	shinyReleased.Store(nil)
	got = filterPersonalShiny("15624440781314219107", 25, 0, 0, rules(), humans)
	if len(got) != 1 || got[0].ShinyPredict != 0 {
		t.Fatalf("unknown release list: expected only the normal rule, got %+v", got)
	}
}
