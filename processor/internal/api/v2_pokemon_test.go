package api

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"

	"github.com/danielgtaylor/huma/v2"
	"github.com/gin-gonic/gin"

	"github.com/pokemon/poracleng/processor/internal/config"
	"github.com/pokemon/poracleng/processor/internal/db"
	"github.com/pokemon/poracleng/processor/internal/gamedata"
	"github.com/pokemon/poracleng/processor/internal/i18n"
	"github.com/pokemon/poracleng/processor/internal/rowtext"
	"github.com/pokemon/poracleng/processor/internal/store"
)

// --- test harness ---------------------------------------------------------

// pushRecord captures a confirmation push (overriding v2SendConfirmation).
type pushRecord struct {
	target  string
	message string
}

// newV2PokemonTestAPI wires the strict v2 pokemon endpoints against mock stores
// through a REAL huma+gin engine. It returns the engine, the monster store
// (id-scoped), and a pointer to the slice that records confirmation pushes.
// The returned restore func resets the v2SendConfirmation seam.
func newV2PokemonTestAPI(t *testing.T) (*gin.Engine, *store.MockTrackingStore[db.MonsterTrackingAPI], *[]pushRecord, func()) {
	t.Helper()
	gin.SetMode(gin.TestMode)
	r := gin.New()
	humaAPI := NewHumaAPI(r, r.Group("/api"), "test")

	humans := store.NewMockHumanStore()
	humans.AddHuman(&store.Human{ID: "u1", Type: "discord:user", Name: "User1", Enabled: true, Language: "en", CurrentProfileNo: 1})
	humans.AddHuman(&store.Human{ID: "u2", Type: "discord:user", Name: "User2", Enabled: true, Language: "en", CurrentProfileNo: 1})

	monsterStore := store.NewMockTrackingStore[db.MonsterTrackingAPI](
		store.MonsterGetUID, store.MonsterSetUID,
	).WithIDScope(func(m *db.MonsterTrackingAPI) string { return m.ID })

	// Minimal non-nil GameData so MonsterRowText doesn't panic on GetMonster /
	// gender-emoji lookups. Empty maps are enough — rowtext tolerates misses.
	gd := &gamedata.GameData{
		Monsters: map[gamedata.MonsterKey]*gamedata.Monster{},
		Util: &gamedata.UtilData{
			Genders: map[int]gamedata.GenderInfo{
				1: {Emoji: "M"},
				2: {Emoji: "F"},
			},
		},
	}

	deps := &TrackingDeps{
		Humans:   humans,
		Tracking: &store.TrackingStores{Monsters: monsterStore},
		Config:   &config.Config{},
		RowText: &rowtext.Generator{
			GD:                  gd,
			Translations:        i18n.Load(""),
			DefaultTemplateName: "1",
		},
		Translations: i18n.Load(""),
		// Dispatcher nil; pushes go through the v2SendConfirmation seam below.
	}

	pushes := &[]pushRecord{}
	orig := v2SendConfirmation
	v2SendConfirmation = func(_ *TrackingDeps, human *store.HumanLite, message, _ string) {
		*pushes = append(*pushes, pushRecord{target: human.ID, message: message})
	}
	restore := func() { v2SendConfirmation = orig }

	RegisterV2TrackingPokemon(humaAPI, deps)
	return r, monsterStore, pushes, restore
}

func v2DoReq(t *testing.T, r http.Handler, method, path, body string) *httptest.ResponseRecorder {
	t.Helper()
	var rdr *bytes.Reader
	if body != "" {
		rdr = bytes.NewReader([]byte(body))
	} else {
		rdr = bytes.NewReader(nil)
	}
	req := httptest.NewRequest(method, path, rdr)
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	return w
}

func v2DecodeBody(t *testing.T, w *httptest.ResponseRecorder) map[string]any {
	t.Helper()
	var m map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &m); err != nil {
		t.Fatalf("decode body: %v\nraw: %s", err, w.Body.String())
	}
	return m
}

func v2RulesArray(t *testing.T, body map[string]any, key string) []map[string]any {
	t.Helper()
	raw, ok := body[key]
	if !ok {
		t.Fatalf("response missing %q: %v", key, body)
	}
	arr, ok := raw.([]any)
	if !ok {
		t.Fatalf("%q is not an array: %T", key, raw)
	}
	out := make([]map[string]any, len(arr))
	for i, e := range arr {
		out[i] = e.(map[string]any)
	}
	return out
}

