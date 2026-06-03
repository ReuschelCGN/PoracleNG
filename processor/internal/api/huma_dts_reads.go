package api

import (
	"context"
	"os"
	"path/filepath"
	"strings"

	"github.com/danielgtaylor/huma/v2"
	log "github.com/sirupsen/logrus"

	"github.com/pokemon/poracleng/processor/internal/backup"
	"github.com/pokemon/poracleng/processor/internal/buttonactions"
	"github.com/pokemon/poracleng/processor/internal/dts"
)

// dtsEmojiLookup is the minimal emoji-resolution surface the /dts/emoji read
// needs. *dts.EmojiLookup satisfies it; the interface keeps the Register
// signature testable.
type dtsEmojiLookup interface {
	Defaults() map[string]string
	PlatformOverrides() map[string]map[string]string
	MergedFor(platform string) map[string]string
}

// dtsTemplateReader is the minimal template-store surface the DTS read
// endpoints need. *dts.TemplateStore satisfies it; the interface keeps the
// Register signatures testable since TemplateStore has unexported fields and
// can't be populated from outside the dts package.
type dtsTemplateReader interface {
	FilteredEntries(filterType, filterPlatform, filterLanguage, filterID string) []dts.DTSEntry
	ResolveEntryContent(entry dts.DTSEntry) (any, string)
	DeleteEntry(filterType, filterPlatform, filterLanguage, filterID string) error
	GetEntry(filterType, filterPlatform, filterLanguage, filterID string) *dts.DTSEntry
	Partials() map[string]string
	ClearCache()
}

// dtsEmojiInput carries the optional platform query param.
type dtsEmojiInput struct {
	Platform string `query:"platform"`
}

// RegisterDTSEmoji registers GET /api/dts/emoji. With a platform query it
// returns the merged flat map for that platform; otherwise the full
// defaults+overrides set. Replaces gin HandleDTSEmoji. Success JSON preserved.
func RegisterDTSEmoji(api huma.API, emoji dtsEmojiLookup) {
	huma.Register(api, huma.Operation{
		OperationID: "get-dts-emoji", Method: "GET", Path: "/dts/emoji",
		Summary: "Emoji lookup map for template editing", Tags: []string{"dts"},
		Security: []map[string][]string{{"poracleSecret": {}}},
	}, func(_ context.Context, in *dtsEmojiInput) (*anyBodyOutput, error) {
		if in.Platform != "" {
			return &anyBodyOutput{Body: map[string]any{
				"status":   "ok",
				"platform": in.Platform,
				"emoji":    emoji.MergedFor(in.Platform),
			}}, nil
		}
		return &anyBodyOutput{Body: map[string]any{
			"status":    "ok",
			"defaults":  emoji.Defaults(),
			"platforms": emoji.PlatformOverrides(),
		}}, nil
	})
}

// dtsTemplatesQueryInput carries the optional filter query params.
type dtsTemplatesQueryInput struct {
	Type     string `query:"type"`
	Platform string `query:"platform"`
	Language string `query:"language"`
	ID       string `query:"id"`
}

// dtsEntryWithContent mirrors the anonymous struct the gin handler used: a
// DTSEntry with an optional resolved templateFileContent.
type dtsEntryWithContent struct {
	dts.DTSEntry
	TemplateFileContent string `json:"templateFileContent,omitempty"`
}

// RegisterDTSGetTemplates registers GET /api/dts/templates, returning filtered
// DTS entries with resolved content. Replaces gin HandleDTSGetTemplates.
func RegisterDTSGetTemplates(api huma.API, ts dtsTemplateReader) {
	huma.Register(api, huma.Operation{
		OperationID: "get-dts-templates", Method: "GET", Path: "/dts/templates",
		Summary: "DTS template entries with full content", Tags: []string{"dts"},
		Security: []map[string][]string{{"poracleSecret": {}}},
	}, func(_ context.Context, in *dtsTemplatesQueryInput) (*anyBodyOutput, error) {
		entries := ts.FilteredEntries(in.Type, in.Platform, in.Language, in.ID)
		result := make([]dtsEntryWithContent, len(entries))
		for i, e := range entries {
			resolved, fileContent := ts.ResolveEntryContent(e)
			if resolved != nil {
				e.Template = resolved
			}
			result[i].DTSEntry = e
			if fileContent != "" {
				result[i].TemplateFileContent = fileContent
			}
		}
		return &anyBodyOutput{Body: map[string]any{"status": "ok", "templates": result}}, nil
	})
}

// dtsDeleteTemplateInput carries the key fields identifying the entry to delete.
type dtsDeleteTemplateInput struct {
	Type     string `query:"type"`
	Platform string `query:"platform"`
	Language string `query:"language"`
	ID       string `query:"id"`
}

