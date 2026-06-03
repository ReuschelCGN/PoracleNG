package api

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"github.com/danielgtaylor/huma/v2"
	"github.com/gin-gonic/gin"

	"github.com/pokemon/poracleng/processor/internal/store"
)

// newFeaturesTestAPI builds a gin engine with a fresh huma API mounted on /api.
func newFeaturesTestAPI(t *testing.T) (*gin.Engine, huma.API) {
	t.Helper()
	gin.SetMode(gin.TestMode)
	r := gin.New()
	return r, NewHumaAPI(r, r.Group("/api"), "test")
}

func summaryHumaDeps() (*SummaryDeps, *store.MockSummaryScheduleStore, *int32) {
	mock := store.NewMockSummaryScheduleStore()
	var triggered int32
	deps := &SummaryDeps{
		Schedules: mock,
		Dispatch:  func(_, _ string) { atomic.AddInt32(&triggered, 1) },
	}
	return deps, mock, &triggered
}

func TestHumaSummaryGet_OK(t *testing.T) {
	r, api := newFeaturesTestAPI(t)
	deps, mock, _ := summaryHumaDeps()
	mock.Seed(store.SummarySchedule{ID: "u1", AlertType: "quest", ActiveHours: `[{"day":1,"hours":7,"mins":30}]`})
	RegisterSummaries(api, deps)

	req := httptest.NewRequest(http.MethodGet, "/api/summaries/u1/quest", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", w.Code, w.Body.String())
	}
	var body struct {
		Status   string `json:"status"`
		Schedule struct {
			ID          string          `json:"id"`
			AlertType   string          `json:"alert_type"`
			ActiveHours json.RawMessage `json:"active_hours"`
		} `json:"schedule"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("unmarshal: %v body=%s", err, w.Body.String())
	}
	if body.Status != "ok" || body.Schedule.ID != "u1" || body.Schedule.AlertType != "quest" {
		t.Fatalf("unexpected body: %s", w.Body.String())
	}
}

func TestHumaSummaryGet_Missing404(t *testing.T) {
	r, api := newFeaturesTestAPI(t)
	deps, _, _ := summaryHumaDeps()
	RegisterSummaries(api, deps)

	req := httptest.NewRequest(http.MethodGet, "/api/summaries/u1/quest", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d body=%s", w.Code, w.Body.String())
	}
}

func TestHumaSummaryGet_UnknownAlertType400(t *testing.T) {
	r, api := newFeaturesTestAPI(t)
	deps, _, _ := summaryHumaDeps()
	RegisterSummaries(api, deps)

	req := httptest.NewRequest(http.MethodGet, "/api/summaries/u1/raid", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d body=%s", w.Code, w.Body.String())
	}
}

func TestHumaSummaryList_OK(t *testing.T) {
	r, api := newFeaturesTestAPI(t)
	deps, mock, _ := summaryHumaDeps()
	mock.Seed(store.SummarySchedule{ID: "u1", AlertType: "quest", ActiveHours: `[]`})
	RegisterSummaries(api, deps)

	req := httptest.NewRequest(http.MethodGet, "/api/summaries/u1", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", w.Code, w.Body.String())
	}
	var body struct {
		Status    string `json:"status"`
		Schedules []struct {
			ID        string `json:"id"`
			AlertType string `json:"alert_type"`
		} `json:"schedules"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if body.Status != "ok" || len(body.Schedules) != 1 || body.Schedules[0].ID != "u1" {
		t.Fatalf("unexpected body: %s", w.Body.String())
	}
}

func TestHumaSummaryDelete_OK(t *testing.T) {
	r, api := newFeaturesTestAPI(t)
	deps, mock, _ := summaryHumaDeps()
	mock.Seed(store.SummarySchedule{ID: "u1", AlertType: "quest", ActiveHours: `[]`})
	RegisterSummaries(api, deps)

	req := httptest.NewRequest(http.MethodDelete, "/api/summaries/u1/quest", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", w.Code, w.Body.String())
	}
	var body map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if body["status"] != "ok" {
		t.Fatalf("unexpected body: %s", w.Body.String())
	}
}

func TestHumaSummaryTrigger_OK(t *testing.T) {
	r, api := newFeaturesTestAPI(t)
	deps, _, triggered := summaryHumaDeps()
	RegisterSummaries(api, deps)

	req := httptest.NewRequest(http.MethodPost, "/api/summaries/u1/quest/trigger", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", w.Code, w.Body.String())
	}
	if atomic.LoadInt32(triggered) != 1 {
		t.Fatalf("expected dispatch to fire once, got %d", atomic.LoadInt32(triggered))
	}
}

func TestHumaSummaryGet_FeatureDisabled503(t *testing.T) {
	r, api := newFeaturesTestAPI(t)
	deps := &SummaryDeps{} // Schedules nil, Dispatch nil
	RegisterSummaries(api, deps)

	req := httptest.NewRequest(http.MethodGet, "/api/summaries/u1/quest", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("expected 503, got %d body=%s", w.Code, w.Body.String())
	}
}

// --- command endpoint ---

func TestHumaCommand_MissingFields400(t *testing.T) {
	r, api := newFeaturesTestAPI(t)
	RegisterCommand(api, nil)

	body := bytes.NewBufferString(`{"text":"","user_id":""}`)
	req := httptest.NewRequest(http.MethodPost, "/api/command", body)
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d body=%s", w.Code, w.Body.String())
	}
}

func TestHumaCommand_NilParser500(t *testing.T) {
	r, api := newFeaturesTestAPI(t)
	RegisterCommand(api, nil) // deps nil → parser nil

	body := bytes.NewBufferString(`{"text":"!version","user_id":"123"}`)
	req := httptest.NewRequest(http.MethodPost, "/api/command", body)
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusInternalServerError {
		t.Fatalf("expected 500, got %d body=%s", w.Code, w.Body.String())
	}
}
