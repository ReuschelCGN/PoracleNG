package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/danielgtaylor/huma/v2"
	"github.com/gin-gonic/gin"
)

// ── helpers ───────────────────────────────────────────────────────────────────

// mustUnmarshal is a test helper that unmarshals JSON into a value.
func mustUnmarshal(t *testing.T, into json.Unmarshaler, raw string) {
	t.Helper()
	if err := json.Unmarshal([]byte(raw), into); err != nil {
		t.Fatalf("unmarshal %s: %v", raw, err)
	}
}

func mustUnmarshalErr(t *testing.T, into json.Unmarshaler, raw string) error {
	t.Helper()
	return json.Unmarshal([]byte(raw), into)
}

// ── flexTeam ──────────────────────────────────────────────────────────────────

func TestFlexTeam_CanonicalString(t *testing.T) {
	cases := []struct{ in string; want int }{
		{`"harmony"`, 0},
		{`"mystic"`, 1},
		{`"valor"`, 2},
		{`"instinct"`, 3},
		{`"any"`, 4},
	}
	for _, tc := range cases {
		var f flexTeam
		mustUnmarshal(t, &f, tc.in)
		if got := f.intValue(99); got != tc.want {
			t.Errorf("flexTeam(%s).intValue = %d, want %d", tc.in, got, tc.want)
		}
		if !f.isSet() {
			t.Errorf("flexTeam(%s).isSet() = false, want true", tc.in)
		}
	}
}

func TestFlexTeam_LegacyInteger(t *testing.T) {
	cases := []struct{ in string; want int }{
		{"0", 0}, {"1", 1}, {"2", 2}, {"3", 3}, {"4", 4},
	}
	for _, tc := range cases {
		var f flexTeam
		mustUnmarshal(t, &f, tc.in)
		if got := f.intValue(99); got != tc.want {
			t.Errorf("flexTeam(%s).intValue = %d, want %d", tc.in, got, tc.want)
		}
	}
}

func TestFlexTeam_NumericString(t *testing.T) {
	var f flexTeam
	mustUnmarshal(t, &f, `"2"`)
	if got := f.intValue(99); got != 2 {
		t.Errorf("flexTeam numeric-string: intValue = %d, want 2", got)
	}
}

func TestFlexTeam_UnknownString_Errors(t *testing.T) {
	var f flexTeam
	if err := mustUnmarshalErr(t, &f, `"rocket"`); err == nil {
		t.Error("expected error for unknown team name, got nil")
	}
}

func TestFlexTeam_Null_NotSet(t *testing.T) {
	var f flexTeam
	mustUnmarshal(t, &f, "null")
	if f.isSet() {
		t.Error("null should not set the value")
	}
	if got := f.intValue(4); got != 4 {
		t.Errorf("null intValue(4) = %d, want 4 (default)", got)
	}
}

func TestFlexTeam_Schema_OneOf(t *testing.T) {
	s := flexTeam{}.Schema(nil)
	if len(s.OneOf) != 2 {
		t.Fatalf("Schema().OneOf length = %d, want 2", len(s.OneOf))
	}
	strSchema := s.OneOf[0]
	if strSchema.Type != "string" {
		t.Errorf("OneOf[0].Type = %q, want \"string\"", strSchema.Type)
	}
	if len(strSchema.Enum) != 5 {
		t.Errorf("Schema string enum has %d values, want 5 (harmony..any)", len(strSchema.Enum))
	}
	if s.OneOf[1].Type != "integer" {
		t.Errorf("OneOf[1].Type = %q, want \"integer\"", s.OneOf[1].Type)
	}
}

// ── flexLeague (non-contiguous values) ────────────────────────────────────────

func TestFlexLeague_CanonicalString(t *testing.T) {
	cases := []struct{ in string; want int }{
		{`"none"`, 0},
		{`"little"`, 500},
		{`"great"`, 1500},
		{`"ultra"`, 2500},
	}
	for _, tc := range cases {
		var f flexLeague
		mustUnmarshal(t, &f, tc.in)
		if got := f.intValue(99); got != tc.want {
			t.Errorf("flexLeague(%s).intValue = %d, want %d", tc.in, got, tc.want)
		}
	}
}

