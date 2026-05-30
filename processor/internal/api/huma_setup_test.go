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

func TestLegacyErrorModelSerialises(t *testing.T) {
	InstallLegacyErrorModel()
	err := humaNewError(http.StatusNotFound, "human not found")
	if err.GetStatus() != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", err.GetStatus())
	}
	b, e := json.Marshal(err)
	if e != nil {
		t.Fatalf("marshal: %v", e)
	}
	var got map[string]any
	_ = json.Unmarshal(b, &got)
	if got["status"] != "error" {
		t.Errorf("status field = %v, want \"error\"", got["status"])
	}
	if got["message"] != "human not found" {
		t.Errorf("message field = %v, want \"human not found\"", got["message"])
	}
	if _, hasTitle := got["title"]; hasTitle {
		t.Errorf("legacy body must not contain RFC9457 \"title\" field: %s", b)
	}
}

func TestPublicDocsUnauthenticated(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	apiGroup := r.Group("/api")
	apiGroup.Use(RequireSecretGin("topsecret")) // gate /api
	_ = NewHumaAPI(r, apiGroup, "test-version") // mounts docs on r (public)

	for _, path := range []string{"/openapi.json", "/docs"} {
		req := httptest.NewRequest(http.MethodGet, path, nil)
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)
		if w.Code != http.StatusOK {
			t.Errorf("GET %s unauthenticated = %d, want 200", path, w.Code)
		}
	}
}

// schemaTestInput / schemaTestOutput are the types used by the three
// $schema-leak and validation-message tests below.
type schemaTestInput struct {
	Body struct {
		Value int `json:"value"`
	}
}
type schemaTestOutput struct {
	Body struct {
		Status string `json:"status"`
		Value  int    `json:"value"`
	}
}

// buildSchemaTestAPI creates a gin engine with a single POST /api/schema-test
// endpoint and returns both the engine and the huma API handle.
func buildSchemaTestAPI(t *testing.T) (*gin.Engine, huma.API) {
	t.Helper()
	gin.SetMode(gin.TestMode)
	r := gin.New()
	humaAPI := NewHumaAPI(r, r.Group("/api"), "test")
	huma.Register(humaAPI, huma.Operation{
		OperationID: "schema-test",
		Method:      http.MethodPost,
		Path:        "/schema-test",
	}, func(_ context.Context, in *schemaTestInput) (*schemaTestOutput, error) {
		out := &schemaTestOutput{}
		out.Body.Status = "ok"
		out.Body.Value = in.Body.Value
		return out, nil
	})
	return r, humaAPI
}

// TestNoSchemaLeakInSuccessBody asserts that a valid request does NOT receive
// a "$schema" field in the response body, while the expected status/value
// fields are present.
func TestNoSchemaLeakInSuccessBody(t *testing.T) {
	r, _ := buildSchemaTestAPI(t)

	req := httptest.NewRequest(http.MethodPost, "/api/schema-test",
		strings.NewReader(`{"value":42}`))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}

	var got map[string]any
	if err := json.NewDecoder(w.Body).Decode(&got); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if _, hasSchema := got["$schema"]; hasSchema {
		t.Errorf("success body must not contain $schema field; full body: %v", got)
	}
	if got["status"] != "ok" {
		t.Errorf("status = %v, want \"ok\"", got["status"])
	}
	if got["value"] != float64(42) {
		t.Errorf("value = %v, want 42", got["value"])
	}
}

// TestNoSchemaLeakInErrorBody triggers a 422 (invalid body type) and asserts
// that the error envelope has ONLY "status" and "message" keys — no "$schema".
func TestNoSchemaLeakInErrorBody(t *testing.T) {
	r, _ := buildSchemaTestAPI(t)

	// Send a string where an integer is expected; huma will produce a 422.
	req := httptest.NewRequest(http.MethodPost, "/api/schema-test",
		strings.NewReader(`{"value":"not-an-int"}`))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusUnprocessableEntity {
		t.Fatalf("expected 422, got %d: %s", w.Code, w.Body.String())
	}

	var got map[string]any
	if err := json.NewDecoder(w.Body).Decode(&got); err != nil {
		t.Fatalf("decode error body: %v", err)
	}
	if _, hasSchema := got["$schema"]; hasSchema {
		t.Errorf("error body must not contain $schema field; full body: %v", got)
	}
	if got["status"] != "error" {
		t.Errorf("status = %v, want \"error\"", got["status"])
	}
	if _, hasMsg := got["message"]; !hasMsg {
		t.Errorf("error body must contain message field; full body: %v", got)
	}
	// Exact two-key shape: only "status" and "message".
	for k := range got {
		if k != "status" && k != "message" {
			t.Errorf("unexpected key %q in error body; full body: %v", k, got)
		}
	}
}

// TestValidationMessageIncludesFieldDetail asserts that a 422 error message
// is not the bare "validation failed" string — it must contain per-field
// detail so that API clients can understand which field was invalid.
func TestValidationMessageIncludesFieldDetail(t *testing.T) {
	r, _ := buildSchemaTestAPI(t)

	req := httptest.NewRequest(http.MethodPost, "/api/schema-test",
		strings.NewReader(`{"value":"not-an-int"}`))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusUnprocessableEntity {
		t.Fatalf("expected 422, got %d: %s", w.Code, w.Body.String())
	}

	var got struct {
		Message string `json:"message"`
	}
	if err := json.NewDecoder(w.Body).Decode(&got); err != nil {
		t.Fatalf("decode error body: %v", err)
	}
	if got.Message == "validation failed" {
		t.Errorf("message is bare %q — must include field-level detail", got.Message)
	}
	// The offending field name ("value") or its location ("body.value") must
	// appear somewhere in the message.
	if !strings.Contains(got.Message, "value") {
		t.Errorf("message %q does not mention the offending field \"value\"", got.Message)
	}
}
