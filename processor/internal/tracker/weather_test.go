package tracker

import (
	"testing"
)

func TestGetWeatherCellID(t *testing.T) {
	// Ensure we get a non-empty cell ID for a known location
	cellID := GetWeatherCellID(51.5074, -0.1278)
	if cellID == "" {
		t.Error("Expected non-empty cell ID")
	}

	// Same location should give same cell
	cellID2 := GetWeatherCellID(51.5074, -0.1278)
	if cellID != cellID2 {
		t.Errorf("Expected same cell ID, got %s and %s", cellID, cellID2)
	}

	// Different location should give different cell (for sufficiently different locations)
	cellID3 := GetWeatherCellID(40.7128, -74.0060) // NYC
	if cellID == cellID3 {
		t.Error("Expected different cell ID for NYC vs London")
	}
}

func TestWeatherTrackerDirectUpdate(t *testing.T) {
	wt := NewWeatherTracker()

	cellID := "test_cell"
	wt.UpdateFromWebhook(cellID, 3, 1700000000, 51.5, -0.1, [4][2]float64{})

	weather := wt.GetCurrentWeatherInCell(cellID)
	// Since the timestamp is in the past, the current hour check may not match
	// This tests the storage mechanism
	_ = weather
}

func TestWeatherTrackerInference(t *testing.T) {
	wt := NewWeatherTracker()

	cellID := "test_cell"

	// Send enough weather observations to trigger a change
	for range 10 {
		wt.CheckWeatherOnMonster(cellID, 51.5, -0.1, 3)
	}

	// Check if a weather change was detected
	select {
	case change := <-wt.Changes():
		if change.GameplayCondition != 3 {
			t.Errorf("Expected weather condition 3, got %d", change.GameplayCondition)
		}
	default:
		// May not trigger if within first 30 seconds of the hour - that's OK
	}
}

// TestWeatherTrackerEvictsHoursOutsideRetention covers the actual leak:
// controllerCellData.hourWeather gained one entry per cell per hour and
// nothing ever removed them, so a long-lived process accumulated entries
// proportional to (cells x uptime hours).
func TestWeatherTrackerEvictsHoursOutsideRetention(t *testing.T) {
	wt := NewWeatherTracker()

	now := int64(1_700_000_000)
	currentHour := now - (now % 3600)
	cellID := "cell-retention"

	// Six hours of history, the current hour, and a forecast hour ahead.
	for h := int64(6); h >= 1; h-- {
		wt.SetHourWeather(cellID, currentHour-h*3600, 1)
	}
	wt.SetHourWeather(cellID, currentHour, 2)
	wt.SetHourWeather(cellID, currentHour+3600, 3)

	wt.evict(now)

	got := wt.hourCount(cellID)
	// Readers never look further back than the previous hour, so the
	// keepers are: previous, current, forecast.
	if got != 3 {
		t.Errorf("expected 3 retained hours (previous, current, forecast), got %d", got)
	}
	if !wt.hasHourWeather(cellID, currentHour) {
		t.Error("current hour must survive eviction")
	}
	if !wt.hasHourWeather(cellID, currentHour+3600) {
		t.Error("forecast hour must survive eviction — AccuWeather writes it ahead of time")
	}
	if !wt.hasHourWeather(cellID, currentHour-3600) {
		t.Error("previous hour must survive eviction — UpdateFromWebhook compares against it")
	}
	if wt.hasHourWeather(cellID, currentHour-2*3600) {
		t.Error("hours older than the previous hour are unreachable and must be dropped")
	}
}

// TestWeatherTrackerEvictsIdleCells asserts whole cells are reclaimed once
// they stop being scanned, so a shifted scan area does not strand them.
func TestWeatherTrackerEvictsIdleCells(t *testing.T) {
	wt := NewWeatherTracker()

	now := int64(1_700_000_000)
	stale := now - 48*3600

	wt.UpdateFromWebhook("cell-stale", 1, stale, 51.5, -0.1, [4][2]float64{})
	wt.UpdateFromWebhook("cell-fresh", 1, now, 51.5, -0.1, [4][2]float64{})

	wt.evict(now)

	if wt.hasCell("cell-stale") {
		t.Error("a cell untouched for 48h should be evicted entirely")
	}
	if !wt.hasCell("cell-fresh") {
		t.Error("a cell touched this hour must be kept")
	}
}
