package api

import (
	"github.com/danielgtaylor/huma/v2"

	"github.com/pokemon/poracleng/processor/internal/db"
	"github.com/pokemon/poracleng/processor/internal/i18n"
	"github.com/pokemon/poracleng/processor/internal/store"
)

// v2LureRule is the strict v2 lure tracking request/response rule object.
//
// lure_id is REQUIRED (non-pointer) and must be one of the documented discrete
// set; it is a plain INT (a game-master dictionary value, not a string enum).
// The discrete set is validated explicitly in translateV2Lure because huma's
// min/max/enum can't express a sparse int set cleanly. Common fields are
// optional pointers (omitted ⇒ documented default via valueOr). ping is
// server-managed (not a caller input).
type v2LureRule struct {
	LureID int `json:"lure_id" required:"true" doc:"Lure id; one of 0 (any) | 501 | 502 | 503 | 504 | 505 | 506 (required)"`

	// Common fields.
	Distance *int    `json:"distance,omitempty" doc:"Radius in metres; 0 = use the profile's areas (default 0)"`
	Template *string `json:"template,omitempty" doc:"Template name; empty = server default"`
	Clean    *bool   `json:"clean,omitempty" doc:"Auto-delete the alert on expiry (default false)"`
	Edit     *bool   `json:"edit,omitempty" doc:"Keep the message updated in place (default false)"`
	Summary  *bool   `json:"summary,omitempty" doc:"Route into the summary digest (default false)"`

	OverrideLocationLabel *string  `json:"override_location_label,omitempty" doc:"Saved-location label to use instead of the profile location (requires distance > 0; mutually exclusive with override_areas)"`
	OverrideAreas         []string `json:"override_areas,omitempty" doc:"Restrict this rule to these geofence areas (mutually exclusive with distance > 0 and override_location_label)"`
}

// translateV2Lure converts a strict v2 lure rule into the stored LureTrackingAPI,
// applying documented defaults, the clean bitmask, profile, and
// validated/normalized override fields. It rejects an out-of-set lure_id with a
// 422 (matching the v1 handler's validLureIDs guard). ping is always stored ""
// (server-managed).
func translateV2Lure(deps *TrackingDeps, humanID string, profileNo int, oc overrideContext, req *v2LureRule) (db.LureTrackingAPI, error) {
	if !validLureIDs[req.LureID] {
		return db.LureTrackingAPI{}, huma.Error422UnprocessableEntity("Unrecognised lure_id value")
	}

	distance := valueOr(req.Distance, 0)
	const maxDistance = 40000000 // Earth circumference (metres)
	if distance > maxDistance {
		distance = maxDistance
	}

	overrideLabel := valueOr(req.OverrideLocationLabel, "")
	if msg, code := validateOverrideFields(deps, oc, humanID, overrideLabel, req.OverrideAreas, distance); msg != "" {
		return db.LureTrackingAPI{}, humaErr(code, msg)
	}

	row := db.LureTrackingAPI{
		ID:                    humanID,
		ProfileNo:             profileNo,
		Ping:                  "", // server-managed
		Template:              valueOr(req.Template, ""),
		Distance:              distance,
		LureID:                req.LureID,
		Clean:                 packClean(valueOr(req.Clean, false), valueOr(req.Edit, false), valueOr(req.Summary, false)),
		OverrideLocationLabel: overrideLabel,
		OverrideAreas:         normalizeOverrideAreas(req.OverrideAreas),
	}
	return row, nil
}

// lureRowToRule converts a stored LureTrackingAPI back into the strict v2 rule
// shape for responses.
func lureRowToRule(row *db.LureTrackingAPI) v2LureRule {
	clean := db.IsClean(row.Clean)
	edit := db.IsEdit(row.Clean)
	summary := db.IsSummary(row.Clean)
	return v2LureRule{
		LureID:                row.LureID,
		Distance:              ptr(row.Distance),
		Template:              ptr(row.Template),
		Clean:                 ptr(clean),
		Edit:                  ptr(edit),
		Summary:               ptr(summary),
		OverrideLocationLabel: ptr(row.OverrideLocationLabel),
		OverrideAreas:         row.OverrideAreas,
	}
}

// RegisterV2TrackingLure registers the strict v2 lure tracking endpoints via the
// generic resource helpers.
func RegisterV2TrackingLure(api huma.API, deps *TrackingDeps) {
	registerV2Tracking(api, deps, v2TrackingType[v2LureRule, db.LureTrackingAPI]{
		Name: "lure",
		Store: func(d *TrackingDeps) store.TrackingStore[db.LureTrackingAPI] {
			return d.Tracking.Lures
		},
		Translate: translateV2Lure,
		ToRule:    lureRowToRule,
		GetUID:    store.LureGetUID,
		SetUID:    store.LureSetUID,
		RowText: func(d *TrackingDeps, tr *i18n.Translator, row *db.LureTrackingAPI) string {
			return d.RowText.LureRowText(tr, toLureTracking(row))
		},
	})
}