// RegisterDTSDeleteTemplate registers DELETE /api/dts/templates, removing the
// keyed entry from memory and disk. Replaces gin HandleDTSDeleteTemplate.
// Missing type/platform/id yields 400; "not found" yields 404; readonly or
// other store errors yield 403.
func RegisterDTSDeleteTemplate(api huma.API, ts dtsTemplateReader) {
	huma.Register(api, huma.Operation{
		OperationID: "delete-dts-template", Method: "DELETE", Path: "/dts/templates",
		Summary: "Delete a DTS template entry", Tags: []string{"dts"},
		Security: []map[string][]string{{"poracleSecret": {}}},
	}, func(_ context.Context, in *dtsDeleteTemplateInput) (*anyBodyOutput, error) {
		if in.Type == "" || in.Platform == "" || in.ID == "" {
			return nil, huma.Error400BadRequest("type, platform, and id query parameters are required")
		}
		if err := ts.DeleteEntry(in.Type, in.Platform, in.Language, in.ID); err != nil {
			if strings.Contains(err.Error(), "not found") {
				return nil, huma.Error404NotFound(err.Error())
			}
			return nil, huma.Error403Forbidden(err.Error())
		}
		return &anyBodyOutput{Body: map[string]any{"status": "ok"}}, nil
	})
}

// dtsTemplateFileWriteInput carries the entry key fields plus the new content.
type dtsTemplateFileWriteInput struct {
	Type     string `query:"type"`
	Platform string `query:"platform"`
	Language string `query:"language"`
	ID       string `query:"id"`
	Body     struct {
		Content string `json:"content"`
	}
}

// RegisterDTSTemplateFileWrite registers PUT /api/dts/templates/file, updating
// the raw content of a templateFile entry. The path is derived from the entry's
// key fields — no client paths are used. Replaces gin HandleDTSTemplateFileWrite.
// Missing entry → 404; non-templateFile entry → 400; readonly → 403; path
// traversal → 403; filesystem errors → 500.
func RegisterDTSTemplateFileWrite(api huma.API, ts dtsTemplateReader, configDir string) {
	huma.Register(api, huma.Operation{
		OperationID: "put-dts-template-file", Method: "PUT", Path: "/dts/templates/file",
		Summary: "Update raw templateFile content", Tags: []string{"dts"},
		Security: []map[string][]string{{"poracleSecret": {}}},
	}, func(_ context.Context, in *dtsTemplateFileWriteInput) (*anyBodyOutput, error) {
		entry := ts.GetEntry(in.Type, in.Platform, in.Language, in.ID)
		if entry == nil {
			return nil, huma.Error404NotFound("template not found")
		}
		if entry.TemplateFile == "" {
			return nil, huma.Error400BadRequest("template uses inline JSON, not a templateFile")
		}
		if entry.Readonly {
			return nil, huma.Error403Forbidden("template is readonly (bundled default)")
		}

		path := filepath.Join(configDir, entry.TemplateFile)
		// Safety: ensure resolved path stays under configDir.
		absPath, _ := filepath.Abs(path)
		absConfig, _ := filepath.Abs(configDir)
		if !strings.HasPrefix(absPath, absConfig+string(filepath.Separator)) {
			return nil, huma.Error403Forbidden("invalid template file path")
		}

		if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
			return nil, huma.Error500InternalServerError("create directory: " + err.Error())
		}

		// Snapshot the existing file (if any) before overwriting.
		backupRel, err := backup.Save(configDir, entry.TemplateFile)
		if err != nil {
			return nil, huma.Error500InternalServerError("backup existing: " + err.Error())
		}

		if err := os.WriteFile(path, []byte(in.Body.Content), 0644); err != nil { //nolint:gosec // operator-writable template content, same perms as legacy handler
			return nil, huma.Error500InternalServerError("write file: " + err.Error())
		}

		ts.ClearCache()

		log.Infof("dts: updated template file %s via API", entry.TemplateFile)
		return &anyBodyOutput{Body: map[string]any{
			"status":       "ok",
			"templateFile": entry.TemplateFile,
			"backup":       backupRel,
		}}, nil
	})
}

// RegisterDTSFieldTypes registers GET /api/dts/fields, returning the list of
// available DTS type names. Replaces gin HandleDTSFieldTypes.
func RegisterDTSFieldTypes(api huma.API) {
	huma.Register(api, huma.Operation{
		OperationID: "get-dts-fields", Method: "GET", Path: "/dts/fields",
		Summary: "List all DTS type names", Tags: []string{"dts"},
		Security: []map[string][]string{{"poracleSecret": {}}},
	}, func(_ context.Context, _ *struct{}) (*anyBodyOutput, error) {
		types := make([]string, 0, len(fieldsByType))
		for t := range fieldsByType {
			types = append(types, t)
		}
		return &anyBodyOutput{Body: map[string]any{"status": "ok", "types": types}}, nil
	})
}

// dtsFieldsInput carries the type path param.
type dtsFieldsInput struct {
	Type string `path:"type"`
}

