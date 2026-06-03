package api

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"maps"
	"strings"

	"github.com/danielgtaylor/huma/v2"
	raymond "github.com/mailgun/raymond/v2"
	log "github.com/sirupsen/logrus"

	"github.com/pokemon/poracleng/processor/internal/delivery"
	"github.com/pokemon/poracleng/processor/internal/dts"
)

// openJSON is a freeform JSON request body. It preserves the raw bytes so the
// handler can unmarshal them with the exact same logic the legacy gin handler
// used (via c.ShouldBindJSON into a local struct). It implements
// huma.SchemaProvider to emit an OPEN schema (accept anything), because these
// endpoints take polymorphic / dynamically-keyed payloads that huma's default
// schema generation would otherwise reject.
type openJSON json.RawMessage

// UnmarshalJSON captures the raw bytes verbatim.
func (o *openJSON) UnmarshalJSON(data []byte) error {
	*o = append((*o)[:0], data...)
	return nil
}

// MarshalJSON returns the captured bytes (or null when empty).
func (o openJSON) MarshalJSON() ([]byte, error) {
	if len(o) == 0 {
		return []byte("null"), nil
	}
	return o, nil
}

// Schema returns an empty (fully open) schema so huma accepts any JSON value.
func (openJSON) Schema(huma.Registry) *huma.Schema {
	// Empty schema = no constraints; accepts object, array, scalar, anything.
	return &huma.Schema{}
}

// dtsRenderReader is the minimal template-store surface the on-demand render
// endpoint needs. *dts.TemplateStore satisfies it; the interface keeps the
// Register signature testable since TemplateStore has unexported fields.
type dtsRenderReader interface {
	Get(templateType, platform, templateID, language string) *raymond.Template
}

// dtsSaveWriter is the minimal surface the save-templates endpoint needs.
type dtsSaveWriter interface {
	SaveEntry(entry dts.DTSEntry) error
}

// dtsMetadataReader is the minimal surface the config/templates metadata read
// needs.
type dtsMetadataReader interface {
	TemplateMetadata(includeDescriptions bool) map[string]any
}

// dtsSendTestReader is the minimal surface the sendtest endpoint needs from the
// template store (partials) and renderer (shortener).
type dtsSendTestReader interface {
	Partials() map[string]string
}

// dtsShortener is the minimal surface needed to resolve <S< >S> markers; a nil
// renderer is tolerated (markers left untouched).
type dtsShortener interface {
	Shortener() *dts.ShlinkShortener
}

// dtsDispatcher is the minimal delivery surface the sendtest endpoint needs.
// *delivery.Dispatcher satisfies it.
type dtsDispatcher interface {
	Dispatch(job *delivery.Job)
}

// templateConfigInput carries the optional includeDescriptions query param.
type templateConfigInput struct {
	IncludeDescriptions string `query:"includeDescriptions"`
}

// RegisterTemplateConfig registers GET /api/config/templates, returning DTS
// template metadata for PoracleWeb. Replaces gin HandleTemplateConfig. The
// response is a dynamic-keyed map merged with {"status":"ok"} (freeform body).
func RegisterTemplateConfig(api huma.API, ts dtsMetadataReader) {
	huma.Register(api, huma.Operation{
		OperationID: "get-config-templates", Method: "GET", Path: "/config/templates",
		Summary:     "DTS template list",
		Description: "Returns dynamic-keyed DTS template metadata for PoracleWeb. The response shape is freeform (open body): a {\"status\":\"ok\"} envelope merged with a per-type metadata map.",
		Tags:        []string{"dts"},
		Security:    []map[string][]string{{"poracleSecret": {}}},
	}, func(_ context.Context, in *templateConfigInput) (*anyBodyOutput, error) {
		metadata := ts.TemplateMetadata(in.IncludeDescriptions == "true")
		resp := map[string]any{"status": "ok"}
		maps.Copy(resp, metadata)
		return &anyBodyOutput{Body: resp}, nil
	})
}

// dtsRenderInput carries the freeform render request. The body is open because
// `view` is an arbitrary map of template variables.
type dtsRenderInput struct {
	Body openJSON
}