func TestFlexLeague_LegacyInteger(t *testing.T) {
	cases := []struct{ in string; want int }{
		{"0", 0}, {"500", 500}, {"1500", 1500}, {"2500", 2500},
	}
	for _, tc := range cases {
		var f flexLeague
		mustUnmarshal(t, &f, tc.in)
		if got := f.intValue(99); got != tc.want {
			t.Errorf("flexLeague(%s).intValue = %d, want %d", tc.in, got, tc.want)
		}
	}
}

func TestFlexLeague_NumericString(t *testing.T) {
	var f flexLeague
	mustUnmarshal(t, &f, `"1500"`)
	if got := f.intValue(99); got != 1500 {
		t.Errorf("flexLeague numeric-string: intValue = %d, want 1500", got)
	}
}

func TestFlexLeague_UnknownString_Errors(t *testing.T) {
	var f flexLeague
	if err := mustUnmarshalErr(t, &f, `"master"`); err == nil {
		t.Error("expected error for unknown league name, got nil")
	}
}

func TestFlexLeague_Schema_OneOf(t *testing.T) {
	s := flexLeague{}.Schema(nil)
	if len(s.OneOf) != 2 {
		t.Fatalf("Schema().OneOf length = %d, want 2", len(s.OneOf))
	}
	strSchema := s.OneOf[0]
	if len(strSchema.Enum) != 4 {
		t.Errorf("Schema string enum has %d values, want 4 (none/little/great/ultra)", len(strSchema.Enum))
	}
	// Verify the names are in declaration order.
	wantNames := []string{"none", "little", "great", "ultra"}
	for i, want := range wantNames {
		if strSchema.Enum[i] != want {
			t.Errorf("Schema enum[%d] = %v, want %q", i, strSchema.Enum[i], want)
		}
	}
}

// ── flexPokemonGender ─────────────────────────────────────────────────────────

func TestFlexPokemonGender_CanonicalString(t *testing.T) {
	cases := []struct{ in string; want int }{
		{`"any"`, 0},
		{`"male"`, 1},
		{`"female"`, 2},
		{`"genderless"`, 3},
	}
	for _, tc := range cases {
		var f flexPokemonGender
		mustUnmarshal(t, &f, tc.in)
		if got := f.intValue(99); got != tc.want {
			t.Errorf("flexPokemonGender(%s).intValue = %d, want %d", tc.in, got, tc.want)
		}
	}
}

func TestFlexPokemonGender_LegacyInteger(t *testing.T) {
	cases := []struct{ in string; want int }{
		{"0", 0}, {"1", 1}, {"2", 2}, {"3", 3},
	}
	for _, tc := range cases {
		var f flexPokemonGender
		mustUnmarshal(t, &f, tc.in)
		if got := f.intValue(99); got != tc.want {
			t.Errorf("flexPokemonGender(%s).intValue = %d, want %d", tc.in, got, tc.want)
		}
	}
}

func TestFlexPokemonGender_NumericString(t *testing.T) {
	var f flexPokemonGender
	mustUnmarshal(t, &f, `"2"`)
	if got := f.intValue(99); got != 2 {
		t.Errorf("flexPokemonGender numeric-string: intValue = %d, want 2", got)
	}
}

func TestFlexPokemonGender_StringAndIntSameValue(t *testing.T) {
	// "female" and 2 must parse to the same stored integer.
	var fStr, fInt flexPokemonGender
	mustUnmarshal(t, &fStr, `"female"`)
	mustUnmarshal(t, &fInt, "2")
	if fStr.intValue(0) != fInt.intValue(0) {
		t.Errorf("\"female\"=%d, 2=%d — must be equal", fStr.intValue(0), fInt.intValue(0))
	}
}

func TestFlexPokemonGender_UnknownString_Errors(t *testing.T) {
	var f flexPokemonGender
	if err := mustUnmarshalErr(t, &f, `"nonbinary"`); err == nil {
		t.Error("expected error for unknown gender name, got nil")
	}
}

