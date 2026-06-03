package api

import (
	"github.com/danielgtaylor/huma/v2"
	"github.com/guregu/null/v6"

	"github.com/pokemon/poracleng/processor/internal/db"
	"github.com/pokemon/poracleng/processor/internal/i18n"
	"github.com/pokemon/poracleng/processor/internal/store"
)

// v2RaidRule is the strict v2 raid tracking request/response rule object.
//
// All filter fields are optional POINTERS (omitted ⇒ documented default via
// valueOr); there is no required field. team and rsvp_changes are STRING enums
// (teamEnum / rsvpChangesEnum), both stored as ints. gym_id is a nullable string
// (null/empty = any). ping is server-managed (not a caller input).
//
// v2 is STRICT and deliberately drops v1's array expansions: v1 accepted
// `level` as an int OR [int,...] (one row per level) and `pokemon_form` as
// [{pokemon_id,form}] (one row per pair). v2 models level/pokemon_id/form as
// SINGLE ints — a client wanting multiple levels/forms POSTs multiple rule
// objects (the create body is already an array). There is NO pokemon_form
// field and `level` is a plain int; the level-array and pokemon_form shapes are
// now unknown fields and 422 under additionalProperties:false / type checking.
//
// pokemon_id defaults to 9000, the engine sentinel for "track by level, not a
// specific pokemon" (consistent with the shipped maxbattle type). level/move/
// evolution also default to 9000 ("any"), per the field audit raid table.
type v2RaidRule struct {
	PokemonID *int    `json:"pokemon_id,omitempty" doc:"Pokédex id; 9000 = any (track by level) (default 9000)"`
	Form      *int    `json:"form,omitempty" doc:"Form id; 0 = any (default 0)"`
	Level     *int    `json:"level,omitempty" doc:"Raid level; 9000 = any. Single int — POST multiple rule objects for multiple levels (default 9000)"`
	Team      *string `json:"team,omitempty" enum:"harmony,mystic,valor,instinct,any" doc:"Team enum: harmony|mystic|valor|instinct|any (0|1|2|3|4) (default any)"`
	Exclusive *bool   `json:"exclusive,omitempty" doc:"EX-raid only (default false)"`
	Move      *int    `json:"move,omitempty" doc:"Charge move id; 9000 = any (default 9000)"`
	Evolution *int    `json:"evolution,omitempty" doc:"Mega evolution id; 9000 = any (default 9000)"`
	GymID     *string `json:"gym_id,omitempty" doc:"Restrict to a specific gym id; null/empty = any (default any)"`

	RSVPChanges *string `json:"rsvp_changes,omitempty" enum:"none,rsvp,rsvp_only" doc:"RSVP change handling: none|rsvp|rsvp_only (0|1|2) (default none)"`

	// Common fields.
	Distance *int    `json:"distance,omitempty" doc:"Radius in metres; 0 = use the profile's areas (default 0)"`
	Template *string `json:"template,omitempty" doc:"Template name; empty = server default"`
	Clean    *bool   `json:"clean,omitempty" doc:"Auto-delete the alert on expiry (default false)"`
	Edit     *bool   `json:"edit,omitempty" doc:"Keep the message updated in place (default false)"`
	Summary  *bool   `json:"summary,omitempty" doc:"Route into the summary digest (default false)"`

	OverrideLocationLabel *string  `json:"override_location_label,omitempty" doc:"Saved-location label to use instead of the profile location (requires distance > 0; mutually exclusive with override_areas)"`
	OverrideAreas         []string `json:"override_areas,omitempty" doc:"Restrict this rule to these geofence areas (mutually exclusive with distance > 0 and override_location_label)"`
}

