package api

import (
	"net/http"

	"github.com/danielgtaylor/huma/v2"
)

// legacyError is the wire shape PoracleWeb/ReactMap already expect from /api.
// It implements huma.StatusError so huma uses it for every generated error.
type legacyError struct {
	StatusCode int    `json:"-"`
	Status     string `json:"status"`  // always "error"
	Message    string `json:"message"` // human-readable detail
}

func (e *legacyError) Error() string  { return e.Message }
func (e *legacyError) GetStatus() int { return e.StatusCode }

// humaNewError is the value we assign into huma.NewError; kept as a named
// package func so tests can call it directly.
func humaNewError(status int, msg string, _ ...error) huma.StatusError {
	if msg == "" {
		msg = http.StatusText(status)
	}
	return &legacyError{StatusCode: status, Status: "error", Message: msg}
}

// InstallLegacyErrorModel overrides huma's RFC-9457 error model with the
// legacy {status,message} envelope. Call once at startup before registering.
func InstallLegacyErrorModel() {
	huma.NewError = humaNewError
}
