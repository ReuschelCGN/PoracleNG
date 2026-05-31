package api

// flex_enum.go — Lenient string-enum field types for the huma tracking API.
//
// Each enum field:
//   - Stores the same integer (or string for fort_type) the DB already holds.
//   - Accepts the CANONICAL wire form (a named string such as "great") AND the
//     LEGACY wire form (the raw integer such as 1500) AND a numeric string ("1500").
//   - Exposes Schema() → oneOf[{type:"string",enum:[names…]},{type:"integer"}] so
//     huma's JSON-schema validator admits BOTH forms before our UnmarshalJSON runs.
//   - Exposes intValue(default int) int and isSet() bool, mirroring flexInt.
//
// Rule on unknown string names: return an error (causes 422).  Out-of-range
// integers silently fall back to the supplied sentinel (matching how the gin
// handlers clamped values with flexInt).
//
// fort_type is stored as a string in the DB, so flexStringEnum has its own
// strValue(default string) string accessor instead of intValue.

import (
	"encoding/json"
	"fmt"
	"strconv"

	"github.com/danielgtaylor/huma/v2"
)

// ── shared helpers ────────────────────────────────────────────────────────────

// enumEntry maps a canonical string name to the integer value stored in the DB.
type enumEntry struct {
	Name  string
	Value int
}

// intEnumConfig describes a single integer-valued enum.  names is an ordered
// slice so the Schema() enum array is stable and the OpenAPI spec is deterministic.
type intEnumConfig struct {
	names  []string       // canonical names, in declaration order (drives Schema enum list)
	byName map[string]int // name → stored integer
	byInt  map[int]bool   // set of valid stored integers
}

// newIntEnumConfig builds an intEnumConfig from an ordered slice of entries.
func newIntEnumConfig(entries []enumEntry) intEnumConfig {
	names := make([]string, len(entries))
	byName := make(map[string]int, len(entries))
	byInt := make(map[int]bool, len(entries))
	for i, e := range entries {
		names[i] = e.Name
		byName[e.Name] = e.Value
		byInt[e.Value] = true
	}
	return intEnumConfig{names: names, byName: byName, byInt: byInt}
}

// parseIntEnum decodes raw JSON into the stored integer for an integer-valued
// enum field.  It accepts:
//   - a JSON string that is a known canonical name → stored int
//   - a JSON integer → stored int (if it is in the valid set; else 0 is returned
//     and the caller decides whether to error or clamp)
//   - a JSON numeric-string → same as integer path
//
// Returns (value, true) on success, or (0, false) when the input is an
// unrecognised string name (caller should return an error) or an out-of-range
// integer (caller may clamp/default).
//
// An explicit JSON null clears the field (returns 0, false), consistent with
// how flexInt handles null.
func parseIntEnum(cfg intEnumConfig, data []byte) (value int, ok bool, knownString bool, err error) {
	s := string(data)
	if s == "null" {
		return 0, false, false, nil
	}

	// Try JSON string first.
	var str string
	if jsonErr := json.Unmarshal(data, &str); jsonErr == nil {
		// Numeric string ("1500") → treat as integer path.
		if n, convErr := strconv.Atoi(str); convErr == nil {
			return n, cfg.byInt[n], false, nil
		}
		// Named string.
		if v, found := cfg.byName[str]; found {
			return v, true, true, nil
		}
		return 0, false, true, fmt.Errorf("unknown enum value %q; valid names: %v", str, cfg.names)
	}

	// Try JSON number.
	var num json.Number
	if jsonErr := json.Unmarshal(data, &num); jsonErr == nil {
		n, _ := strconv.Atoi(num.String())
		return n, cfg.byInt[n], false, nil
	}

	return 0, false, false, fmt.Errorf("cannot parse enum from %s", s)
}

// intEnumSchema builds the shared oneOf[{string enum},{integer}] schema used by
// every integer-valued enum field.  The description is included on the outer
// schema.
func intEnumSchema(cfg intEnumConfig, description string) *huma.Schema {
	// Build the string-enum schema with the list of canonical names.
	enumVals := make([]interface{}, len(cfg.names))
	for i, n := range cfg.names {
		enumVals[i] = n
	}
	return &huma.Schema{
		OneOf: []*huma.Schema{
			{Type: "string", Enum: enumVals},
			{Type: "integer"},
		},
		Description: description,
	}
}

// ── flexTeam ─────────────────────────────────────────────────────────────────

