package api

import (
	"encoding/json"
	"net/http"
	"testing"
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
