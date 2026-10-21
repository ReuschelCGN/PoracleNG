package matching

import (
	"testing"

	"github.com/pokemon/poracleng/processor/internal/db"
)

const testPID = "0123456789abcdef"

// Reference values computed with the original Node.js shinyDie and an
// independent Python port of java.util.Random.nextFloat.
func TestShinyRoll(t *testing.T) {
	if roll, ok := ShinyRoll("2633", testPID, BaseShinyRate); !ok || roll != 0 {
		t.Fatalf("expected shiny roll 0, got %d (ok=%v)", roll, ok)
	}
	if roll, ok := ShinyRoll("1", testPID, BaseShinyRate); !ok || roll == 0 {
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
		{"2633", map[int]int{512: 0, 128: 0, 64: 0, 25: 0, 20: 0}},
		{"1", map[int]int{512: 113, 128: 28, 64: 14, 25: 5, 20: 4}},
		{"2", map[int]int{512: 219, 128: 54, 64: 27, 25: 10, 20: 8}},
		{"3", map[int]int{512: 420, 128: 105, 64: 52, 25: 20, 20: 16}},
		{"15", map[int]int{512: 2, 128: 0, 64: 0, 25: 0, 20: 0}},
	}
	for _, c := range cases {
		for rate, want := range c.want {
			if got, ok := ShinyRoll(c.enc, testPID, rate); !ok || got != want {
				t.Errorf("enc %s rate %d: got %d, want %d", c.enc, rate, got, want)
			}
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
	got := filterPersonalShiny("2633", 25, 0, 0, append([]*db.MonsterTracking(nil), rules...), humans)
	if len(got) != 2 || got[0].ID != "linked" || got[1].ShinyPredict != 0 {
		t.Fatalf("shiny spawn: unexpected result %+v", got)
	}
	got = filterPersonalShiny("1", 25, 0, 0, append([]*db.MonsterTracking(nil), rules...), humans)
	if len(got) != 1 || got[0].ShinyPredict != 0 {
		t.Fatalf("non-shiny spawn: unexpected result %+v", got)
	}
}

// During a boost the configured probability applies; species outside its region retain 1/512.
func TestFilterPersonalShinyBoosted(t *testing.T) {
	defer shinyBoosted.Store(nil)
	defer shinyReleased.Store(nil)
	SetShinyReleased([]int{570, 16})
	humans := map[string]*db.Human{"linked": {ID: "linked", ShinyPlayerID: testPID}}
	rules := func() []*db.MonsterTracking { return []*db.MonsterTracking{{ID: "linked", ShinyPredict: 1}} }
	m := map[int]shinyBoost{570: {Rate: 25}}
	shinyBoosted.Store(&m)
	if got := filterPersonalShiny("15", 570, 0, 0, rules(), humans); len(got) != 1 {
		t.Fatalf("nextFloat()<1/25 should be shiny on CD, got %+v", got)
	}
	if got := filterPersonalShiny("1", 570, 0, 0, rules(), humans); len(got) != 0 {
		t.Fatalf("roll above 1/25 must not be shiny on CD, got %+v", got)
	}
	if got := filterPersonalShiny("2633", 16, 0, 0, rules(), humans); len(got) != 1 {
		t.Fatalf("unboosted species keeps 1/512, got %+v", got)
	}

	// Event nur in Europa: in Lima gilt weiter 1/512
	m = map[int]shinyBoost{570: {Rate: 25, Regions: [][4]float64{{34, -25, 72, 45}}}}
	shinyBoosted.Store(&m)
	if got := filterPersonalShiny("15", 570, 48.8, 9.5, rules(), humans); len(got) != 1 {
		t.Fatalf("event region: nextFloat()<1/25 should be shiny, got %+v", got)
	}
	if got := filterPersonalShiny("15", 570, -12.05, -77.04, rules(), humans); len(got) != 0 {
		t.Fatalf("outside event region the CD roll must not count, got %+v", got)
	}
	if got := filterPersonalShiny("2633", 570, -12.05, -77.04, rules(), humans); len(got) != 1 {
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
	if got := filterPersonalShiny("2633", 25, 0, 0, rules(), humans); len(got) != 2 {
		t.Fatalf("released species: expected both rules, got %+v", got)
	}
	got := filterPersonalShiny("2633", 931, 0, 0, rules(), humans)
	if len(got) != 1 || got[0].ShinyPredict != 0 {
		t.Fatalf("unreleased species: expected only the normal rule, got %+v", got)
	}
	// Liste unbekannt (Controller nicht erreichbar): keine persönlichen Shinys
	shinyReleased.Store(nil)
	got = filterPersonalShiny("2633", 25, 0, 0, rules(), humans)
	if len(got) != 1 || got[0].ShinyPredict != 0 {
		t.Fatalf("unknown release list: expected only the normal rule, got %+v", got)
	}
}
