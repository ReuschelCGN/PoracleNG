package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/pokemon/poracleng/processor/internal/store"
)

// ── collapseClean unit tests ─────────────────────────────────────────────────

// TestCollapseClean covers the full truth-table for collapseClean.
func TestCollapseClean(t *testing.T) {
	boolPtr := func(b bool) *bool { return &b }

	cases := []struct {
		name    string
		clean   flexBool
		edit    *bool
		summary *bool
		want    int
	}{
		{
			name:  "bool true → 1",
			clean: mustFlexBool(t, "true"),
			want:  1,
		},
		{
			name:  "bool false → 0",
			clean: mustFlexBool(t, "false"),
			want:  0,
		},
		{
			name:  "legacy int 3 preserved → 3",
			clean: mustFlexBool(t, "3"),
			want:  3,
		},
		{
			name: "edit→bit2",
			edit: boolPtr(true),
			want: 2,
		},
		{
			name:    "summary→bit4",
			summary: boolPtr(true),
			want:    4,
		},
		{
			name:    "all→7",
			clean:   mustFlexBool(t, "true"),
			edit:    boolPtr(true),
			summary: boolPtr(true),
			want:    7,
		},
		{
			name:    "legacy-int-1 + summary→5",
			clean:   mustFlexBool(t, "1"),
			summary: boolPtr(true),
			want:    5,
		},
		{
			name:    "edit false → no bit2",
			clean:   mustFlexBool(t, "true"),
			edit:    boolPtr(false),
			summary: boolPtr(false),
			want:    1,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := collapseClean(tc.clean, tc.edit, tc.summary)
			if got != tc.want {
				t.Errorf("collapseClean(%v, %v, %v) = %d, want %d",
					tc.clean, tc.edit, tc.summary, got, tc.want)
			}
		})
	}
}

// mustFlexBool is a test helper that unmarshals a JSON token into a flexBool.
func mustFlexBool(t *testing.T, s string) flexBool {
	t.Helper()
	var f flexBool
	if err := json.Unmarshal([]byte(s), &f); err != nil {
		t.Fatalf("mustFlexBool(%q): %v", s, err)
	}
	return f
}

// ── POST validation / parse boundary tests ──────────────────────────────────

// TestPostMonster_404_UnknownUser: POST to an unknown user returns 404 with
// the legacy error envelope — proves routing, method binding, and error shape.
func TestPostMonster_404_UnknownUser(t *testing.T) {
	mock := store.NewMockHumanStore()
	r := buildHumaTestEngine(t, mock, true, RegisterTrackingMonster)

	body := `{"pokemon_id":25,"min_iv":90}`
	req := httptest.NewRequest(http.MethodPost, "/api/tracking/pokemon/nobody",
		strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d: %s", w.Code, w.Body.String())
	}

	var got map[string]any
	if err := json.NewDecoder(w.Body).Decode(&got); err != nil {
		t.Fatalf("decode body: %v", err)
	}
	if got["status"] != "error" {
		t.Errorf("status = %v, want \"error\"", got["status"])
	}
	if got["message"] != "User not found" {
		t.Errorf("message = %v, want \"User not found\"", got["message"])
	}
}

