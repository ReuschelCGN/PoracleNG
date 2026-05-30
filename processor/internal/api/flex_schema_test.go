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

type legacyFormBody struct {
	N flexInt  `json:"n"`
	B flexBool `json:"b"`
}
type legacyFormInput struct{ Body lenient[legacyFormBody] }
type legacyFormOutput struct {
	Body struct {
		Status string `json:"status"`
		N      int    `json:"n"`
		B      int    `json:"b"`
	}
}

func TestLenientBodyAcceptsLegacyWireFormats(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	api := NewHumaAPI(r, r.Group("/api"), "test")
	huma.Register(api, huma.Operation{
		OperationID: "legacy-form-test", Method: http.MethodPost, Path: "/legacy",
	}, func(ctx context.Context, in *legacyFormInput) (*legacyFormOutput, error) {
		out := &legacyFormOutput{}
		out.Body.Status = "ok"
		out.Body.N = in.Body.Value.N.intValue(0)
		out.Body.B = in.Body.Value.B.intValue(0)
		return out, nil
	})

	cases := []struct {
		body    string
		wantN   int
		wantB   int
	}{
		{`{"n":"90","b":false}`, 90, 0},        // string int, bool false
		{`{"n":90,"b":3}`, 90, 3},              // native int, int-as-bool-field
		{`{"n":90,"b":true,"extra":1}`, 90, 1}, // unknown field must NOT 422
	}
	for _, tc := range cases {
		req := httptest.NewRequest(http.MethodPost, "/api/legacy", strings.NewReader(tc.body))
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)
		if w.Code != http.StatusOK {
			t.Errorf("body %s -> %d (%s), want 200", tc.body, w.Code, w.Body.String())
			continue
		}
		// huma serialises the handler's output.Body fields as the top-level JSON
		// response body (i.e. {"status":"ok","n":90,"b":1}), not wrapped in "body".
		var resp struct {
			N int `json:"n"`
			B int `json:"b"`
		}
		if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
			t.Errorf("body %s: decode response: %v", tc.body, err)
			continue
		}
		if resp.N != tc.wantN {
			t.Errorf("body %s: N = %d, want %d", tc.body, resp.N, tc.wantN)
		}
		if resp.B != tc.wantB {
			t.Errorf("body %s: B = %d, want %d", tc.body, resp.B, tc.wantB)
		}
	}
}

// strictBody is a minimal struct with one normal field used to prove that a
// lenient[T] registration does not contaminate the strict schema for T.
type strictBody struct {
	X int `json:"x"`
}

func TestLenientDoesNotContaminateStrictSchema(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	api := NewHumaAPI(r, r.Group("/api"), "test")

	// lenient endpoint — registers lenient[strictBody], flipping AdditionalProperties on its copy.
	huma.Register(api, huma.Operation{OperationID: "lenient-ep", Method: http.MethodPost, Path: "/lenient"},
		func(ctx context.Context, in *struct{ Body lenient[strictBody] }) (*struct{}, error) {
			return &struct{}{}, nil
		})
	// strict endpoint — bare strictBody, same registry, must retain additionalProperties:false.
	huma.Register(api, huma.Operation{OperationID: "strict-ep", Method: http.MethodPost, Path: "/strict"},
		func(ctx context.Context, in *struct{ Body strictBody }) (*struct{}, error) {
			return &struct{}{}, nil
		})

	// strict endpoint MUST reject unknown fields with 422.
	req := httptest.NewRequest(http.MethodPost, "/api/strict", strings.NewReader(`{"x":1,"unknown":2}`))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusUnprocessableEntity {
		t.Errorf("strict endpoint accepted unknown field (code %d, body %s) — schema contaminated by lenient registration",
			w.Code, w.Body.String())
	}
}