// --- create (single + array) ----------------------------------------------

func TestV2Pokemon_CreateArray_OK(t *testing.T) {
	r, _, pushes, restore := newV2PokemonTestAPI(t)
	defer restore()

	body := `[{"pokemon_id":149,"min_iv":95,"gender":"female","clean":true},{"pokemon_id":384,"pvp_ranking_league":1500}]`
	w := v2DoReq(t, r, http.MethodPost, "/api/v2/humans/u1/tracking/pokemon", body)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
	resp := v2DecodeBody(t, w)
	created := v2RulesArray(t, resp, "created")
	if len(created) != 2 {
		t.Fatalf("expected 2 created, got %d: %v", len(created), resp)
	}
	for _, rule := range created {
		if _, ok := rule["uid"]; !ok {
			t.Fatalf("created rule missing uid: %v", rule)
		}
		if uidF, ok := rule["uid"].(float64); !ok || uidF <= 0 {
			t.Fatalf("created rule has invalid uid: %v", rule["uid"])
		}
	}
	// Confirmation push fired (not silent).
	if len(*pushes) != 1 {
		t.Fatalf("expected 1 push, got %d", len(*pushes))
	}
}

func TestV2Pokemon_CreateSingleElementArray_OK(t *testing.T) {
	r, ms, _, restore := newV2PokemonTestAPI(t)
	defer restore()

	w := v2DoReq(t, r, http.MethodPost, "/api/v2/humans/u1/tracking/pokemon", `[{"pokemon_id":25}]`)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
	rows := ms.AllRows()
	if len(rows) != 1 || rows[0].PokemonID != 25 {
		t.Fatalf("expected 1 stored row for pikachu, got %+v", rows)
	}
}

// --- strict rejection -------------------------------------------------------

func TestV2Pokemon_RejectsUnknownBodyField(t *testing.T) {
	r, _, _, restore := newV2PokemonTestAPI(t)
	defer restore()
	w := v2DoReq(t, r, http.MethodPost, "/api/v2/humans/u1/tracking/pokemon", `[{"pokemon_id":25,"bogus_field":1}]`)
	if w.Code != http.StatusUnprocessableEntity {
		t.Fatalf("expected 422 for unknown body field, got %d: %s", w.Code, w.Body.String())
	}
}

func TestV2Pokemon_RejectsWrongType(t *testing.T) {
	r, _, _, restore := newV2PokemonTestAPI(t)
	defer restore()
	w := v2DoReq(t, r, http.MethodPost, "/api/v2/humans/u1/tracking/pokemon", `[{"pokemon_id":25,"min_iv":"x"}]`)
	if w.Code != http.StatusUnprocessableEntity {
		t.Fatalf("expected 422 for string min_iv, got %d: %s", w.Code, w.Body.String())
	}
}

func TestV2Pokemon_RejectsMissingPokemonID(t *testing.T) {
	r, _, _, restore := newV2PokemonTestAPI(t)
	defer restore()
	w := v2DoReq(t, r, http.MethodPost, "/api/v2/humans/u1/tracking/pokemon", `[{"min_iv":95}]`)
	if w.Code != http.StatusUnprocessableEntity {
		t.Fatalf("expected 422 for missing pokemon_id, got %d: %s", w.Code, w.Body.String())
	}
}

func TestV2Pokemon_RejectsBadGender(t *testing.T) {
	r, _, _, restore := newV2PokemonTestAPI(t)
	defer restore()
	w := v2DoReq(t, r, http.MethodPost, "/api/v2/humans/u1/tracking/pokemon", `[{"pokemon_id":25,"gender":"alien"}]`)
	if w.Code != http.StatusUnprocessableEntity {
		t.Fatalf("expected 422 for bad gender, got %d: %s", w.Code, w.Body.String())
	}
}

func TestV2Pokemon_RejectsUnknownQueryParam(t *testing.T) {
	r, _, _, restore := newV2PokemonTestAPI(t)
	defer restore()
	w := v2DoReq(t, r, http.MethodGet, "/api/v2/humans/u1/tracking/pokemon?bogus=1", "")
	if w.Code != http.StatusUnprocessableEntity {
		t.Fatalf("expected 422 for unknown query param, got %d: %s", w.Code, w.Body.String())
	}
}

