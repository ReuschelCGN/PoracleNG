package api

import (
	"strings"

	"github.com/danielgtaylor/huma/v2"

	"github.com/pokemon/poracleng/processor/internal/db"
	"github.com/pokemon/poracleng/processor/internal/gamedata"
	"github.com/pokemon/poracleng/processor/internal/i18n"
	"github.com/pokemon/poracleng/processor/internal/store"
)

// invasion and incident BOTH back onto the `invasion` table (stored grunt_type
// string + gender int). A row is an INCIDENT iff its grunt_type is a known
// pokestop-event name; otherwise it is an INVASION (a pokemon-type name, or the
// catch-all "everything"/"boss"). isEventGruntType is the discriminator; the two
// types register opposite Filters over it so each endpoint sees only its rows.

// isEventGruntType reports whether a stored grunt_type string is a pokestop
// event name (lowercased gd.Util.PokestopEvent[*].Name). Comparison is
// lowercased on both sides. gd or its Util being nil ⇒ never an event.
func isEventGruntType(gruntType string, gd *gamedata.GameData) bool {
	if gd == nil || gd.Util == nil {
		return false
	}
	want := strings.ToLower(gruntType)
	for _, evt := range gd.Util.PokestopEvent {
		if strings.ToLower(evt.Name) == want {
			return true
		}
	}
	return false
}

// gdFromDeps returns the GameData reachable through the rowtext generator, or
// nil when unset (defence-in-depth; translation handlers 422 on a nil gd path).
func gdFromDeps(deps *TrackingDeps) *gamedata.GameData {
	if deps.RowText == nil {
		return nil
	}
	return deps.RowText.GD
}

// typeNameToID builds the reverse "lowercased type name → type id" map from
// gd.Types (e.g. "grass" → 12). Used by v2InvasionToRule to recover a stored
// type-name grunt_type as a type_id for the read-back response.
func typeNameToID(gd *gamedata.GameData) map[string]int {
	out := map[string]int{}
	if gd == nil {
		return out
	}
	for id, ti := range gd.Types {
		if ti == nil || ti.Name == "" {
			continue
		}
		out[strings.ToLower(ti.Name)] = id
	}
	return out
}

// v2InvasionRule is the strict v2 invasion tracking request/response rule object.
//
// EXACTLY ONE of {type_id, grunt_id, everything, boss} must be set per rule
// (0 set or >1 set ⇒ 422). They down-translate to the stored grunt_type string
// the matcher already compares against:
//   - type_id  (1-18): grunt_type = lower(gd.Types[type_id].Name); + optional gender.
//   - grunt_id (game-master id): grunt_type derived from the grunt's TypeID/template;
//     gender is the grunt's own gender (a specific grunt implies its gender).
//   - everything=true: grunt_type = "everything" (matches every invasion).
//   - boss=true:       grunt_type = "boss" (matches only boss encounters).
//
// gender is ONLY valid with type_id. gender + grunt_id/everything/boss ⇒ 422.
//
// (Pokestop-event rows live in the SAME table but are addressed via the separate
// /incident endpoint; see v2_incident.go.)
type v2InvasionRule struct {
	TypeID     *int    `json:"type_id,omitempty" doc:"Pokemon type id (1-18); grunt_type resolves to that type's lowercased name. Mutually exclusive with grunt_id/everything/boss."`
	GruntID    *int    `json:"grunt_id,omitempty" doc:"Game-master grunt id; resolves to its type name and implies its own gender. Mutually exclusive with type_id/everything/boss; gender not allowed."`
	Everything *bool   `json:"everything,omitempty" doc:"Match every invasion (stored grunt_type \"everything\"). Mutually exclusive with the others; gender not allowed."`
	Boss       *bool   `json:"boss,omitempty" doc:"Match only boss encounters (stored grunt_type \"boss\"). Mutually exclusive with the others; gender not allowed."`
	Gender     *string `json:"gender,omitempty" enum:"any,male,female" doc:"Grunt gender filter: any|male|female (default any). ONLY valid with type_id."`

	// Common fields. invasion HAS a clean column ⇒ clean/edit/summary apply.
	Distance *int    `json:"distance,omitempty" doc:"Radius in metres; 0 = use the profile's areas (default 0)"`
	Template *string `json:"template,omitempty" doc:"Template name; empty = server default"`
	Clean    *bool   `json:"clean,omitempty" doc:"Auto-delete the alert on expiry (default false)"`
	Edit     *bool   `json:"edit,omitempty" doc:"Keep the message updated in place (default false)"`
	Summary  *bool   `json:"summary,omitempty" doc:"Route into the summary digest (default false)"`

	OverrideLocationLabel *string  `json:"override_location_label,omitempty" doc:"Saved-location label to use instead of the profile location (requires distance > 0; mutually exclusive with override_areas)"`
	OverrideAreas         []string `json:"override_areas,omitempty" doc:"Restrict this rule to these geofence areas (mutually exclusive with distance > 0 and override_location_label)"`
}

