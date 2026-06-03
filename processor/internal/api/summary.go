package api

import (
	"encoding/json"
	"slices"

	"github.com/pokemon/poracleng/processor/internal/store"
)

// SummaryDeps groups the dependencies for the /api/summaries endpoints.
// Kept narrow on purpose so tests can swap in mocks without dragging the
// full TrackingDeps surface in.
type SummaryDeps struct {
	// Schedules backs Get/Set/Delete/ListByType. nil disables CRUD —
	// handlers respond 503 so callers know the feature is off rather
	// than 404 (which would be misleading).
	Schedules store.SummaryScheduleStore
	// Dispatch is invoked synchronously by the trigger endpoint to flush
	// the buffer for (humanID, alertType). nil disables the trigger
	// endpoint with a 503.
	Dispatch func(humanID, alertType string)
	// ReloadFunc is called after Set / Delete so an in-flight scheduler
	// tick picks up the change without waiting for the next periodic
	// reload. nil is tolerated.
	ReloadFunc func()
}

// summarySetRequest is the POST body shape. We accept either a stringified
// JSON value or an arbitrary structure; both flow through json.Marshal so
// the schedule store always sees a canonical JSON-encoded string.
type summarySetRequest struct {
	ActiveHours any `json:"active_hours"`
}

// summaryScheduleResponse is the JSON shape returned by GET endpoints.
// We keep it stable independent of the SummarySchedule struct so adding
// internal fields doesn't leak through to API consumers.
type summaryScheduleResponse struct {
	ID          string          `json:"id"`
	AlertType   string          `json:"alert_type"`
	ActiveHours json.RawMessage `json:"active_hours"`
}

func toSummaryResponse(s *store.SummarySchedule) summaryScheduleResponse {
	hours := json.RawMessage(s.ActiveHours)
	if len(hours) == 0 {
		hours = json.RawMessage("[]")
	}
	return summaryScheduleResponse{
		ID:          s.ID,
		AlertType:   s.AlertType,
		ActiveHours: hours,
	}
}

// knownSummaryAlertTypes lists the alert types that have a summary
// renderer wired in DispatchQuestSummary. The list-for-user endpoint
// iterates these so we don't need a new "list by id" store method.
var knownSummaryAlertTypes = []string{"quest"}

// isKnownSummaryAlertType is the membership test used by every handler
// that accepts an alertType path parameter. Returning a 400 for unknown
// values lets clients building tooling get a clear signal instead of a
// silent 200-with-no-effect (DispatchQuestSummary itself no-ops on
// unknown alert types).
func isKnownSummaryAlertType(t string) bool {
	return slices.Contains(knownSummaryAlertTypes, t)
}
