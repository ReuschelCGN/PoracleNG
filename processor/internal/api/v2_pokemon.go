package api

import (
	"github.com/danielgtaylor/huma/v2"

	"github.com/pokemon/poracleng/processor/internal/db"
	"github.com/pokemon/poracleng/processor/internal/i18n"
	"github.com/pokemon/poracleng/processor/internal/store"
)

// v2PokemonRule is the strict v2 pokemon tracking request/response rule object.
//
// Optional filter fields are POINTERS so "omitted ⇒ documented default" is
// unambiguous: huma leaves an omitted pointer nil (we deliberately use NO
// `default:` tags, which huma WOULD auto-populate the pointer from — verified
// empirically), and the handler applies the documented default via valueOr.
// pokemon_id is required (non-pointer). gender is the only string enum here.
//
// Defaults documented in each field's doc string come from the field audit
// pokemon table. pvp_ranking_evolution is intentionally OMITTED (depends on the
// unmerged pvp-mega-evolution branch; added in a follow-up).
type v2PokemonRule struct {
	PokemonID int `json:"pokemon_id" required:"true" doc:"Pokédex id (required)"`

	Form *int `json:"form,omitempty" doc:"Form id (game-master). Omit to match any form (stored as 0 = any)."`

	MinIV *int `json:"min_iv,omitempty" doc:"Minimum IV %. Omit to impose no lower bound (stored as -1 = no lower bound)."`
	MaxIV *int `json:"max_iv,omitempty" doc:"Maximum IV %. Omit to impose no upper bound (stored as 100 = the IV ceiling, i.e. no upper bound)."`

	MinCP *int `json:"min_cp,omitempty" doc:"Minimum CP. Omit to impose no lower bound (stored as 0 = no lower bound)."`
	MaxCP *int `json:"max_cp,omitempty" doc:"Maximum CP. Omit to impose no upper bound (stored as 9000 = the CP cap sentinel, i.e. no upper bound)."`

	MinLevel *int `json:"min_level,omitempty" doc:"Minimum encounter level. Omit to impose no lower bound (stored as 0 = no lower bound)."`
	MaxLevel *int `json:"max_level,omitempty" doc:"Maximum encounter level. Omit to impose no upper bound (stored as 55 = the level ceiling, i.e. no upper bound)."`

	ATK *int `json:"atk,omitempty" doc:"Minimum ATK IV (0-15). Omit to impose no floor (stored as 0 = no floor)."`
	DEF *int `json:"def,omitempty" doc:"Minimum DEF IV (0-15). Omit to impose no floor (stored as 0 = no floor)."`
	STA *int `json:"sta,omitempty" doc:"Minimum STA IV (0-15). Omit to impose no floor (stored as 0 = no floor)."`

	MaxATK *int `json:"max_atk,omitempty" doc:"Maximum ATK IV (0-15). Omit to impose no ceiling (stored as 15 = the IV ceiling, i.e. no upper bound)."`
	MaxDEF *int `json:"max_def,omitempty" doc:"Maximum DEF IV (0-15). Omit to impose no ceiling (stored as 15 = the IV ceiling, i.e. no upper bound)."`
	MaxSTA *int `json:"max_sta,omitempty" doc:"Maximum STA IV (0-15). Omit to impose no ceiling (stored as 15 = the IV ceiling, i.e. no upper bound)."`

	Gender *string `json:"gender,omitempty" enum:"any,male,female,genderless" doc:"Gender filter: any|male|female|genderless. Omit to match any gender (defaults to 'any', stored as 0)."`

	MinWeight *int `json:"min_weight,omitempty" doc:"Minimum weight in grams. Omit to impose no lower bound (stored as 0 = no lower bound)."`
	MaxWeight *int `json:"max_weight,omitempty" doc:"Maximum weight in grams. Omit to impose no upper bound (stored as 9000000 = no upper weight sentinel)."`

	MinTime *int `json:"min_time,omitempty" doc:"Minimum seconds remaining on the spawn. Omit to impose no minimum (stored as 0 = no minimum)."`

	Rarity    *int `json:"rarity,omitempty" doc:"Minimum rarity tier. Omit to match any rarity (stored as -1 = any)."`
	MaxRarity *int `json:"max_rarity,omitempty" doc:"Maximum rarity tier (1-6). Omit to impose no upper bound (stored as 6 = the top tier, i.e. no upper bound)."`

	Size    *int `json:"size,omitempty" doc:"Minimum size tier. Omit to match any size (stored as -1 = any)."`
	MaxSize *int `json:"max_size,omitempty" doc:"Maximum size tier (1-5). Omit to impose no upper bound (stored as 5 = the top tier, i.e. no upper bound)."`

	PVPRankingLeague *int `json:"pvp_ranking_league,omitempty" doc:"PVP league CP cap (the stored int IS the cap): 0 | 500 | 1500 | 2500. Omit (or 0) for IV-mode tracking with no PVP filter (stored as 0 = none/IV mode)."`
	PVPRankingBest   *int `json:"pvp_ranking_best,omitempty" doc:"Best (lowest, 1-based) PVP rank to alert on. Omit to start from rank 1 (stored as 1 = best possible rank)."`
	PVPRankingWorst  *int `json:"pvp_ranking_worst,omitempty" doc:"Worst (highest) PVP rank to alert on. Omit to impose no upper rank limit (stored as 4096 = no upper rank limit sentinel; PVP ranks never exceed it)."`
	PVPRankingMinCP  *int `json:"pvp_ranking_min_cp,omitempty" doc:"PVP CP floor. Omit to impose no floor (stored as 0 = no floor)."`
	PVPRankingCap    *int `json:"pvp_ranking_cap,omitempty" doc:"PVP level cap. Omit to use the league default cap (stored as 0 = league default)."`

	// Common fields.
	Distance *int    `json:"distance,omitempty" doc:"Radius in metres around the anchor location. Omit (or 0) to match by the profile's geofence areas instead of a radius — 0 means area-based, NOT zero metres (stored as 0)."`
	Template *string `json:"template,omitempty" doc:"DTS template name. Omit (or empty) to use the server's configured default template (stored as \"\")."`
	Clean    *bool   `json:"clean,omitempty" doc:"Auto-delete the alert on expiry (clean bitmask bit 1). Omit to disable (default false)."`
	Edit     *bool   `json:"edit,omitempty" doc:"Keep the message updated in place (clean bitmask bit 2). Omit to disable (default false)."`
	Summary  *bool   `json:"summary,omitempty" doc:"Route into the summary digest (clean bitmask bit 4). Omit to disable (default false)."`

	OverrideLocationLabel *string  `json:"override_location_label,omitempty" doc:"Saved-location label to use instead of the profile location (requires distance > 0; mutually exclusive with override_areas). Omit for none."`
	OverrideAreas         []string `json:"override_areas,omitempty" doc:"Restrict this rule to these geofence areas (mutually exclusive with distance > 0 and override_location_label). Omit for none."`
}

