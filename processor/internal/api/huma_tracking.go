package api

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"reflect"
	"strconv"
	"strings"

	"github.com/danielgtaylor/huma/v2"
	log "github.com/sirupsen/logrus"

	"github.com/pokemon/poracleng/processor/internal/bot"
	"github.com/pokemon/poracleng/processor/internal/db"
	"github.com/pokemon/poracleng/processor/internal/store"
)

// humaLookupHuman mirrors lookupHuman but takes plain parameters instead of a
// gin.Context. profileNo is the resolved profile number: -1 means "use the
// human's current profile". Returns (nil, 0, nil) when the human is not found;
// the caller should return a 404 in that case.
func humaLookupHuman(deps *TrackingDeps, id string, profileNo int) (*store.HumanLite, int, error) {
	human, err := deps.Humans.GetLite(id)
	if err != nil {
		return nil, 0, err
	}
	if human == nil {
		return nil, 0, nil
	}

	pNo := human.CurrentProfileNo
	if profileNo >= 0 {
		pNo = profileNo
	}

	return human, pNo, nil
}

// listMonsterInput is the huma input type for GET /api/tracking/pokemon/{id}.
//
// huma does not support pointer types for query parameters. We use -1 as a
// sentinel meaning "not provided"; the handler then falls back to the human's
// current profile number. This mirrors the logic in lookupHuman.
type listMonsterInput struct {
	ID        string `path:"id"          doc:"Human/channel/webhook id"`
	ProfileNo int    `query:"profile_no" doc:"Profile number; defaults to the user's active profile" default:"-1"`
}

// listMonsterOutput is the huma output type — preserves the legacy
// {"status":"ok","pokemon":[...]} envelope.
type listMonsterOutput struct {
	Body struct {
		Status  string `json:"status"`
		Pokemon any    `json:"pokemon"`
	}
}

// ── monsterRuleRequest ───────────────────────────────────────────────────────

// monsterRuleRequest is the huma-facing per-row body shape for the POST
// endpoint. It extends the gin monsterInsertRequest with explicit Edit and
// Summary fields so callers no longer need to know the clean bitmask encoding.
//
// Clean still accepts a legacy integer bitmask (e.g. clean=3) via flexBool
// for backward compatibility. collapseClean packs all three into the stored
// column at insert/update time.
type monsterRuleRequest struct {
	UID                   flexInt  `json:"uid"`
	PokemonID             flexInt  `json:"pokemon_id"`
	ProfileNo             flexInt  `json:"profile_no"`
	Distance              flexInt  `json:"distance"`
	Template              any      `json:"template"`
	Clean                 flexBool `json:"clean"`
	Edit                  *bool    `json:"edit"`    // bit2 of stored clean column
	Summary               *bool    `json:"summary"` // bit4 of stored clean column
	Form                  flexInt  `json:"form"`
	MinIV                 flexInt  `json:"min_iv"`
	MaxIV                 flexInt  `json:"max_iv"`
	MinCP                 flexInt  `json:"min_cp"`
	MaxCP                 flexInt  `json:"max_cp"`
	MinLevel              flexInt  `json:"min_level"`
	MaxLevel              flexInt  `json:"max_level"`
	ATK                   flexInt  `json:"atk"`
	DEF                   flexInt  `json:"def"`
	STA                   flexInt  `json:"sta"`
	MaxATK                flexInt  `json:"max_atk"`
	MaxDEF                flexInt  `json:"max_def"`
	MaxSTA                flexInt  `json:"max_sta"`
	Gender                flexInt  `json:"gender"`
	MinWeight             flexInt  `json:"min_weight"`
	MaxWeight             flexInt  `json:"max_weight"`
	MinTime               flexInt  `json:"min_time"`
	Rarity                flexInt  `json:"rarity"`
	MaxRarity             flexInt  `json:"max_rarity"`
	Size                  flexInt  `json:"size"`
	MaxSize               flexInt  `json:"max_size"`
	PVPRankingLeague      flexInt  `json:"pvp_ranking_league"`
	PVPRankingBest        flexInt  `json:"pvp_ranking_best"`
	PVPRankingWorst       flexInt  `json:"pvp_ranking_worst"`
	PVPRankingMinCP       flexInt  `json:"pvp_ranking_min_cp"`
	PVPRankingCap         flexInt  `json:"pvp_ranking_cap"`
	OverrideLocationLabel string   `json:"override_location_label"`
	OverrideAreas         []string `json:"override_areas"`
}

// monsterRuleRows is the POST body: accepts a single rule object or an array
// of them. It implements both json.Unmarshaler (for the single-or-array peek)
// and huma.SchemaProvider (for the OpenAPI schema with correct
// additionalProperties on the item schema).
type monsterRuleRows []monsterRuleRequest