func TestV2Pokemon_RejectsEmptyArray(t *testing.T) {
	r, _, _, restore := newV2PokemonTestAPI(t)
	defer restore()
	w := v2DoReq(t, r, http.MethodPost, "/api/v2/humans/u1/tracking/pokemon", `[]`)
	if w.Code != http.StatusUnprocessableEntity {
		t.Fatalf("expected 422 for empty array, got %d: %s", w.Code, w.Body.String())
	}
}

// --- gender round-trip + bitmask + defaults --------------------------------

func TestV2Pokemon_GenderRoundTrip(t *testing.T) {
	r, ms, _, restore := newV2PokemonTestAPI(t)
	defer restore()

	v2DoReq(t, r, http.MethodPost, "/api/v2/humans/u1/tracking/pokemon", `[{"pokemon_id":25,"gender":"female"}]`)
	rows := ms.AllRows()
	if len(rows) != 1 || rows[0].Gender != 2 {
		t.Fatalf("expected stored gender 2 (female), got %+v", rows)
	}
	// Read it back: response gender should be "female".
	w := v2DoReq(t, r, http.MethodGet, "/api/v2/humans/u1/tracking/pokemon", "")
	out := v2RulesArray(t, v2DecodeBody(t, w), "rules")
	if out[0]["gender"] != "female" {
		t.Fatalf("expected gender female on read-back, got %v", out[0]["gender"])
	}
}

func TestV2Pokemon_CleanEditSummaryBitmask(t *testing.T) {
	r, ms, _, restore := newV2PokemonTestAPI(t)
	defer restore()

	v2DoReq(t, r, http.MethodPost, "/api/v2/humans/u1/tracking/pokemon", `[{"pokemon_id":25,"clean":true,"edit":true,"summary":true}]`)
	rows := ms.AllRows()
	if len(rows) != 1 || rows[0].Clean != 7 {
		t.Fatalf("expected clean bitmask 7 (clean+edit+summary), got %+v", rows)
	}

	// Only edit → bit 2.
	v2DoReq(t, r, http.MethodPost, "/api/v2/humans/u1/tracking/pokemon", `[{"pokemon_id":26,"edit":true}]`)
	for _, row := range ms.AllRows() {
		if row.PokemonID == 26 && row.Clean != 2 {
			t.Fatalf("expected edit-only clean bitmask 2, got %d", row.Clean)
		}
	}
}

func TestV2Pokemon_DefaultsOnOmission(t *testing.T) {
	r, ms, _, restore := newV2PokemonTestAPI(t)
	defer restore()

	v2DoReq(t, r, http.MethodPost, "/api/v2/humans/u1/tracking/pokemon", `[{"pokemon_id":25}]`)
	rows := ms.AllRows()
	if len(rows) != 1 {
		t.Fatalf("expected 1 row, got %d", len(rows))
	}
	row := rows[0]
	checks := map[string]struct{ got, want int }{
		"min_iv":    {row.MinIV, -1},
		"max_iv":    {row.MaxIV, 100},
		"max_cp":    {row.MaxCP, 9000},
		"max_level": {row.MaxLevel, 55},
		"max_atk":   {row.MaxATK, 15},
		"max_weight": {row.MaxWeight, 9000000},
		"rarity":    {row.Rarity, -1},
		"max_rarity": {row.MaxRarity, 6},
		"size":      {row.Size, -1},
		"max_size":  {row.MaxSize, 5},
		"pvp_best":  {row.PVPRankingBest, 1},
		"pvp_worst": {row.PVPRankingWorst, 4096},
		"gender":    {row.Gender, 0},
		"clean":     {row.Clean, 0},
		"distance":  {row.Distance, 0},
	}
	for name, c := range checks {
		if c.got != c.want {
			t.Errorf("default %s: got %d want %d", name, c.got, c.want)
		}
	}
	if row.Ping != "" {
		t.Errorf("ping should be server-managed empty, got %q", row.Ping)
	}
}