// RegisterDTSRender registers POST /api/dts/render, rendering a DTS template on
// demand. Replaces gin HandleDTSRender. The request body is freeform (open):
// {type,id,platform,language} plus an arbitrary `view` map. The response
// `message` is the parsed rendered template (any JSON value).
func RegisterDTSRender(api huma.API, ts dtsRenderReader) {
	huma.Register(api, huma.Operation{
		OperationID: "post-dts-render", Method: "POST", Path: "/dts/render",
		Summary:     "Render a DTS template",
		Description: "Renders a DTS template on demand. Request body is open: {type,id,platform,language} plus an arbitrary `view` variable map. Response `message` is the rendered template parsed back to JSON (any shape).",
		Tags:        []string{"dts"},
		Security:    []map[string][]string{{"poracleSecret": {}}},
	}, func(_ context.Context, in *dtsRenderInput) (*anyBodyOutput, error) {
		var req struct {
			Type     string         `json:"type"`
			ID       string         `json:"id"`
			Platform string         `json:"platform"`
			Language string         `json:"language"`
			View     map[string]any `json:"view"`
		}
		if err := json.Unmarshal(in.Body, &req); err != nil {
			return nil, huma.Error400BadRequest("invalid request body: " + err.Error())
		}

		tmpl := ts.Get(req.Type, req.Platform, req.ID, req.Language)
		if tmpl == nil {
			return nil, huma.Error404NotFound(fmt.Sprintf("no template found for %s/%s/%s/%s", req.Type, req.Platform, req.ID, req.Language))
		}

		view := req.View
		if view == nil {
			view = make(map[string]any)
		}

		df := raymond.NewDataFrame()
		df.Set("language", req.Language)
		df.Set("platform", req.Platform)

		rendered, err := tmpl.ExecWith(view, df)
		if err != nil {
			return nil, huma.Error500InternalServerError("template render failed: " + err.Error())
		}

		var message any
		if err := json.Unmarshal([]byte(rendered), &message); err != nil {
			return nil, huma.Error500InternalServerError("rendered template is not valid JSON: " + err.Error())
		}

		return &anyBodyOutput{Body: map[string]any{"status": "ok", "message": message}}, nil
	})
}

// dtsSaveInput carries the freeform save request: an array of DTSEntry whose
// `template` field is polymorphic. Kept open so any valid entry shape parses.
type dtsSaveInput struct {
	Body openJSON
}

// dtsSaveOutput is the typed body for POST /api/dts/templates: {status, saved}
// where saved is the count of entries persisted.
type dtsSaveOutput struct {
	Body struct {
		Status string `json:"status"`
		Saved  int    `json:"saved"`
	}
}

// RegisterDTSSaveTemplates registers POST /api/dts/templates, saving an array
// of DTS entries. Replaces gin HandleDTSSaveTemplates. The request body is open
// ([]DTSEntry with a polymorphic `template` value). Each entry is saved to its
// own file; readonly entries are rejected (403).
func RegisterDTSSaveTemplates(api huma.API, ts dtsSaveWriter) {
	huma.Register(api, huma.Operation{
		OperationID: "post-dts-templates", Method: "POST", Path: "/dts/templates",
		Summary:     "Save DTS template entries",
		Description: "Saves an array of DTS entries. Request body is open: []DTSEntry where each entry's `template` field is polymorphic (string, object, or array). Readonly (bundled) entries are rejected.",
		Tags:        []string{"dts"},
		Security:    []map[string][]string{{"poracleSecret": {}}},
	}, func(_ context.Context, in *dtsSaveInput) (*dtsSaveOutput, error) {
		var entries []dts.DTSEntry
		if err := json.Unmarshal(in.Body, &entries); err != nil {
			log.Warnf("dts save: invalid request body: %v", err)
			return nil, huma.Error400BadRequest("invalid request body: " + err.Error())
		}

		if len(entries) == 0 {
			log.Warnf("dts save: received empty entries array")
			return nil, huma.Error400BadRequest("no templates provided")
		}

		for i, entry := range entries {
			if entry.Type == "" || entry.Platform == "" {
				msg := fmt.Sprintf("entry %d missing required fields (type=%q, platform=%q, id=%q)", i, entry.Type, entry.Platform, entry.ID)
				log.Warnf("dts save: %s", msg)
				return nil, huma.Error400BadRequest(msg)
			}
		}

		saved := 0
		for _, entry := range entries {
			if err := ts.SaveEntry(entry); err != nil {
				log.Warnf("dts save: failed to save %s/%s/%s/%s: %v", entry.Type, entry.Platform, entry.ID, entry.Language, err)
				return nil, huma.Error403Forbidden(err.Error())
			}
			saved++
		}

		log.Infof("dts save: saved %d template(s) via API", saved)
		out := &dtsSaveOutput{}
		out.Body.Status = "ok"
		out.Body.Saved = saved
		return out, nil
	})
}