// teamEnum: harmony=0, mystic=1, valor=2, instinct=3, any=4.
// Used by raid, egg, and gym tracking.
var teamEnum = newIntEnumConfig([]enumEntry{
	{"harmony", 0},
	{"mystic", 1},
	{"valor", 2},
	{"instinct", 3},
	{"any", 4},
})

// flexTeam is a lenient string-enum field for the gym/raid/egg team column.
// Canonical wire form: string name ("mystic").
// Legacy-accepted: integer (0–4) or numeric string.
// Out-of-range integer: caller clamps to the type's sentinel (raid/egg→4, gym→error).
type flexTeam struct {
	value *int
}

func (f *flexTeam) UnmarshalJSON(data []byte) error {
	v, ok, isStr, err := parseIntEnum(teamEnum, data)
	if err != nil && isStr {
		return err // unknown name → propagate
	}
	if err != nil {
		return fmt.Errorf("flexTeam: %w", err)
	}
	if !ok && string(data) == "null" {
		f.value = nil
		return nil
	}
	f.value = &v
	return nil
}

func (f flexTeam) intValue(defaultVal int) int {
	if f.value == nil {
		return defaultVal
	}
	return *f.value
}

func (f flexTeam) isSet() bool { return f.value != nil }

// Schema implements huma.SchemaProvider.
func (flexTeam) Schema(_ huma.Registry) *huma.Schema {
	return intEnumSchema(teamEnum,
		"Team filter. Canonical: string name (\"mystic\"). Legacy: integer 0–4. 0=harmony, 1=mystic, 2=valor, 3=instinct, 4=any.")
}

// ── flexRSVPChanges ───────────────────────────────────────────────────────────

// rsvpChangesEnum: none=0, rsvp=1, rsvp_only=2.
var rsvpChangesEnum = newIntEnumConfig([]enumEntry{
	{"none", 0},
	{"rsvp", 1},
	{"rsvp_only", 2},
})

// flexRSVPChanges is a lenient string-enum field for the raid/egg rsvp_changes
// column.  Out-of-range integers are silently clamped to 0 by the caller
// (matching the existing gin-handler behaviour).
type flexRSVPChanges struct {
	value *int
}

func (f *flexRSVPChanges) UnmarshalJSON(data []byte) error {
	v, ok, isStr, err := parseIntEnum(rsvpChangesEnum, data)
	if err != nil && isStr {
		return err
	}
	if err != nil {
		return fmt.Errorf("flexRSVPChanges: %w", err)
	}
	if !ok && string(data) == "null" {
		f.value = nil
		return nil
	}
	f.value = &v
	return nil
}

func (f flexRSVPChanges) intValue(defaultVal int) int {
	if f.value == nil {
		return defaultVal
	}
	return *f.value
}

func (f flexRSVPChanges) isSet() bool { return f.value != nil }

// Schema implements huma.SchemaProvider.
func (flexRSVPChanges) Schema(_ huma.Registry) *huma.Schema {
	return intEnumSchema(rsvpChangesEnum,
		"RSVP change tracking mode. Canonical: \"none\" | \"rsvp\" | \"rsvp_only\". Legacy: integer 0–2.")
}

// ── flexPokemonGender ─────────────────────────────────────────────────────────

// pokemonGenderEnum: any=0, male=1, female=2, genderless=3.
// Used by pokemon tracking (all 4 values).
var pokemonGenderEnum = newIntEnumConfig([]enumEntry{
	{"any", 0},
	{"male", 1},
	{"female", 2},
	{"genderless", 3},
})

// flexPokemonGender is a lenient string-enum for the pokemon gender column
// (values 0–3; genderless only exists for pokemon, not invasion).
type flexPokemonGender struct {
	value *int
}

func (f *flexPokemonGender) UnmarshalJSON(data []byte) error {
	v, ok, isStr, err := parseIntEnum(pokemonGenderEnum, data)
	if err != nil && isStr {
		return err
	}
	if err != nil {
		return fmt.Errorf("flexPokemonGender: %w", err)
	}
	if !ok && string(data) == "null" {
		f.value = nil
		return nil
	}
	f.value = &v
	return nil
}

func (f flexPokemonGender) intValue(defaultVal int) int {
	if f.value == nil {
		return defaultVal
	}
	return *f.value
}

func (f flexPokemonGender) isSet() bool { return f.value != nil }

// Schema implements huma.SchemaProvider.
func (flexPokemonGender) Schema(_ huma.Registry) *huma.Schema {
	return intEnumSchema(pokemonGenderEnum,
		"Gender filter for pokemon. Canonical: \"any\" | \"male\" | \"female\" | \"genderless\". Legacy: integer 0–3.")
}