// TestV2Pokemon_OmittedPointerStaysNil verifies decision #2: with NO `default:`
// tag, huma leaves an omitted optional pointer nil at the handler boundary (so
// the handler's valueOr applies the documented default). This is the empirical
// check that we are NOT relying on huma auto-populating pointers.
func TestV2Pokemon_OmittedPointerStaysNil(t *testing.T) {
	var rule v2PokemonRule
	if err := json.Unmarshal([]byte(`{"pokemon_id":25}`), &rule); err != nil {
		t.Fatal(err)
	}
	if rule.MaxCP != nil {
		t.Fatalf("expected MaxCP nil on omission, got %v", *rule.MaxCP)
	}
	if rule.Gender != nil {
		t.Fatalf("expected Gender nil on omission, got %v", *rule.Gender)
	}
	// valueOr applies the documented default.
	if got := valueOr(rule.MaxCP, 9000); got != 9000 {
		t.Fatalf("valueOr default mismatch: %d", got)
	}
}

// --- list + get-by-uid ------------------------------------------------------

func TestV2Pokemon_ListShape(t *testing.T) {
	r, _, _, restore := newV2PokemonTestAPI(t)
	defer restore()
	v2DoReq(t, r, http.MethodPost, "/api/v2/humans/u1/tracking/pokemon", `[{"pokemon_id":25},{"pokemon_id":26}]`)
	w := v2DoReq(t, r, http.MethodGet, "/api/v2/humans/u1/tracking/pokemon", "")
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
	rules := v2RulesArray(t, v2DecodeBody(t, w), "rules")
	if len(rules) != 2 {
		t.Fatalf("expected 2 rules, got %d", len(rules))
	}
}

func TestV2Pokemon_GetByUID_OwnedAndCrossHuman(t *testing.T) {
	r, _, _, restore := newV2PokemonTestAPI(t)
	defer restore()

	// u1 creates a rule.
	w := v2DoReq(t, r, http.MethodPost, "/api/v2/humans/u1/tracking/pokemon", `[{"pokemon_id":25}]`)
	created := v2RulesArray(t, v2DecodeBody(t, w), "created")
	uid := int64(created[0]["uid"].(float64))

	// u1 can fetch it.
	pathOwn := "/api/v2/humans/u1/tracking/pokemon/" + itoa(uid)
	w = v2DoReq(t, r, http.MethodGet, pathOwn, "")
	if w.Code != http.StatusOK {
		t.Fatalf("owner get: expected 200, got %d: %s", w.Code, w.Body.String())
	}

	// u2 cannot fetch u1's uid → 404.
	pathOther := "/api/v2/humans/u2/tracking/pokemon/" + itoa(uid)
	w = v2DoReq(t, r, http.MethodGet, pathOther, "")
	if w.Code != http.StatusNotFound {
		t.Fatalf("cross-human get: expected 404, got %d: %s", w.Code, w.Body.String())
	}
}

// --- delete + bulk ----------------------------------------------------------

func TestV2Pokemon_DeleteSingle(t *testing.T) {
	r, ms, pushes, restore := newV2PokemonTestAPI(t)
	defer restore()
	w := v2DoReq(t, r, http.MethodPost, "/api/v2/humans/u1/tracking/pokemon", `[{"pokemon_id":25}]`)
	uid := int64(v2RulesArray(t, v2DecodeBody(t, w), "created")[0]["uid"].(float64))
	*pushes = (*pushes)[:0]

	w = v2DoReq(t, r, http.MethodDelete, "/api/v2/humans/u1/tracking/pokemon/"+itoa(uid), "")
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
	deleted := v2RulesArray(t, v2DecodeBody(t, w), "deleted")
	if len(deleted) != 1 {
		t.Fatalf("expected 1 deleted, got %d", len(deleted))
	}
	if len(ms.AllRows()) != 0 {
		t.Fatalf("row not deleted: %+v", ms.AllRows())
	}
	if len(*pushes) != 1 {
		t.Fatalf("expected removal push, got %d", len(*pushes))
	}
}

func TestV2Pokemon_DeleteCrossHuman404(t *testing.T) {
	r, ms, _, restore := newV2PokemonTestAPI(t)
	defer restore()
	w := v2DoReq(t, r, http.MethodPost, "/api/v2/humans/u1/tracking/pokemon", `[{"pokemon_id":25}]`)
	uid := int64(v2RulesArray(t, v2DecodeBody(t, w), "created")[0]["uid"].(float64))

	w = v2DoReq(t, r, http.MethodDelete, "/api/v2/humans/u2/tracking/pokemon/"+itoa(uid), "")
	if w.Code != http.StatusNotFound {
		t.Fatalf("expected 404 deleting another human's uid, got %d: %s", w.Code, w.Body.String())
	}
	if len(ms.AllRows()) != 1 {
		t.Fatalf("u1's row must survive a cross-human delete: %+v", ms.AllRows())
	}
}