// translateV2Raid converts a strict v2 raid rule into the stored
// RaidTrackingAPI, applying documented defaults, the team/rsvp enum→int
// translation, the clean bitmask, profile, and validated/normalized override
// fields. ping is always stored "" (server-managed).
func translateV2Raid(deps *TrackingDeps, humanID string, profileNo int, oc overrideContext, req *v2RaidRule) (db.RaidTrackingAPI, error) {
	// team/rsvp are enum-validated by huma; toStored/resolveStored is
	// defence-in-depth and applies the documented default for nil.
	team := teamEnum.resolveStored(req.Team)
	rsvp := rsvpChangesEnum.resolveStored(req.RSVPChanges)

	distance := valueOr(req.Distance, 0)
	const maxDistance = 40000000 // Earth circumference (metres)
	if distance > maxDistance {
		distance = maxDistance
	}

	overrideLabel := valueOr(req.OverrideLocationLabel, "")
	if msg, code := validateOverrideFields(deps, oc, humanID, overrideLabel, req.OverrideAreas, distance); msg != "" {
		return db.RaidTrackingAPI{}, humaErr(code, msg)
	}

	var gymID null.String
	if req.GymID != nil && *req.GymID != "" {
		gymID = null.StringFrom(*req.GymID)
	}

	row := db.RaidTrackingAPI{
		ID:                    humanID,
		ProfileNo:             profileNo,
		Ping:                  "", // server-managed
		Template:              valueOr(req.Template, ""),
		Distance:              distance,
		Team:                  team,
		PokemonID:             valueOr(req.PokemonID, 9000),
		Form:                  valueOr(req.Form, 0),
		Level:                 valueOr(req.Level, 9000),
		Exclusive:             db.IntBool(valueOr(req.Exclusive, false)),
		Move:                  valueOr(req.Move, 9000),
		Evolution:             valueOr(req.Evolution, 9000),
		GymID:                 gymID,
		RSVPChanges:           rsvp,
		Clean:                 packClean(valueOr(req.Clean, false), valueOr(req.Edit, false), valueOr(req.Summary, false)),
		OverrideLocationLabel: overrideLabel,
		OverrideAreas:         normalizeOverrideAreas(req.OverrideAreas),
	}
	return row, nil
}

// raidRowToRule converts a stored RaidTrackingAPI back into the strict v2 rule
// shape for responses.
func raidRowToRule(row *db.RaidTrackingAPI) v2RaidRule {
	clean := db.IsClean(row.Clean)
	edit := db.IsEdit(row.Clean)
	summary := db.IsSummary(row.Clean)
	exclusive := bool(row.Exclusive)
	team := teamEnum.fromStored(row.Team)
	rsvp := rsvpChangesEnum.fromStored(row.RSVPChanges)
	var gymID *string
	if row.GymID.Valid && row.GymID.String != "" {
		s := row.GymID.String
		gymID = &s
	}
	return v2RaidRule{
		PokemonID:             ptr(row.PokemonID),
		Form:                  ptr(row.Form),
		Level:                 ptr(row.Level),
		Team:                  ptr(team),
		Exclusive:             ptr(exclusive),
		Move:                  ptr(row.Move),
		Evolution:             ptr(row.Evolution),
		GymID:                 gymID,
		RSVPChanges:           ptr(rsvp),
		Distance:              ptr(row.Distance),
		Template:              ptr(row.Template),
		Clean:                 ptr(clean),
		Edit:                  ptr(edit),
		Summary:               ptr(summary),
		OverrideLocationLabel: ptr(row.OverrideLocationLabel),
		OverrideAreas:         row.OverrideAreas,
	}
}

// RegisterV2TrackingRaid registers the strict v2 raid tracking endpoints via the
// generic resource helpers.
func RegisterV2TrackingRaid(api huma.API, deps *TrackingDeps) {
	registerV2Tracking(api, deps, v2TrackingType[v2RaidRule, db.RaidTrackingAPI]{
		Name: "raid",
		Store: func(d *TrackingDeps) store.TrackingStore[db.RaidTrackingAPI] {
			return d.Tracking.Raids
		},
		Translate: translateV2Raid,
		ToRule:    raidRowToRule,
		GetUID:    store.RaidGetUID,
		SetUID:    store.RaidSetUID,
		RowText: func(d *TrackingDeps, tr *i18n.Translator, row *db.RaidTrackingAPI) string {
			return d.RowText.RaidRowText(tr, toRaidTracking(row))
		},
	})
}