// translateV2Invasion converts a strict v2 invasion rule into the stored
// InvasionTrackingAPI. It enforces exactly-one-mode and gender placement, then
// down-translates the chosen mode into (grunt_type, gender). ping is "" (server
// managed); clean packs the bitmask.
func translateV2Invasion(deps *TrackingDeps, humanID string, profileNo int, oc overrideContext, req *v2InvasionRule) (db.InvasionTrackingAPI, error) {
	gd := gdFromDeps(deps)

	// Exactly-one-mode.
	modes := 0
	if req.TypeID != nil {
		modes++
	}
	if req.GruntID != nil {
		modes++
	}
	if req.Everything != nil && *req.Everything {
		modes++
	}
	if req.Boss != nil && *req.Boss {
		modes++
	}
	if modes == 0 {
		return db.InvasionTrackingAPI{}, huma.Error422UnprocessableEntity(
			"exactly one of type_id, grunt_id, everything, boss must be set")
	}
	if modes > 1 {
		return db.InvasionTrackingAPI{}, huma.Error422UnprocessableEntity(
			"type_id, grunt_id, everything and boss are mutually exclusive (set exactly one)")
	}

	// gender is only valid with type_id.
	if req.Gender != nil && req.TypeID == nil {
		return db.InvasionTrackingAPI{}, huma.Error422UnprocessableEntity(
			"gender is only valid together with type_id")
	}

	var gruntType string
	gender := 0

	switch {
	case req.TypeID != nil:
		ti, ok := lookupType(gd, *req.TypeID)
		if !ok {
			return db.InvasionTrackingAPI{}, huma.Error422UnprocessableEntity(
				"unknown type_id")
		}
		gruntType = strings.ToLower(ti.Name)
		gender = invasionGenderEnum.resolveStored(req.Gender)

	case req.GruntID != nil:
		grunt, ok := lookupGrunt(gd, *req.GruntID)
		if !ok {
			return db.InvasionTrackingAPI{}, huma.Error422UnprocessableEntity(
				"unknown grunt_id")
		}
		gruntType = gruntTypeName(gd, grunt)
		gender = grunt.Gender // a specific grunt implies its gender (1 or 2)

	case req.Everything != nil && *req.Everything:
		gruntType = "everything"

	case req.Boss != nil && *req.Boss:
		gruntType = "boss"
	}

	distance := valueOr(req.Distance, 0)
	const maxDistance = 40000000 // Earth circumference (metres)
	if distance > maxDistance {
		distance = maxDistance
	}

	overrideLabel := valueOr(req.OverrideLocationLabel, "")
	if msg, code := validateOverrideFields(deps, oc, humanID, overrideLabel, req.OverrideAreas, distance); msg != "" {
		return db.InvasionTrackingAPI{}, humaErr(code, msg)
	}

	row := db.InvasionTrackingAPI{
		ID:                    humanID,
		ProfileNo:             profileNo,
		Ping:                  "", // server-managed
		Template:              valueOr(req.Template, ""),
		Distance:              distance,
		Gender:                gender,
		GruntType:             gruntType,
		Clean:                 packClean(valueOr(req.Clean, false), valueOr(req.Edit, false), valueOr(req.Summary, false)),
		OverrideLocationLabel: overrideLabel,
		OverrideAreas:         normalizeOverrideAreas(req.OverrideAreas),
	}
	return row, nil
}

