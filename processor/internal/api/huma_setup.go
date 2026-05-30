package api

import (
	"fmt"
	"net/http"
	"strings"

	"github.com/danielgtaylor/huma/v2"
	"github.com/danielgtaylor/huma/v2/adapters/humagin"
	"github.com/gin-gonic/gin"
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
//
// When errs are present their per-field detail strings are appended to msg so
// that 422 responses are informative rather than the opaque "validation
// failed". The envelope shape ({status, message}) is never altered — we only
// enrich the message text.
func humaNewError(status int, msg string, errs ...error) huma.StatusError {
	if msg == "" {
		msg = http.StatusText(status)
	}
	if len(errs) > 0 {
		parts := make([]string, 0, len(errs))
		for _, e := range errs {
			if e != nil {
				parts = append(parts, e.Error())
			}
		}
		if len(parts) > 0 {
			msg = msg + ": " + strings.Join(parts, "; ")
		}
	}
	return &legacyError{StatusCode: status, Status: "error", Message: msg}
}

// InstallLegacyErrorModel overrides huma's RFC-9457 error model with the
// legacy {status,message} envelope. Call once at startup before registering.
func InstallLegacyErrorModel() {
	huma.NewError = humaNewError
}

// NewHumaAPI installs the legacy error model, builds a huma API bound to the
// authenticated /api group, declares the X-Poracle-Secret security scheme, and
// serves the OpenAPI spec + docs UI at PUBLIC top-level paths (no secret).
func NewHumaAPI(r *gin.Engine, apiGroup *gin.RouterGroup, version string) huma.API {
	InstallLegacyErrorModel()

	cfg := huma.DefaultConfig("PoracleNG API", version)

	// DefaultConfig registers a SchemaLinkTransformer via CreateHooks that
	// injects a "$schema" field into every response body at runtime. This
	// breaks byte-compatibility with existing clients (PoracleWeb, ReactMap)
	// that expect exactly {"status":"ok",...} or {"status":"error","message":"..."}.
	// Clear the hooks before NewWithGroup runs them so the transformer is
	// never installed. The OpenAPI document itself is unaffected — the
	// transformer only mutates live response bodies, not the spec.
	cfg.CreateHooks = nil

	// Disable huma's built-in mounts; we serve our own public copies on r.
	cfg.OpenAPIPath = ""
	cfg.DocsPath = ""
	cfg.SchemasPath = ""
	cfg.Components.SecuritySchemes = map[string]*huma.SecurityScheme{
		"poracleSecret": {Type: "apiKey", In: "header", Name: "X-Poracle-Secret"},
	}

	humaAPI := humagin.NewWithGroup(r, apiGroup, cfg)

	// Public spec + docs (top-level, outside /api, so RequireSecretGin never runs).
	r.GET("/openapi.json", func(c *gin.Context) {
		b, err := humaAPI.OpenAPI().MarshalJSON()
		if err != nil {
			c.Data(http.StatusInternalServerError, "text/plain", []byte(fmt.Sprintf("openapi marshal: %v", err)))
			return
		}
		c.Data(http.StatusOK, "application/json", b)
	})
	r.GET("/docs", func(c *gin.Context) {
		c.Data(http.StatusOK, "text/html", []byte(docsHTML))
	})
	return humaAPI
}

// docsHTML is a minimal Stoplight Elements page pointed at /openapi.json.
const docsHTML = `<!doctype html><html><head><meta charset="utf-8">
<title>PoracleNG API</title>
<script src="https://unpkg.com/@stoplight/elements/web-components.min.js"></script>
<link rel="stylesheet" href="https://unpkg.com/@stoplight/elements/styles.min.css">
</head><body><elements-api apiDescriptionUrl="/openapi.json" router="hash" layout="sidebar"/></body></html>`
