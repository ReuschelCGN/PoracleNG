package api

import (
	"github.com/danielgtaylor/huma/v2"

	"github.com/pokemon/poracleng/processor/internal/db"
	"github.com/pokemon/poracleng/processor/internal/i18n"
	"github.com/pokemon/poracleng/processor/internal/store"
)

// v2QuestRule is the strict v2 quest tracking request/response rule object.
//
// reward_type is REQUIRED (non-pointer) and must be one of the documented
// discrete set; it is a plain INT (a game-master/proto reward category, not a
// string enum). The discrete set is validated explicitly in translateV2Quest
// because huma's min/max/enum can't express a sparse int set cleanly (matching
// the v1 handler's validRewardTypes guard, surfaced as 422 in v2). The remaining
// filter fields are optional pointers (omitted ⇒ documented default via
// valueOr). ping is server-managed (not a caller input).
//
// summary opts a matched quest into the summary digest (clean bit 4) — the
// distinguishing behaviour of quest tracking. quest also carries the clean/edit
// lifecycle bits, all packed into the clean column.
type v2QuestRule struct {
	RewardType int   `json:"reward_type" required:"true" doc:"Reward category; one of 2 (item) | 3 (stardust) | 4 (candy) | 7 (pokemon) | 12 (mega_energy) (required)"`
	Reward     *int  `json:"reward,omitempty" doc:"Reward selector; 0 = any. Meaning depends on reward_type: item id (2), stardust amount (3), pokemon/candy id (4/7/12) (default 0)"`
	Form       *int  `json:"form,omitempty" doc:"Form id; 0 = any. Meaningful when reward_type=7 (pokemon) (default 0)"`
	Shiny      *bool `json:"shiny,omitempty" doc:"Only alert on shiny-possible quest rewards (default false)"`
	Amount     *int  `json:"amount,omitempty" doc:"Minimum reward amount; 0 = any. Meaningful for reward_type 2/4/12 (default 0)"`

	// Common fields.
	Distance *int    `json:"distance,omitempty" doc:"Radius in metres; 0 = use the profile's areas (default 0)"`
	Template *string `json:"template,omitempty" doc:"Template name; empty = server default"`
	Clean    *bool   `json:"clean,omitempty" doc:"Auto-delete the alert on expiry (default false)"`
	Edit     *bool   `json:"edit,omitempty" doc:"Keep the message updated in place (default false)"`
	Summary  *bool   `json:"summary,omitempty" doc:"Route into the summary digest (default false)"`

	OverrideLocationLabel *string  `json:"override_location_label,omitempty" doc:"Saved-location label to use instead of the profile location (requires distance > 0; mutually exclusive with override_areas)"`
	OverrideAreas         []string `json:"override_areas,omitempty" doc:"Restrict this rule to these geofence areas (mutually exclusive with distance > 0 and override_location_label)"`
}

// translateV2Quest converts a strict v2 quest rule into the stored
// QuestTrackingAPI, applying documented defaults, the clean bitmask (incl. the
// summary bit), profile, and validated/normalized override fields. It rejects an
// out-of-set reward_type with a 422 (matching the v1 handler's validRewardTypes
// guard, which returns 400). ping is always stored "" (server-managed).
func translateV2Quest(deps *TrackingDeps, humanID string, profileNo int, oc overrideContext, req *v2QuestRule) (db.QuestTrackingAPI, error) {
	if !validRewardTypes[req.RewardType] {
		return db.QuestTrackingAPI{}, huma.Error422UnprocessableEntity("Unrecognised reward_type value")
	}

	distance := valueOr(req.Distance, 0)
	const maxDistance = 40000000 // Earth circumference (metres)
	if distance > maxDistance {
		distance = maxDistance
	}

	overrideLabel := valueOr(req.OverrideLocationLabel, "")
	if msg, code := validateOverrideFields(deps, oc, humanID, overrideLabel, req.OverrideAreas, distance); msg != "" {
		return db.QuestTrackingAPI{}, humaErr(code, msg)
	}

	row := db.QuestTrackingAPI{
		ID:                    humanID,
		ProfileNo:             profileNo,
		Ping:                  "", // server-managed
		Template:              valueOr(req.Template, ""),
		Distance:              distance,
		RewardType:            req.RewardType,
		Reward:                valueOr(req.Reward, 0),
		Form:                  valueOr(req.Form, 0),
		Shiny:                 db.IntBool(valueOr(req.Shiny, false)),
		Amount:                valueOr(req.Amount, 0),
		Clean:                 packClean(valueOr(req.Clean, false), valueOr(req.Edit, false), valueOr(req.Summary, false)),
		OverrideLocationLabel: overrideLabel,
		OverrideAreas:         normalizeOverrideAreas(req.OverrideAreas),
	}
	return row, nil
}

// questRowToRule converts a stored QuestTrackingAPI back into the strict v2 rule
// shape for responses.
func questRowToRule(row *db.QuestTrackingAPI) v2QuestRule {
	clean := db.IsClean(row.Clean)
	edit := db.IsEdit(row.Clean)
	summary := db.IsSummary(row.Clean)
	return v2QuestRule{
		RewardType:            row.RewardType,
		Reward:                ptr(row.Reward),
		Form:                  ptr(row.Form),
		Shiny:                 ptr(bool(row.Shiny)),
		Amount:                ptr(row.Amount),
		Distance:              ptr(row.Distance),
		Template:              ptr(row.Template),
		Clean:                 ptr(clean),
		Edit:                  ptr(edit),
		Summary:               ptr(summary),
		OverrideLocationLabel: ptr(row.OverrideLocationLabel),
		OverrideAreas:         row.OverrideAreas,
	}
}

// RegisterV2TrackingQuest registers the strict v2 quest tracking endpoints via
// the generic resource helpers.
func RegisterV2TrackingQuest(api huma.API, deps *TrackingDeps) {
	registerV2Tracking(api, deps, v2TrackingType[v2QuestRule, db.QuestTrackingAPI]{
		Name: "quest",
		Store: func(d *TrackingDeps) store.TrackingStore[db.QuestTrackingAPI] {
			return d.Tracking.Quests
		},
		Translate: translateV2Quest,
		ToRule:    questRowToRule,
		GetUID:    store.QuestGetUID,
		SetUID:    store.QuestSetUID,
		RowText: func(d *TrackingDeps, tr *i18n.Translator, row *db.QuestTrackingAPI) string {
			return d.RowText.QuestRowText(tr, toQuestTracking(row))
		},
	})
}
