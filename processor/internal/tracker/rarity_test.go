package tracker

import (
	"runtime"
	"testing"
)

func testStatsConfig() StatsConfig {
	return StatsConfig{
		MinSampleSize:       0, // no minimum for tests
		WindowHours:         24,
		RefreshIntervalMins: 60,
		Uncommon:            1.0,
		Rare:                0.5,
		VeryRare:            0.03,
		UltraRare:           0.01,
	}
}

func TestStatsTrackerRarity(t *testing.T) {
	st := NewStatsTracker(testStatsConfig())

	// Initially unknown
	group := st.GetRarityGroup(25)
	if group != RarityUnknown {
		t.Errorf("Expected unknown rarity, got %d", group)
	}

	// Record lots of sightings
	for range 1000 {
		st.RecordSighting(25, false, false) // Very common
	}
	for range 100 {
		st.RecordSighting(150, false, false) // Uncommon
	}
	for range 10 {
		st.RecordSighting(151, false, false) // Rare
	}
	st.RecordSighting(132, false, false) // Ultra rare

	// Force recalculation
	st.recalculate()

	// Pidgey should be common
	group = st.GetRarityGroup(25)
	if group != RarityCommon {
		t.Errorf("Expected common (1) for pokemon 25, got %d", group)
	}

	// Mewtwo should be rarer
	group = st.GetRarityGroup(132)
	if group < RarityRare {
		t.Errorf("Expected rare or rarer for pokemon 132, got %d", group)
	}
}

func TestStatsTrackerShiny(t *testing.T) {
	st := NewStatsTracker(testStatsConfig())

	// Record IV-scanned encounters with some shiny
	for i := range 200 {
		st.RecordSighting(25, true, i < 2) // 2 shiny out of 200
	}

	st.recalculate()

	rate := st.GetShinyRate(25)
	if rate == 0 {
		t.Error("Expected non-zero shiny rate")
	}
	// Expected ratio: 200/2 = 100
	if rate < 99 || rate > 101 {
		t.Errorf("Expected shiny rate ~100, got %f", rate)
	}

	stats := st.ExportShinyStats()
	if _, ok := stats[25]; !ok {
		t.Error("Expected pokemon 25 in shiny stats export")
	}
}

func TestStatsTrackerShinyMinEncounters(t *testing.T) {
	st := NewStatsTracker(testStatsConfig())

	// Record fewer than minIVSeenForShiny encounters
	for i := range 50 {
		st.RecordSighting(25, true, i == 0) // 1 shiny out of 50
	}

	st.recalculate()

	// Should not report shiny stats with too few encounters
	rate := st.GetShinyRate(25)
	if rate != 0 {
		t.Errorf("Expected zero shiny rate with too few encounters, got %f", rate)
	}
}

func TestStatsTrackerReset(t *testing.T) {
	st := NewStatsTracker(testStatsConfig())

	st.RecordSighting(25, false, false)
	st.recalculate()

	group := st.GetRarityGroup(25)
	if group == RarityUnknown {
		t.Error("Expected known rarity after recording")
	}

	st.Reset()
	group = st.GetRarityGroup(25)
	if group != RarityUnknown {
		t.Error("Expected unknown rarity after reset")
	}
}

// TestStatsTrackerMemoryIsIndependentOfSightingVolume pins the property that
// makes this tracker safe at scanner throughput: resident memory is a function
// of (species x time buckets), not of how many sightings were recorded.
//
// The per-sighting slice this replaced cost ~27.8 B per recorded sighting, so
// a busy install (2000 webhooks/sec over an 8h window) held ~1.6 GB here. The
// bucketed counters hold a bounded number of map entries no matter the volume.
func TestStatsTrackerMemoryIsIndependentOfSightingVolume(t *testing.T) {
	cfg := testStatsConfig()
	cfg.WindowHours = 8
	st := NewStatsTracker(cfg)

	const sightings = 2_000_000
	const species = 600

	runtime.GC()
	var before runtime.MemStats
	runtime.ReadMemStats(&before)

	for i := range sightings {
		st.RecordSighting(i%species, i%10 == 0, i%1000 == 0)
	}

	runtime.GC()
	var after runtime.MemStats
	runtime.ReadMemStats(&after)

	growth := int64(after.HeapAlloc) - int64(before.HeapAlloc)
	if growth < 0 {
		growth = 0
	}

	// Per-sighting storage would be ~55 MB for this volume. Bounded counters
	// stay far under; 30 MB leaves generous headroom for either the ring or
	// GC noise without letting a linear implementation through.
	const limit = 30 << 20
	if growth > limit {
		t.Errorf("recording %d sightings grew the heap by %.1f MB, want <= %d MB — memory is scaling with sighting count",
			sightings, float64(growth)/(1<<20), limit>>20)
	}
}

// TestStatsTrackerDropsSightingsOutsideWindow covers behaviour the slice-based
// tracker had but never tested: sightings older than window_hours stop counting.
func TestStatsTrackerDropsSightingsOutsideWindow(t *testing.T) {
	cfg := testStatsConfig()
	cfg.WindowHours = 1
	st := NewStatsTracker(cfg)

	now := int64(1_700_000_000)
	st.now = func() int64 { return now }

	for range 500 {
		st.RecordSighting(25, false, false)
	}
	st.recalculate()
	if got := st.GetRarityGroup(25); got == RarityUnknown {
		t.Fatal("expected a rarity group while the sightings are inside the window")
	}

	// Step past the window. Nothing new is recorded, so every bucket is stale.
	now += 2 * 3600
	st.recalculate()

	if got := st.GetRarityGroup(25); got != RarityUnknown {
		t.Errorf("expected RarityUnknown after the window elapsed, got %d", got)
	}
}

// TestStatsTrackerRingRecyclesStaleBuckets guards the wrap-around: a bucket
// reused a full window later must not carry its previous occupant's counts.
func TestStatsTrackerRingRecyclesStaleBuckets(t *testing.T) {
	cfg := testStatsConfig()
	cfg.WindowHours = 1
	st := NewStatsTracker(cfg)

	now := int64(1_700_000_000)
	st.now = func() int64 { return now }

	for range 300 {
		st.RecordSighting(25, false, false)
	}

	// Same ring slot, one full window later.
	now += int64(len(st.buckets)) * 60
	st.RecordSighting(150, false, false)
	st.recalculate()

	if got := st.GetRarityGroup(25); got != RarityUnknown {
		t.Errorf("pokemon 25 should have aged out of the ring, got group %d", got)
	}
	if got := st.GetRarityGroup(150); got == RarityUnknown {
		t.Error("pokemon 150 was just recorded and should have a group")
	}
}