// ── flexInvasionGender ────────────────────────────────────────────────────────

// invasionGenderEnum: any=0, male=1, female=2.
// Invasion gender does NOT include genderless (only pokemon does).
var invasionGenderEnum = newIntEnumConfig([]enumEntry{
	{"any", 0},
	{"male", 1},
	{"female", 2},
})

// flexInvasionGender is a lenient string-enum for the invasion gender column
// (values 0–2; no genderless).
type flexInvasionGender struct {
	value *int
}

func (f *flexInvasionGender) UnmarshalJSON(data []byte) error {
	v, ok, isStr, err := parseIntEnum(invasionGenderEnum, data)
	if err != nil && isStr {
		return err
	}
	if err != nil {
		return fmt.Errorf("flexInvasionGender: %w", err)
	}
	if !ok && string(data) == "null" {
		f.value = nil
		return nil
	}
	f.value = &v
	return nil
}

func (f flexInvasionGender) intValue(defaultVal int) int {
	if f.value == nil {
		return defaultVal
	}
	return *f.value
}

func (f flexInvasionGender) isSet() bool { return f.value != nil }

// Schema implements huma.SchemaProvider.
func (flexInvasionGender) Schema(_ huma.Registry) *huma.Schema {
	return intEnumSchema(invasionGenderEnum,
		"Gender filter for invasions. Canonical: \"any\" | \"male\" | \"female\". Legacy: integer 0–2. Note: \"genderless\" is not valid here (pokemon only).")
}

// ── flexLeague ────────────────────────────────────────────────────────────────

// leagueEnum: none=0, little=500, great=1500, ultra=2500.
// The stored integer IS the league's CP cap — values are non-contiguous.
var leagueEnum = newIntEnumConfig([]enumEntry{
	{"none", 0},
	{"little", 500},
	{"great", 1500},
	{"ultra", 2500},
})

// flexLeague is a lenient string-enum for pvp_ranking_league.
// The integer stored in the DB is the CP cap (0=IV mode, 500=little, 1500=great, 2500=ultra).
// Out-of-range integers fall back to 0 (no league / IV mode) — caller applies this.
type flexLeague struct {
	value *int
}

func (f *flexLeague) UnmarshalJSON(data []byte) error {
	v, ok, isStr, err := parseIntEnum(leagueEnum, data)
	if err != nil && isStr {
		return err
	}
	if err != nil {
		return fmt.Errorf("flexLeague: %w", err)
	}
	if !ok && string(data) == "null" {
		f.value = nil
		return nil
	}
	f.value = &v
	return nil
}

func (f flexLeague) intValue(defaultVal int) int {
	if f.value == nil {
		return defaultVal
	}
	return *f.value
}

func (f flexLeague) isSet() bool { return f.value != nil }

// Schema implements huma.SchemaProvider.
func (flexLeague) Schema(_ huma.Registry) *huma.Schema {
	return intEnumSchema(leagueEnum,
		"PVP league. Canonical: \"none\" | \"little\" | \"great\" | \"ultra\". Legacy: integer CP cap (0/500/1500/2500). 0=IV mode (no PVP filter).")
}

// ── flexRewardType ────────────────────────────────────────────────────────────

// rewardTypeEnum: item=2, stardust=3, candy=4, pokemon=7, mega_energy=12.
// Non-contiguous values; handler returns 400 for values not in this set.
var rewardTypeEnum = newIntEnumConfig([]enumEntry{
	{"item", 2},
	{"stardust", 3},
	{"candy", 4},
	{"pokemon", 7},
	{"mega_energy", 12},
})

// flexRewardType is a lenient string-enum for quest reward_type.
// The handler validates the integer is in the valid set and returns 400 otherwise.
type flexRewardType struct {
	value *int
}

func (f *flexRewardType) UnmarshalJSON(data []byte) error {
	v, ok, isStr, err := parseIntEnum(rewardTypeEnum, data)
	if err != nil && isStr {
		return err
	}
	if err != nil {
		return fmt.Errorf("flexRewardType: %w", err)
	}
	if !ok && string(data) == "null" {
		f.value = nil
		return nil
	}
	f.value = &v
	return nil
}

func (f flexRewardType) intValue(defaultVal int) int {
	if f.value == nil {
		return defaultVal
	}
	return *f.value
}

func (f flexRewardType) isSet() bool { return f.value != nil }

