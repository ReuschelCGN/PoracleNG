package main

import (
	"encoding/json"
	"fmt"

	"github.com/pokemon/poracleng/processor/internal/bot"
	"github.com/pokemon/poracleng/processor/internal/delivery"
	"github.com/pokemon/poracleng/processor/internal/enrichment"
	"github.com/pokemon/poracleng/processor/internal/webhook"
)

func (ps *ProcessorService) ProcessTest(webhookType string, raw json.RawMessage, target bot.TestTarget) error {
	if ps.dtsRenderer == nil {
		return fmt.Errorf("DTS templates not loaded — check startup logs for template loading errors")
	}
	if ps.renderCh == nil {
		return fmt.Errorf("render queue not available")
	}
	if ps.dispatcher == nil {
		return fmt.Errorf("message delivery not configured — check Discord/Telegram token settings")
	}

	// Validate that a matching DTS template exists before enqueueing.
	// Resolve the actual DTS type by peeking at the webhook data for types
	// that branch (pokestop→lure/invasion, raid→egg/raid).
	dtsType := resolveDTSTypeFromRaw(webhookType, raw)
	platform := delivery.PlatformFromType(target.Type)
	language := target.Language
	if language == "" {
		language = ps.cfg.General.Locale
	}
	if err := ps.dtsRenderer.CheckTemplate(dtsType, platform, target.Template, language); err != nil {
		return err
	}

	matchedUser := webhook.MatchedUser{
		ID:        target.ID,
		Name:      target.Name,
		Type:      target.Type,
		Language:  target.Language,
		Latitude:  target.Latitude,
		Longitude: target.Longitude,
		Template:  target.Template,
		Clean:     0,
	}

	switch webhookType {
	case "pokemon":
		return ps.processTestPokemon(raw, matchedUser)
	case "raid", "egg":
		return ps.processTestRaid(raw, matchedUser)
	case "invasion":
		return ps.processTestInvasion(raw, matchedUser)
	case "incident":
		return ps.processTestIncident(raw, matchedUser)
	case "quest":
		return ps.processTestQuest(raw, matchedUser)
	case "gym":
		return ps.processTestGym(raw, matchedUser)
	case "nest":
		return ps.processTestNest(raw, matchedUser)
	case "fort_update":
		return ps.processTestFort(raw, matchedUser)
	case "max_battle":
		return ps.processTestMaxbattle(raw, matchedUser)
	case "pokestop":
		return ps.processTestPokestop(raw, matchedUser)
	case "showcase":
		return ps.processTestShowcase(raw, matchedUser)
	default:
		return fmt.Errorf("unsupported test webhook type: %s", webhookType)
	}
}

// renderJobFromEnrich wraps a shared enrichResult into a delivery RenderJob for
// a single test target. perUser is computed here with the REAL target user
// (unlike the editor's synthetic user). raw is unused today (enrichment
// already parsed it) but kept in the signature for symmetry with the
// enrich* functions and for future derived-type handling (raid RSVP, etc.).
func (ps *ProcessorService) renderJobFromEnrich(r *enrichResult, target webhook.MatchedUser, alertType string, raw json.RawMessage, isPokemon, isEncountered bool) RenderJob {
	matched := []webhook.MatchedUser{target}
	perLang := map[string]map[string]any{}
	if r.perLang != nil {
		perLang[target.Language] = r.perLang
	}
	var perUser map[string]map[string]any
	if isPokemon && ps.enricher.PVPDisplay != nil && r.perLang != nil {
		perUser = ps.enricher.PokemonPerUser(perLang, matched)
	}
	return RenderJob{
		AlertType:         alertType,
		TemplateType:      r.templateType,
		IsPokemon:         isPokemon,
		IsEncountered:     isEncountered,
		Enrichment:        r.base,
		PerLangEnrichment: perLang,
		PerUserEnrichment: perUser,
		WebhookFields:     r.webhookFields,
		MatchedUsers:      matched,
		MatchedAreas:      []webhook.MatchedArea{},
		TileGate:          ps.newTileGate(r.tilePending),
		LogReference:      "test",
	}
}

