package api

import (
	"github.com/danielgtaylor/huma/v2"

	"github.com/pokemon/poracleng/processor/internal/db"
	"github.com/pokemon/poracleng/processor/internal/i18n"
	"github.com/pokemon/poracleng/processor/internal/store"
)

// v2MaxbattleRule is the strict v2 maxbattle tracking request/response rule
// object.
//
// All filter fields are optional POINTERS (omitted ⇒ documented default via
// valueOr); there is no required field. The track-by-level rule (level >= 1 when
// pokemon_id == 9000) is validated explicitly in translateV2Maxbattle, matching
// v1. gmax is a BOOL on the wire, stored as 0/1. station_id is a nullable
// string. ping is server-managed (not a caller input).
//
// pokemon_id, level, move, evolution all default to 9000 (the "any / by level"
// sentinel the engine uses), per the field audit maxbattle table.
type v2MaxbattleRule struct {
	PokemonID *int    `json:"pokemon_id,omitempty" doc:"Pokédex id; 9000 = track by level (default 9000)"`
	Level     *int    `json:"level,omitempty" doc:"Max battle level; 9000 = any. Required (>= 1) when pokemon_id is 9000 (default 9000)"`
	Form      *int    `json:"form,omitempty" doc:"Form id; 0 = any (default 0)"`
	Move      *int    `json:"move,omitempty" doc:"Charge move id; 9000 = any (default 9000)"`
	Gmax      *bool   `json:"gmax,omitempty" doc:"Gigantamax only (default false)"`
	Evolution *int    `json:"evolution,omitempty" doc:"Evolution id; 9000 = any (default 9000)"`
	StationID *string `json:"station_id,omitempty" doc:"Restrict to a specific station id (default none)"`

	// Common fields.
	Distance *int    `json:"distance,omitempty" doc:"Radius in metres; 0 = use the profile's areas (default 0)"`
	Template *string `json:"template,omitempty" doc:"Template name; empty = server default"`
	Clean    *bool   `json:"clean,omitempty" doc:"Auto-delete the alert on expiry (default false)"`
	Edit     *bool   `json:"edit,omitempty" doc:"Keep the message updated in place (default false)"`
	Summary  *bool   `json:"summary,omitempty" doc:"Route into the summary digest (default false)"`

	OverrideLocationLabel *string  `json:"override_location_label,omitempty" doc:"Saved-location label to use instead of the profile location (requires distance > 0; mutually exclusive with override_areas)"`
	OverrideAreas         []string `json:"override_areas,omitempty" doc:"Restrict this rule to these geofence areas (mutually exclusive with distance > 0 and override_location_label)"`
}

// translateV2Maxbattle converts a strict v2 maxbattle rule into the stored
// MaxbattleTrackingAPI, applying documented defaults, the track-by-level
// validation, gmax bool→int, the clean bitmask, profile, and
// validated/normalized override fields. ping is always stored "" (server-managed).
func translateV2Maxbattle(deps *TrackingDeps, humanID string, profileNo int, oc overrideContext, req *v2MaxbattleRule) (db.MaxbattleTrackingAPI, error) {
	pokemonID := valueOr(req.PokemonID, 9000)

	// v1 rule: when tracking by level (no pokemon), a concrete level >= 1 is
	// required; otherwise level is forced to the 9000 "any" sentinel.
	level := 9000
	if pokemonID == 9000 {
		level = valueOr(req.Level, 9000)
		if level < 1 {
			return db.MaxbattleTrackingAPI{}, huma.Error422UnprocessableEntity("Invalid level (must be specified if no pokemon_id)")
		}
	}

	distance := valueOr(req.Distance, 0)
	const maxDistance = 40000000 // Earth circumference (metres)
	if distance > maxDistance {
		distance = maxDistance
	}

	overrideLabel := valueOr(req.OverrideLocationLabel, "")
	if msg, code := validateOverrideFields(deps, oc, humanID, overrideLabel, req.OverrideAreas, distance); msg != "" {
		return db.MaxbattleTrackingAPI{}, humaErr(code, msg)
	}

	gmax := 0
	if valueOr(req.Gmax, false) {
		gmax = 1
	}

	var stationID *string
	if req.StationID != nil && *req.StationID != "" {
		stationID = req.StationID
	}

	row := db.MaxbattleTrackingAPI{
		ID:                    humanID,
		ProfileNo:             profileNo,
		Ping:                  "", // server-managed
		Template:              valueOr(req.Template, ""),
		Distance:              distance,
		PokemonID:             pokemonID,
		Form:                  valueOr(req.Form, 0),
		Level:                 level,
		Move:                  valueOr(req.Move, 9000),
		Gmax:                  gmax,
		Evolution:             valueOr(req.Evolution, 9000),
		StationID:             stationID,
		Clean:                 packClean(valueOr(req.Clean, false), valueOr(req.Edit, false), valueOr(req.Summary, false)),
		OverrideLocationLabel: overrideLabel,
		OverrideAreas:         normalizeOverrideAreas(req.OverrideAreas),
	}
	return row, nil
}

// maxbattleRowToRule converts a stored MaxbattleTrackingAPI back into the strict
// v2 rule shape for responses.
func maxbattleRowToRule(row *db.MaxbattleTrackingAPI) v2MaxbattleRule {
	clean := db.IsClean(row.Clean)
	edit := db.IsEdit(row.Clean)
	summary := db.IsSummary(row.Clean)
	gmax := row.Gmax != 0
	return v2MaxbattleRule{
		PokemonID:             ptr(row.PokemonID),
		Level:                 ptr(row.Level),
		Form:                  ptr(row.Form),
		Move:                  ptr(row.Move),
		Gmax:                  ptr(gmax),
		Evolution:             ptr(row.Evolution),
		StationID:             row.StationID,
		Distance:              ptr(row.Distance),
		Template:              ptr(row.Template),
		Clean:                 ptr(clean),
		Edit:                  ptr(edit),
		Summary:               ptr(summary),
		OverrideLocationLabel: ptr(row.OverrideLocationLabel),
		OverrideAreas:         row.OverrideAreas,
	}
}

// RegisterV2TrackingMaxbattle registers the strict v2 maxbattle tracking
// endpoints via the generic resource helpers.
//
// Note: MaxbattleTrackingAPI carries no diff:"match" tags, so the shared
// ApplyDiff path treats every candidate as a fresh insert (matching the v1
// "always inserts" behaviour) — the generic layer needs no special-casing.
func RegisterV2TrackingMaxbattle(api huma.API, deps *TrackingDeps) {
	registerV2Tracking(api, deps, v2TrackingType[v2MaxbattleRule, db.MaxbattleTrackingAPI]{
		Name: "maxbattle",
		Store: func(d *TrackingDeps) store.TrackingStore[db.MaxbattleTrackingAPI] {
			return d.Tracking.Maxbattles
		},
		Translate: translateV2Maxbattle,
		ToRule:    maxbattleRowToRule,
		GetUID:    store.MaxbattleGetUID,
		SetUID:    store.MaxbattleSetUID,
		RowText: func(d *TrackingDeps, tr *i18n.Translator, row *db.MaxbattleTrackingAPI) string {
			return d.RowText.MaxbattleRowText(tr, toMaxbattleTracking(row))
		},
	})
}