// valueOr returns *p when p is non-nil, else def. The strict-default helper.
func valueOr[T any](p *T, def T) T {
	if p == nil {
		return def
	}
	return *p
}

// packClean collapses the clean/edit/summary booleans into the stored bitmask
// (bit1=clean, bit2=edit, bit4=summary). See internal/db/clean.go.
func packClean(clean, edit, summary bool) int {
	v := 0
	if clean {
		v |= 1
	}
	if edit {
		v |= 2
	}
	if summary {
		v |= 4
	}
	return v
}

// translateV2Pokemon converts a strict v2 pokemon rule into the stored
// MonsterTrackingAPI, applying documented defaults, gender enum→int, the clean
// bitmask, profile, and validated/normalized override fields. ping is always
// stored "" (server-managed). Returns an huma error on override-field violation.
func translateV2Pokemon(deps *TrackingDeps, humanID string, profileNo int, oc overrideContext, req *v2PokemonRule) (db.MonsterTrackingAPI, error) {
	distance := valueOr(req.Distance, 0)
	const maxDistance = 40000000 // Earth circumference (metres)
	if distance > maxDistance {
		distance = maxDistance
	}

	overrideLabel := valueOr(req.OverrideLocationLabel, "")
	if msg, code := validateOverrideFields(deps, oc, humanID, overrideLabel, req.OverrideAreas, distance); msg != "" {
		return db.MonsterTrackingAPI{}, humaErr(code, msg)
	}

	template := valueOr(req.Template, "")

	row := db.MonsterTrackingAPI{
		ID:                    humanID,
		ProfileNo:             profileNo,
		Ping:                  "", // server-managed
		Template:              template,
		Distance:              distance,
		PokemonID:             req.PokemonID,
		Form:                  valueOr(req.Form, 0),
		MinIV:                 valueOr(req.MinIV, -1),
		MaxIV:                 valueOr(req.MaxIV, 100),
		MinCP:                 valueOr(req.MinCP, 0),
		MaxCP:                 valueOr(req.MaxCP, 9000),
		MinLevel:              valueOr(req.MinLevel, 0),
		MaxLevel:              valueOr(req.MaxLevel, 55),
		ATK:                   valueOr(req.ATK, 0),
		DEF:                   valueOr(req.DEF, 0),
		STA:                   valueOr(req.STA, 0),
		MaxATK:                valueOr(req.MaxATK, 15),
		MaxDEF:                valueOr(req.MaxDEF, 15),
		MaxSTA:                valueOr(req.MaxSTA, 15),
		Gender:                genderEnum.resolveStored(req.Gender),
		MinWeight:             valueOr(req.MinWeight, 0),
		MaxWeight:             valueOr(req.MaxWeight, 9000000),
		MinTime:               valueOr(req.MinTime, 0),
		Rarity:                valueOr(req.Rarity, -1),
		MaxRarity:             valueOr(req.MaxRarity, 6),
		Size:                  valueOr(req.Size, -1),
		MaxSize:               valueOr(req.MaxSize, 5),
		PVPRankingLeague:      valueOr(req.PVPRankingLeague, 0),
		PVPRankingBest:        valueOr(req.PVPRankingBest, 1),
		PVPRankingWorst:       valueOr(req.PVPRankingWorst, 4096),
		PVPRankingMinCP:       valueOr(req.PVPRankingMinCP, 0),
		PVPRankingCap:         valueOr(req.PVPRankingCap, 0),
		Clean:                 packClean(valueOr(req.Clean, false), valueOr(req.Edit, false), valueOr(req.Summary, false)),
		OverrideLocationLabel: overrideLabel,
		OverrideAreas:         normalizeOverrideAreas(req.OverrideAreas),
	}
	return row, nil
}