func (ps *ProcessorService) processTestPokemon(raw json.RawMessage, target webhook.MatchedUser) error {
	r, err := ps.enrichPokemon(raw, target.Language, false)
	if err != nil {
		return err
	}
	if ps.renderCh == nil {
		return fmt.Errorf("render queue not available")
	}
	isEncountered := false
	if v, ok := r.extras["encountered"].(bool); ok {
		isEncountered = v
	}
	ps.renderCh <- ps.renderJobFromEnrich(r, target, "pokemon", raw, true, isEncountered)
	return nil
}

func (ps *ProcessorService) processTestRaid(raw json.RawMessage, target webhook.MatchedUser) error {
	// isEgg=false: the actual type is always determined by raid.PokemonID
	// inside enrichRaid (isEgg only forces "egg" for the explicit /api/test
	// "egg" webhookType passthrough, which this test path never uses since
	// both "raid" and "egg" webhookType route here and let the payload decide).
	// freshenStaleTime=false: preserves this path's pre-existing behaviour of
	// never bumping a stale Start/End window (see enrichRaid's doc comment).
	r, err := ps.enrichRaid(raw, target.Language, false, false)
	if err != nil {
		return err
	}
	if ps.renderCh == nil {
		return fmt.Errorf("render queue not available")
	}
	ps.renderCh <- ps.renderJobFromEnrich(r, target, r.templateType, raw, false, false)
	return nil
}

func (ps *ProcessorService) processTestInvasion(raw json.RawMessage, target webhook.MatchedUser) error {
	// freshenStaleTime=false: preserves this path's pre-existing behaviour of
	// never bumping a stale IncidentExpiration (see enrichInvasion's doc comment).
	r, err := ps.enrichInvasion(raw, target.Language, false)
	if err != nil {
		return err
	}
	if ps.renderCh == nil {
		return fmt.Errorf("render queue not available")
	}
	ps.renderCh <- ps.renderJobFromEnrich(r, target, "invasion", raw, false, false)
	return nil
}

func (ps *ProcessorService) processTestIncident(raw json.RawMessage, target webhook.MatchedUser) error {
	// freshenStaleTime=false: mirrors processTestInvasion's pre-existing
	// behaviour of never bumping a stale IncidentExpiration/ExpireTimestamp
	// (see enrichInvasion's doc comment; enrichIncident shares the same flag).
	r, err := ps.enrichIncident(raw, target.Language, false)
	if err != nil {
		return err
	}
	if ps.renderCh == nil {
		return fmt.Errorf("render queue not available")
	}
	ps.renderCh <- ps.renderJobFromEnrich(r, target, "incident", raw, false, false)
	return nil
}

func (ps *ProcessorService) processTestShowcase(raw json.RawMessage, target webhook.MatchedUser) error {
	var sc webhook.ShowcaseWebhook
	if err := json.Unmarshal(raw, &sc); err != nil {
		return fmt.Errorf("parse showcase: %w", err)
	}

	// Showcases render through the incident template with a synthesised
	// display_type=9 — mirrors ProcessShowcase.
	enrichmentData, tilePending := ps.enricher.Invasion(
		sc.Latitude, sc.Longitude, sc.ShowcaseExpiry, sc.PokestopID, sc.URL,
		0, showcaseDisplayType, 0, enrichment.TileModeURL)
	enrichmentData["pokestop_name"] = sc.Name
	matched := []webhook.MatchedUser{target}

	var perLang map[string]map[string]any
	if ps.enricher.GameData != nil && ps.enricher.Translations != nil {
		m := ps.enricher.InvasionTranslate(
			enrichmentData, sc.Latitude, sc.Longitude, 0, nil, sc.ShowcaseRankings, target.Language)
		for k, v := range ps.enricher.ShowcaseFocusTranslate(sc.ShowcaseFocus, target.Language) {
			m[k] = v
		}
		perLang = map[string]map[string]any{target.Language: m}
	}

	if ps.renderCh == nil {
		return fmt.Errorf("render queue not available")
	}
	webhookFields := parseWebhookFields(raw)
	ps.renderCh <- RenderJob{
		AlertType:         "incident",
		TemplateType:      "showcase",
		Enrichment:        enrichmentData,
		PerLangEnrichment: perLang,
		WebhookFields:     webhookFields,
		MatchedUsers:      matched,
		MatchedAreas:      []webhook.MatchedArea{},
		TileGate:          ps.newTileGate(tilePending),
		LogReference:      "test",
	}
	return nil
}

