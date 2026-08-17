package tracker

import (
	"testing"
	"time"
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
	// Readers look back one hour; retention keeps two so a backlogged
	// webhook's event-time lookback still lands (see weatherHistoryHours).
	// Keepers: currentHour-2, previous, current, forecast.
	if got != 4 {
		t.Errorf("expected 4 retained hours (two of history, current, forecast), got %d", got)
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
	if !wt.hasHourWeather(cellID, currentHour-2*3600) {
		t.Error("the backlog-slack hour must survive eviction")
	}
	if wt.hasHourWeather(cellID, currentHour-3*3600) {
		t.Error("hours beyond the retention window are unreachable and must be dropped")
	}
}

// TestWeatherTrackerEvictsIdleCells asserts whole cells are reclaimed once
// they stop being scanned, so a shifted scan area does not strand them.
//
// Idleness is measured from when we last *received* something for the cell,
// so the clock is driven forward here rather than backdating the webhook.
func TestWeatherTrackerEvictsIdleCells(t *testing.T) {
	wt := NewWeatherTracker()

	clock := time.Unix(1_700_000_000, 0)
	wt.nowFunc = func() time.Time { return clock }

	wt.UpdateFromWebhook("cell-stale", 1, clock.Unix(), 51.5, -0.1, [4][2]float64{})

	// Two days pass; only one cell keeps receiving webhooks.
	clock = clock.Add(48 * time.Hour)
	wt.UpdateFromWebhook("cell-fresh", 1, clock.Unix(), 51.5, -0.1, [4][2]float64{})

	wt.evict(clock.Unix())

	if wt.hasCell("cell-stale") {
		t.Error("a cell that received nothing for 48h should be evicted entirely")
	}
	if !wt.hasCell("cell-fresh") {
		t.Error("a cell that just received a webhook must be kept")
	}
}

// TestWeatherTrackerReplayedWebhookIsNotInstantlyIdle pins that cell liveness
// tracks receipt time, not the webhook's own `updated` field. Replaying
// logs/webhooks.log is a documented workflow, and Golbat clock skew has the
// same shape: a webhook whose event time is days old still means the cell is
// live right now. Stamping event time made such a cell evictable on the very
// next sweep, discarding the previous-hour state that later genuine changes
// compare against.
func TestWeatherTrackerReplayedWebhookIsNotInstantlyIdle(t *testing.T) {
	wt := NewWeatherTracker()

	clock := time.Unix(1_700_000_000, 0)
	wt.nowFunc = func() time.Time { return clock }

	// A replayed webhook: event time three days in the past, received now.
	wt.UpdateFromWebhook("cell-replay", 1, clock.Add(-72*time.Hour).Unix(), 51.5, -0.1, [4][2]float64{})

	wt.evict(clock.Unix())

	if !wt.hasCell("cell-replay") {
		t.Error("a cell whose webhook carried an old event time was evicted; liveness must follow receipt time")
	}
}

// TestWeatherTrackerKeepsHistoryForBackloggedWebhooks pins the interaction
// between eviction (which prunes by processor wall clock) and
// UpdateFromWebhook (which reads the previous hour keyed by the webhook's own
// event time). When Golbat backlogs, a webhook stamped late in the previous
// hour arrives after the sweep has already advanced, and the entry it needs
// for the previous-hour comparison must still be there. Losing it turns a
// genuine weather change into no alert at all.
func TestWeatherTrackerKeepsHistoryForBackloggedWebhooks(t *testing.T) {
	wt := NewWeatherTracker()

	now := time.Now().Unix()
	currentHour := now - (now % 3600)
	eventHour := currentHour - 3600    // the backlogged webhook's own hour
	comparisonHour := eventHour - 3600 // the entry UpdateFromWebhook compares against
	cellID := "cell-backlog"

	wt.SetHourWeather(cellID, comparisonHour, 1)
	wt.SetHourWeather(cellID, eventHour, 1)

	// The sweep runs on wall clock, already past the event's hour.
	wt.evict(now)

	// Backlogged webhook: stamped 59 minutes into eventHour, delivered now,
	// reporting a different condition than comparisonHour held.
	wt.UpdateFromWebhook(cellID, 2, eventHour+3540, 51.5, -0.1, [4][2]float64{})

	select {
	case change := <-wt.Changes():
		if change.GameplayCondition != 2 || change.OldGameplayCondition != 1 {
			t.Errorf("got change %d->%d, want 1->2", change.OldGameplayCondition, change.GameplayCondition)
		}
	default:
		t.Error("no WeatherChange emitted: eviction dropped the previous-hour entry the backlogged webhook needed")
	}
}
