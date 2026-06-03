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

	Form *int `json:"form,omitempty" doc:"Form id; 0 = any (default 0)"`

	MinIV *int `json:"min_iv,omitempty" doc:"Minimum IV %; -1 = no lower bound (default -1)"`
	MaxIV *int `json:"max_iv,omitempty" doc:"Maximum IV % (default 100)"`

	MinCP *int `json:"min_cp,omitempty" doc:"Minimum CP (default 0)"`
	MaxCP *int `json:"max_cp,omitempty" doc:"Maximum CP (default 9000)"`

	MinLevel *int `json:"min_level,omitempty" doc:"Minimum level (default 0)"`
	MaxLevel *int `json:"max_level,omitempty" doc:"Maximum level (default 55)"`

	ATK *int `json:"atk,omitempty" doc:"Minimum ATK IV 0-15 (default 0)"`
	DEF *int `json:"def,omitempty" doc:"Minimum DEF IV 0-15 (default 0)"`
	STA *int `json:"sta,omitempty" doc:"Minimum STA IV 0-15 (default 0)"`

	MaxATK *int `json:"max_atk,omitempty" doc:"Maximum ATK IV 0-15 (default 15)"`
	MaxDEF *int `json:"max_def,omitempty" doc:"Maximum DEF IV 0-15 (default 15)"`
	MaxSTA *int `json:"max_sta,omitempty" doc:"Maximum STA IV 0-15 (default 15)"`

	Gender *string `json:"gender,omitempty" enum:"any,male,female,genderless" doc:"Gender filter: any|male|female|genderless (default any)"`

	MinWeight *int `json:"min_weight,omitempty" doc:"Minimum weight in grams (default 0)"`
	MaxWeight *int `json:"max_weight,omitempty" doc:"Maximum weight in grams (default 9000000)"`

	MinTime *int `json:"min_time,omitempty" doc:"Minimum seconds remaining (default 0)"`

	Rarity    *int `json:"rarity,omitempty" doc:"Minimum rarity; -1 = any (default -1)"`
	MaxRarity *int `json:"max_rarity,omitempty" doc:"Maximum rarity 1-6 (default 6)"`

	Size    *int `json:"size,omitempty" doc:"Minimum size; -1 = any (default -1)"`
	MaxSize *int `json:"max_size,omitempty" doc:"Maximum size 1-5 (default 5)"`

	PVPRankingLeague *int `json:"pvp_ranking_league,omitempty" doc:"PVP league CP cap: 0 (none/IV mode) | 500 | 1500 | 2500 (default 0)"`
	PVPRankingBest   *int `json:"pvp_ranking_best,omitempty" doc:"Best (lowest) PVP rank to alert on (default 1)"`
	PVPRankingWorst  *int `json:"pvp_ranking_worst,omitempty" doc:"Worst (highest) PVP rank to alert on (default 4096)"`
	PVPRankingMinCP  *int `json:"pvp_ranking_min_cp,omitempty" doc:"PVP CP floor (default 0)"`
	PVPRankingCap    *int `json:"pvp_ranking_cap,omitempty" doc:"PVP level cap; 0 = league default (default 0)"`

	// Common fields.
	Distance *int    `json:"distance,omitempty" doc:"Radius in metres; 0 = use the profile's areas (default 0)"`
	Template *string `json:"template,omitempty" doc:"Template name; empty = server default"`
	Clean    *bool   `json:"clean,omitempty" doc:"Auto-delete the alert on expiry (default false)"`
	Edit     *bool   `json:"edit,omitempty" doc:"Keep the message updated in place (default false)"`
	Summary  *bool   `json:"summary,omitempty" doc:"Route into the summary digest (default false)"`

	OverrideLocationLabel *string  `json:"override_location_label,omitempty" doc:"Saved-location label to use instead of the profile location (requires distance > 0; mutually exclusive with override_areas)"`
	OverrideAreas         []string `json:"override_areas,omitempty" doc:"Restrict this rule to these geofence areas (mutually exclusive with distance > 0 and override_location_label)"`
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
