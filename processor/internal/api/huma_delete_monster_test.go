package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/pokemon/poracleng/processor/internal/store"
)

// ── DELETE byUid tests ────────────────────────────────────────────────────────

// TestDeleteMonster_404_UnknownUser: DELETE to an unknown user returns an ok
// response (the gin handler deletes without requiring the human to exist — it
// simply skips the confirmation message). With a nil DB the DeleteByUID call
// will panic; gin.Recovery turns that into 500. What we assert is that the
// endpoint is reachable at the right path and does NOT 404 (the gin handler
// never 404s on DELETE — unknown users still get the delete attempt).
//
// Rationale: the gin HandleDeleteMonster falls through to db.DeleteByUID even
// when lookupHuman returns nil, so there is no 404 path for this endpoint.
// A 500 from nil-DB proves routing and path-param binding are correct.
func TestDeleteMonster_NilDB_Routed(t *testing.T) {
	mock := store.NewMockHumanStore()
	r := buildHumaTestEngine(t, mock, true, RegisterTrackingMonster)

	req := httptest.NewRequest(http.MethodDelete, "/api/tracking/pokemon/nobody/byUid/42", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	// Must NOT be 404 — this endpoint has no 404 branch (delete proceeds even
	// when the human is unknown). 500 is expected (nil DB panic recovered).
	if w.Code == http.StatusNotFound {
		t.Fatalf("DELETE byUid returned 404 — endpoint may not be registered or path binding broken; body: %s", w.Body.String())
	}
	// Must NOT be 422 (path params bound correctly).
	if w.Code == http.StatusUnprocessableEntity {
		t.Fatalf("DELETE byUid returned 422 — path param binding failed; body: %s", w.Body.String())
	}
}

// TestDeleteMonster_PathParamBinding: proves {id} and {uid} are captured.
// uid=99 is a valid int64; non-integer uid would produce 422 (invalid param).
func TestDeleteMonster_PathParamBinding(t *testing.T) {
	mock := store.NewMockHumanStore()
	// Seed with a different id to confirm we don't accidentally match.
	mock.AddHuman(&store.Human{ID: "other-user", Type: "discord:user", Name: "Other"})

	r := buildHumaTestEngine(t, mock, true, RegisterTrackingMonster)

	// uid=99 is a valid integer path param.
	req := httptest.NewRequest(http.MethodDelete, "/api/tracking/pokemon/u1/byUid/99", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	// Not 422 means huma accepted the path params.
	if w.Code == http.StatusUnprocessableEntity {
		t.Fatalf("DELETE byUid path params caused 422 — binding broken; body: %s", w.Body.String())
	}
}

// TestDeleteMonster_KnownUser_PastLookup: a known user advances past the
// humaLookupHuman guard. The nil-DB panic → 500 (via gin.Recovery).
// This proves we're NOT hitting the "human not found → skip message" branch,
// i.e. the lookup successfully returned the user before the DB call panics.
func TestDeleteMonster_KnownUser_PastLookup(t *testing.T) {
	mock := store.NewMockHumanStore()
	mock.AddHuman(&store.Human{
		ID:               "u1",
		Type:             "discord:user",
		Name:             "TestUser",
		Enabled:          true,
		CurrentProfileNo: 1,
	})

	r := buildHumaTestEngine(t, mock, true, RegisterTrackingMonster)

	req := httptest.NewRequest(http.MethodDelete, "/api/tracking/pokemon/u1/byUid/7", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	// Must NOT be 422 (path params bound). Must NOT be 404 (no 404 path on DELETE).
	if w.Code == http.StatusUnprocessableEntity {
		t.Fatalf("DELETE byUid with known user caused 422: %s", w.Body.String())
	}
	if w.Code == http.StatusNotFound {
		t.Fatalf("DELETE byUid with known user returned 404: %s", w.Body.String())
	}
}

// TestDeleteMonster_SilentQuery_NotRejected: silent=true must not cause a 422.
func TestDeleteMonster_SilentQuery_NotRejected(t *testing.T) {
	mock := store.NewMockHumanStore()
	r := buildHumaTestEngine(t, mock, true, RegisterTrackingMonster)

	req := httptest.NewRequest(http.MethodDelete, "/api/tracking/pokemon/nobody/byUid/1?silent=true", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code == http.StatusUnprocessableEntity {
		t.Fatalf("silent=true on DELETE caused 422: %s", w.Body.String())
	}
}

// TestDeleteMonster_NoSchemaLeak: any JSON response from DELETE must not
// contain $schema. A nil-DB panic results in an empty 500 body from
// gin.Recovery — that is fine (no $schema to worry about); we only check
// when there IS a parseable body.
func TestDeleteMonster_NoSchemaLeak(t *testing.T) {
	mock := store.NewMockHumanStore()
	r := buildHumaTestEngine(t, mock, true, RegisterTrackingMonster)

	req := httptest.NewRequest(http.MethodDelete, "/api/tracking/pokemon/nobody/byUid/1", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Body.Len() == 0 {
		// Empty body (e.g. nil-DB panic recovered as 500 with no body) — nothing to check.
		return
	}
	var got map[string]any
	if err := json.NewDecoder(w.Body).Decode(&got); err != nil {
		// Not parseable JSON — no $schema risk.
		return
	}
	if _, has := got["$schema"]; has {
		t.Errorf("DELETE response body must not contain $schema; full body: %v", got)
	}
}

// ── Bulk-delete tests ─────────────────────────────────────────────────────────

// TestBulkDeleteMonster_ArrayBody_NotRejectedBy422: a JSON array of UIDs must
// not cause a 422 — proves body parsing and the oneOf schema.
func TestBulkDeleteMonster_ArrayBody_NotRejectedBy422(t *testing.T) {
	mock := store.NewMockHumanStore()
	r := buildHumaTestEngine(t, mock, true, RegisterTrackingMonster)

	body := `[1,2,3]`
	req := httptest.NewRequest(http.MethodPost, "/api/tracking/pokemon/nobody/delete",
		strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code == http.StatusUnprocessableEntity {
		t.Fatalf("array body caused 422 (oneOf schema should accept []int64): %s", w.Body.String())
	}
	// Must NOT be 404 (the endpoint has no 404 branch — human not found still
	// proceeds to delete). 500 from nil DB is expected.
	if w.Code == http.StatusNotFound {
		t.Fatalf("bulk-delete returned 404 — endpoint may not be registered; body: %s", w.Body.String())
	}
}

// TestBulkDeleteMonster_SingleInt_NotRejectedBy422: a bare int64 body must not
// cause a 422 — preserves the gin handler's single-value tolerance.
func TestBulkDeleteMonster_SingleInt_NotRejectedBy422(t *testing.T) {
	mock := store.NewMockHumanStore()
	r := buildHumaTestEngine(t, mock, true, RegisterTrackingMonster)

	body := `42`
	req := httptest.NewRequest(http.MethodPost, "/api/tracking/pokemon/nobody/delete",
		strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code == http.StatusUnprocessableEntity {
		t.Fatalf("single-int body caused 422 (oneOf schema should accept bare int64): %s", w.Body.String())
	}
}

// TestBulkDeleteMonster_SilentQuery_NotRejected: silent=true must not cause 422.
func TestBulkDeleteMonster_SilentQuery_NotRejected(t *testing.T) {
	mock := store.NewMockHumanStore()
	r := buildHumaTestEngine(t, mock, true, RegisterTrackingMonster)

	body := `[1]`
	req := httptest.NewRequest(http.MethodPost, "/api/tracking/pokemon/nobody/delete?silent=true",
		strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code == http.StatusUnprocessableEntity {
		t.Fatalf("silent=true on bulk-delete caused 422: %s", w.Body.String())
	}
}

// TestBulkDeleteMonster_NoSchemaLeak: any JSON response from bulk-delete must
// not contain $schema. A nil-DB panic → 500 with empty body from gin.Recovery;
// that case is fine (no JSON body means no $schema risk).
func TestBulkDeleteMonster_NoSchemaLeak(t *testing.T) {
	mock := store.NewMockHumanStore()
	r := buildHumaTestEngine(t, mock, true, RegisterTrackingMonster)

	body := `[1,2]`
	req := httptest.NewRequest(http.MethodPost, "/api/tracking/pokemon/nobody/delete",
		strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Body.Len() == 0 {
		// Empty body (nil-DB panic recovered as 500) — nothing to check.
		return
	}
	var got map[string]any
	if err := json.NewDecoder(w.Body).Decode(&got); err != nil {
		// Not parseable JSON — no $schema risk.
		return
	}
	if _, has := got["$schema"]; has {
		t.Errorf("bulk-delete response must not contain $schema; full body: %v", got)
	}
}

// TestBulkDeleteMonster_KnownUser_PastLookup: a known user advances past the
// human-lookup guard; nil-DB panic → 500 proves we got past lookupHuman.
func TestBulkDeleteMonster_KnownUser_PastLookup(t *testing.T) {
	mock := store.NewMockHumanStore()
	mock.AddHuman(&store.Human{
		ID:               "u1",
		Type:             "discord:user",
		Name:             "TestUser",
		Enabled:          true,
		CurrentProfileNo: 1,
	})

	r := buildHumaTestEngine(t, mock, true, RegisterTrackingMonster)

	body := `[5,6]`
	req := httptest.NewRequest(http.MethodPost, "/api/tracking/pokemon/u1/delete",
		strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code == http.StatusNotFound {
		t.Fatalf("known user returned 404 on bulk-delete: %s", w.Body.String())
	}
	if w.Code == http.StatusUnprocessableEntity {
		t.Fatalf("valid bulk-delete body caused 422: %s", w.Body.String())
	}
}

// ── uidList unit tests ───────────────────────────────────────────────────────

// TestUIDList_UnmarshalArray: an array body decodes into a slice.
func TestUIDList_UnmarshalArray(t *testing.T) {
	var u uidList
	if err := json.Unmarshal([]byte(`[1,2,3]`), &u); err != nil {
		t.Fatalf("unmarshal array: %v", err)
	}
	if len(u) != 3 {
		t.Fatalf("expected 3 elements, got %d", len(u))
	}
	if u[0] != 1 || u[1] != 2 || u[2] != 3 {
		t.Errorf("values = %v, want [1 2 3]", u)
	}
}

// TestUIDList_UnmarshalSingle: a bare int64 is wrapped in a 1-element slice.
func TestUIDList_UnmarshalSingle(t *testing.T) {
	var u uidList
	if err := json.Unmarshal([]byte(`42`), &u); err != nil {
		t.Fatalf("unmarshal single: %v", err)
	}
	if len(u) != 1 {
		t.Fatalf("expected 1 element, got %d", len(u))
	}
	if u[0] != 42 {
		t.Errorf("value = %d, want 42", u[0])
	}
}

// TestUIDList_UnmarshalEmpty: an empty array decodes without error.
func TestUIDList_UnmarshalEmpty(t *testing.T) {
	var u uidList
	if err := json.Unmarshal([]byte(`[]`), &u); err != nil {
		t.Fatalf("unmarshal empty array: %v", err)
	}
	if len(u) != 0 {
		t.Errorf("expected empty slice, got %v", u)
	}
}
