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
	PokemonID *int    `json:"pokemon_id,omitempty" nullable:"true" doc:"Pokédex id of the max boss. Omit to track by battle level rather than a specific boss (stored as 9000 = the project-wide 'any/track-by-level' sentinel). When set to a specific id, level is ignored (forced to the 9000 sentinel). Returned as null when at its wildcard."`
	Level     *int    `json:"level,omitempty" nullable:"true" doc:"Max battle tier. ONLY consulted when pokemon_id is omitted/9000 (track-by-level mode), where a concrete tier (>= 1, or 90 = all tiers) is required. With a specific pokemon_id, level is ignored and stored as the 9000 sentinel ('level unused'). Returned as null when at its wildcard."`
	Form      *int    `json:"form,omitempty" nullable:"true" doc:"Form id (game-master). Omit to match any form (stored as 0 = any). Returned as null when at its wildcard."`
	Move      *int    `json:"move,omitempty" nullable:"true" doc:"Charge move id (game-master). Omit to match any move (stored as 9000 = the project-wide 'any' sentinel). Returned as null when at its wildcard."`
	Gmax      *bool   `json:"gmax,omitempty" nullable:"true" doc:"Match Gigantamax only. Omit to match regardless (default false). Returned as null when false."`
	Evolution *int    `json:"evolution,omitempty" nullable:"true" doc:"Evolution id (game-master). Omit to match any evolution (stored as 9000 = the project-wide 'any' sentinel). Returned as null when at its wildcard."`
	StationID *string `json:"station_id,omitempty" nullable:"true" doc:"Restrict to a specific power-spot station id. Omit (or empty/null) to match any station (stored as null). Returned as null when unset."`

	// Common fields.
	Distance *int    `json:"distance,omitempty" nullable:"true" doc:"Radius in metres around the anchor location. Omit (or 0) to match by the profile's geofence areas instead of a radius — 0 means area-based, NOT zero metres (stored as 0). Returned as null when at its wildcard."`
	Template *string `json:"template,omitempty" nullable:"true" doc:"DTS template name. Omit (or empty) to use the server's configured default template (stored as \"\"). Returned as null when at its wildcard."`
	Clean    *bool   `json:"clean,omitempty" nullable:"true" doc:"Auto-delete the alert on expiry (clean bitmask bit 1). Omit to disable (default false). Returned as null when false."`
	Edit     *bool   `json:"edit,omitempty" nullable:"true" doc:"Keep the message updated in place (clean bitmask bit 2). Omit to disable (default false). Returned as null when false."`
	Summary  *bool   `json:"summary,omitempty" nullable:"true" doc:"Route into the summary digest (clean bitmask bit 4). Omit to disable (default false). Returned as null when false."`

	OverrideLocationLabel *string  `json:"override_location_label,omitempty" nullable:"true" doc:"Saved-location label to use instead of the profile location (requires distance > 0; mutually exclusive with override_areas). Omit for none. Returned as null when unset."`
	OverrideAreas         []string `json:"override_areas,omitempty" doc:"Restrict this rule to these geofence areas (mutually exclusive with distance > 0 and override_location_label). Omit for none. Returned as null when unset."`
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
	var stationID *string
	if row.StationID != nil && *row.StationID != "" {
		stationID = row.StationID
	}
	return v2MaxbattleRule{
		PokemonID:             ptrUnless(row.PokemonID, 9000),
		Level:                 ptrUnless(row.Level, 9000),
		Form:                  ptrUnless(row.Form, 0),
		Move:                  ptrUnless(row.Move, 9000),
		Gmax:                  ptrUnless(row.Gmax != 0, false),
		Evolution:             ptrUnless(row.Evolution, 9000),
		StationID:             stationID,
		Distance:              ptrUnless(row.Distance, 0),
		Template:              ptrUnless(row.Template, ""),
		Clean:                 ptrUnless(db.IsClean(row.Clean), false),
		Edit:                  ptrUnless(db.IsEdit(row.Clean), false),
		Summary:               ptrUnless(db.IsSummary(row.Clean), false),
		OverrideLocationLabel: ptrUnless(row.OverrideLocationLabel, ""),
		OverrideAreas:         ptrUnlessSlice(row.OverrideAreas),
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