// TestPostMonster_SingleObject_NotRejectedBy422: A single rule object body
// must NOT produce a 422 (validation failure). It will 404 (unknown user) or
// 500 (nil DB reached), but never 422.
func TestPostMonster_SingleObject_NotRejectedBy422(t *testing.T) {
	mock := store.NewMockHumanStore()
	r := buildHumaTestEngine(t, mock, true, RegisterTrackingMonster)

	body := `{"pokemon_id":25,"min_iv":"90","clean":true,"edit":true}`
	req := httptest.NewRequest(http.MethodPost, "/api/tracking/pokemon/nobody",
		strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code == http.StatusUnprocessableEntity {
		t.Fatalf("single-object body caused 422 (body should parse without validation failure): %s",
			w.Body.String())
	}
}

// TestPostMonster_ArrayBody_NotRejectedBy422: An array body must NOT produce a 422.
func TestPostMonster_ArrayBody_NotRejectedBy422(t *testing.T) {
	mock := store.NewMockHumanStore()
	r := buildHumaTestEngine(t, mock, true, RegisterTrackingMonster)

	body := `[{"pokemon_id":25,"min_iv":90},{"pokemon_id":1,"min_iv":0}]`
	req := httptest.NewRequest(http.MethodPost, "/api/tracking/pokemon/nobody",
		strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code == http.StatusUnprocessableEntity {
		t.Fatalf("array body caused 422 (body should parse without validation failure): %s",
			w.Body.String())
	}
}

// TestPostMonster_UnknownFieldInItem_NotRejectedBy422: Unknown fields in a
// rule item (additionalProperties) must NOT produce a 422.
func TestPostMonster_UnknownFieldInItem_NotRejectedBy422(t *testing.T) {
	mock := store.NewMockHumanStore()
	r := buildHumaTestEngine(t, mock, true, RegisterTrackingMonster)

	body := `{"pokemon_id":25,"min_iv":"90","unknown_field":"surprise","another":42}`
	req := httptest.NewRequest(http.MethodPost, "/api/tracking/pokemon/nobody",
		strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code == http.StatusUnprocessableEntity {
		t.Fatalf("unknown fields in item caused 422 (additionalProperties should be true): %s",
			w.Body.String())
	}
}

// TestPostMonster_ArrayWithUnknownFields_NotRejectedBy422: Unknown fields in
// an array item must NOT produce a 422.
func TestPostMonster_ArrayWithUnknownFields_NotRejectedBy422(t *testing.T) {
	mock := store.NewMockHumanStore()
	r := buildHumaTestEngine(t, mock, true, RegisterTrackingMonster)

	body := `[{"pokemon_id":25,"min_iv":90,"weird_client_field":"yes"}]`
	req := httptest.NewRequest(http.MethodPost, "/api/tracking/pokemon/nobody",
		strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code == http.StatusUnprocessableEntity {
		t.Fatalf("unknown fields in array item caused 422: %s", w.Body.String())
	}
}

// TestPostMonster_FlexFields_NotRejectedBy422: flex fields (string int, bool)
// must NOT produce a 422.
func TestPostMonster_FlexFields_NotRejectedBy422(t *testing.T) {
	mock := store.NewMockHumanStore()
	r := buildHumaTestEngine(t, mock, true, RegisterTrackingMonster)

	// min_iv as string, clean as bool, distance as string — all flex coercion.
	body := `{"pokemon_id":25,"min_iv":"90","clean":false,"distance":"500"}`
	req := httptest.NewRequest(http.MethodPost, "/api/tracking/pokemon/nobody",
		strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code == http.StatusUnprocessableEntity {
		t.Fatalf("flex-field body caused 422: %s", w.Body.String())
	}
}

// TestPostMonster_SilentQuery_NotRejectedBy422: silent=true must not cause a
// 422 — proves boolean query param binding.
func TestPostMonster_SilentQuery_NotRejectedBy422(t *testing.T) {
	mock := store.NewMockHumanStore()
	r := buildHumaTestEngine(t, mock, true, RegisterTrackingMonster)

	body := `{"pokemon_id":25}`
	req := httptest.NewRequest(http.MethodPost, "/api/tracking/pokemon/nobody?silent=true",
		strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code == http.StatusUnprocessableEntity {
		t.Fatalf("silent=true query param caused 422: %s", w.Body.String())
	}
}

// TestPostMonster_SuppressMessageQuery_NotRejectedBy422: suppressMessage query
// param must not cause a 422 — proves the alias param binding.
func TestPostMonster_SuppressMessageQuery_NotRejectedBy422(t *testing.T) {
	mock := store.NewMockHumanStore()
	r := buildHumaTestEngine(t, mock, true, RegisterTrackingMonster)

	body := `{"pokemon_id":25}`
	req := httptest.NewRequest(http.MethodPost, "/api/tracking/pokemon/nobody?suppressMessage=true",
		strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code == http.StatusUnprocessableEntity {
		t.Fatalf("suppressMessage query param caused 422: %s", w.Body.String())
	}
}

// TestPostMonster_MissingPokemonID_Rejected: A body without pokemon_id must be
// rejected. pokemon_id is the only required field in the schema. With our
// schema-level required=["pokemon_id"], huma rejects this with 422 before the
// handler runs. If the schema-level check is somehow bypassed, the handler's
// own errPokemonIDRequired guard returns 400. Either way, 2xx must NOT be returned.
func TestPostMonster_MissingPokemonID_Rejected(t *testing.T) {
	mock := store.NewMockHumanStore()
	mock.AddHuman(&store.Human{
		ID:               "u1",
		Type:             "discord:user",
		Name:             "TestUser",
		Enabled:          true,
		CurrentProfileNo: 0,
	})
	r := buildHumaTestEngine(t, mock, true, RegisterTrackingMonster)

	// Body has other valid fields but no pokemon_id.
	body := `{"min_iv":90,"max_iv":100}`
	req := httptest.NewRequest(http.MethodPost, "/api/tracking/pokemon/u1",
		strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	// Must be 400 (handler rejects) or 422 (schema-level required check).
	// Must NOT be 2xx or 404.
	if w.Code == http.StatusNotFound {
		t.Fatalf("known user returned 404: %s", w.Body.String())
	}
	if w.Code >= 200 && w.Code < 300 {
		t.Fatalf("missing pokemon_id was accepted (code %d); must be 400 or 422: %s",
			w.Code, w.Body.String())
	}
	if w.Code != http.StatusBadRequest && w.Code != http.StatusUnprocessableEntity {
		t.Fatalf("unexpected status %d for missing pokemon_id (want 400 or 422): %s",
			w.Code, w.Body.String())
	}
}

// TestPostMonster_MissingPokemonID_ArrayItem_Rejected: An array body where one
// item is missing pokemon_id must be rejected.
func TestPostMonster_MissingPokemonID_ArrayItem_Rejected(t *testing.T) {
	mock := store.NewMockHumanStore()
	mock.AddHuman(&store.Human{
		ID:               "u1",
		Type:             "discord:user",
		Name:             "TestUser",
		Enabled:          true,
		CurrentProfileNo: 0,
	})
	r := buildHumaTestEngine(t, mock, true, RegisterTrackingMonster)

	// Second item has no pokemon_id.
	body := `[{"pokemon_id":25,"min_iv":90},{"min_iv":50}]`
	req := httptest.NewRequest(http.MethodPost, "/api/tracking/pokemon/u1",
		strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code >= 200 && w.Code < 300 {
		t.Fatalf("array with item missing pokemon_id was accepted (code %d): %s",
			w.Code, w.Body.String())
	}
}

// TestPostMonster_SuccessEnvelopeKeys: When the user IS found, the handler
// proceeds to the store. With a nil Tracking store it panics; gin.Recovery
// returns 500. The test verifies we don't get 404 (user found) and not 422
// (body valid). The success envelope keys are verified via collapseClean unit
// tests + GET test for shape; for the POST the 200 path needs a real DB
// (sqlx, no mock interface) which is tested end-to-end at integration level.
func TestPostMonster_KnownUser_PastValidation(t *testing.T) {
	mock := store.NewMockHumanStore()
	mock.AddHuman(&store.Human{
		ID:               "u1",
		Type:             "discord:user",
		Name:             "TestUser",
		Enabled:          true,
		Language:         "en",
		CurrentProfileNo: 1,
	})
	r := buildHumaTestEngine(t, mock, true, RegisterTrackingMonster)

	body := `{"pokemon_id":25,"min_iv":90}`
	req := httptest.NewRequest(http.MethodPost, "/api/tracking/pokemon/u1",
		strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	// Must NOT be 404 (user was found) or 422 (body was valid).
	if w.Code == http.StatusNotFound {
		t.Fatalf("known user returned 404 — lookup broken: %s", w.Body.String())
	}
	if w.Code == http.StatusUnprocessableEntity {
		t.Fatalf("valid body caused 422: %s", w.Body.String())
	}
	// 500 is expected here (nil Tracking store nil-dereference) — that proves
	// the handler passed the human-lookup and body-parse gates.
}

// TestPostMonster_monsterRuleRows_UnmarshalSingle verifies the custom
// UnmarshalJSON for monsterRuleRows wraps a single object in a slice.
func TestPostMonster_monsterRuleRows_UnmarshalSingle(t *testing.T) {
	var rows monsterRuleRows
	if err := json.Unmarshal([]byte(`{"pokemon_id":25,"min_iv":90}`), &rows); err != nil {
		t.Fatalf("unmarshal single object: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("expected 1 row, got %d", len(rows))
	}
	if rows[0].PokemonID.intValue(0) != 25 {
		t.Errorf("pokemon_id = %v, want 25", rows[0].PokemonID)
	}
	if rows[0].MinIV.intValue(-1) != 90 {
		t.Errorf("min_iv = %v, want 90", rows[0].MinIV)
	}
}

// TestPostMonster_monsterRuleRows_UnmarshalArray verifies the custom
// UnmarshalJSON for monsterRuleRows handles an array body.
func TestPostMonster_monsterRuleRows_UnmarshalArray(t *testing.T) {
	var rows monsterRuleRows
	if err := json.Unmarshal([]byte(`[{"pokemon_id":1},{"pokemon_id":2}]`), &rows); err != nil {
		t.Fatalf("unmarshal array: %v", err)
	}
	if len(rows) != 2 {
		t.Fatalf("expected 2 rows, got %d", len(rows))
	}
	if rows[0].PokemonID.intValue(0) != 1 {
		t.Errorf("rows[0].pokemon_id = %v, want 1", rows[0].PokemonID)
	}
	if rows[1].PokemonID.intValue(0) != 2 {
		t.Errorf("rows[1].pokemon_id = %v, want 2", rows[1].PokemonID)
	}
}

// TestPostMonster_monsterRuleRows_UnmarshalUnknownFields verifies that unknown
// fields in items are silently discarded (additionalProperties tolerance).
func TestPostMonster_monsterRuleRows_UnmarshalUnknownFields(t *testing.T) {
	var rows monsterRuleRows
	err := json.Unmarshal(
		[]byte(`{"pokemon_id":99,"unknown_client_field":"ignored","another":42}`),
		&rows,
	)
	if err != nil {
		t.Fatalf("unexpected error with unknown fields: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("expected 1 row, got %d", len(rows))
	}
	if rows[0].PokemonID.intValue(0) != 99 {
		t.Errorf("pokemon_id = %v, want 99", rows[0].PokemonID)
	}
}

// TestPostMonster_monsterRuleRows_CleanEditSummary verifies that the
// clean/edit/summary fields are correctly deserialized from a rule object.
func TestPostMonster_monsterRuleRows_CleanEditSummary(t *testing.T) {
	var rows monsterRuleRows
	err := json.Unmarshal(
		[]byte(`{"pokemon_id":25,"clean":true,"edit":true,"summary":false}`),
		&rows,
	)
	if err != nil {
		t.Fatalf("unmarshal clean/edit/summary: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("expected 1 row, got %d", len(rows))
	}
	row := rows[0]
	packed := collapseClean(row.Clean, row.Edit, row.Summary)
	// clean=true → bit1=1, edit=true → bit2=2, summary=false → bit4=0 → total 3
	if packed != 3 {
		t.Errorf("collapseClean(true, &true, &false) = %d, want 3", packed)
	}
}

// TestPostMonster_NoSchemaLeak: 404 error from POST must not contain $schema.
func TestPostMonster_NoSchemaLeak(t *testing.T) {
	mock := store.NewMockHumanStore()
	r := buildHumaTestEngine(t, mock, true, RegisterTrackingMonster)

	body := `{"pokemon_id":25}`
	req := httptest.NewRequest(http.MethodPost, "/api/tracking/pokemon/nobody",
		strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	var got map[string]any
	if err := json.NewDecoder(w.Body).Decode(&got); err != nil {
		t.Fatalf("decode body: %v", err)
	}
	if _, has := got["$schema"]; has {
		t.Errorf("error body must not contain $schema field; full body: %v", got)
	}
}

// ── Pokemon enum field retrofit tests ────────────────────────────────────────
//
// These tests verify that gender and pvp_ranking_league accept BOTH the new
// canonical string form AND the legacy integer form without a 422.

// TestPostMonster_GenderStringForm_NotRejectedBy422: "gender":"female" must pass
// huma's schema validation.
func TestPostMonster_GenderStringForm_NotRejectedBy422(t *testing.T) {
	mock := store.NewMockHumanStore()
	r := buildHumaTestEngine(t, mock, true, RegisterTrackingMonster)

	body := `{"pokemon_id":25,"gender":"female"}`
	req := httptest.NewRequest(http.MethodPost, "/api/tracking/pokemon/nobody",
		strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code == http.StatusUnprocessableEntity {
		t.Fatalf("gender:\"female\" caused 422: %s", w.Body.String())
	}
}

// TestPostMonster_GenderIntForm_NotRejectedBy422: "gender":2 (legacy integer)
// must pass huma's schema validation.
func TestPostMonster_GenderIntForm_NotRejectedBy422(t *testing.T) {
	mock := store.NewMockHumanStore()
	r := buildHumaTestEngine(t, mock, true, RegisterTrackingMonster)

	body := `{"pokemon_id":25,"gender":2}`
	req := httptest.NewRequest(http.MethodPost, "/api/tracking/pokemon/nobody",
		strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code == http.StatusUnprocessableEntity {
		t.Fatalf("gender:2 caused 422: %s", w.Body.String())
	}
}

// TestPostMonster_GenderStringAndIntSameStoredValue: "gender":"female" and
// "gender":2 must both parse to the same stored integer (2).
func TestPostMonster_GenderStringAndIntSameStoredValue(t *testing.T) {
	var rowStr, rowInt monsterRuleRequest

	if err := json.Unmarshal([]byte(`{"pokemon_id":25,"gender":"female"}`), &rowStr); err != nil {
		t.Fatalf("unmarshal string form: %v", err)
	}
	if err := json.Unmarshal([]byte(`{"pokemon_id":25,"gender":2}`), &rowInt); err != nil {
		t.Fatalf("unmarshal int form: %v", err)
	}

	gStr := rowStr.Gender.intValue(0)
	gInt := rowInt.Gender.intValue(0)
	if gStr != 2 {
		t.Errorf("gender:\"female\" parsed to %d, want 2", gStr)
	}
	if gInt != 2 {
		t.Errorf("gender:2 parsed to %d, want 2", gInt)
	}
	if gStr != gInt {
		t.Errorf("string form gender=%d, int form gender=%d — must be equal", gStr, gInt)
	}
}

// TestPostMonster_LeagueStringForm_NotRejectedBy422: "pvp_ranking_league":"great"
// must pass huma's schema validation.
func TestPostMonster_LeagueStringForm_NotRejectedBy422(t *testing.T) {
	mock := store.NewMockHumanStore()
	r := buildHumaTestEngine(t, mock, true, RegisterTrackingMonster)

	body := `{"pokemon_id":25,"pvp_ranking_league":"great"}`
	req := httptest.NewRequest(http.MethodPost, "/api/tracking/pokemon/nobody",
		strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code == http.StatusUnprocessableEntity {
		t.Fatalf("pvp_ranking_league:\"great\" caused 422: %s", w.Body.String())
	}
}

// TestPostMonster_LeagueIntForm_NotRejectedBy422: "pvp_ranking_league":1500
// (legacy CP cap integer) must pass huma's schema validation.
func TestPostMonster_LeagueIntForm_NotRejectedBy422(t *testing.T) {
	mock := store.NewMockHumanStore()
	r := buildHumaTestEngine(t, mock, true, RegisterTrackingMonster)

	body := `{"pokemon_id":25,"pvp_ranking_league":1500}`
	req := httptest.NewRequest(http.MethodPost, "/api/tracking/pokemon/nobody",
		strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code == http.StatusUnprocessableEntity {
		t.Fatalf("pvp_ranking_league:1500 caused 422: %s", w.Body.String())
	}
}

// TestPostMonster_LeagueStringAndIntSameStoredValue: "pvp_ranking_league":"great"
// and "pvp_ranking_league":1500 must parse to the same stored integer (1500).
func TestPostMonster_LeagueStringAndIntSameStoredValue(t *testing.T) {
	var rowStr, rowInt monsterRuleRequest

	if err := json.Unmarshal([]byte(`{"pokemon_id":25,"pvp_ranking_league":"great"}`), &rowStr); err != nil {
		t.Fatalf("unmarshal string form: %v", err)
	}
	if err := json.Unmarshal([]byte(`{"pokemon_id":25,"pvp_ranking_league":1500}`), &rowInt); err != nil {
		t.Fatalf("unmarshal int form: %v", err)
	}

	lStr := rowStr.PVPRankingLeague.intValue(0)
	lInt := rowInt.PVPRankingLeague.intValue(0)
	if lStr != 1500 {
		t.Errorf("pvp_ranking_league:\"great\" parsed to %d, want 1500", lStr)
	}
	if lInt != 1500 {
		t.Errorf("pvp_ranking_league:1500 parsed to %d, want 1500", lInt)
	}
	if lStr != lInt {
		t.Errorf("string form league=%d, int form league=%d — must be equal", lStr, lInt)
	}
}

// TestPostMonster_OpenAPI_GenderAndLeagueAreStringEnums verifies that the
// generated OpenAPI schema for the pokemon endpoint shows gender and
// pvp_ranking_league as string enums (not raw objects or bare integers).
func TestPostMonster_OpenAPI_GenderAndLeagueAreStringEnums(t *testing.T) {
	mock := store.NewMockHumanStore()
	// We only need the huma API to get the spec; the exact endpoint doesn't matter.
	r := buildHumaTestEngine(t, mock, false, RegisterTrackingMonster)

	req := httptest.NewRequest(http.MethodGet, "/openapi.json", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("GET /openapi.json: %d %s", w.Code, w.Body.String())
	}

	specBody := w.Body.String()
	// The OpenAPI must contain the gender string enum values.
	for _, name := range []string{"any", "male", "female", "genderless"} {
		if !strings.Contains(specBody, `"`+name+`"`) {
			t.Errorf("OpenAPI spec missing gender enum value %q", name)
		}
	}
	// The OpenAPI must contain the league string enum values.
	for _, name := range []string{"none", "little", "great", "ultra"} {
		if !strings.Contains(specBody, `"`+name+`"`) {
			t.Errorf("OpenAPI spec missing pvp_ranking_league enum value %q", name)
		}
	}
}