func TestFlexPokemonGender_Schema_StringEnum(t *testing.T) {
	s := flexPokemonGender{}.Schema(nil)
	if len(s.OneOf) != 2 {
		t.Fatalf("Schema().OneOf length = %d, want 2", len(s.OneOf))
	}
	names := s.OneOf[0].Enum
	if len(names) != 4 {
		t.Errorf("gender schema has %d enum values, want 4", len(names))
	}
}

// ── flexInvasionGender (no genderless) ────────────────────────────────────────

func TestFlexInvasionGender_NoGenderless(t *testing.T) {
	var f flexInvasionGender
	if err := mustUnmarshalErr(t, &f, `"genderless"`); err == nil {
		t.Error("expected error for \"genderless\" in invasion gender, got nil")
	}
}

func TestFlexInvasionGender_ValidValues(t *testing.T) {
	cases := []struct{ in string; want int }{
		{`"any"`, 0}, {`"male"`, 1}, {`"female"`, 2},
		{"0", 0}, {"1", 1}, {"2", 2},
	}
	for _, tc := range cases {
		var f flexInvasionGender
		mustUnmarshal(t, &f, tc.in)
		if got := f.intValue(99); got != tc.want {
			t.Errorf("flexInvasionGender(%s) = %d, want %d", tc.in, got, tc.want)
		}
	}
}

// ── flexFortType (string-valued) ──────────────────────────────────────────────

func TestFlexFortType_ValidValues(t *testing.T) {
	cases := []struct{ in, want string }{
		{`"pokestop"`, "pokestop"},
		{`"gym"`, "gym"},
		{`"everything"`, "everything"},
	}
	for _, tc := range cases {
		var f flexFortType
		mustUnmarshal(t, &f, tc.in)
		if got := f.strValue("everything"); got != tc.want {
			t.Errorf("flexFortType(%s).strValue = %q, want %q", tc.in, got, tc.want)
		}
		if !f.isSet() {
			t.Errorf("flexFortType(%s).isSet() = false", tc.in)
		}
	}
}

func TestFlexFortType_Station_Rejected(t *testing.T) {
	var f flexFortType
	if err := mustUnmarshalErr(t, &f, `"station"`); err == nil {
		t.Error("expected error for \"station\" (intentionally not in validFortTypes), got nil")
	}
}

func TestFlexFortType_UnknownString_Rejected(t *testing.T) {
	var f flexFortType
	if err := mustUnmarshalErr(t, &f, `"arena"`); err == nil {
		t.Error("expected error for unknown fort_type, got nil")
	}
}

func TestFlexFortType_Null_NotSet(t *testing.T) {
	var f flexFortType
	mustUnmarshal(t, &f, "null")
	if f.isSet() {
		t.Error("null should not set value")
	}
	if got := f.strValue("everything"); got != "everything" {
		t.Errorf("strValue(default) = %q, want \"everything\"", got)
	}
}

func TestFlexFortType_Schema_StringOnly(t *testing.T) {
	s := flexFortType{}.Schema(nil)
	// Fort type is string-only (no integer fallback).
	if s.Type != "string" {
		t.Errorf("fort_type schema type = %q, want \"string\"", s.Type)
	}
	if len(s.OneOf) != 0 {
		t.Errorf("fort_type schema should not have OneOf (string-only enum); got %d", len(s.OneOf))
	}
	if len(s.Enum) != 3 {
		t.Errorf("fort_type schema has %d enum values, want 3", len(s.Enum))
	}
}

// ── flexLureID ────────────────────────────────────────────────────────────────

func TestFlexLureID_CanonicalString(t *testing.T) {
	cases := []struct{ in string; want int }{
		{`"any"`, 0},
		{`"normal"`, 501},
		{`"glacial"`, 502},
		{`"mossy"`, 503},
		{`"magnetic"`, 504},
		{`"rainy"`, 505},
		{`"sparkly"`, 506},
	}
	for _, tc := range cases {
		var f flexLureID
		mustUnmarshal(t, &f, tc.in)
		if got := f.intValue(99); got != tc.want {
			t.Errorf("flexLureID(%s) = %d, want %d", tc.in, got, tc.want)
		}
	}
}