// dtsEnrichInput carries the freeform enrich request: a `webhook` RawMessage
// plus typed type/language/platform fields.
type dtsEnrichInput struct {
	Body openJSON
}

// RegisterDTSEnrich registers POST /api/dts/enrich, running a webhook through
// the enrichment pipeline. Replaces gin HandleDTSEnrich. The request body is
// open: {type,language,platform} plus an arbitrary `webhook` object. The
// response `variables` is the enriched variable map (any shape).
func RegisterDTSEnrich(api huma.API, svc EnrichService) {
	huma.Register(api, huma.Operation{
		OperationID: "post-dts-enrich", Method: "POST", Path: "/dts/enrich",
		Summary:     "Run webhook through enrichment pipeline",
		Description: "Runs a webhook through the enrichment pipeline. Request body is open: {type,language,platform} plus an arbitrary `webhook` object. Response `variables` is the enriched variable map (any shape).",
		Tags:        []string{"dts"},
		Security:    []map[string][]string{{"poracleSecret": {}}},
	}, func(_ context.Context, in *dtsEnrichInput) (*anyBodyOutput, error) {
		var req struct {
			Type     string          `json:"type"`
			Webhook  json.RawMessage `json:"webhook"`
			Language string          `json:"language"`
			Platform string          `json:"platform"`
		}
		if err := json.Unmarshal(in.Body, &req); err != nil {
			return nil, huma.Error400BadRequest("invalid request body: " + err.Error())
		}

		if req.Type == "" {
			return nil, huma.Error400BadRequest("type is required")
		}
		if len(req.Webhook) == 0 {
			return nil, huma.Error400BadRequest("webhook is required")
		}
		if req.Language == "" {
			req.Language = "en"
		}
		if req.Platform == "" {
			req.Platform = "discord"
		}

		variables, err := svc.EnrichWebhook(req.Type, req.Webhook, req.Language, req.Platform)
		if err != nil {
			log.Errorf("dts enrich: %v", err)
			return nil, huma.Error500InternalServerError(err.Error())
		}

		return &anyBodyOutput{Body: map[string]any{"status": "ok", "variables": variables}}, nil
	})
}

// dtsSendTestInput carries the freeform sendtest request: a polymorphic
// `template` value plus a `variables` map and typed target/language/platform.
type dtsSendTestInput struct {
	Body openJSON
}

// dtsSendTestOutput is the typed body for POST /api/dts/sendtest: {status,
// message} where message is the fixed string "sent" on success.
type dtsSendTestOutput struct {
	Body struct {
		Status  string `json:"status"`
		Message string `json:"message"`
	}
}

