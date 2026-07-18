// Package dtsmap holds the single canonical table mapping every DTS
// template-type name (and every raw webhook-type spelling) to the webhook
// source it is enriched from. It is shared between the processor's
// enrichment dispatch (cmd/processor) and the API's testdata endpoints
// (internal/api) — cmd/processor can import internal packages, but
// internal/api cannot import package main, so the table lives here as the
// single source of truth both sides use.
package dtsmap

import "maps"

// Source describes where a DTS template-type name comes from: which raw
// webhook type it is enriched from, the canonical DTS template type name
// used for template selection, and whether the source data is a "derived"
// event — one that doesn't arrive directly on the webhook receiver but is
// synthesized from processor-internal state (e.g. an encounter change, an
// RSVP update, a summary digest).
type Source struct {
	WebhookType  string
	TemplateType string
	Derived      bool
}

// types is the single canonical table mapping every name the editor,
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
// (see resolveDTSTypeFromRaw in cmd/processor/test.go), not by name — it
// can't resolve to a single TemplateType on its own. Callers that walk this
// table (e.g. the testdata endpoint) must keep that guard: don't match
// testdata entries against a resolved WebhookType of "pokestop" the way
// every other entry is matched — split by payload shape instead.
var types = map[string]Source{
	// DTS template-type names.
	//
	// The four derived types' WebhookType values are the literal
	// testdata.json "type" field / live-dispatch wire spelling established
	// by the tasks that actually implemented them (monster_changed,
	// rsvp_changes, quest_summary use underscores; weatherchange has no
	// separator at all) — NOT the hyphenated CLI-display spelling
	// (monster-changed, rsvp-changes, quest-summary, weather-change) also
	// registered below as identity aliases. Walkers that match testdata
	// entries by `entry.Type == src.WebhookType` (see internal/api's
	// testdata endpoint) depend on this being the true wire string.
	"monster":        {WebhookType: "pokemon", TemplateType: "monster"},
	"monsterNoIv":    {WebhookType: "pokemon", TemplateType: "monsterNoIv"},
	"monsterChanged": {WebhookType: "monster_changed", TemplateType: "monsterChanged", Derived: true},
	"raid":           {WebhookType: "raid", TemplateType: "raid"},
	"egg":            {WebhookType: "raid", TemplateType: "egg"},
	"rsvpChanges":    {WebhookType: "rsvp_changes", TemplateType: "rsvpChanges", Derived: true},
	"quest":          {WebhookType: "quest", TemplateType: "quest"},
	"questSummary":   {WebhookType: "quest_summary", TemplateType: "questSummary", Derived: true},
	"invasion":       {WebhookType: "pokestop", TemplateType: "invasion"},
	"incident":       {WebhookType: "incident", TemplateType: "incident", Derived: true},
	"showcase":       {WebhookType: "showcase", TemplateType: "showcase"},
	"lure":           {WebhookType: "pokestop", TemplateType: "lure"},
	"weatherchange":  {WebhookType: "weatherchange", TemplateType: "weatherchange", Derived: true},
	"gym":            {WebhookType: "gym", TemplateType: "gym"},
	"nest":           {WebhookType: "nest", TemplateType: "nest"},
	"maxbattle":      {WebhookType: "max_battle", TemplateType: "maxbattle"},

	// Identity entries for raw webhook-type spellings not already covered
	// above.
	"pokemon":     {WebhookType: "pokemon", TemplateType: "monster"},
	"max_battle":  {WebhookType: "max_battle", TemplateType: "maxbattle"},
	"fort_update": {WebhookType: "fort_update", TemplateType: "fort-update"},
	"fort-update": {WebhookType: "fort-update", TemplateType: "fort-update"},

	// Identity entries for the derived event's CLI-display (hyphenated)
	// spelling (see !poracle-test's validHooks in
	// internal/bot/commands/poracletest.go, which converts hyphens to
	// underscores before dispatch/testdata lookup) and its underlying
	// wire/testdata.json spelling, so a derived name resolves the same way
	// no matter which of the three forms (DTS template-type name, CLI
	// hyphenated display name, or wire underscore name) a caller uses.
	"monster-changed": {WebhookType: "monster_changed", TemplateType: "monsterChanged", Derived: true},
	"rsvp-changes":    {WebhookType: "rsvp_changes", TemplateType: "rsvpChanges", Derived: true},
	"quest-summary":   {WebhookType: "quest_summary", TemplateType: "questSummary", Derived: true},
	"weather-change":  {WebhookType: "weatherchange", TemplateType: "weatherchange", Derived: true},
	"monster_changed": {WebhookType: "monster_changed", TemplateType: "monsterChanged", Derived: true},
	"rsvp_changes":    {WebhookType: "rsvp_changes", TemplateType: "rsvpChanges", Derived: true},
	"quest_summary":   {WebhookType: "quest_summary", TemplateType: "questSummary", Derived: true},
}

// Alias resolves a DTS template-type name (e.g. "monster", "egg",
// "monsterChanged") OR a raw webhook type (e.g. "pokemon", "max_battle") to
// its canonical Source. The second return value is false when name is not
// recognized.
func Alias(name string) (Source, bool) {
	src, ok := types[name]
	return src, ok
}

// TypeMap returns a defensive copy of the full canonical table, for callers
// (e.g. the API) to expose to clients.
func TypeMap() map[string]Source {
	out := make(map[string]Source, len(types))
	maps.Copy(out, types)
	return out
}