// lookupType / lookupGrunt are nil-safe accessors over GameData.
func lookupType(gd *gamedata.GameData, id int) (*gamedata.TypeInfo, bool) {
	if gd == nil {
		return nil, false
	}
	ti, ok := gd.Types[id]
	if !ok || ti == nil {
		return nil, false
	}
	return ti, true
}

func lookupGrunt(gd *gamedata.GameData, id int) (*gamedata.Grunt, bool) {
	if gd == nil {
		return nil, false
	}
	g, ok := gd.Grunts[id]
	if !ok || g == nil {
		return nil, false
	}
	return g, true
}

// gruntTypeName resolves a grunt to its stored grunt_type string, mirroring
// matching.ResolveGruntTypeName's regular-grunt branch: typed grunts resolve via
// TypeID → type name; untyped grunts derive from the template.
func gruntTypeName(gd *gamedata.GameData, grunt *gamedata.Grunt) string {
	if grunt.TypeID > 0 && gd != nil {
		if ti, ok := gd.Types[grunt.TypeID]; ok && ti != nil {
			return strings.ToLower(ti.Name)
		}
	}
	return gamedata.TypeNameFromTemplate(grunt.Template)
}

// v2InvasionToRule converts a stored InvasionTrackingAPI back into the strict v2
// rule shape. "everything"/"boss" map to their flags; any other (non-event)
// grunt_type maps to {type_id, gender} via the reverse type-name map. (grunt_id
// mode resolved to a type name on write, so it is read back as type_id+gender —
// faithful to what is stored.)
func v2InvasionToRule(gd *gamedata.GameData, row *db.InvasionTrackingAPI) v2InvasionRule {
	clean := db.IsClean(row.Clean)
	edit := db.IsEdit(row.Clean)
	summary := db.IsSummary(row.Clean)

	rule := v2InvasionRule{
		Distance:              ptr(row.Distance),
		Template:              ptr(row.Template),
		Clean:                 ptr(clean),
		Edit:                  ptr(edit),
		Summary:               ptr(summary),
		OverrideLocationLabel: ptr(row.OverrideLocationLabel),
		OverrideAreas:         row.OverrideAreas,
	}

	switch strings.ToLower(row.GruntType) {
	case "everything":
		rule.Everything = ptr(true)
	case "boss":
		rule.Boss = ptr(true)
	default:
		if id, ok := typeNameToID(gd)[strings.ToLower(row.GruntType)]; ok {
			rule.TypeID = ptr(id)
		}
		rule.Gender = ptr(invasionGenderEnum.fromStored(row.Gender))
	}
	return rule
}

// RegisterV2TrackingInvasion registers the strict v2 invasion tracking endpoints.
// The Filter restricts every read path to NON-event rows (event rows belong to
// the /incident endpoint), so the two types coexist in the shared invasion table.
func RegisterV2TrackingInvasion(api huma.API, deps *TrackingDeps) {
	gd := gdFromDeps(deps)
	registerV2Tracking(api, deps, v2TrackingType[v2InvasionRule, db.InvasionTrackingAPI]{
		Name: "invasion",
		Store: func(d *TrackingDeps) store.TrackingStore[db.InvasionTrackingAPI] {
			return d.Tracking.Invasions
		},
		Translate: translateV2Invasion,
		ToRule: func(row *db.InvasionTrackingAPI) v2InvasionRule {
			return v2InvasionToRule(gd, row)
		},
		GetUID: store.InvasionGetUID,
		SetUID: store.InvasionSetUID,
		RowText: func(d *TrackingDeps, tr *i18n.Translator, row *db.InvasionTrackingAPI) string {
			return d.RowText.InvasionRowText(tr, toInvasionTracking(row))
		},
		Filter: func(row *db.InvasionTrackingAPI) bool {
			return !isEventGruntType(row.GruntType, gd)
		},
	})
}
