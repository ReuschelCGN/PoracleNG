package tracker

import (
	"testing"
	"time"
)

// TestWeatherTrackerNotifiesOnEvict asserts the sweep reports which cells it
// dropped, so components keyed by the same cell ids can release their own
// state instead of each re-deriving liveness.
func TestWeatherTrackerNotifiesOnEvict(t *testing.T) {
	clock := newTestClock(1_700_000_000)
	wt := NewWeatherTracker(WithClock(clock.now))
	defer wt.Close()

	var evicted []string
	wt.SetOnEvict(func(cellIDs []string) { evicted = append(evicted, cellIDs...) })

	wt.UpdateFromWebhook("cell-gone", 1, clock.now().Unix(), 51.5, -0.1, [4][2]float64{})

	clock.advance(48 * time.Hour)
	wt.UpdateFromWebhook("cell-live", 1, clock.now().Unix(), 51.5, -0.1, [4][2]float64{})
	wt.evict(clock.now().Unix())

	if len(evicted) != 1 || evicted[0] != "cell-gone" {
		t.Fatalf("onEvict got %v, want exactly [cell-gone]", evicted)
	}
}

// TestAccuWeatherForgetCells asserts the forecast client releases every
// per-cell map it owns. cellMutexes, cellLocations and cellForecasts share the
// WeatherTracker's keyspace but had no delete path at all, so a shifted scan
// area stranded one mutex, one location key and one forecastState per
// abandoned S2 cell for the life of the process.
func TestAccuWeatherForgetCells(t *testing.T) {
	wt := NewWeatherTracker()
	defer wt.Close()

	aw := NewAccuWeatherClient(AccuWeatherConfig{}, wt)

	aw.cellMutexes["cell-gone"] = nil
	aw.cellLocations["cell-gone"] = "12345"
	aw.cellForecasts["cell-gone"] = &forecastState{}
	aw.cellLocations["cell-live"] = "67890"

	aw.ForgetCells([]string{"cell-gone"})

	if _, ok := aw.cellMutexes["cell-gone"]; ok {
		t.Error("cellMutexes still holds the evicted cell")
	}
	if _, ok := aw.cellLocations["cell-gone"]; ok {
		t.Error("cellLocations still holds the evicted cell")
	}
	if _, ok := aw.cellForecasts["cell-gone"]; ok {
		t.Error("cellForecasts still holds the evicted cell")
	}
	if _, ok := aw.cellLocations["cell-live"]; !ok {
		t.Error("a live cell was dropped")
	}
}

// TestWeatherTrackerEvictReportsEachCellOnce pins that a cell holding both
// controller and local state is reported to onEvict exactly once, and that the
// dedup behind that stays linear in the number of cells dropped: a shifted
// scan area can drop tens of thousands in one sweep, all under wt.mu.
func TestWeatherTrackerEvictReportsEachCellOnce(t *testing.T) {
	clock := newTestClock(1_700_000_000)
	wt := NewWeatherTracker(WithClock(clock.now))
	defer wt.Close()

	const cells = 5000
	for i := range cells {
		id := encounterIDForTest(i)
		// Both maps, so the naive dedup would have to scan for each one.
		wt.UpdateFromWebhook(id, 1, clock.now().Unix(), 51.5, -0.1, [4][2]float64{})
		wt.CheckWeatherOnMonster(id, 51.5, -0.1, 3)
	}

	var dropped []string
	wt.SetOnEvict(func(ids []string) { dropped = append(dropped, ids...) })

	clock.advance(48 * time.Hour)
	wt.evict(clock.now().Unix())

	if len(dropped) != cells {
		t.Fatalf("onEvict reported %d cells, want %d", len(dropped), cells)
	}
	seen := make(map[string]struct{}, len(dropped))
	for _, id := range dropped {
		if _, dup := seen[id]; dup {
			t.Fatalf("cell %s reported more than once", id)
		}
		seen[id] = struct{}{}
	}
}

// BenchmarkWeatherEvictDropsManyCells covers the sweep that follows a
// scan-area shift, where one pass drops every cell it holds. The whole loop
// runs under wt.mu, so its cost is a stall on every webhook path.
func BenchmarkWeatherEvictDropsManyCells(b *testing.B) {
	const cells = 10000

	for b.Loop() {
		b.StopTimer()
		clock := newTestClock(1_700_000_000)
		wt := NewWeatherTracker(WithClock(clock.now))
		for i := range cells {
			id := encounterIDForTest(i)
			wt.UpdateFromWebhook(id, 1, clock.now().Unix(), 51.5, -0.1, [4][2]float64{})
			wt.CheckWeatherOnMonster(id, 51.5, -0.1, 3)
		}
		clock.advance(48 * time.Hour)
		b.StartTimer()

		wt.evict(clock.now().Unix())

		b.StopTimer()
		wt.Close()
		b.StartTimer()
	}
}