func TestV2Pokemon_BulkDelete(t *testing.T) {
	r, ms, _, restore := newV2PokemonTestAPI(t)
	defer restore()
	w := v2DoReq(t, r, http.MethodPost, "/api/v2/humans/u1/tracking/pokemon", `[{"pokemon_id":25},{"pokemon_id":26},{"pokemon_id":27}]`)
	created := v2RulesArray(t, v2DecodeBody(t, w), "created")
	u0 := int64(created[0]["uid"].(float64))
	u1 := int64(created[1]["uid"].(float64))

	w = v2DoReq(t, r, http.MethodDelete, "/api/v2/humans/u1/tracking/pokemon?uid="+itoa(u0)+","+itoa(u1), "")
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
	deleted := v2RulesArray(t, v2DecodeBody(t, w), "deleted")
	if len(deleted) != 2 {
		t.Fatalf("expected 2 deleted, got %d", len(deleted))
	}
	if len(ms.AllRows()) != 1 {
		t.Fatalf("expected 1 surviving row, got %d", len(ms.AllRows()))
	}
}

func TestV2Pokemon_BulkDelete_BadUID(t *testing.T) {
	r, _, _, restore := newV2PokemonTestAPI(t)
	defer restore()
	w := v2DoReq(t, r, http.MethodDelete, "/api/v2/humans/u1/tracking/pokemon?uid=1,x,3", "")
	if w.Code != http.StatusUnprocessableEntity {
		t.Fatalf("expected 422 for non-int uid token, got %d: %s", w.Code, w.Body.String())
	}
}

func TestV2Pokemon_BulkDelete_MissingUID(t *testing.T) {
	r, _, _, restore := newV2PokemonTestAPI(t)
	defer restore()
	w := v2DoReq(t, r, http.MethodDelete, "/api/v2/humans/u1/tracking/pokemon", "")
	if w.Code != http.StatusUnprocessableEntity {
		t.Fatalf("expected 422 for missing uid, got %d: %s", w.Code, w.Body.String())
	}
}

// --- PUT full-replace -------------------------------------------------------

func TestV2Pokemon_PutFullReplace_NewUID(t *testing.T) {
	r, ms, _, restore := newV2PokemonTestAPI(t)
	defer restore()
	w := v2DoReq(t, r, http.MethodPost, "/api/v2/humans/u1/tracking/pokemon", `[{"pokemon_id":25,"min_iv":90,"max_cp":1500}]`)
	oldUID := int64(v2RulesArray(t, v2DecodeBody(t, w), "created")[0]["uid"].(float64))

	// PUT replaces; omitted max_cp must reset to default 9000.
	w = v2DoReq(t, r, http.MethodPut, "/api/v2/humans/u1/tracking/pokemon/"+itoa(oldUID), `{"pokemon_id":25,"min_iv":50}`)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
	updated := v2RulesArray(t, v2DecodeBody(t, w), "updated")
	if len(updated) != 1 {
		t.Fatalf("expected 1 updated, got %d", len(updated))
	}
	newUID := int64(updated[0]["uid"].(float64))
	if newUID == oldUID {
		t.Fatalf("PUT must yield a NEW uid; got same %d", newUID)
	}
	rows := ms.AllRows()
	if len(rows) != 1 {
		t.Fatalf("expected exactly 1 row after replace, got %d", len(rows))
	}
	if rows[0].MinIV != 50 || rows[0].MaxCP != 9000 {
		t.Fatalf("replace did not reset omitted fields: min_iv=%d max_cp=%d", rows[0].MinIV, rows[0].MaxCP)
	}
}

