package api

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/danielgtaylor/huma/v2"
	"github.com/gin-gonic/gin"
)

type spikeBody struct {
	N flexInt  `json:"n"`
	B flexBool `json:"b"`
}
type spikeInput struct{ Body lenient[spikeBody] }
type spikeOutput struct {
	Body struct {
		Status string `json:"status"`
		N      int    `json:"n"`
		B      int    `json:"b"`
	}
}

func TestLeniencySpike(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	api := NewHumaAPI(r, r.Group("/api"), "test")
	huma.Register(api, huma.Operation{
		OperationID: "spike", Method: http.MethodPost, Path: "/spike",
	}, func(ctx context.Context, in *spikeInput) (*spikeOutput, error) {
		out := &spikeOutput{}
		out.Body.Status = "ok"
		out.Body.N = in.Body.Value.N.intValue(0)
		out.Body.B = in.Body.Value.B.intValue(0)
		return out, nil
	})

	cases := []string{
		`{"n":"90","b":false}`,        // string int, bool
		`{"n":90,"b":3}`,              // native int, int-as-bool-field
		`{"n":90,"b":true,"extra":1}`, // unknown field must NOT 422
	}
	for _, body := range cases {
		req := httptest.NewRequest(http.MethodPost, "/api/spike", strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)
		if w.Code != http.StatusOK {
			t.Errorf("body %s -> %d (%s), want 200", body, w.Code, w.Body.String())
		}
	}

}
