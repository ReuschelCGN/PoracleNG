package api

import (
	"github.com/danielgtaylor/huma/v2"

	"github.com/pokemon/poracleng/processor/internal/db"
	"github.com/pokemon/poracleng/processor/internal/i18n"
	"github.com/pokemon/poracleng/processor/internal/store"
)

// v2GymRule is the strict v2 gym tracking request/response rule object.
//
// team is a REQUIRED string enum (harmony|mystic|valor|instinct|any → 0|1|2|3|4),
// matching v1's gin handler which 400s when team is absent or out of range.
// It is a non-pointer field with required:"true" + the teamEnum string set, so
// an omitted or unknown team is rejected by huma's generated schema (422). The
// remaining filters are optional pointers (omitted ⇒ documented default via
// valueOr). gym has a clean column, so clean/edit/summary bools pack into the
// bitmask. ping is server-managed (not a caller input).
type v2GymRule struct {
	Team string `json:"team" required:"true" enum:"harmony,mystic,valor,instinct,any" doc:"Required team: harmony|mystic|valor|instinct|any (0|1|2|3|4)"`

	SlotChanges   *bool   `json:"slot_changes,omitempty" doc:"Alert on slot (defender count) changes (default false)"`
	BattleChanges *bool   `json:"battle_changes,omitempty" doc:"Alert on battle (in-progress) changes (default false)"`
	GymID         *string `json:"gym_id,omitempty" doc:"Restrict to a specific gym id; null/empty = any (default any)"`

	// Common fields.
	Distance *int    `json:"distance,omitempty" doc:"Radius in metres; 0 = use the profile's areas (default 0)"`
	Template *string `json:"template,omitempty" doc:"Template name; empty = server default"`
	Clean    *bool   `json:"clean,omitempty" doc:"Auto-delete the alert on expiry (default false)"`
	Edit     *bool   `json:"edit,omitempty" doc:"Keep the message updated in place (default false)"`
	Summary  *bool   `json:"summary,omitempty" doc:"Route into the summary digest (default false)"`

	OverrideLocationLabel *string  `json:"override_location_label,omitempty" doc:"Saved-location label to use instead of the profile location (requires distance > 0; mutually exclusive with override_areas)"`
	OverrideAreas         []string `json:"override_areas,omitempty" doc:"Restrict this rule to these geofence areas (mutually exclusive with distance > 0 and override_location_label)"`
}

// translateV2Gym converts a strict v2 gym rule into the stored GymTrackingAPI,
// applying the required team enum→int, documented defaults, the clean bitmask,
// profile, and validated/normalized override fields. ping is always stored ""
// (server-managed).
func translateV2Gym(deps *TrackingDeps, humanID string, profileNo int, oc overrideContext, req *v2GymRule) (db.GymTrackingAPI, error) {
	// team is required and enum-validated by huma; toStored is defence-in-depth.
	team, ok := teamEnum.toStored(req.Team)
	if !ok {
		return db.GymTrackingAPI{}, huma.Error422UnprocessableEntity("Invalid team: " + req.Team)
	}

	distance := valueOr(req.Distance, 0)
	const maxDistance = 40000000 // Earth circumference (metres)
	if distance > maxDistance {
		distance = maxDistance
	}

	overrideLabel := valueOr(req.OverrideLocationLabel, "")
	if msg, code := validateOverrideFields(deps, oc, humanID, overrideLabel, req.OverrideAreas, distance); msg != "" {
		return db.GymTrackingAPI{}, humaErr(code, msg)
	}

	var gymID *string
	if req.GymID != nil && *req.GymID != "" {
		gymID = req.GymID
	}

	row := db.GymTrackingAPI{
		ID:                    humanID,
		ProfileNo:             profileNo,
		Ping:                  "", // server-managed
		Template:              valueOr(req.Template, ""),
		Distance:              distance,
		Team:                  team,
		SlotChanges:           db.IntBool(valueOr(req.SlotChanges, false)),
		BattleChanges:         db.IntBool(valueOr(req.BattleChanges, false)),
		GymID:                 gymID,
		Clean:                 packClean(valueOr(req.Clean, false), valueOr(req.Edit, false), valueOr(req.Summary, false)),
		OverrideLocationLabel: overrideLabel,
		OverrideAreas:         normalizeOverrideAreas(req.OverrideAreas),
	}
	return row, nil
}

// gymRowToRule converts a stored GymTrackingAPI back into the strict v2 rule
// shape for responses.
func gymRowToRule(row *db.GymTrackingAPI) v2GymRule {
	clean := db.IsClean(row.Clean)
	edit := db.IsEdit(row.Clean)
	summary := db.IsSummary(row.Clean)
	slot := bool(row.SlotChanges)
	battle := bool(row.BattleChanges)
	return v2GymRule{
		Team:                  teamEnum.fromStored(row.Team),
		SlotChanges:           ptr(slot),
		BattleChanges:         ptr(battle),
		GymID:                 row.GymID,
		Distance:              ptr(row.Distance),
		Template:              ptr(row.Template),
		Clean:                 ptr(clean),
		Edit:                  ptr(edit),
		Summary:               ptr(summary),
		OverrideLocationLabel: ptr(row.OverrideLocationLabel),
		OverrideAreas:         row.OverrideAreas,
	}
}

// RegisterV2TrackingGym registers the strict v2 gym tracking endpoints via the
// generic resource helpers.
func RegisterV2TrackingGym(api huma.API, deps *TrackingDeps) {
	registerV2Tracking(api, deps, v2TrackingType[v2GymRule, db.GymTrackingAPI]{
		Name: "gym",
		Store: func(d *TrackingDeps) store.TrackingStore[db.GymTrackingAPI] {
			return d.Tracking.Gyms
		},
		Translate: translateV2Gym,
		ToRule:    gymRowToRule,
		GetUID:    store.GymGetUID,
		SetUID:    store.GymSetUID,
		RowText: func(d *TrackingDeps, tr *i18n.Translator, row *db.GymTrackingAPI) string {
			return d.RowText.GymRowText(tr, toGymTracking(row))
		},
	})
}