// RegisterDTSFields registers GET /api/dts/fields/{type}, returning the field
// surface for a DTS type. Unknown types return just the common fields (200, not
// 404 — matching the gin handler). Replaces gin HandleDTSFields.
func RegisterDTSFields(api huma.API) {
	huma.Register(api, huma.Operation{
		OperationID: "get-dts-fields-type", Method: "GET", Path: "/dts/fields/{type}",
		Summary: "Template fields, block scopes, and snippets for a type", Tags: []string{"dts"},
		Security: []map[string][]string{{"poracleSecret": {}}},
	}, func(_ context.Context, in *dtsFieldsInput) (*anyBodyOutput, error) {
		entry, ok := fieldsByType[in.Type]
		if !ok {
			return &anyBodyOutput{Body: map[string]any{
				"status": "ok",
				"type":   in.Type,
				"fields": commonFields,
			}}, nil
		}
		resp := map[string]any{
			"status": "ok",
			"type":   in.Type,
			"fields": entry.Fields,
		}
		if len(entry.BlockScopes) > 0 {
			resp["blockScopes"] = entry.BlockScopes
		}
		if len(entry.Snippets) > 0 {
			resp["snippets"] = entry.Snippets
		}
		return &anyBodyOutput{Body: resp}, nil
	})
}

// RegisterDTSPartials registers GET /api/dts/partials, returning the Handlebars
// partials map. Replaces gin HandleDTSPartials.
func RegisterDTSPartials(api huma.API, ts dtsTemplateReader) {
	huma.Register(api, huma.Operation{
		OperationID: "get-dts-partials", Method: "GET", Path: "/dts/partials",
		Summary: "Handlebars partials for client-side rendering", Tags: []string{"dts"},
		Security: []map[string][]string{{"poracleSecret": {}}},
	}, func(_ context.Context, _ *struct{}) (*anyBodyOutput, error) {
		return &anyBodyOutput{Body: map[string]any{"status": "ok", "partials": ts.Partials()}}, nil
	})
}

// dtsTestdataInput carries the optional type filter query param.
type dtsTestdataInput struct {
	Type string `query:"type"`
}

// RegisterDTSTestdata registers GET /api/dts/testdata, returning test webhook
// scenarios merged from config + fallback testdata.json. Replaces gin
// HandleDTSTestdata. A missing testdata.json yields 404.
func RegisterDTSTestdata(api huma.API, configDir, fallbackDir string) {
	huma.Register(api, huma.Operation{
		OperationID: "get-dts-testdata", Method: "GET", Path: "/dts/testdata",
		Summary: "Test webhook scenarios from testdata.json", Tags: []string{"dts"},
		Security: []map[string][]string{{"poracleSecret": {}}},
	}, func(_ context.Context, in *dtsTestdataInput) (*anyBodyOutput, error) {
		entries := loadTestdata(configDir, fallbackDir)
		if entries == nil {
			return nil, huma.Error404NotFound("testdata.json not found")
		}
		if in.Type != "" {
			var filtered []TestDataEntry
			for _, e := range entries {
				if e.Type == in.Type {
					filtered = append(filtered, e)
				}
			}
			entries = filtered
		}
		return &anyBodyOutput{Body: map[string]any{"status": "ok", "testdata": entries}}, nil
	})
}

// RegisterButtonActions registers GET /api/dts/actions, returning the list of
// registered button actions and their editor metadata. Replaces gin
// HandleButtonActionsList. A nil registry yields 503.
func RegisterButtonActions(api huma.API, reg *buttonactions.Registry) {
	huma.Register(api, huma.Operation{
		OperationID: "get-dts-actions", Method: "GET", Path: "/dts/actions",
		Summary: "List registered button actions + their scopes/params", Tags: []string{"dts"},
		Security: []map[string][]string{{"poracleSecret": {}}},
	}, func(_ context.Context, _ *struct{}) (*anyBodyOutput, error) {
		if reg == nil {
			return nil, huma.Error503ServiceUnavailable("button actions not configured")
		}
		names := reg.Names()
		out := make([]ActionInfo, 0, len(names))
		for _, n := range names {
			out = append(out, describeAction(n))
		}
		return &anyBodyOutput{Body: map[string]any{"actions": out}}, nil
	})
}

// RegisterDTSReads registers the in-block DTS editor read endpoints — the ones
// that must stay gated behind dtsRenderer != nil. /dts/actions is registered
// separately (RegisterButtonActions) because it lives outside that block.
func RegisterDTSReads(api huma.API, emoji dtsEmojiLookup, ts dtsTemplateReader, configDir, fallbackDir string) {
	RegisterDTSEmoji(api, emoji)
	RegisterDTSGetTemplates(api, ts)
	RegisterDTSDeleteTemplate(api, ts)
	RegisterDTSTemplateFileWrite(api, ts, configDir)
	RegisterDTSFieldTypes(api)
	RegisterDTSFields(api)
	RegisterDTSPartials(api, ts)
	RegisterDTSTestdata(api, configDir, fallbackDir)
}

// compile-time assertions that the concrete types satisfy the read interfaces.
var (
	_ dtsEmojiLookup    = (*dts.EmojiLookup)(nil)
	_ dtsTemplateReader = (*dts.TemplateStore)(nil)
)