// Schema implements huma.SchemaProvider.
func (flexRewardType) Schema(_ huma.Registry) *huma.Schema {
	return intEnumSchema(rewardTypeEnum,
		"Quest reward type. Canonical: \"item\" | \"stardust\" | \"candy\" | \"pokemon\" | \"mega_energy\". Legacy: integer (2/3/4/7/12).")
}

// ── flexLureID ────────────────────────────────────────────────────────────────

// lureIDEnum: any=0 plus lure item IDs 501–506.
// String names are derived from resources/data/util.json "lures" entries
// (display name lowercased, "Lure" suffix removed):
//   501 → "normal"  (util.json: "Normal Lure")
//   502 → "glacial" (util.json: "Glacial Lure")
//   503 → "mossy"   (util.json: "Mossy Lure")
//   504 → "magnetic"(util.json: "Magnetic Lure")
//   505 → "rainy"   (util.json: "Rainy Lure")
//   506 → "sparkly" (util.json: "Sparkly Lure")
var lureIDEnum = newIntEnumConfig([]enumEntry{
	{"any", 0},
	{"normal", 501},
	{"glacial", 502},
	{"mossy", 503},
	{"magnetic", 504},
	{"rainy", 505},
	{"sparkly", 506},
})

// flexLureID is a lenient string-enum for lure_id.
// The handler validates the integer is in the valid set and returns 400 otherwise.
type flexLureID struct {
	value *int
}

func (f *flexLureID) UnmarshalJSON(data []byte) error {
	v, ok, isStr, err := parseIntEnum(lureIDEnum, data)
	if err != nil && isStr {
		return err
	}
	if err != nil {
		return fmt.Errorf("flexLureID: %w", err)
	}
	if !ok && string(data) == "null" {
		f.value = nil
		return nil
	}
	f.value = &v
	return nil
}

func (f flexLureID) intValue(defaultVal int) int {
	if f.value == nil {
		return defaultVal
	}
	return *f.value
}

func (f flexLureID) isSet() bool { return f.value != nil }

// Schema implements huma.SchemaProvider.
func (flexLureID) Schema(_ huma.Registry) *huma.Schema {
	return intEnumSchema(lureIDEnum,
		"Lure type. Canonical: \"any\" | \"normal\" | \"glacial\" | \"mossy\" | \"magnetic\" | \"rainy\" | \"sparkly\". Legacy: integer (0/501–506). Names derived from resources/data/util.json.")
}

// ── flexFortType — string-valued enum ────────────────────────────────────────

// Fort type is stored as a VARCHAR string in the DB, not an integer.
// Valid API values: "pokestop", "gym", "everything".
// Note: the bot command also accepts "station" but the API handler intentionally
// rejects it (see signed-off decision: PRESERVE; "station" is not in validFortTypes).
var validFortTypeSet = map[string]bool{
	"pokestop":    true,
	"gym":         true,
	"everything":  true,
}

// validFortTypeNames is the ordered list for the Schema enum array.
var validFortTypeNames = []string{"pokestop", "gym", "everything"}

// flexFortType is a string-enum field for fort_type.  Unlike the integer enums,
// it stores a string value and exposes strValue(default string) string.
//
// Only "pokestop", "gym", and "everything" are valid.  Any other string is
// rejected with an error (causes 422 when huma validates the body).
// No legacy-integer form exists for this field.
type flexFortType struct {
	value *string
}

func (f *flexFortType) UnmarshalJSON(data []byte) error {
	s := string(data)
	if s == "null" {
		f.value = nil
		return nil
	}
	var str string
	if err := json.Unmarshal(data, &str); err != nil {
		return fmt.Errorf("flexFortType: expected a string, got %s", s)
	}
	if !validFortTypeSet[str] {
		return fmt.Errorf("unknown fort_type %q; valid values: pokestop, gym, everything", str)
	}
	f.value = &str
	return nil
}

func (f flexFortType) strValue(defaultVal string) string {
	if f.value == nil {
		return defaultVal
	}
	return *f.value
}

func (f flexFortType) isSet() bool { return f.value != nil }

// Schema implements huma.SchemaProvider.
// Fort type is a string-only enum; there is no legacy integer form.
func (flexFortType) Schema(_ huma.Registry) *huma.Schema {
	enumVals := make([]interface{}, len(validFortTypeNames))
	for i, n := range validFortTypeNames {
		enumVals[i] = n
	}
	return &huma.Schema{
		Type:        "string",
		Enum:        enumVals,
		Description: "Fort type filter. Valid values: \"pokestop\" | \"gym\" | \"everything\". Note: \"station\" is accepted by the bot command but rejected by the API.",
	}
}