func TestV2Pokemon_PutCrossHuman404(t *testing.T) {
	r, _, _, restore := newV2PokemonTestAPI(t)
	defer restore()
	w := v2DoReq(t, r, http.MethodPost, "/api/v2/humans/u1/tracking/pokemon", `[{"pokemon_id":25}]`)
	uid := int64(v2RulesArray(t, v2DecodeBody(t, w), "created")[0]["uid"].(float64))

	w = v2DoReq(t, r, http.MethodPut, "/api/v2/humans/u2/tracking/pokemon/"+itoa(uid), `{"pokemon_id":25}`)
	if w.Code != http.StatusNotFound {
		t.Fatalf("expected 404 replacing another human's uid, got %d: %s", w.Code, w.Body.String())
	}
}

// --- include_descriptions (reads AND mutations) ----------------------------

func TestV2Pokemon_IncludeDescriptionsOnRead(t *testing.T) {
	r, _, _, restore := newV2PokemonTestAPI(t)
	defer restore()
	v2DoReq(t, r, http.MethodPost, "/api/v2/humans/u1/tracking/pokemon", `[{"pokemon_id":25}]`)

	// Without the flag: no description.
	w := v2DoReq(t, r, http.MethodGet, "/api/v2/humans/u1/tracking/pokemon", "")
	if _, ok := v2RulesArray(t, v2DecodeBody(t, w), "rules")[0]["description"]; ok {
		t.Fatalf("description should be absent without include_descriptions")
	}
	// With the flag: description present.
	w = v2DoReq(t, r, http.MethodGet, "/api/v2/humans/u1/tracking/pokemon?include_descriptions=true", "")
	rule := v2RulesArray(t, v2DecodeBody(t, w), "rules")[0]
	if d, ok := rule["description"].(string); !ok || d == "" {
		t.Fatalf("expected non-empty description, got %v", rule["description"])
	}
}

func TestV2Pokemon_IncludeDescriptionsOnCreate(t *testing.T) {
	r, _, _, restore := newV2PokemonTestAPI(t)
	defer restore()
	w := v2DoReq(t, r, http.MethodPost, "/api/v2/humans/u1/tracking/pokemon?include_descriptions=true", `[{"pokemon_id":25}]`)
	rule := v2RulesArray(t, v2DecodeBody(t, w), "created")[0]
	if d, ok := rule["description"].(string); !ok || d == "" {
		t.Fatalf("expected description on created rule, got %v", rule["description"])
	}
	// The assembled prefixed message is NEVER in the HTTP body.
	if _, ok := v2DecodeBody(t, w)["message"]; ok {
		t.Fatalf("response body must not contain an assembled 'message' field")
	}
}

// --- silent -----------------------------------------------------------------

func TestV2Pokemon_SilentSuppressesPush(t *testing.T) {
	r, _, pushes, restore := newV2PokemonTestAPI(t)
	defer restore()
	w := v2DoReq(t, r, http.MethodPost, "/api/v2/humans/u1/tracking/pokemon?silent=true", `[{"pokemon_id":25}]`)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
	if len(*pushes) != 0 {
		t.Fatalf("silent=true must suppress the push, got %d", len(*pushes))
	}
}

// --- human-not-found --------------------------------------------------------

func TestV2Pokemon_UnknownHuman404(t *testing.T) {
	r, _, _, restore := newV2PokemonTestAPI(t)
	defer restore()
	w := v2DoReq(t, r, http.MethodGet, "/api/v2/humans/nope/tracking/pokemon", "")
	if w.Code != http.StatusNotFound {
		t.Fatalf("expected 404 for unknown human, got %d: %s", w.Code, w.Body.String())
	}
}

// --- v2RuleEnvelope schema sanity (SchemaProvider) -------------------------

func TestV2RuleEnvelope_SchemaIncludesUID(t *testing.T) {
	reg := huma.NewMapRegistry("#/components/schemas/", huma.DefaultSchemaNamer)
	s := v2RuleEnvelope[v2PokemonRule]{}.Schema(reg)
	if s.Type != huma.TypeObject {
		t.Fatalf("expected object schema, got %q", s.Type)
	}
	if _, ok := s.Properties["uid"]; !ok {
		t.Fatalf("envelope schema missing uid property")
	}
	if _, ok := s.Properties["pokemon_id"]; !ok {
		t.Fatalf("envelope schema missing pokemon_id property (Req flatten)")
	}
}

// itoa is a tiny helper to keep paths readable.
func itoa(v int64) string {
	return strconv.FormatInt(v, 10)
}