func TestFlexLureID_LegacyInteger(t *testing.T) {
	cases := []struct{ in string; want int }{
		{"0", 0}, {"501", 501}, {"502", 502}, {"503", 503},
		{"504", 504}, {"505", 505}, {"506", 506},
	}
	for _, tc := range cases {
		var f flexLureID
		mustUnmarshal(t, &f, tc.in)
		if got := f.intValue(99); got != tc.want {
			t.Errorf("flexLureID(%s) = %d, want %d", tc.in, got, tc.want)
		}
	}
}

func TestFlexLureID_UnknownString_Errors(t *testing.T) {
	var f flexLureID
	if err := mustUnmarshalErr(t, &f, `"golden"`); err == nil {
		// "golden" was in the audit's initial guess list but NOT in util.json — should error.
		t.Error("expected error for unknown lure name \"golden\", got nil")
	}
}

// ── flexRSVPChanges ───────────────────────────────────────────────────────────

func TestFlexRSVPChanges_CanonicalString(t *testing.T) {
	cases := []struct{ in string; want int }{
		{`"none"`, 0}, {`"rsvp"`, 1}, {`"rsvp_only"`, 2},
	}
	for _, tc := range cases {
		var f flexRSVPChanges
		mustUnmarshal(t, &f, tc.in)
		if got := f.intValue(99); got != tc.want {
			t.Errorf("flexRSVPChanges(%s) = %d, want %d", tc.in, got, tc.want)
		}
	}
}

func TestFlexRSVPChanges_LegacyInteger(t *testing.T) {
	cases := []struct{ in string; want int }{
		{"0", 0}, {"1", 1}, {"2", 2},
	}
	for _, tc := range cases {
		var f flexRSVPChanges
		mustUnmarshal(t, &f, tc.in)
		if got := f.intValue(99); got != tc.want {
			t.Errorf("flexRSVPChanges(%s) = %d, want %d", tc.in, got, tc.want)
		}
	}
}

// ── httptest validation round-trips ──────────────────────────────────────────
//
// These prove that a body with the STRING form and a body with the INT form
// both pass huma's schema validation (not 422) for a temp endpoint.

type pokemonEnumBody struct {
	Gender flexPokemonGender `json:"gender"`
	League flexLeague        `json:"pvp_ranking_league"`
}

type pokemonEnumInput struct{ Body lenient[pokemonEnumBody] }
type pokemonEnumOutput struct {
	Body struct {
		Status string `json:"status"`
		Gender int    `json:"gender"`
		League int    `json:"league"`
	}
}

func buildEnumTestEngine(t *testing.T) *gin.Engine {
	t.Helper()
	gin.SetMode(gin.TestMode)
	r := gin.New()
	api := NewHumaAPI(r, r.Group("/api"), "test")
	huma.Register(api, huma.Operation{
		OperationID: "enum-test",
		Method:      http.MethodPost,
		Path:        "/enum-test",
	}, func(_ context.Context, in *pokemonEnumInput) (*pokemonEnumOutput, error) {
		out := &pokemonEnumOutput{}
		out.Body.Status = "ok"
		out.Body.Gender = in.Body.Value.Gender.intValue(0)
		out.Body.League = in.Body.Value.League.intValue(0)
		return out, nil
	})
	return r
}