// UnmarshalJSON peeks the first non-space byte. '[' → decode as array directly.
// '{' → decode as a single object and wrap in a 1-element slice.
// Any other byte returns an error.
func (m *monsterRuleRows) UnmarshalJSON(b []byte) error {
	first := bytes.TrimLeft(b, " \t\r\n")
	if len(first) == 0 {
		return &json.SyntaxError{}
	}
	if first[0] == '[' {
		// Decode as []monsterRuleRequest. json.Unmarshal uses the default
		// decoder for each element: unknown fields are silently ignored
		// (standard Go json behaviour with no DisallowUnknownFields).
		var rows []monsterRuleRequest
		if err := json.Unmarshal(b, &rows); err != nil {
			return err
		}
		*m = rows
		return nil
	}
	// Single object — wrap in a 1-element slice.
	var single monsterRuleRequest
	if err := json.Unmarshal(b, &single); err != nil {
		return err
	}
	*m = monsterRuleRows{single}
	return nil
}

// Schema implements huma.SchemaProvider for monsterRuleRows.
//
// The body is "one rule object OR an array of rule objects". Huma validates
// the raw JSON against this schema BEFORE calling UnmarshalJSON, so the schema
// must accept both shapes for the validator to pass.
//
// We use oneOf[singleItem, arrayOfItems] where both alternatives carry
// additionalProperties:true so unknown client fields are permitted.
//
// The registry's stored schema for monsterRuleRequest is NOT mutated —
// we shallow-copy it before setting AdditionalProperties, following the
// same approach as lenient[T].Schema.
func (monsterRuleRows) Schema(r huma.Registry) *huma.Schema {
	// Get the inline (non-ref) schema for monsterRuleRequest. allowRef=false so
	// we get the full schema inline rather than a $ref.
	orig := r.Schema(reflect.TypeOf(monsterRuleRequest{}), false, "")

	// Shallow-copy; flip additionalProperties only on the copy.
	itemSchema := *orig
	itemSchema.AdditionalProperties = true
	// All fields in monsterRuleRequest are optional from the API perspective —
	// only pokemon_id is truly required, and that's validated in the handler,
	// not the schema. Clear the required list so partial rule objects don't 422.
	itemSchema.Required = nil

	// Array variant: array of items, each with additionalProperties.
	arraySchema := &huma.Schema{
		Type:  "array",
		Items: &itemSchema,
	}

	// Single-object variant: same item schema (no wrapping array).
	singleSchema := &itemSchema

	return &huma.Schema{
		OneOf: []*huma.Schema{singleSchema, arraySchema},
	}
}

// ── POST input/output types ──────────────────────────────────────────────────

// createMonsterInput is the huma input for POST /api/tracking/pokemon/{id}.
//
// huma does not support pointer types for query parameters. We use "" as a
// sentinel for suppressMessage / silent (presence == suppress). ProfileNo
// uses -1 as sentinel (not provided).
type createMonsterInput struct {
	ID              string         `path:"id"               doc:"Human/channel/webhook id"`
	ProfileNo       int            `query:"profile_no"      doc:"Profile number; defaults to the user's active profile" default:"-1"`
	Silent          string         `query:"silent"          doc:"If non-empty, suppress confirmation message"`
	SuppressMessage string         `query:"suppressMessage" doc:"If non-empty, suppress confirmation message"`
	Body            monsterRuleRows `               doc:"One rule object or an array of rule objects"`
}

// createMonsterOutput is the huma output for POST /api/tracking/pokemon/{id}.
// The Body struct mirrors the legacy JSON envelope from trackingJSONOK.
type createMonsterOutput struct {
	Body struct {
		Status         string  `json:"status"`
		Message        string  `json:"message"`
		NewUIDs        []int64 `json:"newUids"`
		AlreadyPresent int     `json:"alreadyPresent"`
		Updates        int     `json:"updates"`
		Insert         int     `json:"insert"`
	}
}

// ── handler ──────────────────────────────────────────────────────────────────

