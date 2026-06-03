package api

import (
	"github.com/danielgtaylor/huma/v2"

	"github.com/pokemon/poracleng/processor/internal/db"
	"github.com/pokemon/poracleng/processor/internal/i18n"
	"github.com/pokemon/poracleng/processor/internal/store"
)

// v2NestRule is the strict v2 nest tracking request/response rule object.
//
// All filter fields are optional POINTERS (omitted ⇒ documented default via
// valueOr); there is no required field. No enums. Defaults come from the field
// audit nest table. ping is server-managed (not a caller input).
type v2NestRule struct {
	PokemonID   *int `json:"pokemon_id,omitempty" doc:"Pokédex id; 0 = any (default 0)"`
	Form        *int `json:"form,omitempty" doc:"Form id; 0 = any (default 0)"`
	MinSpawnAvg *int `json:"min_spawn_avg,omitempty" doc:"Minimum spawn average to alert on (default 0)"`

	// Common fields.
	Distance *int    `json:"distance,omitempty" doc:"Radius in metres; 0 = use the profile's areas (default 0)"`
	Template *string `json:"template,omitempty" doc:"Template name; empty = server default"`
	Clean    *bool   `json:"clean,omitempty" doc:"Auto-delete the alert on expiry (default false)"`
	Edit     *bool   `json:"edit,omitempty" doc:"Keep the message updated in place (default false)"`
	Summary  *bool   `json:"summary,omitempty" doc:"Route into the summary digest (default false)"`

	OverrideLocationLabel *string  `json:"override_location_label,omitempty" doc:"Saved-location label to use instead of the profile location (requires distance > 0; mutually exclusive with override_areas)"`
	OverrideAreas         []string `json:"override_areas,omitempty" doc:"Restrict this rule to these geofence areas (mutually exclusive with distance > 0 and override_location_label)"`
}

// translateV2Nest converts a strict v2 nest rule into the stored NestTrackingAPI,
// applying documented defaults, the clean bitmask, profile, and
// validated/normalized override fields. ping is always stored "" (server-managed).
func translateV2Nest(deps *TrackingDeps, humanID string, profileNo int, oc overrideContext, req *v2NestRule) (db.NestTrackingAPI, error) {
	distance := valueOr(req.Distance, 0)
	const maxDistance = 40000000 // Earth circumference (metres)
	if distance > maxDistance {
		distance = maxDistance
	}

	overrideLabel := valueOr(req.OverrideLocationLabel, "")
	if msg, code := validateOverrideFields(deps, oc, humanID, overrideLabel, req.OverrideAreas, distance); msg != "" {
		return db.NestTrackingAPI{}, humaErr(code, msg)
	}

	row := db.NestTrackingAPI{
		ID:                    humanID,
		ProfileNo:             profileNo,
		Ping:                  "", // server-managed
		Template:              valueOr(req.Template, ""),
		Distance:              distance,
		PokemonID:             valueOr(req.PokemonID, 0),
		MinSpawnAvg:           valueOr(req.MinSpawnAvg, 0),
		Form:                  valueOr(req.Form, 0),
		Clean:                 packClean(valueOr(req.Clean, false), valueOr(req.Edit, false), valueOr(req.Summary, false)),
		OverrideLocationLabel: overrideLabel,
		OverrideAreas:         normalizeOverrideAreas(req.OverrideAreas),
	}
	return row, nil
}

// nestRowToRule converts a stored NestTrackingAPI back into the strict v2 rule
// shape for responses.
func nestRowToRule(row *db.NestTrackingAPI) v2NestRule {
	clean := db.IsClean(row.Clean)
	edit := db.IsEdit(row.Clean)
	summary := db.IsSummary(row.Clean)
	return v2NestRule{
		PokemonID:             ptr(row.PokemonID),
		Form:                  ptr(row.Form),
		MinSpawnAvg:           ptr(row.MinSpawnAvg),
		Distance:              ptr(row.Distance),
		Template:              ptr(row.Template),
		Clean:                 ptr(clean),
		Edit:                  ptr(edit),
		Summary:               ptr(summary),
		OverrideLocationLabel: ptr(row.OverrideLocationLabel),
		OverrideAreas:         row.OverrideAreas,
	}
}

// RegisterV2TrackingNest registers the strict v2 nest tracking endpoints via the
// generic resource helpers.
func RegisterV2TrackingNest(api huma.API, deps *TrackingDeps) {
	registerV2Tracking(api, deps, v2TrackingType[v2NestRule, db.NestTrackingAPI]{
		Name: "nest",
		Store: func(d *TrackingDeps) store.TrackingStore[db.NestTrackingAPI] {
			return d.Tracking.Nests
		},
		Translate: translateV2Nest,
		ToRule:    nestRowToRule,
		GetUID:    store.NestGetUID,
		SetUID:    store.NestSetUID,
		RowText: func(d *TrackingDeps, tr *i18n.Translator, row *db.NestTrackingAPI) string {
			return d.RowText.NestRowText(tr, toNestTracking(row))
		},
	})
}
