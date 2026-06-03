package api

import (
	"context"

	"github.com/danielgtaylor/huma/v2"
)

// statusOKOutput is the typed body returned by reload-style ops: {"status":"ok"}.
type statusOKOutput struct {
	Body struct {
		Status string `json:"status"`
	}
}

// RegisterReload registers a reload-style op that returns {"status":"ok"} on
// success (preserving the legacy success body) or a problem+json error on
// failure, for the given method/path on the shared huma API. The summary and
// description are supplied per call site so each reload op documents exactly
// what it reloads (state-only vs. geofence-inclusive vs. DTS templates).
func RegisterReload(api huma.API, opID, method, path, summary, description string, fn func() error) {
	huma.Register(api, huma.Operation{
		OperationID: opID, Method: method, Path: path,
		Summary: summary, Description: description, Tags: []string{"reload"},
		Security: []map[string][]string{{"poracleSecret": {}}},
	}, func(_ context.Context, _ *struct{}) (*statusOKOutput, error) {
		if err := fn(); err != nil {
			return nil, huma.Error500InternalServerError(err.Error())
		}
		out := &statusOKOutput{}
		out.Body.Status = "ok"
		return out, nil
	})
}
