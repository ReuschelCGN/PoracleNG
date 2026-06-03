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
	PokemonID *int    `json:"pokemon_id,omitempty" doc:"Pokédex id of the raid boss. Omit to track by raid level rather than a specific boss (stored as 9000 = the project-wide 'any/track-by-level' sentinel). When set to a specific id, level is ignored."`
	Form      *int    `json:"form,omitempty" doc:"Form id (game-master). Omit to match any form (stored as 0 = any)."`
	Level     *int    `json:"level,omitempty" doc:"Raid tier. ONLY consulted when pokemon_id is omitted/9000 (track-by-level mode), where a concrete tier (1-6, or 90 = all tiers) is required. With a specific pokemon_id, level is ignored and stored as the 9000 sentinel ('level unused'). Single int — POST multiple rule objects for multiple tiers."`
	Team      *string `json:"team,omitempty" enum:"harmony,mystic,valor,instinct,any" doc:"Controlling team: harmony|mystic|valor|instinct|any (0|1|2|3|4). Omit to match any team (defaults to 'any', stored as 4)."`
	Exclusive *bool   `json:"exclusive,omitempty" doc:"Match EX-raids only. Omit to match regardless (default false)."`
	Move      *int    `json:"move,omitempty" doc:"Charge move id (game-master). Omit to match any move (stored as 9000 = the project-wide 'any' sentinel)."`
	Evolution *int    `json:"evolution,omitempty" doc:"Mega evolution id (game-master). Omit to match any evolution (stored as 9000 = the project-wide 'any' sentinel)."`
	GymID     *string `json:"gym_id,omitempty" doc:"Restrict to a specific gym id. Omit (or empty/null) to match any gym (stored as null)."`

	RSVPChanges *string `json:"rsvp_changes,omitempty" enum:"none,rsvp,rsvp_only" doc:"RSVP change handling: none|rsvp|rsvp_only (0|1|2). Omit to disable RSVP updates (defaults to 'none', stored as 0)."`

	// Common fields.
	Distance *int    `json:"distance,omitempty" doc:"Radius in metres around the anchor location. Omit (or 0) to match by the profile's geofence areas instead of a radius — 0 means area-based, NOT zero metres (stored as 0)."`
	Template *string `json:"template,omitempty" doc:"DTS template name. Omit (or empty) to use the server's configured default template (stored as \"\")."`
	Clean    *bool   `json:"clean,omitempty" doc:"Auto-delete the alert on expiry (clean bitmask bit 1). Omit to disable (default false)."`
	Edit     *bool   `json:"edit,omitempty" doc:"Keep the message updated in place (clean bitmask bit 2). Omit to disable (default false)."`
	Summary  *bool   `json:"summary,omitempty" doc:"Route into the summary digest (clean bitmask bit 4). Omit to disable (default false)."`

	OverrideLocationLabel *string  `json:"override_location_label,omitempty" doc:"Saved-location label to use instead of the profile location (requires distance > 0; mutually exclusive with override_areas). Omit for none."`
	OverrideAreas         []string `json:"override_areas,omitempty" doc:"Restrict this rule to these geofence areas (mutually exclusive with distance > 0 and override_location_label). Omit for none."`
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
