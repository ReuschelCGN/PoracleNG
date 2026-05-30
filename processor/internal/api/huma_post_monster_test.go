package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"

	"github.com/pokemon/poracleng/processor/internal/config"
	"github.com/pokemon/poracleng/processor/internal/i18n"
	"github.com/pokemon/poracleng/processor/internal/rowtext"
	"github.com/pokemon/poracleng/processor/internal/store"
)

// buildHumaPostMonsterTestEngine creates a minimal gin+huma engine for POST tests.
// It wires a seeded MockHumanStore so the lookup path succeeds (user found →
// proceeds to the DB layer). The Tracking store is nil, so the handler will
// fail when it tries to query existing monsters — but that is fine: tests in
// this file either target 404 paths or assert on validation/parse behaviour
// before the DB is reached.
func buildHumaPostMonsterTestEngine(t *testing.T, humans store.HumanStore) *gin.Engine {
	t.Helper()
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(gin.Recovery())
	apiGroup := r.Group("/api")
	apiGroup.Use(RequireSecretGin(""))

	humaAPI := NewHumaAPI(r, apiGroup, "test")

	deps := &TrackingDeps{
		DB:           nil, // nil — only valid for 404/parse paths
		Humans:       humans,
		Config:       &config.Config{},
		RowText:      &rowtext.Generator{DefaultTemplateName: "1"},
		Translations: i18n.NewBundle(),
		Tracking:     nil, // nil — only valid for 404/parse paths
	}
	RegisterTrackingMonster(humaAPI, deps)
	return r
}

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
	r := buildHumaPostMonsterTestEngine(t, mock)

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
	r := buildHumaPostMonsterTestEngine(t, mock)

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
	r := buildHumaPostMonsterTestEngine(t, mock)

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
	r := buildHumaPostMonsterTestEngine(t, mock)

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
	r := buildHumaPostMonsterTestEngine(t, mock)

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
	r := buildHumaPostMonsterTestEngine(t, mock)

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

// TestPostMonster_SilentQuery_NotRejectedBy422: silent query param must not
// cause a 422 — proves query param binding.
func TestPostMonster_SilentQuery_NotRejectedBy422(t *testing.T) {
	mock := store.NewMockHumanStore()
	r := buildHumaPostMonsterTestEngine(t, mock)

	body := `{"pokemon_id":25}`
	req := httptest.NewRequest(http.MethodPost, "/api/tracking/pokemon/nobody?silent=1",
		strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code == http.StatusUnprocessableEntity {
		t.Fatalf("silent query param caused 422: %s", w.Body.String())
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
	r := buildHumaPostMonsterTestEngine(t, mock)

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
	boolTrue := true
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
	_ = boolTrue
}

// TestPostMonster_NoSchemaLeak: 404 error from POST must not contain $schema.
func TestPostMonster_NoSchemaLeak(t *testing.T) {
	mock := store.NewMockHumanStore()
	r := buildHumaPostMonsterTestEngine(t, mock)

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