// pokemonRowToRule converts a stored MonsterTrackingAPI back into the strict v2
// rule shape for responses. All optional fields are emitted as concrete values
// (pointers to the stored value) so the response is fully specified.
func pokemonRowToRule(row *db.MonsterTrackingAPI) v2PokemonRule {
	gender := genderEnum.fromStored(row.Gender)
	clean := db.IsClean(row.Clean)
	edit := db.IsEdit(row.Clean)
	summary := db.IsSummary(row.Clean)
	return v2PokemonRule{
		PokemonID:             row.PokemonID,
		Form:                  ptr(row.Form),
		MinIV:                 ptr(row.MinIV),
		MaxIV:                 ptr(row.MaxIV),
		MinCP:                 ptr(row.MinCP),
		MaxCP:                 ptr(row.MaxCP),
		MinLevel:              ptr(row.MinLevel),
		MaxLevel:              ptr(row.MaxLevel),
		ATK:                   ptr(row.ATK),
		DEF:                   ptr(row.DEF),
		STA:                   ptr(row.STA),
		MaxATK:                ptr(row.MaxATK),
		MaxDEF:                ptr(row.MaxDEF),
		MaxSTA:                ptr(row.MaxSTA),
		Gender:                ptr(gender),
		MinWeight:             ptr(row.MinWeight),
		MaxWeight:             ptr(row.MaxWeight),
		MinTime:               ptr(row.MinTime),
		Rarity:                ptr(row.Rarity),
		MaxRarity:             ptr(row.MaxRarity),
		Size:                  ptr(row.Size),
		MaxSize:               ptr(row.MaxSize),
		PVPRankingLeague:      ptr(row.PVPRankingLeague),
		PVPRankingBest:        ptr(row.PVPRankingBest),
		PVPRankingWorst:       ptr(row.PVPRankingWorst),
		PVPRankingMinCP:       ptr(row.PVPRankingMinCP),
		PVPRankingCap:         ptr(row.PVPRankingCap),
		Distance:              ptr(row.Distance),
		Template:              ptr(row.Template),
		Clean:                 ptr(clean),
		Edit:                  ptr(edit),
		Summary:               ptr(summary),
		OverrideLocationLabel: ptr(row.OverrideLocationLabel),
		OverrideAreas:         row.OverrideAreas,
	}
}

// ptr returns a pointer to v (response builder helper).
func ptr[T any](v T) *T { return &v }

// RegisterV2TrackingPokemon registers the strict v2 pokemon tracking endpoints
// (list/create/get/put/delete/bulk-delete) via the generic resource helpers.
func RegisterV2TrackingPokemon(api huma.API, deps *TrackingDeps) {
	registerV2Tracking(api, deps, v2TrackingType[v2PokemonRule, db.MonsterTrackingAPI]{
		Name: "pokemon",
		Store: func(d *TrackingDeps) store.TrackingStore[db.MonsterTrackingAPI] {
			return d.Tracking.Monsters
		},
		Translate: translateV2Pokemon,
		ToRule:    pokemonRowToRule,
		GetUID:    store.MonsterGetUID,
		SetUID:    store.MonsterSetUID,
		RowText: func(d *TrackingDeps, tr *i18n.Translator, row *db.MonsterTrackingAPI) string {
			return d.RowText.MonsterRowText(tr, toMonsterTracking(row))
		},
	})
}