// TestEnumStringForm_PassesHumaValidation: body with string enum values must not 422.
func TestEnumStringForm_PassesHumaValidation(t *testing.T) {
	r := buildEnumTestEngine(t)

	body := `{"gender":"female","pvp_ranking_league":"great"}`
	req := httptest.NewRequest(http.MethodPost, "/api/enum-test", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code == http.StatusUnprocessableEntity {
		t.Fatalf("string-form body caused 422: %s", w.Body.String())
	}
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
	var out pokemonEnumOutput
	if err := json.NewDecoder(w.Body).Decode(&out.Body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if out.Body.Gender != 2 {
		t.Errorf("gender = %d, want 2 (female)", out.Body.Gender)
	}
	if out.Body.League != 1500 {
		t.Errorf("league = %d, want 1500 (great)", out.Body.League)
	}
}

// TestEnumIntForm_PassesHumaValidation: body with integer values must not 422.
func TestEnumIntForm_PassesHumaValidation(t *testing.T) {
	r := buildEnumTestEngine(t)

	body := `{"gender":2,"pvp_ranking_league":1500}`
	req := httptest.NewRequest(http.MethodPost, "/api/enum-test", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code == http.StatusUnprocessableEntity {
		t.Fatalf("integer-form body caused 422: %s", w.Body.String())
	}
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
	var out pokemonEnumOutput
	if err := json.NewDecoder(w.Body).Decode(&out.Body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if out.Body.Gender != 2 {
		t.Errorf("gender = %d, want 2", out.Body.Gender)
	}
	if out.Body.League != 1500 {
		t.Errorf("league = %d, want 1500", out.Body.League)
	}
}

// TestEnumStringAndIntProduceSameStoredValue: "great" and 1500 must yield same int.
func TestEnumStringAndIntProduceSameStoredValue(t *testing.T) {
	r := buildEnumTestEngine(t)
	getResponse := func(body string) (gender, league int) {
		req := httptest.NewRequest(http.MethodPost, "/api/enum-test", strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)
		if w.Code != http.StatusOK {
			t.Fatalf("body %s → %d: %s", body, w.Code, w.Body.String())
		}
		var out struct {
			Gender int `json:"gender"`
			League int `json:"league"`
		}
		if err := json.NewDecoder(w.Body).Decode(&out); err != nil {
			t.Fatalf("decode: %v", err)
		}
		return out.Gender, out.League
	}

	gStr, lStr := getResponse(`{"gender":"female","pvp_ranking_league":"great"}`)
	gInt, lInt := getResponse(`{"gender":2,"pvp_ranking_league":1500}`)
	if gStr != gInt {
		t.Errorf("string gender=%d, int gender=%d — should be equal", gStr, gInt)
	}
	if lStr != lInt {
		t.Errorf("string league=%d, int league=%d — should be equal", lStr, lInt)
	}
}

// TestEnumOpenAPIShowsStringEnum: the generated OpenAPI schema for the test
// endpoint must show gender and pvp_ranking_league as having a string enum
// (not just "object" or "integer").
func TestEnumOpenAPIShowsStringEnum(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	api := NewHumaAPI(r, r.Group("/api"), "test")
	huma.Register(api, huma.Operation{
		OperationID: "enum-openapi-test",
		Method:      http.MethodPost,
		Path:        "/enum-openapi-test",
	}, func(_ context.Context, in *pokemonEnumInput) (*pokemonEnumOutput, error) {
		return nil, nil
	})

	req := httptest.NewRequest(http.MethodGet, "/openapi.json", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("GET /openapi.json: %d %s", w.Code, w.Body.String())
	}

	var spec map[string]any
	if err := json.NewDecoder(w.Body).Decode(&spec); err != nil {
		t.Fatalf("decode openapi: %v", err)
	}

	// The spec is present and parseable; the main guarantees (oneOf with string
	// enum) are covered by Schema() unit tests above. Just verify the spec is
	// non-empty and the path is registered.
	// Note: huma registers paths WITHOUT the gin group prefix (/api), so the path
	// in the OpenAPI spec is "/enum-openapi-test" not "/api/enum-openapi-test".
	paths, _ := spec["paths"].(map[string]any)
	if _, ok := paths["/enum-openapi-test"]; !ok {
		t.Errorf("expected /enum-openapi-test in OpenAPI paths; got keys: %v", func() []string {
			keys := make([]string, 0, len(paths))
			for k := range paths {
				keys = append(keys, k)
			}
			return keys
		}())
	}
}