// RegisterTrackingMonster registers the GET and POST /tracking/pokemon/{id}
// huma operations on the given huma.API. The path is relative to the /api
// group so the full public path is /api/tracking/pokemon/{id}.
func RegisterTrackingMonster(humaAPI huma.API, deps *TrackingDeps) {
	// GET
	huma.Register(humaAPI, huma.Operation{
		OperationID: "list-monster-tracking",
		Method:      http.MethodGet,
		Path:        "/tracking/pokemon/{id}",
		Summary:     "List pokemon tracking rules",
		Tags:        []string{"tracking"},
		Security:    []map[string][]string{{"poracleSecret": {}}},
	}, func(ctx context.Context, in *listMonsterInput) (*listMonsterOutput, error) {
		human, profileNo, err := humaLookupHuman(deps, in.ID, in.ProfileNo)
		if err != nil {
			return nil, humaNewError(http.StatusInternalServerError, err.Error())
		}
		if human == nil {
			return nil, humaNewError(http.StatusNotFound, "User not found")
		}

		monsters, err := db.SelectMonstersByIDProfile(deps.DB, human.ID, profileNo)
		if err != nil {
			return nil, humaNewError(http.StatusInternalServerError, "database error")
		}

		tr := translatorFor(deps, human)

		type monsterWithDesc struct {
			db.MonsterTrackingAPI
			Description string `json:"description"`
		}

		result := make([]monsterWithDesc, len(monsters))
		for i := range monsters {
			mt := toMonsterTracking(&monsters[i])
			result[i] = monsterWithDesc{
				MonsterTrackingAPI: monsters[i],
				Description:        deps.RowText.MonsterRowText(tr, mt),
			}
		}

		out := &listMonsterOutput{}
		out.Body.Status = "ok"
		out.Body.Pokemon = result
		return out, nil
	})

	// POST
	huma.Register(humaAPI, huma.Operation{
		OperationID:  "create-monster-tracking",
		Method:       http.MethodPost,
		Path:         "/tracking/pokemon/{id}",
		Summary:      "Create or update pokemon tracking rules",
		Tags:         []string{"tracking"},
		Security:     []map[string][]string{{"poracleSecret": {}}},
		DefaultStatus: http.StatusOK,
	}, func(ctx context.Context, in *createMonsterInput) (*createMonsterOutput, error) {
		human, profileNo, err := humaLookupHuman(deps, in.ID, in.ProfileNo)
		if err != nil {
			return nil, humaNewError(http.StatusInternalServerError, err.Error())
		}
		if human == nil {
			return nil, humaNewError(http.StatusNotFound, "User not found")
		}

		language := resolveLanguage(deps, human)
		tr := translatorFor(deps, human)
		silent := in.Silent != "" || in.SuppressMessage != ""

		insertReqs := []monsterRuleRequest(in.Body)

		defaultTemplate := deps.RowText.DefaultTemplateName
		if defaultTemplate == "" {
			defaultTemplate = "1"
		}

		// cleanRow applies defaults and validates a single rule, matching the gin
		// handler's cleanRow closure.
		cleanRow := func(req monsterRuleRequest) (db.MonsterTrackingAPI, error) {
			if !req.PokemonID.isSet() {
				return db.MonsterTrackingAPI{}, errPokemonIDRequired
			}

			pokemonID := req.PokemonID.intValue(0)

			distance := req.Distance.intValue(0)
			const maxDistanceDefault = 40000000
			if distance > maxDistanceDefault {
				distance = maxDistanceDefault
			}

			template := defaultTemplate
			if req.Template != nil {
				switch v := req.Template.(type) {
				case string:
					if v != "" {
						template = v
					}
				case float64:
					template = strconv.Itoa(int(v))
				case json.Number:
					template = string(v)
				}
			}

			pNo := profileNo
			if req.ProfileNo.isSet() {
				pNo = req.ProfileNo.intValue(profileNo)
			}

			row := db.MonsterTrackingAPI{
				ID:               human.ID,
				ProfileNo:        pNo,
				Ping:             "",
				Template:         template,
				PokemonID:        pokemonID,
				Distance:         distance,
				MinIV:            req.MinIV.intValue(-1),
				MaxIV:            req.MaxIV.intValue(100),
				MinCP:            req.MinCP.intValue(0),
				MaxCP:            req.MaxCP.intValue(9000),
				MinLevel:         req.MinLevel.intValue(0),
				MaxLevel:         req.MaxLevel.intValue(55),
				ATK:              req.ATK.intValue(0),
				DEF:              req.DEF.intValue(0),
				STA:              req.STA.intValue(0),
				MaxATK:           req.MaxATK.intValue(15),
				MaxDEF:           req.MaxDEF.intValue(15),
				MaxSTA:           req.MaxSTA.intValue(15),
				Gender:           req.Gender.intValue(0),
				Form:             req.Form.intValue(0),
				Clean:            collapseClean(req.Clean, req.Edit, req.Summary),
				MinWeight:        req.MinWeight.intValue(0),
				MaxWeight:        req.MaxWeight.intValue(9000000),
				MinTime:          req.MinTime.intValue(0),
				Rarity:           req.Rarity.intValue(-1),
				MaxRarity:        req.MaxRarity.intValue(6),
				Size:             req.Size.intValue(-1),
				MaxSize:          req.MaxSize.intValue(5),
				PVPRankingLeague: req.PVPRankingLeague.intValue(0),
				PVPRankingBest:   req.PVPRankingBest.intValue(1),
				PVPRankingWorst:  req.PVPRankingWorst.intValue(4096),
				PVPRankingMinCP:  req.PVPRankingMinCP.intValue(0),
				PVPRankingCap:    req.PVPRankingCap.intValue(0),
			}

			if req.UID.isSet() {
				row.UID = int64(req.UID.intValue(0))
			}

			return row, nil
		}

		// Pre-fetch override context once so per-row validation doesn't re-query.
		oc, ocMsg, ocCode := newOverrideContext(deps, human.ID)
		if ocMsg != "" {
			return nil, humaNewError(ocCode, ocMsg)
		}

		// Split: rows with uid are explicit updates, without are inserts.
		var insert []db.MonsterTrackingAPI
		var updates []db.MonsterTrackingAPI

		for _, req := range insertReqs {
			if msg, code := validateOverrideFields(deps, oc, human.ID, req.OverrideLocationLabel, req.OverrideAreas, req.Distance.intValue(0)); msg != "" {
				return nil, humaNewError(code, msg)
			}
			row, err := cleanRow(req)
			if err != nil {
				return nil, humaNewError(http.StatusBadRequest, err.Error())
			}
			row.OverrideLocationLabel = req.OverrideLocationLabel
			row.OverrideAreas = normalizeOverrideAreas(req.OverrideAreas)
			if req.UID.isSet() {
				updates = append(updates, row)
			} else {
				insert = append(insert, row)
			}
		}

		// Fetch existing for diff (only for new inserts).
		tracked, err := deps.Tracking.Monsters.SelectByIDProfile(human.ID, profileNo)
		if err != nil {
			log.Errorf("Tracking API: select existing monsters: %s", err)
			return nil, humaNewError(http.StatusInternalServerError, "database error")
		}

		diff := store.DiffAndClassify(tracked, insert, store.MonsterGetUID, store.MonsterSetUID)

		// Merge: diff-classified updates go into the explicit updates slice.
		updates = append(updates, diff.Updates...)

		// Build confirmation message.
		var message string
		totalChanges := len(diff.AlreadyPresent) + len(updates) + len(diff.Inserts)
		if totalChanges > 50 {
			message = tr.Tf("tracking.bulk_changes",
				bot.CommandPrefixForType(deps.Config, human.Type), tr.T("tracking.tracked"))
		} else {
			var sb strings.Builder
			for i := range diff.AlreadyPresent {
				mt := toMonsterTracking(&diff.AlreadyPresent[i])
				sb.WriteString(tr.T("tracking.unchanged"))
				sb.WriteString(deps.RowText.MonsterRowText(tr, mt))
				sb.WriteByte('\n')
			}
			for i := range updates {
				mt := toMonsterTracking(&updates[i])
				sb.WriteString(tr.T("tracking.updated"))
				sb.WriteString(deps.RowText.MonsterRowText(tr, mt))
				sb.WriteByte('\n')
			}
			for i := range diff.Inserts {
				mt := toMonsterTracking(&diff.Inserts[i])
				sb.WriteString(tr.T("tracking.new"))
				sb.WriteString(deps.RowText.MonsterRowText(tr, mt))
				sb.WriteByte('\n')
			}
			message = sb.String()
		}

		// Persist: inserts first, then updates.
		var newUIDs []int64

		for i := range diff.Inserts {
			uid, err := deps.Tracking.Monsters.Insert(&diff.Inserts[i])
			if err != nil {
				log.Errorf("Tracking API: insert monster: %s", err)
				return nil, humaNewError(http.StatusInternalServerError, "database error")
			}
			newUIDs = append(newUIDs, uid)
		}

		for i := range updates {
			if err := db.UpdateMonsterByUID(deps.DB, &updates[i]); err != nil {
				log.Errorf("Tracking API: update monster: %s", err)
				return nil, humaNewError(http.StatusInternalServerError, "database error")
			}
			newUIDs = append(newUIDs, updates[i].UID)
		}

		reloadState(deps)

		if !silent {
			sendConfirmation(deps, human, message, language)
		}

		responseMsg := message
		if silent {
			responseMsg = ""
		}

		out := &createMonsterOutput{}
		out.Body.Status = "ok"
		out.Body.Message = responseMsg
		out.Body.NewUIDs = newUIDs
		out.Body.AlreadyPresent = len(diff.AlreadyPresent)
		out.Body.Updates = len(updates)
		out.Body.Insert = len(diff.Inserts)
		return out, nil
	})
}
