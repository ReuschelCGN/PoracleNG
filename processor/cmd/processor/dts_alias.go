package main

import "maps"

// dtsSource describes where a DTS template-type name comes from: which raw
// webhook type it is enriched from, the canonical DTS template type name
// used for template selection, and whether the source data is a "derived"
// event — one that doesn't arrive directly on the webhook receiver but is
// synthesized from processor-internal state (e.g. an encounter change, an
// RSVP update, a summary digest). Derived sources have no corresponding
// testdata.json "type" yet as of this task; Tasks 3-7 add EnrichWebhook
// handling and testdata fixtures for them.
type dtsSource struct {
	WebhookType  string
	TemplateType string
	Derived      bool
}

// dtsTypes is the single canonical table mapping every name the editor,
// !poracle-test, and the DTS template-selection system may use for an alert
// type to its underlying webhook source and DTS template type. It covers
// two kinds of entries:
//
//   - DTS template-type names (the keys in internal/api/dts_fields.go's
//     fieldsByType, e.g. "monster", "egg", "monsterChanged") — these are
//     the names the editor and !poracle-test address types by.
//   - Identity entries for raw webhook-type spellings (e.g. "pokemon",
//     "max_battle", "fort_update") that aren't already a DTS name above, so
//     callers can pass either spelling interchangeably.
//
// "pokestop" is intentionally NOT an identity entry: it's the shared Golbat
// wire category for both invasion and lure, disambiguated by payload shape
// (see resolveDTSTypeFromRaw in test.go), not by name — it can't resolve to
// a single TemplateType on its own.
var dtsTypes = map[string]dtsSource{
	// DTS template-type names.
	"monster":        {WebhookType: "pokemon", TemplateType: "monster"},
	"monsterNoIv":    {WebhookType: "pokemon", TemplateType: "monsterNoIv"},
	"monsterChanged": {WebhookType: "monster-changed", TemplateType: "monsterChanged", Derived: true},
	"raid":           {WebhookType: "raid", TemplateType: "raid"},
	"egg":            {WebhookType: "raid", TemplateType: "egg"},
	"rsvpChanges":    {WebhookType: "rsvp-changes", TemplateType: "rsvpChanges", Derived: true},
	"quest":          {WebhookType: "quest", TemplateType: "quest"},
	"questSummary":   {WebhookType: "quest-summary", TemplateType: "questSummary", Derived: true},
	"invasion":       {WebhookType: "pokestop", TemplateType: "invasion"},
	"incident":       {WebhookType: "incident", TemplateType: "incident", Derived: true},
	"showcase":       {WebhookType: "showcase", TemplateType: "showcase"},
	"lure":           {WebhookType: "pokestop", TemplateType: "lure"},
	"weatherchange":  {WebhookType: "weather-change", TemplateType: "weatherchange", Derived: true},
	"gym":            {WebhookType: "gym", TemplateType: "gym"},
	"nest":           {WebhookType: "nest", TemplateType: "nest"},
	"maxbattle":      {WebhookType: "max_battle", TemplateType: "maxbattle"},

	// Identity entries for raw webhook-type spellings not already covered
	// above.
	"pokemon":     {WebhookType: "pokemon", TemplateType: "monster"},
	"max_battle":  {WebhookType: "max_battle", TemplateType: "maxbattle"},
	"fort_update": {WebhookType: "fort_update", TemplateType: "fort-update"},
	"fort-update": {WebhookType: "fort-update", TemplateType: "fort-update"},

	// Identity entries for the derived event's own webhook-type spelling
	// (the testdata.json "type" value a later task will introduce), so a
	// derived name resolves the same way whether addressed by its DTS
	// template name or its underlying event-type string.
	"monster-changed": {WebhookType: "monster-changed", TemplateType: "monsterChanged", Derived: true},
	"rsvp-changes":    {WebhookType: "rsvp-changes", TemplateType: "rsvpChanges", Derived: true},
	"quest-summary":   {WebhookType: "quest-summary", TemplateType: "questSummary", Derived: true},
	"weather-change":  {WebhookType: "weather-change", TemplateType: "weatherchange", Derived: true},
}

// dtsAlias resolves a DTS template-type name (e.g. "monster", "egg",
// "monsterChanged") OR a raw webhook type (e.g. "pokemon", "max_battle") to
// its canonical dtsSource. The second return value is false when name is
// not recognized.
func dtsAlias(name string) (dtsSource, bool) {
	src, ok := dtsTypes[name]
	return src, ok
}

// dtsTypeMap returns a defensive copy of the full canonical table, for the
// API to expose (see superpowers/sdd task-2-brief.md Task 8).
func dtsTypeMap() map[string]dtsSource {
	out := make(map[string]dtsSource, len(dtsTypes))
	maps.Copy(out, dtsTypes)
	return out
}