func (ps *ProcessorService) processTestQuest(raw json.RawMessage, target webhook.MatchedUser) error {
	r, err := ps.enrichQuest(raw, target.Language)
	if err != nil {
		return err
	}
	if ps.renderCh == nil {
		return fmt.Errorf("render queue not available")
	}
	ps.renderCh <- ps.renderJobFromEnrich(r, target, "quest", raw, false, false)
	return nil
}

func (ps *ProcessorService) processTestGym(raw json.RawMessage, target webhook.MatchedUser) error {
	r, err := ps.enrichGym(raw, target.Language)
	if err != nil {
		return err
	}
	if ps.renderCh == nil {
		return fmt.Errorf("render queue not available")
	}
	ps.renderCh <- ps.renderJobFromEnrich(r, target, "gym", raw, false, false)
	return nil
}

func (ps *ProcessorService) processTestNest(raw json.RawMessage, target webhook.MatchedUser) error {
	r, err := ps.enrichNest(raw, target.Language)
	if err != nil {
		return err
	}
	if ps.renderCh == nil {
		return fmt.Errorf("render queue not available")
	}
	ps.renderCh <- ps.renderJobFromEnrich(r, target, "nest", raw, false, false)
	return nil
}

func (ps *ProcessorService) processTestFort(raw json.RawMessage, target webhook.MatchedUser) error {
	r, err := ps.enrichFort(raw, target.Language)
	if err != nil {
		return err
	}
	if ps.renderCh == nil {
		return fmt.Errorf("render queue not available")
	}
	ps.renderCh <- ps.renderJobFromEnrich(r, target, "fort-update", raw, false, false)
	return nil
}

func (ps *ProcessorService) processTestMaxbattle(raw json.RawMessage, target webhook.MatchedUser) error {
	r, err := ps.enrichMaxbattle(raw, target.Language)
	if err != nil {
		return err
	}
	if ps.renderCh == nil {
		return fmt.Errorf("render queue not available")
	}
	ps.renderCh <- ps.renderJobFromEnrich(r, target, "maxbattle", raw, false, false)
	return nil
}

func (ps *ProcessorService) processTestPokestop(raw json.RawMessage, target webhook.MatchedUser) error {
	// Pokestop can be invasion or lure — peek at fields
	var peek struct {
		LureExpiration     int64 `json:"lure_expiration"`
		IncidentExpiration int64 `json:"incident_expiration"`
	}
	if err := json.Unmarshal(raw, &peek); err != nil {
		return fmt.Errorf("parse pokestop: %w", err)
	}

	if peek.LureExpiration > 0 {
		r, err := ps.enrichLure(raw, target.Language)
		if err != nil {
			return err
		}
		if ps.renderCh == nil {
			return fmt.Errorf("render queue not available")
		}
		ps.renderCh <- ps.renderJobFromEnrich(r, target, "lure", raw, false, false)
		return nil
	}

	return ps.processTestInvasion(raw, target)
}

// resolveDTSTypeFromRaw determines the DTS template type by peeking at the raw webhook JSON.
// Handles branching types: pokestop→lure/invasion, raid→egg/raid.
func resolveDTSTypeFromRaw(webhookType string, raw json.RawMessage) string {
	switch webhookType {
	case "pokemon":
		return "monster"
	case "raid":
		var peek struct {
			PokemonID int `json:"pokemon_id"`
		}
		if json.Unmarshal(raw, &peek) == nil && peek.PokemonID > 0 {
			return "raid"
		}
		return "egg"
	case "egg":
		return "egg"
	case "pokestop":
		var peek struct {
			LureExpiration int64 `json:"lure_expiration"`
		}
		if json.Unmarshal(raw, &peek) == nil && peek.LureExpiration > 0 {
			return "lure"
		}
		return "invasion"
	case "fort_update":
		return "fort-update"
	case "max_battle":
		return "maxbattle"
	case "showcase":
		return "showcase"
	default:
		return webhookType
	}
}

// Ensure ProcessorService implements TestProcessor
var _ bot.TestProcessor = (*ProcessorService)(nil)
