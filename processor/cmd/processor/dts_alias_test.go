package main

import "testing"

// TestDtsAlias_DTSNamesResolve covers the DTS template-type name → canonical
// webhook-source resolution described in CLAUDE.md's DTS template selection
// docs and superpowers/sdd task-2-brief.md. dtsAlias is the single table
// used by EnrichWebhook (this task), and later by testdata lookup / the
// !poracle-test command / the API's type-list endpoint (later tasks).
func TestDtsAlias_DTSNamesResolve(t *testing.T) {
	cases := []struct {
		name             string
		wantWebhookType  string
		wantTemplateType string
		wantDerived      bool
	}{
		{"monster", "pokemon", "monster", false},
		{"monsterNoIv", "pokemon", "monsterNoIv", false},
		{"monsterChanged", "monster-changed", "monsterChanged", true},
		{"raid", "raid", "raid", false},
		{"egg", "raid", "egg", false},
		{"rsvpChanges", "rsvp-changes", "rsvpChanges", true},
		{"quest", "quest", "quest", false},
		{"questSummary", "quest-summary", "questSummary", true},
		{"invasion", "pokestop", "invasion", false},
		{"incident", "incident", "incident", true},
		{"showcase", "showcase", "showcase", false},
		{"lure", "pokestop", "lure", false},
		{"weatherchange", "weather-change", "weatherchange", true},
		{"gym", "gym", "gym", false},
		{"nest", "nest", "nest", false},
		{"maxbattle", "max_battle", "maxbattle", false},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			src, ok := dtsAlias(tc.name)
			if !ok {
				t.Fatalf("dtsAlias(%q) ok = false, want true", tc.name)
			}
			if src.WebhookType != tc.wantWebhookType {
				t.Errorf("dtsAlias(%q).WebhookType = %q, want %q", tc.name, src.WebhookType, tc.wantWebhookType)
			}
			if src.TemplateType != tc.wantTemplateType {
				t.Errorf("dtsAlias(%q).TemplateType = %q, want %q", tc.name, src.TemplateType, tc.wantTemplateType)
			}
			if src.Derived != tc.wantDerived {
				t.Errorf("dtsAlias(%q).Derived = %v, want %v", tc.name, src.Derived, tc.wantDerived)
			}
		})
	}
}

// TestDtsAlias_RawWebhookTypesResolve confirms raw webhook-type spellings
// (as used by the existing EnrichWebhook switch and by Golbat/testdata.json)
// also resolve via dtsAlias, so callers don't need to know whether a given
// string is a DTS template name or the underlying wire type.
func TestDtsAlias_RawWebhookTypesResolve(t *testing.T) {
	cases := []struct {
		name             string
		wantWebhookType  string
		wantTemplateType string
	}{
		{"pokemon", "pokemon", "monster"},
		{"max_battle", "max_battle", "maxbattle"},
		{"fort_update", "fort_update", "fort-update"},
		{"fort-update", "fort-update", "fort-update"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			src, ok := dtsAlias(tc.name)
			if !ok {
				t.Fatalf("dtsAlias(%q) ok = false, want true", tc.name)
			}
			if src.WebhookType != tc.wantWebhookType {
				t.Errorf("dtsAlias(%q).WebhookType = %q, want %q", tc.name, src.WebhookType, tc.wantWebhookType)
			}
			if src.TemplateType != tc.wantTemplateType {
				t.Errorf("dtsAlias(%q).TemplateType = %q, want %q", tc.name, src.TemplateType, tc.wantTemplateType)
			}
		})
	}
}

func TestDtsAlias_Unknown(t *testing.T) {
	if _, ok := dtsAlias("not-a-real-type"); ok {
		t.Errorf("dtsAlias(unknown) ok = true, want false")
	}
}

// TestDtsTypeMap_ContainsCanonicalEntries checks the full-table accessor
// (exposed for the API in a later task) contains at least the canonical DTS
// names and is a defensive copy (mutating the result must not affect the
// package-level table).
// TestEnrichWebhook_ResolvesDTSNames confirms EnrichWebhook itself performs
// the name resolution: calling it with a DTS template-type name behaves
// exactly like calling it with the underlying raw webhook type, and derived
// names (not yet wired into the switch — Tasks 3-7 do that) still return the
// pre-existing "unsupported" error rather than silently misrouting.
func TestEnrichWebhook_ResolvesDTSNames(t *testing.T) {
	ps := newEnrichParityService(t)

	pokemonRaw := loadTestdataSample(t, "pokemon", "hundo")
	if _, err := ps.EnrichWebhook("monster", pokemonRaw, "en", "discord"); err != nil {
		t.Errorf(`EnrichWebhook("monster", ...) error: %v, want success like EnrichWebhook("pokemon", ...)`, err)
	}
	if _, err := ps.EnrichWebhook("monsterNoIv", pokemonRaw, "en", "discord"); err != nil {
		t.Errorf(`EnrichWebhook("monsterNoIv", ...) error: %v, want success (resolves to "pokemon")`, err)
	}
	if _, err := ps.EnrichWebhook("pokemon", pokemonRaw, "en", "discord"); err != nil {
		t.Errorf(`EnrichWebhook("pokemon", ...) error: %v, want success`, err)
	}

	eggRaw := loadTestdataSample(t, "raid", "egg1")
	if _, err := ps.EnrichWebhook("egg", eggRaw, "en", "discord"); err != nil {
		t.Errorf(`EnrichWebhook("egg", ...) error: %v, want success (resolves to "raid")`, err)
	}
	if _, err := ps.EnrichWebhook("raid", eggRaw, "en", "discord"); err != nil {
		t.Errorf(`EnrichWebhook("raid", ...) error: %v, want success`, err)
	}

	invasionRaw := loadTestdataSample(t, "pokestop", "invasion")
	if _, err := ps.EnrichWebhook("invasion", invasionRaw, "en", "discord"); err != nil {
		t.Errorf(`EnrichWebhook("invasion", ...) error: %v, want success (must not be rewritten to unsupported "pokestop")`, err)
	}

	lureRaw := loadTestdataSample(t, "pokestop", "lure")
	if _, err := ps.EnrichWebhook("lure", lureRaw, "en", "discord"); err != nil {
		t.Errorf(`EnrichWebhook("lure", ...) error: %v, want success (must not be rewritten to unsupported "pokestop")`, err)
	}

	if _, err := ps.EnrichWebhook("monsterChanged", pokemonRaw, "en", "discord"); err == nil {
		t.Errorf(`EnrichWebhook("monsterChanged", ...) error = nil, want an "unsupported" error — derived-type handling isn't wired until Tasks 3-7`)
	}
}

func TestDtsTypeMap_ContainsCanonicalEntries(t *testing.T) {
	m := dtsTypeMap()
	for _, name := range []string{"monster", "raid", "egg", "quest", "invasion", "lure", "gym", "nest", "maxbattle"} {
		if _, ok := m[name]; !ok {
			t.Errorf("dtsTypeMap() missing entry %q", name)
		}
	}

	delete(m, "monster")
	if _, ok := dtsAlias("monster"); !ok {
		t.Errorf("mutating dtsTypeMap() result affected dtsAlias — table should be defensively copied")
	}
}
