package api

import (
	"fmt"
	"net/http"

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

// NewHumaAPI installs the legacy error model, builds a huma API bound to the
// authenticated /api group, declares the X-Poracle-Secret security scheme, and
// serves the OpenAPI spec + docs UI at PUBLIC top-level paths (no secret).
func NewHumaAPI(r *gin.Engine, apiGroup *gin.RouterGroup, version string) huma.API {
	InstallLegacyErrorModel()

	cfg := huma.DefaultConfig("PoracleNG API", version)
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