// RegisterDTSSendTest registers POST /api/dts/sendtest, rendering a template
// with provided variables and delivering it to a target. Replaces gin
// HandleDTSSendTest. The request body is open: a polymorphic `template` value,
// an arbitrary `variables` map, plus {target,language,platform}.
func RegisterDTSSendTest(api huma.API, dispatcher dtsDispatcher, ts dtsSendTestReader, renderer dtsShortener) {
	huma.Register(api, huma.Operation{
		OperationID: "post-dts-sendtest", Method: "POST", Path: "/dts/sendtest",
		Summary:     "Render and deliver a test message",
		Description: "Renders a template with provided variables and delivers it to a target. Request body is open: a polymorphic `template` value (string or object), an arbitrary `variables` map, plus {target,language,platform}.",
		Tags:        []string{"dts"},
		Security:    []map[string][]string{{"poracleSecret": {}}},
	}, func(_ context.Context, in *dtsSendTestInput) (*dtsSendTestOutput, error) {
		if dispatcher == nil {
			return nil, huma.Error503ServiceUnavailable("delivery dispatcher not configured")
		}

		var req struct {
			Template  any            `json:"template"`
			Variables map[string]any `json:"variables"`
			Target    struct {
				ID   string `json:"id"`
				Type string `json:"type"`
			} `json:"target"`
			Language string `json:"language"`
			Platform string `json:"platform"`
		}
		if err := json.Unmarshal(in.Body, &req); err != nil {
			return nil, huma.Error400BadRequest("invalid request body: " + err.Error())
		}

		if req.Template == nil {
			return nil, huma.Error400BadRequest("template is required")
		}
		if req.Target.ID == "" {
			return nil, huma.Error400BadRequest("target.id is required")
		}
		if req.Target.Type == "" {
			req.Target.Type = "discord:user"
		}
		if req.Language == "" {
			req.Language = "en"
		}
		if req.Platform == "" {
			req.Platform = "discord"
		}

		// JSON-stringify the template object with SetEscapeHTML(false) to preserve
		// <, >, & in Handlebars expressions like {{#compare x '<' 100}}.
		var buf bytes.Buffer
		enc := json.NewEncoder(&buf)
		enc.SetEscapeHTML(false)
		if err := enc.Encode(req.Template); err != nil {
			return nil, huma.Error400BadRequest("invalid template: " + err.Error())
		}
		templateStr := strings.TrimSpace(buf.String())

		compiled, err := raymond.Parse(templateStr)
		if err != nil {
			log.Warnf("dts sendtest: template compile error: %v\nTemplate: %s", err, templateStr)
			return nil, huma.Error400BadRequest("template compile error: " + err.Error())
		}

		// Register partials so {{> partialName}} works.
		partials := ts.Partials()
		if len(partials) > 0 {
			compiled.RegisterPartials(partials)
		}

		df := raymond.NewDataFrame()
		df.Set("language", req.Language)
		df.Set("platform", req.Platform)

		rendered, err := compiled.ExecWith(req.Variables, df)
		if err != nil {
			log.Warnf("dts sendtest: render error: %v", err)
			return nil, huma.Error500InternalServerError("render error: " + err.Error())
		}

		// Process <S< ... >S> shortlink markers the same way the live renderer does.
		if renderer != nil {
			rendered = dts.ShortenMarkers(rendered, renderer.Shortener())
		}

		var message json.RawMessage
		if err := json.Unmarshal([]byte(rendered), &message); err != nil {
			log.Warnf("dts sendtest: rendered template is not valid JSON: %v\nRendered: %s", err, rendered)
			return nil, huma.Error500InternalServerError("rendered template is not valid JSON: " + err.Error())
		}

		job := &delivery.Job{
			Target:       req.Target.ID,
			Type:         req.Target.Type,
			Message:      message,
			Name:         "DTS Editor Test",
			LogReference: "dts-editor",
		}
		dispatcher.Dispatch(job)

		out := &dtsSendTestOutput{}
		out.Body.Status = "ok"
		out.Body.Message = "sent"
		return out, nil
	})
}

// RegisterDTSWrites registers the in-block DTS write/render endpoints that must
// stay gated behind dtsRenderer != nil. The sendtest dispatcher may be nil
// (handled at request time with a 503).
func RegisterDTSWrites(api huma.API, ts interface {
	dtsRenderReader
	dtsSaveWriter
	dtsMetadataReader
	dtsSendTestReader
}, enrich EnrichService, dispatcher dtsDispatcher, renderer dtsShortener) {
	RegisterTemplateConfig(api, ts)
	RegisterDTSRender(api, ts)
	RegisterDTSSaveTemplates(api, ts)
	RegisterDTSEnrich(api, enrich)
	RegisterDTSSendTest(api, dispatcher, ts, renderer)
}

// compile-time assertions that the concrete types satisfy the write interfaces.
var (
	_ dtsRenderReader   = (*dts.TemplateStore)(nil)
	_ dtsSaveWriter     = (*dts.TemplateStore)(nil)
	_ dtsMetadataReader = (*dts.TemplateStore)(nil)
	_ dtsSendTestReader = (*dts.TemplateStore)(nil)
	_ dtsShortener      = (*dts.Renderer)(nil)
	_ dtsDispatcher     = (*delivery.Dispatcher)(nil)
)
